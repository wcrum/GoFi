import assert from 'node:assert/strict'
import { test } from 'node:test'
import { RateTracker, edgesTraveled, formatRate, particlesFor } from './traffic.ts'

const edges = [
  { id: 'a', source: 'ingress', target: 'parse' },
  { id: 'b', source: 'parse', target: 'filter' },
  { id: 'c', source: 'filter', target: 'sink-1' },
  { id: 'd', source: 'filter', target: 'sink-2' },
]

test('a delivered message travels every edge; a dropped one stops at the dropping node', () => {
  assert.deepEqual(edgesTraveled(edges, { dropped: false }), ['a', 'b', 'c', 'd'])
  assert.deepEqual(edgesTraveled(edges, { dropped: true, droppedBy: 'filter' }), ['a', 'b'])
  assert.deepEqual(edgesTraveled(edges, { dropped: true, droppedBy: 'parser' }), ['a'])
  assert.deepEqual(edgesTraveled(edges, { dropped: true, droppedBy: 'unknown-stage' }), [])
})

test('the dead-letter connection (from its Encode node) carries rejected messages only', () => {
  const withDeadLetter = [...edges, { id: 'e', source: 'encode-1', target: 'deadletter' }]
  assert.deepEqual(edgesTraveled(withDeadLetter, { dropped: false }), ['a', 'b', 'c', 'd'])
  assert.deepEqual(edgesTraveled(withDeadLetter, { dropped: true, droppedBy: 'filter' }), ['a', 'b', 'e'])
})

test('rates rise with traffic and decay to idle', () => {
  const tracker = new RateTracker()
  let now = performance.now()
  tracker.add('a', 10)
  now += 1000
  const busy = tracker.tick(now)
  assert.ok(busy.a > 5, `rate ${busy.a}`)
  let rate = busy.a
  for (let i = 0; i < 20; i++) {
    now += 500
    rate = tracker.tick(now).a ?? 0
  }
  assert.equal(rate, 0, 'idle edges drop out')
})

test('particles and labels scale with the rate', () => {
  assert.deepEqual(particlesFor(0), { count: 0, seconds: 0 })
  assert.ok(particlesFor(1000).count > particlesFor(1).count)
  assert.ok(particlesFor(1000).seconds < particlesFor(1).seconds)
  assert.equal(formatRate(0.4), '24/min')
  assert.equal(formatRate(0.5), '0.5/s')
  assert.equal(formatRate(1), '1.0/s')
  assert.equal(formatRate(2.5), '2.5/s')
  assert.equal(formatRate(1500), '1.5k/s')
})
