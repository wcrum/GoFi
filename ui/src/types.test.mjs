import assert from 'node:assert/strict'
import { test } from 'node:test'
import { sourceSummary, sourceTypeOf } from './types.ts'

test('source summaries name each source type', () => {
  assert.equal(sourceSummary({ listen: { network: 'udp', port: 9000 } }), 'UDP :9000')
  assert.equal(sourceSummary({ listen: { network: 'quic', port: 4433, tls: { cert: 'x', key: 'y' } } }), 'QUIC :4433')
  assert.equal(sourceSummary({ fetch: { http: { url: 'https://x/y', every: '5s' } } }), 'Fetch https://x/y every 5s')
  assert.equal(sourceSummary({ subscribe: { mqtt: { url: 'tcp://b:1883', topic: 'a/#' } } }), 'MQTT a/#')
  assert.equal(sourceTypeOf({ subscribe: { sse: { url: 'https://x' } } }), 'subscribe')
})
