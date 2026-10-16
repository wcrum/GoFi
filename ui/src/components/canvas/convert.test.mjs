import assert from 'node:assert/strict'
import { test } from 'node:test'
import { listenerToFlow, flowToListener, orderedSinkNodeIds } from './convert.ts'

const definition = {
  name: 'events',
  source: { port: 9000, network: 'tcp', service: { type: 'NodePort', port: 9100, nodePort: 30900 } },
  framing: { mode: 'line' },
  parser: { format: 'json', decoders: ['base64', 'gzip'], maxDecodedBytes: 262144, errorPolicy: 'drop' },
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
  const { nodes, edges } = listenerToFlow(definition)
  // Node array order can change after dragging; graph connections define execution order.
  const actual = flowToListener(definition.name, [...nodes].reverse(), edges)
  assert.deepEqual(JSON.parse(JSON.stringify(actual)), definition)
  const ids = orderedSinkNodeIds(nodes, edges)
  assert.equal(ids.length, 3)
  assert.equal(nodes.find((node) => node.id === ids[1]).data.sink.type, 'udp')
})

test('decoder editor whitespace is normalized before submission', () => {
  const { nodes, edges } = listenerToFlow({ ...definition, parser: { format: 'json', decoders: ['base64', ' gzip ', ''] } })
  assert.deepEqual(flowToListener('events', nodes, edges).parser.decoders, ['base64', 'gzip'])
})

test('invalid graph cycles fail with a usable error instead of changing stage order', () => {
  const { nodes, edges } = listenerToFlow(definition)
  const invalid = edges.filter((edge) => edge.source !== 'transform')
  invalid.push({ id: 'cycle', source: 'transform', target: 'filter' })
  assert.throws(() => flowToListener('events', nodes, invalid), /cycle/)
})

test('fan-out in the middle of the stage chain is rejected', () => {
  const { nodes, edges } = listenerToFlow(definition)
  const sink = nodes.find((node) => node.type === 'sink' && !node.data.isDeadLetter)
  assert.throws(() => flowToListener('events', nodes, [...edges, { id: 'branch', source: 'filter', target: sink.id }]), /mix of stage and sink targets/)
})
test('Python library selections survive listener graph editing', () => {
  const listener = { ...definition, source: { network: 'tcp', port: 9100, python: { library: 'telemetry', function: 'receive' } }, parser: { format: 'python', python: { library: 'telemetry', function: 'decode' } }, stages: [{ id: 'normalize', kind: 'python', python: { library: 'telemetry', function: 'normalize' } }], sinks: [{ type: 'python', python: { library: 'telemetry', function: 'publish' } }] }
  const graph = listenerToFlow(listener)
  assert.deepEqual(JSON.parse(JSON.stringify(flowToListener(listener.name, graph.nodes, graph.edges))), listener)
})
