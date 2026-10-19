import assert from 'node:assert/strict'
import { test } from 'node:test'
import { DEADLETTER_ID, definitionToGraph, encoderOfSink, graphToDefinition, orderedSinkNodeIds } from './convert.ts'

// Encode is a canvas object derived from each sink's saved `encoding`: sinks
// with the same encoding share one Encode node, and saving writes the node's
// encoding back onto every sink it feeds. The spec shape is unchanged.
const gzipWrapped = { format: 'wrapped', encoders: [{ type: 'gzip' }, { type: 'base64' }] }
const definition = {
  name: 'encoded',
  source: { listen: { port: 9000, network: 'tcp' } },
  framing: { mode: 'line' },
  parser: { format: 'json' },
  stages: [{ id: 'keep', kind: 'filter', type: 'cel', expression: 'true' }],
  sinks: [
    { type: 'tcp', url: 'tcp://a:9001', encoding: gzipWrapped },
    { type: 'stdout' },
    { type: 'http', url: 'http://b:8080', encoding: { format: 'fields' } },
    { type: 'udp', url: 'udp://c:9002', encoding: { format: 'wrapped', encoders: [{ type: 'gzip' }, { type: 'base64' }] } },
  ],
  deadLetterSink: { type: 'stdout', encoding: { format: 'fields' } },
}
const roundTrip = (def) => {
  const { nodes, edges } = definitionToGraph(def)
  return JSON.parse(JSON.stringify(graphToDefinition(def.name, [...nodes].reverse(), edges)))
}

test('sinks with the same encoding share one Encode node and the flow saves unchanged', () => {
  const { nodes, edges } = definitionToGraph(definition)
  const encoders = nodes.filter((n) => n.type === 'encode')
  assert.equal(encoders.length, 2, 'gzip+base64 (two sinks) and fields (one sink and the dead letter)')
  assert.ok(nodes.every((n) => n.type !== 'sink' || n.data.sink.encoding === undefined), 'sink nodes do not carry the encoding themselves')
  const sinks = nodes.filter((n) => n.type === 'sink' && !n.data.isDeadLetter)
  const [tcp, stdout, http, udp] = sinks
  assert.equal(encoderOfSink(nodes, edges, tcp.id), encoderOfSink(nodes, edges, udp.id))
  assert.equal(encoderOfSink(nodes, edges, stdout.id), undefined)
  assert.ok(edges.some((e) => e.source === 'keep' && e.target === stdout.id), 'an unencoded sink connects from the last stage')
  assert.ok(edges.some((e) => e.source === 'keep' && e.target === encoderOfSink(nodes, edges, tcp.id).id), 'the last stage feeds the Encode node')
  assert.equal(encoderOfSink(nodes, edges, DEADLETTER_ID), encoderOfSink(nodes, edges, http.id), 'the dead letter hangs off the shared fields encoder')
  assert.ok(encoderOfSink(nodes, edges, http.id).position.x < http.position.x, 'Encode nodes take the column before the sinks')
  assert.deepEqual(roundTrip(definition), definition)
  assert.deepEqual(orderedSinkNodeIds(nodes, edges), sinks.map((n) => n.id), 'sink order is the saved order, not grouped by encoder')
})

test('a dead letter with its own encoding gets a floating Encode node that nothing in the chain feeds', () => {
  const def = { ...definition, sinks: [{ type: 'stdout' }], deadLetterSink: { type: 'tcp', url: 'tcp://dl:9', encoding: { format: 'raw', encoders: [{ type: 'hex' }] } } }
  const { nodes, edges } = definitionToGraph(def)
  const encoder = encoderOfSink(nodes, edges, DEADLETTER_ID)
  assert.ok(encoder)
  assert.equal(edges.filter((e) => e.target === encoder.id).length, 0, 'no chain input: the dead letter receives rejected messages')
  assert.ok(nodes.find((n) => n.id === DEADLETTER_ID).position.x > encoder.position.x)
  assert.deepEqual(roundTrip(def), def)
})

test('flows without any encoding have no Encode node and save unchanged', () => {
  const plain = { ...definition, sinks: [{ type: 'tcp', url: 'tcp://a:9001' }, { type: 'stdout' }], deadLetterSink: { type: 'stdout' } }
  const { nodes, edges } = definitionToGraph(plain)
  assert.equal(nodes.filter((n) => n.type === 'encode').length, 0)
  assert.equal(edges.filter((e) => e.target === DEADLETTER_ID).length, 0)
  assert.deepEqual(roundTrip(plain), plain)
  const { deadLetterSink: _none, ...noDeadLetter } = plain
  assert.deepEqual(roundTrip(noDeadLetter), noDeadLetter)
})

