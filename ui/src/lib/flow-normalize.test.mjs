import { test } from 'node:test'
import assert from 'node:assert/strict'
import { normalizeFlow } from './flow-normalize.ts'
import { countDefaultRefs, fillDefaultLibrary, pythonRefLabel, storedRef } from './python-ref.ts'
import { parseStageSummary } from './parse-stage.ts'

const base = { name: 'f', source: { listen: { network: 'tcp', port: 1 } }, framing: { mode: 'line' }, stages: [], sinks: [] }

test('legacy parser decoders move to top-level decode', () => {
  const legacy = { ...base, parser: { format: 'json', decoders: [{ type: 'base64' }, { type: 'gzip' }], maxDecodedBytes: 100, errorPolicy: 'drop' } }
  const flow = normalizeFlow(legacy)
  assert.deepEqual(flow.decode, { decoders: [{ type: 'base64' }, { type: 'gzip' }], maxDecodedBytes: 100 })
  assert.deepEqual(flow.parser, { format: 'json', errorPolicy: 'drop' })
  assert.deepEqual(legacy.parser.decoders.length, 2, 'the input is not modified')
})

test('the current shape is unchanged; an empty decode is dropped', () => {
  const current = { ...base, decode: { decoders: [{ type: 'hex' }] }, parser: { format: 'json' } }
  assert.deepEqual(normalizeFlow(current), current)
  assert.equal(normalizeFlow({ ...base, decode: { decoders: [] }, parser: { format: 'none' } }).decode, undefined)
})

test('top-level decode wins over leftover legacy decoders', () => {
  const both = { ...base, decode: { decoders: [{ type: 'hex' }] }, parser: { format: 'json', decoders: [{ type: 'base64' }] } }
  assert.deepEqual(normalizeFlow(both).decode, { decoders: [{ type: 'hex' }] })
  assert.equal(normalizeFlow(both).parser.decoders, undefined)
})

test('Python refs may rely on the flow default library', () => {
  assert.equal(pythonRefLabel({ function: 'f' }), 'f')
  assert.equal(pythonRefLabel({ function: 'f' }, 'hooks'), 'hooks.f')
  assert.equal(pythonRefLabel({ library: 'lib', function: 'f' }, 'hooks'), 'lib.f')
  assert.deepEqual(storedRef({ library: 'hooks', function: 'f' }, 'hooks'), { function: 'f' })
  assert.deepEqual(storedRef({ library: 'lib', function: 'f' }, 'hooks'), { library: 'lib', function: 'f' })
  assert.deepEqual(storedRef({ library: '', function: '', inlineSource: 'x' }, 'hooks'), { library: '', function: '', inlineSource: 'x' })
  const data = [{ spec: { kind: 'parse', parse: { python: { function: 'p' }, decoders: [{ type: 'python', python: { function: 'd' } }] } } }, { spec: { python: { library: '', function: '', inlineSource: 'x' } } }, { sink: { python: { library: 'a', function: 's' } } }]
  assert.equal(countDefaultRefs(data), 2)
  const filled = fillDefaultLibrary(data, 'hooks')
  assert.equal(countDefaultRefs(filled), 0)
  assert.deepEqual(filled[0].spec.parse.python, { function: 'p', library: 'hooks' })
  assert.equal(filled[1], data[1], 'unchanged parts keep their identity')
})

test('parse stage summary reads in pipeline order', () => {
  assert.equal(parseStageSummary({ parse: { from: 'fields["message"]', format: 'json', into: 'payload' } }), 'fields["message"] → json → payload')
  assert.equal(parseStageSummary({ parse: { from: '', decoders: [{ type: 'base64' }], format: 'python', python: { function: 'inner' } } }, 'hooks'), 'raw → base64 → hooks.inner')
})
