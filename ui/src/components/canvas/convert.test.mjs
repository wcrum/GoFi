import assert from 'node:assert/strict'
import { test } from 'node:test'
import { definitionToGraph, graphToDefinition, orderedSinkNodeIds } from './convert.ts'

const definition = {
  name: 'events',
  source: { listen: { port: 9000, network: 'tcp', service: { type: 'NodePort', port: 9100, nodePort: 30900 } } },
  framing: { mode: 'line' },
  parser: { format: 'json', decoders: [{ type: 'base64' }, { type: 'gzip' }], maxDecodedBytes: 262144, errorPolicy: 'drop' },
  stages: [
    { id: 'filter', kind: 'filter', type: 'regex', field: 'level', expression: '^info$' },
    { id: 'transform', kind: 'transform', transformOp: 'set', field: 'accepted', valueExpr: 'true' },
  ],
  sinks: [
    { type: 'tcp', url: 'tcp://other-container:9001', delimiter: '\n' },
    { type: 'udp', url: 'udp://collector.namespace.svc.cluster.local:9002', when: 'fields["accepted"] == true' },
    { type: 'aster', url: 'http://127.0.0.1:8181', aster: { topic: 'events', scope: 'team', tokenFile: '/run/secrets/aster/token' } },
  ],
  deadLetterSink: { type: 'stdout' },
}

test('graph round trip retains exposure, ordered codecs, filters, direct destinations, and Aster', () => {
  const { nodes, edges } = definitionToGraph(definition)
  // Node array order can change after dragging; graph connections define execution order.
  const actual = graphToDefinition(definition.name, [...nodes].reverse(), edges)
  assert.deepEqual(JSON.parse(JSON.stringify(actual)), definition)
  const ids = orderedSinkNodeIds(nodes, edges)
  assert.equal(ids.length, 3)
  assert.equal(nodes.find((node) => node.id === ids[1]).data.sink.type, 'udp')
})

test('unfinished decoder steps are dropped before submission', () => {
  const steps = [{ type: 'base64' }, { type: '' }, { type: 'python', python: { library: 'lib', function: '' } }, { type: 'python', python: { library: 'lib', function: 'unwrap' } }]
  const { nodes, edges } = definitionToGraph({ ...definition, parser: { format: 'json', decoders: steps } })
  assert.deepEqual(graphToDefinition('events', nodes, edges).parser.decoders, [{ type: 'base64' }, { type: 'python', python: { library: 'lib', function: 'unwrap' } }])
})

test('invalid graph cycles fail with a usable error instead of changing stage order', () => {
  const { nodes, edges } = definitionToGraph(definition)
  const invalid = edges.filter((edge) => edge.source !== 'transform')
  invalid.push({ id: 'cycle', source: 'transform', target: 'filter' })
  assert.throws(() => graphToDefinition('events', nodes, invalid), /cycle/)
})

test('fan-out in the middle of the stage chain is rejected', () => {
  const { nodes, edges } = definitionToGraph(definition)
  const sink = nodes.find((node) => node.type === 'sink' && !node.data.isDeadLetter)
  assert.throws(() => graphToDefinition('events', nodes, [...edges, { id: 'branch', source: 'filter', target: sink.id }]), /mix of stage and sink targets/)
})
test('Python library selections survive flow graph editing', () => {
  const flow = { ...definition, source: { listen: { network: 'tcp', port: 9100 } }, framing: { mode: 'python', python: { library: 'telemetry', function: 'split' } }, parser: { format: 'python', python: { library: 'telemetry', function: 'decode' } }, stages: [{ id: 'normalize', kind: 'python', python: { library: 'telemetry', function: 'normalize' } }], sinks: [{ type: 'python', python: { library: 'telemetry', function: 'publish' } }] }
  const graph = definitionToGraph(flow)
  assert.deepEqual(JSON.parse(JSON.stringify(graphToDefinition(flow.name, graph.nodes, graph.edges))), flow)
})