test('connecting a sink to another Encode node moves its encoding; disconnecting removes it', () => {
  const { nodes, edges } = definitionToGraph(definition)
  const [tcp, stdout, http] = nodes.filter((n) => n.type === 'sink' && !n.data.isDeadLetter)
  const fields = encoderOfSink(nodes, edges, http.id)
  // Move tcp from gzip+base64 to fields, as the canvas does: drop its incoming edge, append the new one.
  const moved = [...edges.filter((e) => e.target !== tcp.id), { id: 'moved', source: fields.id, target: tcp.id }]
  let back = graphToDefinition('encoded', nodes, moved)
  assert.deepEqual(back.sinks.map((s) => s.encoding), [undefined, { format: 'fields' }, gzipWrapped, { format: 'fields' }], 'reconnected last, tcp is now the last sink')
  assert.equal(back.sinks[3].type, 'tcp')
  // Disconnect stdout from the chain and connect it directly again (no encoding) is a no-op; connect http directly: encoding gone.
  const direct = [...edges.filter((e) => e.target !== http.id), { id: 'direct', source: 'keep', target: http.id }]
  back = graphToDefinition('encoded', nodes, direct)
  assert.equal(back.sinks.find((s) => s.type === 'http').encoding, undefined)
  assert.deepEqual(back.deadLetterSink.encoding, { format: 'fields' }, 'the dead letter keeps the encoder')
  void stdout
})

test('deleting an Encode node with sinks attached reconnects them to the chain in place (canvas behaviour): same order, no encoding', () => {
  const { nodes, edges } = definitionToGraph(definition)
  const [tcp] = nodes.filter((n) => n.type === 'sink' && !n.data.isDeadLetter)
  const encoder = encoderOfSink(nodes, edges, tcp.id)
  const kept = nodes.filter((n) => n.id !== encoder.id)
  // As FlowCanvas.onNodesDelete does: each outgoing edge is replaced where it was by predecessor -> target.
  const rewired = edges.flatMap((e) => e.target === encoder.id ? [] : e.source === encoder.id ? [{ id: `e-keep-${e.target}`, source: 'keep', target: e.target }] : [e])
  const back = JSON.parse(JSON.stringify(graphToDefinition('encoded', kept, rewired)))
  assert.deepEqual(back.sinks.map((s) => s.type), ['tcp', 'stdout', 'http', 'udp'])
  assert.deepEqual(back.sinks.map((s) => s.encoding), [undefined, undefined, { format: 'fields' }, undefined])
})

test('an Encode node that feeds no sink is ignored; unfinished encoder steps are dropped', () => {
  const { nodes, edges } = definitionToGraph(definition)
  const dangling = { id: 'dangling', type: 'encode', position: { x: 0, y: 0 }, data: { encoding: { format: 'raw' } } }
  const back = graphToDefinition('encoded', [...nodes, dangling], [...edges, { id: 'e-keep-dangling', source: 'keep', target: 'dangling' }])
  assert.equal(back.sinks.length, 4)
  const [tcp] = nodes.filter((n) => n.type === 'sink' && !n.data.isDeadLetter)
  const encoder = encoderOfSink(nodes, edges, tcp.id)
  const edited = nodes.map((n) => n.id === encoder.id ? { ...n, data: { encoding: { format: 'wrapped', encoders: [{ type: 'gzip' }, { type: '' }, { type: 'python', python: { function: '' } }] } } } : n)
  assert.deepEqual(graphToDefinition('encoded', edited, edges).sinks[0].encoding, { format: 'wrapped', encoders: [{ type: 'gzip' }] })
  const emptied = nodes.map((n) => n.id === encoder.id ? { ...n, data: { encoding: { format: 'raw', encoders: [] } } } : n)
  assert.deepEqual(graphToDefinition('encoded', emptied, edges).sinks[0].encoding, { format: 'raw' }, 'an empty encoder list is omitted')
})

test('invalid Encode wiring is rejected with a usable error', () => {
  const { nodes, edges } = definitionToGraph(definition)
  const [tcp, stdout] = nodes.filter((n) => n.type === 'sink' && !n.data.isDeadLetter)
  const encoder = encoderOfSink(nodes, edges, tcp.id)
  assert.throws(() => graphToDefinition('encoded', nodes, [...edges, { id: 'x', source: encoder.id, target: 'keep' }]), /Encode step can only connect to sinks/)
  assert.throws(() => graphToDefinition('encoded', nodes, [...edges, { id: 'x', source: 'keep', target: DEADLETTER_ID }]), /only an Encode step can connect to the dead-letter sink/)
  assert.throws(() => graphToDefinition('encoded', nodes, [...edges, { id: 'x', source: encoder.id, target: stdout.id }]), /connected more than once/)
  // A floating Encode node may feed the dead letter but not a normal sink.
  const floating = { id: 'floating', type: 'encode', position: { x: 0, y: 0 }, data: { encoding: { format: 'raw' } } }
  const rewired = [...edges.filter((e) => e.target !== stdout.id), { id: 'x', source: 'floating', target: stdout.id }]
  assert.throws(() => graphToDefinition('encoded', [...nodes, floating], rewired), /must itself be connected from the last stage/)
  // Mid-chain fan-out to an Encode node is a mixed target, like a sink.
  assert.throws(() => graphToDefinition('encoded', nodes, [...edges, { id: 'x', source: 'parse', target: encoder.id }]), /mix of stage and sink targets/)
})
