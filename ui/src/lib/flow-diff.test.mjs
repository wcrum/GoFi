import test from 'node:test'
import assert from 'node:assert/strict'
import { diffFlows, fieldChanges, matchSinks, sinkIdentity } from './flow-diff.ts'

const base = {
  name: 'events',
  source: { listen: { port: 9000, network: 'tcp' } },
  framing: { mode: 'line' },
  parser: { format: 'json' },
  stages: [
    { id: 's1', kind: 'filter', type: 'cel', expression: 'true' },
    { id: 's2', kind: 'transform', transformOp: 'set', field: 'a', valueExpr: '1' },
  ],
  sinks: [{ type: 'stdout' }, { type: 'tcp', url: 'tcp://a:1' }],
}
const clone = (value) => structuredClone(value)
const byId = (diff) => Object.fromEntries(diff.nodes.map(n => [n.id, n]))
const edgeStatus = (diff) => Object.fromEntries(diff.edges.map(e => [`${e.source}->${e.target}`, e.status]))

test('identical flows are all unchanged', () => {
  const diff = diffFlows(base, clone(base))
  assert.ok(diff.nodes.every(n => n.status === 'unchanged'))
  assert.ok(diff.edges.every(e => e.status === 'unchanged'))
  assert.deepEqual(diff.counts, { added: 0, removed: 0, changed: 0, unchanged: 7 })
  assert.deepEqual(diff.nodes.map(n => n.id), ['ingress', 'frame', 'parse', 'stage:s1', 'stage:s2', 'sink:0', 'sink:1'])
})

test('a changed stage lists its changed fields', () => {
  const after = clone(base)
  after.stages[0].expression = 'fields.x > 1'
  const node = byId(diffFlows(base, after))['stage:s1']
  assert.equal(node.status, 'changed')
  assert.deepEqual(node.changes, [{ path: 'expression', before: 'true', after: 'fields.x > 1' }])
})

test('an added stage splices into the chain; edges re-route', () => {
  const after = clone(base)
  after.stages.splice(1, 0, { id: 's9', kind: 'dedup', keyExpr: 'fields.id' })
  const diff = diffFlows(base, after)
  const nodes = byId(diff)
  assert.equal(nodes['stage:s9'].status, 'added')
  assert.ok(nodes['stage:s9'].position.x > nodes['stage:s1'].position.x && nodes['stage:s9'].position.x < nodes['stage:s2'].position.x)
  const edges = edgeStatus(diff)
  assert.equal(edges['stage:s1->stage:s9'], 'added')
  assert.equal(edges['stage:s9->stage:s2'], 'added')
  assert.equal(edges['stage:s1->stage:s2'], 'removed')
})

test('a removed stage keeps its place and is marked removed', () => {
  const after = clone(base)
  after.stages.splice(0, 1)
  const diff = diffFlows(base, after)
  const nodes = byId(diff)
  assert.equal(nodes['stage:s1'].status, 'removed')
  assert.ok(nodes['stage:s1'].position.x < nodes['stage:s2'].position.x)
  assert.equal(edgeStatus(diff)['parse->stage:s1'], 'removed')
  assert.equal(edgeStatus(diff)['parse->stage:s2'], 'added')
})

test('prefix steps match by fixed id', () => {
  const after = clone(base)
  after.framing = { mode: 'raw' }
  after.decode = { decoders: [{ type: 'base64' }] }
  const nodes = byId(diffFlows(base, after))
  assert.equal(nodes.frame.status, 'removed')
  assert.equal(nodes.decode.status, 'added')
  assert.equal(nodes.parse.status, 'unchanged')
})

