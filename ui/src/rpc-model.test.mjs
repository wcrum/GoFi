import assert from 'node:assert/strict'
import { test } from 'node:test'
import { fromJson } from '@bufbuild/protobuf'
import { listenerToProto, listenerFromProto, trafficFromProto, simulationFromProto } from './rpc-model.ts'
import { SimulateResultSchema, TrafficEventSchema } from './gen/gofi/v1/gofi_pb.ts'

test('typed listener transport preserves direct destinations, decoding, service exposure, and Aster', () => {
  const listener = { name: 'events', source: { port: 9000, network: 'tcp', service: { type: 'NodePort', nodePort: 30090 } }, framing: { mode: 'line' }, parser: { format: 'json', decoders: ['base64', 'gzip'] }, stages: [{ id: 'accept', kind: 'filter', type: 'cel', expression: 'fields["ok"]' }], sinks: [{ type: 'aster', url: 'http://aster:8080', aster: { topic: 'events', scope: 'team', tokenFile: '/run/aster/token' } }] }
  assert.deepEqual(listenerFromProto(listenerToProto(listener)), listener)
})
test('empty repeated listener fields remain usable arrays in UI', () => {
  const listener = listenerFromProto(listenerToProto({ name: 'raw', source: { network: 'udp', port: 9099 }, framing: { mode: 'raw' }, parser: { format: 'none' }, stages: [], sinks: [] }))
  assert.deepEqual(listener.stages, [])
  assert.deepEqual(listener.sinks, [])
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
  const listener = { name: 'python', source: { network: 'tcp', port: 9100, python: { ...ref, function: 'receive' } }, framing: { mode: 'line' }, parser: { format: 'python', python: ref }, stages: [{ id: 'normalize', kind: 'python', python: { ...ref, function: 'normalize' } }], sinks: [{ type: 'python', python: { ...ref, function: 'publish' } }] }
  assert.deepEqual(listenerFromProto(listenerToProto(listener)), listener)
})
test('Kubernetes selector targets survive typed listener transport', () => {
  const listener = { name: 'pods', source: { network: 'tcp', port: 9200 }, framing: { mode: 'line' }, parser: { format: 'none' }, stages: [], sinks: [{ type: 'udp', kubernetes: { namespace: 'gofi', selector: { app: 'receiver', tier: 'demo' }, port: 9001 } }] }
  assert.deepEqual(listenerFromProto(listenerToProto(listener)), listener)
})
