import assert from 'node:assert/strict'
import { test } from 'node:test'
import { fetchKindOf, sourceSummary } from './types.ts'
import { bucketProblem, sqlQueryProblem } from './components/canvas/sources/validation.ts'

test('fetch kinds and summaries cover s3 and sql', () => {
  assert.equal(fetchKindOf({ s3: { bucket: 'b' } }), 's3')
  assert.equal(fetchKindOf({ sql: { driver: 'mysql', query: 'SELECT 1' } }), 'sql')
  assert.equal(fetchKindOf({ http: { url: 'https://x' } }), 'http')
  assert.equal(sourceSummary({ fetch: { s3: { bucket: 'logs', prefix: 'in/', every: '1m' } } }), 'S3 s3://logs/in/ every 1m')
  assert.equal(sourceSummary({ fetch: { sql: { driver: 'postgres', host: 'db', query: 'SELECT 1' } } }), 'SQL postgres db')
})

test('source form checks mirror spec validation', () => {
  assert.equal(bucketProblem('logs'), undefined)
  assert.ok(bucketProblem('No_Caps'))
  assert.equal(sqlQueryProblem('SELECT * FROM t WHERE id > ? ORDER BY id', 'mysql', 'id'), undefined)
  assert.match(sqlQueryProblem('SELECT * FROM t', 'sqlserver', 'id') ?? '', /@p1/)
  assert.match(sqlQueryProblem('  DELETE FROM t', 'postgres') ?? '', /read-only/)
})