test('sinks match by identity regardless of order, then by type', () => {
  assert.deepEqual(matchSinks([{ type: 'stdout' }, { type: 'tcp', url: 'tcp://a:1' }], [{ type: 'tcp', url: 'tcp://a:1' }, { type: 'stdout' }]), [1, 0])
  assert.deepEqual(matchSinks([{ type: 'tcp', url: 'tcp://a:1' }], [{ type: 'tcp', url: 'tcp://b:2' }]), [0])
  assert.deepEqual(matchSinks([{ type: 'tcp', url: 'tcp://a:1' }], [{ type: 'udp', url: 'udp://a:1' }]), [-1])
  assert.notEqual(sinkIdentity({ type: 'tcp', url: 'a' }), sinkIdentity({ type: 'tcp', url: 'b' }))

  const after = clone(base)
  after.sinks = [{ type: 'tcp', url: 'tcp://b:2' }, { type: 'stdout' }, { type: 'nats', subject: 'x' }]
  const diff = diffFlows(base, after)
  const sinks = diff.nodes.filter(n => n.type === 'sink')
  assert.deepEqual(sinks.map(n => [n.status, n.data.sink.type]), [['changed', 'tcp'], ['unchanged', 'stdout'], ['added', 'nats']])
  assert.deepEqual(sinks[0].changes, [{ path: 'url', before: 'tcp://a:1', after: 'tcp://b:2' }])
  assert.equal(edgeStatus(diff)['stage:s2->sink:1'], 'unchanged')
})

test('a removed sink and the dead letter', () => {
  const after = clone(base)
  after.sinks = [{ type: 'stdout' }]
  after.deadLetterSink = { type: 'stdout' }
  const diff = diffFlows(base, after)
  const removed = diff.nodes.find(n => n.status === 'removed')
  assert.equal(removed.data.sink.url, 'tcp://a:1')
  assert.equal(edgeStatus(diff)[`stage:s2->${removed.id}`], 'removed')
  assert.equal(byId(diff).deadletter.status, 'added')
})

test('against an empty document everything is added or removed', () => {
  assert.ok(diffFlows(null, base).nodes.every(n => n.status === 'added'))
  const gone = diffFlows(base, null)
  assert.ok(gone.nodes.every(n => n.status === 'removed'))
  assert.ok(gone.edges.every(e => e.status === 'removed'))
  assert.equal(byId(gone)['stage:s1'].changes.find(c => c.path === 'expression').before, 'true')
})

test('fieldChanges treats absent and empty as equal and walks arrays', () => {
  assert.deepEqual(fieldChanges({ a: [], b: undefined, c: {} }, {}), [])
  assert.deepEqual(fieldChanges({ d: [{ type: 'gzip' }] }, { d: [{ type: 'gzip' }, { type: 'base64' }] }), [{ path: 'd[1].type', after: 'base64' }])
  assert.deepEqual(fieldChanges({ h: { x: '1' } }, { h: 'flat' }), [{ path: 'h', before: { x: '1' }, after: 'flat' }])
  assert.deepEqual(fieldChanges({ n: 1 }, { n: 2, m: true }), [{ path: 'm', after: true }, { path: 'n', before: 1, after: 2 }])
})

test('legacy parser decoders and top-level decode compare equal', () => {
  const current = clone(base)
  current.decode = { decoders: [{ type: 'base64' }] }
  const legacy = clone(base)
  legacy.parser = { ...legacy.parser, decoders: [{ type: 'base64' }] }
  const diff = diffFlows(legacy, current)
  assert.equal(diff.counts.added + diff.counts.removed + diff.counts.changed, 0)
})

test('parse stages match by id like any stage', () => {
  const before = clone(base)
  before.stages = [{ id: 'inner', kind: 'parse', parse: { from: 'fields["message"]', format: 'json' } }, ...before.stages]
  const after = clone(before)
  after.stages[0] = { ...after.stages[0], parse: { ...after.stages[0].parse, into: 'payload' } }
  after.stages.push({ id: 'again', kind: 'parse', parse: { from: 'fields["payload"]["body"]', format: 'xml' } })
  const nodes = byId(diffFlows(before, after))
  assert.equal(nodes['stage:inner'].status, 'changed')
  assert.deepEqual(nodes['stage:inner'].changes, [{ path: 'parse.into', after: 'payload' }])
  assert.equal(nodes['stage:again'].status, 'added')
})
