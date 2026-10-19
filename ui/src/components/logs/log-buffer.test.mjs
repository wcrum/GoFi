import { test } from 'node:test'
import assert from 'node:assert/strict'
import { Batcher, appendBounded, capture, matches, windowRows } from './log-buffer.ts'

const event = (over = {}) => ({ ts: '2026-10-17T12:00:00.000Z', dropped: false, fields: { message: 'Hello World', level: 'INFO' }, meta: { source: 'syslog' }, raw: 'Hello World', ...over })

test('capture precomputes level and lowercase search text once', () => {
  const item = capture(1, 'edge', event())
  assert.equal(item.level, 'info')
  assert.ok(item.search.includes('hello world'))
  assert.ok(item.search.includes('edge'))
  assert.ok(item.search.includes('syslog'))
  assert.equal(capture(2, 'edge', event({ dropped: true, droppedBy: 'drop-noise', fields: {} })).level, 'warn')
  assert.equal(capture(3, 'edge', event({ meta: { parseError: 'bad json' }, fields: {} })).level, 'error')
})

test('matches applies severity, status, and query against precomputed text', () => {
  const ok = capture(1, 'edge', event())
  const rejected = capture(2, 'edge', event({ dropped: true, droppedBy: 'filter', fields: { level: 'error' } }))
  const all = { query: '', severity: 'all', status: 'all' }
  assert.equal(matches(ok, all), true)
  assert.equal(matches(ok, { ...all, severity: 'error' }), false)
  assert.equal(matches(rejected, { ...all, severity: 'error' }), true)
  assert.equal(matches(ok, { ...all, status: 'rejected' }), false)
  assert.equal(matches(rejected, { ...all, status: 'accepted' }), false)
  assert.equal(matches(ok, { ...all, query: 'HELLO' }), true)
  assert.equal(matches(ok, { ...all, query: 'nope' }), false)
  assert.equal(matches(rejected, { ...all, query: 'filter' }), true, 'droppedBy is searchable')
})

test('appendBounded keeps the newest max items', () => {
  assert.deepEqual(appendBounded([1, 2], [3], 5), [1, 2, 3])
  assert.deepEqual(appendBounded([1, 2, 3], [4, 5, 6], 4), [3, 4, 5, 6])
  const same = [1]
  assert.equal(appendBounded(same, [], 4), same, 'empty batch returns the same array')
})

test('batcher flushes many pushes as one batch per schedule tick', () => {
  const flushed = []
  let run
  const batcher = new Batcher(batch => flushed.push(batch), fn => { run = fn })
  batcher.push('a'); batcher.push('b'); batcher.push('c')
  assert.equal(flushed.length, 0)
  assert.equal(batcher.size, 3)
  run()
  assert.deepEqual(flushed, [['a', 'b', 'c']])
  batcher.push('d')
  batcher.clear()
  run()
  assert.equal(flushed.length, 1, 'cleared pending events are not flushed')
  batcher.push('e'); run()
  assert.deepEqual(flushed[1], ['e'])
})

test('windowRows renders only the visible slice with spacers', () => {
  const none = windowRows(0, 0, 400, 24, new Map())
  assert.deepEqual(none, { start: 0, end: 0, top: 0, bottom: 0 })
  const top = windowRows(1000, 0, 240, 24, new Map(), 2)
  assert.equal(top.start, 0)
  assert.equal(top.end, 12, '10 visible + 2 overscan')
  assert.equal(top.top, 0)
  assert.equal(top.bottom, (1000 - 12) * 24)
  const middle = windowRows(1000, 24 * 500, 240, 24, new Map(), 2)
  assert.equal(middle.start, 498)
  assert.equal(middle.end, 512)
  assert.equal(middle.top, 498 * 24)
  assert.equal(middle.top + (middle.end - middle.start) * 24 + middle.bottom, 1000 * 24, 'spacers plus rows span every row')
  // An expanded row above the viewport shifts everything by its extra height.
  const expanded = windowRows(1000, 24 * 500, 240, 24, new Map([[10, 300]]), 0)
  assert.equal(expanded.start, 500 - Math.ceil(300 / 24), 'rows shift down by the expanded height')
  assert.equal(expanded.top, Array.from({ length: expanded.start }, (_, i) => 24 + (i === 10 ? 300 : 0)).reduce((a, b) => a + b, 0))
  const end = windowRows(100, 24 * 100, 240, 24, new Map(), 0)
  assert.equal(end.end, 100)
  assert.equal(end.bottom, 0)
})
