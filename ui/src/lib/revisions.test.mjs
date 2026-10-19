import test from 'node:test'
import assert from 'node:assert/strict'
import { previousRevision, relativeTime, revisionStamp, shortHash, sourceBadge } from './revisions.ts'

const now = new Date('2026-10-17T12:00:00Z')
const ago = (seconds) => new Date(now.getTime() - seconds * 1000)

test('relativeTime reads like Grafana', () => {
  assert.equal(relativeTime(ago(10), now), 'just now')
  assert.equal(relativeTime(ago(5 * 60), now), '5 minutes ago')
  assert.equal(relativeTime(ago(3 * 3600), now), '3 hours ago')
  assert.equal(relativeTime(ago(86400), now), 'yesterday')
  assert.equal(relativeTime(ago(14 * 86400), now), '2 weeks ago')
})

test('previousRevision skips tombstones and newer revisions', () => {
  const revisions = [{ revision: 5, deleted: false }, { revision: 4, deleted: true }, { revision: 3, deleted: false }, { revision: 1, deleted: false }]
  assert.equal(previousRevision(revisions, 5)?.revision, 3)
  assert.equal(previousRevision(revisions, 3)?.revision, 1)
  assert.equal(previousRevision(revisions, 1), undefined)
})

test('revisionStamp and badges', () => {
  assert.equal(revisionStamp({ revision: 7, author: 'ada', source: 'portal', createdAt: ago(300) }, now), 'Revision 7 · saved by ada, 5 minutes ago')
  assert.equal(revisionStamp({ revision: 2, author: 'unknown', source: 'external', createdAt: ago(10) }, now), 'Revision 2 · changed outside GoFi, just now')
  assert.equal(shortHash('0123456789abcdef'), '01234567')
  assert.equal(sourceBadge('rollback').text, 'Restore')
  assert.equal(sourceBadge('weird').text, 'weird')
})
