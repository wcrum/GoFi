import { test } from 'node:test'
import assert from 'node:assert/strict'
import { createInitialLoader, errorText } from './initial-load.ts'

/** A controllable clock and pending-wait queue, so timing is deterministic. */
function harness() {
  let clock = 0
  const waits = []
  const changes = []
  return {
    now: () => clock,
    advance: (ms) => { clock += ms },
    wait: (ms) => new Promise(resolve => waits.push({ ms, resolve })),
    waits,
    /** Resolves every pending minimum-display wait. */
    flushWaits: async () => { for (const w of waits.splice(0)) { clock += w.ms; w.resolve() }; await Promise.resolve() },
    changes,
    onChange: (state, key) => changes.push({ ...state, key }),
  }
}
const tick = () => new Promise(resolve => setImmediate(resolve))

test('a fast response still shows loading for the minimum time', async () => {
  const h = harness()
  const loader = createInitialLoader(async (key) => `data:${key}`, { minMs: 150, now: h.now, wait: h.wait, onChange: h.onChange })
  const done = loader.load('a')
  await tick()
  assert.deepEqual(h.changes.map(c => c.status), ['loading'])
  assert.equal(h.waits.length, 1)
  assert.equal(h.waits[0].ms, 150) // the loader resolved instantly, so the whole minimum remains
  await h.flushWaits()
  await done
  assert.deepEqual(loader.get(), { status: 'ready', data: 'data:a' })
  assert.equal(h.changes.at(-1).key, 'a')
})

test('a slow response is shown as soon as it lands', async () => {
  const h = harness()
  let resolve
  const loader = createInitialLoader(() => new Promise(r => { resolve = r }), { minMs: 150, now: h.now, wait: h.wait, onChange: h.onChange })
  const done = loader.load('a')
  h.advance(400)
  resolve(1)
  await done
  assert.equal(h.waits.length, 0) // no extra delay once the minimum has passed
  assert.equal(loader.get().status, 'ready')
})

test('a result for a superseded key is dropped', async () => {
  const h = harness()
  const pending = {}
  const loader = createInitialLoader((key) => new Promise(r => { pending[key] = r }), { minMs: 0, now: h.now, wait: h.wait, onChange: h.onChange })
  const first = loader.load('a')
  const second = loader.load('b')
  pending.b('B')
  await second
  assert.deepEqual(loader.get(), { status: 'ready', data: 'B' })
  pending.a('A') // arrives late
  await first
  assert.deepEqual(loader.get(), { status: 'ready', data: 'B' })
  assert.deepEqual(h.changes.map(c => c.key), ['a', 'b', 'b'])
})

test('errors are reported with their message and reload retries', async () => {
  const h = harness()
  let attempts = 0
  const loader = createInitialLoader(async () => { attempts += 1; if (attempts === 1) throw new Error('boom'); return attempts }, { minMs: 0, now: h.now, wait: h.wait, onChange: h.onChange })
  await loader.load('a')
  assert.deepEqual(loader.get(), { status: 'error', error: 'boom' })
  await loader.reload()
  assert.deepEqual(loader.get(), { status: 'ready', data: 2 })
  // Reloading a ready page keeps the data on screen: no loading state in between.
  await loader.reload()
  assert.deepEqual(h.changes.map(c => c.status), ['loading', 'error', 'loading', 'ready', 'ready'])
  assert.equal(loader.get().data, 3)
})

test('reload before any load and after dispose are no-ops', async () => {
  const h = harness()
  const loader = createInitialLoader(async () => 1, { minMs: 0, now: h.now, wait: h.wait, onChange: h.onChange })
  await loader.reload()
  assert.equal(h.changes.length, 0)
  const load = loader.load('a')
  loader.dispose()
  await load
  assert.equal(h.changes.map(c => c.status).join(','), 'loading') // the ready transition never reached the listener
})

test('errorText reads Error messages and stringifies the rest', () => {
  assert.equal(errorText(new Error('nope')), 'nope')
  assert.equal(errorText('plain'), 'plain')
})
