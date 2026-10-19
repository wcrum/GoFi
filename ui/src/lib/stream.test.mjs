import { test } from 'node:test'
import assert from 'node:assert/strict'
import { reconnectDelay, DropCounter, IdleWatchdog, shared, RECONNECT_CAP_MS, TRAFFIC_IDLE_TIMEOUT_MS, TRAFFIC_HEARTBEAT_MS, STATS_IDLE_TIMEOUT_MS, STATS_INTERVAL_MS } from './stream.ts'
import { sampleDelta, RollingHistory, totals } from './flow-stats.ts'

test('reconnect delay grows, caps, and jitters within ±50%', () => {
  assert.equal(reconnectDelay(0, () => 0), 500)
  assert.equal(reconnectDelay(0, () => 1), 1500)
  assert.equal(reconnectDelay(1, () => 0.5), 2000)
  assert.equal(reconnectDelay(20, () => 0), RECONNECT_CAP_MS / 2)
  assert.equal(reconnectDelay(20, () => 1), RECONNECT_CAP_MS * 1.5)
  const samples = new Set(Array.from({ length: 50 }, () => reconnectDelay(3)))
  assert.ok(samples.size > 1, 'jitter produces different delays')
  for (const value of samples) assert.ok(value >= 4000 && value <= 12000, `delay ${value} outside ±50% of 8s`)
})

test('idle watchdog fires once after silence and is quiet while touched', async () => {
  let fired = 0
  const watchdog = new IdleWatchdog(30, () => fired++)
  assert.equal(watchdog.armed, true, 'armed from construction (a request that never gets headers counts as idle)')
  for (let i = 0; i < 4; i++) { await new Promise(resolve => setTimeout(resolve, 15)); watchdog.touch() }
  assert.equal(fired, 0, 'a stream that keeps delivering never trips it')
  await new Promise(resolve => setTimeout(resolve, 60))
  assert.equal(fired, 1)
  assert.equal(watchdog.armed, false, 'one-shot until touched again')
  await new Promise(resolve => setTimeout(resolve, 40))
  assert.equal(fired, 1)
  watchdog.touch(); watchdog.stop()
  await new Promise(resolve => setTimeout(resolve, 40))
  assert.equal(fired, 1, 'stop() cancels the countdown')
  assert.equal(new IdleWatchdog(0, () => {}).armed, false, 'a zero timeout disables it')
})

test('idle timeouts allow several missed server heartbeats', () => {
  assert.ok(TRAFFIC_IDLE_TIMEOUT_MS >= 2 * TRAFFIC_HEARTBEAT_MS)
  assert.ok(STATS_IDLE_TIMEOUT_MS >= 3 * STATS_INTERVAL_MS)
})

test('drop counter accumulates stream dropped fields and reports deltas', () => {
  const drops = new DropCounter()
  drops.add(0); drops.add(undefined); drops.add(3n); drops.add(2)
  assert.equal(drops.total, 5)
  assert.equal(drops.take(), 5)
  assert.equal(drops.take(), 0)
  drops.add(1)
  assert.equal(drops.take(), 1)
  assert.equal(drops.total, 6)
})

test('shared resource opens once for many subscribers and closes after linger', async () => {
  let opened = 0, closed = 0, publish
  const resource = shared((emit) => { opened++; publish = emit; return () => { closed++ } }, 10)
  const seenA = [], seenB = []
  const stopA = resource.subscribe(value => seenA.push(value))
  publish('first')
  const stopB = resource.subscribe(value => seenB.push(value))
  assert.equal(opened, 1)
  assert.deepEqual(seenB, ['first'], 'late subscriber gets the latest value')
  publish('second')
  assert.deepEqual(seenA, ['first', 'second'])
  stopA()
  assert.equal(resource.open, true)
  stopB()
  assert.equal(closed, 0, 'lingers after the last subscriber')
  await new Promise(resolve => setTimeout(resolve, 30))
  assert.equal(closed, 1)
  resource.subscribe(() => {})
  assert.equal(opened, 2, 'reopens for a subscriber after close')
})

test('stats deltas treat a falling counter as a runtime restart', () => {
  const previous = { flow: 'a', up: true, observed: 100, rejected: 10, parseErrors: 1, delivered: 90, sinkFailures: 0, tapDropped: 0 }
  assert.deepEqual(sampleDelta(undefined, previous), { observed: 0, dropped: 0, parseErrors: 0 })
  assert.deepEqual(sampleDelta(previous, { ...previous, observed: 130, rejected: 12, parseErrors: 1 }), { observed: 30, dropped: 2, parseErrors: 0 })
  assert.deepEqual(sampleDelta(previous, { ...previous, observed: 5, rejected: 0, parseErrors: 0 }), { observed: 5, dropped: 0, parseErrors: 0 })
})

test('rolling history keeps the last N seconds oldest-first', () => {
  const history = new RollingHistory(3)
  history.add(10, { observed: 1, dropped: 0, parseErrors: 0 })
  history.add(10, { observed: 2, dropped: 1, parseErrors: 0 })
  history.add(12, { observed: 4, dropped: 0, parseErrors: 1 })
  assert.deepEqual(history.series(12).map(bucket => bucket.observed), [3, 0, 4])
  assert.deepEqual(history.series(13).map(bucket => bucket.observed), [0, 4, 0])
  history.add(30, { observed: 1, dropped: 0, parseErrors: 0 })
  assert.deepEqual(history.series(30).map(bucket => bucket.observed), [0, 0, 1], 'old seconds are forgotten')
})

test('totals sum per-flow counters', () => {
  const a = { flow: 'a', up: true, observed: 10, rejected: 1, parseErrors: 2, delivered: 9, sinkFailures: 0, tapDropped: 0 }
  const b = { ...a, flow: 'b', observed: 5, rejected: 0, parseErrors: 0 }
  assert.deepEqual(totals([a, b]), { observed: 15, dropped: 1, parseErrors: 2 })
})
