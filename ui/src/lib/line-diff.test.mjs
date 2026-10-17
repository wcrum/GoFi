import { test } from 'node:test'
import assert from 'node:assert/strict'
import { lineDiff } from './line-diff.ts'

test('marks added and removed lines around common ones', () => {
  const diff = lineDiff('spec:\n  allow:\n  - a\n', 'spec:\n  allow:\n  - a\n  - b\n')
  assert.deepEqual(diff.map(l => l.kind), ['same', 'same', 'same', 'added'])
  assert.equal(diff[3].text, '  - b')
})

test('a replaced line is a removal and an addition', () => {
  assert.deepEqual(lineDiff('x\ny\nz', 'x\nq\nz').map(l => `${l.kind}:${l.text}`), ['same:x', 'removed:y', 'added:q', 'same:z'])
})

test('an empty side is all additions or removals', () => {
  assert.deepEqual(lineDiff('', 'a\nb').map(l => l.kind), ['added', 'added'])
  assert.deepEqual(lineDiff('a', '').map(l => l.kind), ['removed'])
})