test('frame, decode, and parse are separate optional steps between the source and the stages', () => {
  const { nodes, edges } = definitionToGraph(definition)
  const prefix = ['ingress', 'frame', 'decode', 'parse']
  assert.deepEqual(nodes.slice(0, 4).map((node) => node.type), prefix)
  assert.equal(nodes[0].deletable, false, 'the source is required')
  assert.ok(nodes.slice(1, 4).every((node) => node.deletable !== false), 'frame, decode, and parse can be deleted')
  for (let i = 0; i < prefix.length - 1; i++) {
    assert.ok(edges.some((e) => e.source === prefix[i] && e.target === prefix[i + 1]), `${prefix[i]} -> ${prefix[i + 1]}`)
  }
  assert.ok(edges.some((e) => e.source === 'parse' && e.target === 'filter'), 'stages start after parse')
  assert.deepEqual(nodes.find((n) => n.type === 'decode').data, { decoders: [{ type: 'base64' }, { type: 'gzip' }], maxDecodedBytes: 262144 })
  assert.equal(nodes.find((n) => n.type === 'ingress').data.framing, undefined, 'the source no longer carries framing')
})

test('absent steps mean whole-connection frames, no decoding, and no parsing', () => {
  const bare = { ...definition, framing: { mode: 'raw' }, parser: { format: 'none' } }
  const { nodes, edges } = definitionToGraph(bare)
  assert.deepEqual(nodes.filter((n) => ['frame', 'decode', 'parse'].includes(n.type)), [], 'nothing to show')
  assert.ok(edges.some((e) => e.source === 'ingress' && e.target === 'filter'), 'stages follow the source directly')
  const back = graphToDefinition('events', nodes, edges)
  assert.deepEqual(back.framing, { mode: 'raw' })
  assert.deepEqual(back.parser, { format: 'none' })
})

test('deleting a step falls back to its default and keeps the rest', () => {
  const { nodes, edges } = definitionToGraph(definition)
  // Remove Decode and reconnect frame -> parse, as the canvas does on delete.
  const kept = nodes.filter((n) => n.type !== 'decode')
  const rewired = [...edges.filter((e) => e.source !== 'decode' && e.target !== 'decode'), { id: 'e-frame-parse', source: 'frame', target: 'parse' }]
  const back = graphToDefinition('events', kept, rewired)
  assert.deepEqual(back.parser, { format: 'json', errorPolicy: 'drop' }, 'no decoders remain')
  assert.deepEqual(back.framing, { mode: 'line' })
})

test('steps out of order or disconnected are rejected', () => {
  const { nodes, edges } = definitionToGraph(definition)
  const swapped = edges.filter((e) => !['e-ingress-frame', 'e-frame-decode', 'e-decode-parse'].includes(e.id))
  swapped.push({ id: 'a', source: 'ingress', target: 'decode' }, { id: 'b', source: 'decode', target: 'frame' }, { id: 'c', source: 'frame', target: 'parse' })
  assert.throws(() => graphToDefinition('events', nodes, swapped), /order is source, frame, decode, parse/)
  const orphan = edges.filter((e) => e.id !== 'e-ingress-frame')
  orphan.push({ id: 'skip', source: 'ingress', target: 'decode' })
  assert.throws(() => graphToDefinition('events', nodes, orphan), /frame step must be connected/)
})

test('a flow with no stages connects its last step directly to its sinks', () => {
  const { nodes, edges } = definitionToGraph({ ...definition, stages: [] })
  const sinks = nodes.filter((n) => n.type === 'sink' && !n.data.isDeadLetter)
  assert.ok(sinks.every((sink) => edges.some((e) => e.source === 'parse' && e.target === sink.id)))
  assert.deepEqual(graphToDefinition('events', nodes, edges).stages, [])
})

test('graph round trip retains fetch and subscribe sources', () => {
  for (const source of [
    { fetch: { http: { url: 'https://api.example/items', method: 'GET', every: '10s', auth: { type: 'bearer', token: 't' } }, split: '$.items[*]' } },
    { subscribe: { nats: { url: 'nats://nats:4222', subject: 'sensors.>', queue: 'gofi' } } },
    { subscribe: { sse: { url: 'https://feed.example/events', headers: { 'X-Feed': 'a' } } } },
  ]) {
    const pulled = { ...definition, source, framing: { mode: 'raw' } }
    const { nodes, edges } = definitionToGraph(pulled)
    assert.deepEqual(JSON.parse(JSON.stringify(graphToDefinition(pulled.name, nodes, edges))).source, source)
  }
})
