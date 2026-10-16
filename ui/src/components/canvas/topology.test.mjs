import assert from 'node:assert/strict'
import { test } from 'node:test'
import { flowsToTopology, destinationKey } from './topology.ts'
const flow = (name, port) => ({ name, source: { listen: { network: 'tcp', port, service: { type: 'NodePort', nodePort: port + 20000 } } }, framing: { mode: 'line' }, parser: { format: 'json', decoders: ['base64', 'gzip'] }, stages: [{ id: 'filter', kind: 'filter', type: 'cel', expression: 'true' }], sinks: [{ type: 'tcp', url: 'tcp://collector:9001' }] })
test('all flows remain visible with isolated stage ids and shared direct destinations', () => {
  const graph = flowsToTopology([flow('one', 9000), flow('two', 9001)])
  assert.equal(graph.nodes.filter(n => n.data.role === 'source').length, 2)
  assert.equal(graph.nodes.filter(n => n.data.role === 'destination').length, 1)
  assert.equal(new Set(graph.nodes.map(n => n.id)).size, graph.nodes.length)
  assert.equal(graph.nodes.filter(n => n.data.role === 'stage').length, 2)
  assert.match(graph.nodes.find(n => n.data.title === 'one').data.detail, /NodePort 29000/)
  assert.match(graph.nodes.find(n => n.data.role === 'decode').data.detail, /base64 → gzip → json/)
  const ids = new Set(graph.nodes.map(n => n.id))
  for (const edge of graph.edges) { assert.ok(ids.has(edge.source)); assert.ok(ids.has(edge.target)) }
})
test('conditional and dead-letter paths remain configured paths, and topic routes stay distinct', () => {
  const def = flow('routes', 9000)
  def.sinks = [{ type: 'aster', url: 'http://aster:8080', aster: { topic: 'one', scope: 'team' }, when: 'fields["accepted"]' }, { type: 'aster', url: 'http://aster:8080', aster: { topic: 'two', scope: 'team' } }]
  def.deadLetterSink = { type: 'stdout' }
  const graph = flowsToTopology([def])
  assert.equal(graph.nodes.filter(n => n.data.role === 'destination').length, 3)
  assert.ok(graph.edges.some(e => e.label === 'When: fields["accepted"]'))
  assert.ok(graph.edges.some(e => e.data.deadLetter && e.label === 'Rejected events' && e.style.strokeDasharray))
  assert.notEqual(destinationKey(def.sinks[0]), destinationKey(def.sinks[1]))
})
test('empty inventory produces no invented routes', () => assert.deepEqual(flowsToTopology([]), { nodes: [], edges: [] }))
test('inventory order cannot move flows or change destination identity', () => {
  const one = flow('one', 9000), two = flow('two', 9001)
  assert.deepEqual(flowsToTopology([one,two]), flowsToTopology([two,one]))
})
test('pod selector destinations are distinct from direct endpoints and label order is stable', () => {
  const first = { type: 'tcp', kubernetes: { namespace: 'gofi', selector: { app: 'receiver', tier: 'demo' }, port: 9000 } }
  const second = { ...first, kubernetes: { ...first.kubernetes, selector: { tier: 'demo', app: 'receiver' } } }
  assert.equal(destinationKey(first), destinationKey(second))
  assert.notEqual(destinationKey(first), destinationKey({ type: 'tcp', url: 'tcp://receiver:9000' }))
  const graph = flowsToTopology([{ ...flow('selectors', 9000), sinks: [first] }])
  const target = graph.nodes.find(node => node.data.role === 'destination')
  assert.match(target.data.detail, /app=receiver/)
  assert.match(target.data.detail, /:9000/)
})
