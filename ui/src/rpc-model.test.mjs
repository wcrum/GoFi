import assert from 'node:assert/strict'
import { test } from 'node:test'
import { fromJson } from '@bufbuild/protobuf'
import { flowToProto, flowFromProto, trafficFromProto, simulationFromProto } from './rpc-model.ts'
import { SimulateResultSchema, TrafficEventSchema } from './gen/gofi/v1/gofi_pb.ts'

test('typed flow transport preserves direct destinations, decoding, service exposure, and Aster', () => {
  const flow = { name: 'events', source: { listen: { port: 9000, network: 'tcp', service: { type: 'NodePort', nodePort: 30090 } } }, framing: { mode: 'line' }, parser: { format: 'json', decoders: [{ type: 'base64' }, { type: 'gzip' }] }, stages: [{ id: 'accept', kind: 'filter', type: 'cel', expression: 'fields["ok"]' }], sinks: [{ type: 'aster', url: 'http://aster:8080', aster: { topic: 'events', scope: 'team', tokenFile: '/run/aster/token' } }] }
  assert.deepEqual(flowFromProto(flowToProto(flow)), flow)
})
test('empty repeated flow fields remain usable arrays in UI', () => {
  const flow = flowFromProto(flowToProto({ name: 'raw', source: { listen: { network: 'udp', port: 9099 } }, framing: { mode: 'raw' }, parser: { format: 'none' }, stages: [], sinks: [] }))
  assert.deepEqual(flow.stages, [])
  assert.deepEqual(flow.sinks, [])
})
test('streaming traffic preserves false, zero, arbitrary fields, and empty collections', () => {
  const event = trafficFromProto(fromJson(TrafficEventSchema, { ts: 'now', fields: { zero: 0, flag: false, nested: { arr: [1, 'two'] } } }))
  assert.equal(event.dropped, false)
  assert.deepEqual(event.fields, { zero: 0, flag: false, nested: { arr: [1, 'two'] } })
  assert.deepEqual(event.meta, {})
})
test('simulation false outcomes stay false and empty field snapshots remain objects', () => {
  const result = simulationFromProto(fromJson(SimulateResultSchema, { stages: [{ id: 'reject' }], sinks: [{ type: 'stdout' }] }))
  assert.equal(result.dropped, false)
  assert.equal(result.stages[0].pass, false)
  assert.equal(result.sinks[0].sent, false)
  assert.equal(result.sinks[0].index, 0)
  assert.deepEqual(result.parsedFields, {})
  assert.deepEqual(result.stages[0].fields, {})
})
test('typed Python function references survive all four extension points', () => {
  const ref = { library: 'telemetry', function: 'decode' }
  const flow = { name: 'python', source: { listen: { network: 'tcp', port: 9100 } }, framing: { mode: 'python', python: { ...ref, function: 'split' } }, parser: { format: 'python', python: ref, decoders: [{ type: 'python', python: { ...ref, function: 'unwrap' } }] }, stages: [{ id: 'normalize', kind: 'python', python: { ...ref, function: 'normalize' } }], sinks: [{ type: 'python', python: { ...ref, function: 'publish' } }] }
  assert.deepEqual(flowFromProto(flowToProto(flow)), flow)
})
test('Kubernetes selector targets survive typed flow transport', () => {
  const flow = { name: 'pods', source: { listen: { network: 'tcp', port: 9200 } }, framing: { mode: 'line' }, parser: { format: 'none' }, stages: [], sinks: [{ type: 'udp', kubernetes: { namespace: 'gofi', selector: { app: 'receiver', tier: 'demo' }, port: 9001 } }] }
  assert.deepEqual(flowFromProto(flowToProto(flow)), flow)
})

test('a flow without a dead-letter sink encodes (undefined fields are dropped)', async () => {
  const { flowToProto } = await import('./rpc-model.ts')
  const flow = {
    name: 'no-dead-letter', source: { listen: { network: 'tcp', port: 9000, service: { type: 'NodePort', port: undefined, nodePort: undefined } } },
    framing: { mode: 'line' }, parser: { format: 'json' }, stages: [], sinks: [{ type: 'stdout' }], deadLetterSink: undefined,
  }
  const proto = flowToProto(flow)
  assert.equal(proto.name, 'no-dead-letter')
  assert.equal(proto.deadLetterSink, undefined)
  assert.equal(proto.source.listen.service.type, 'NodePort')
})
