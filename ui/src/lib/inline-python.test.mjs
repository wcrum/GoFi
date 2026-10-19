import { test } from 'node:test'
import assert from 'node:assert/strict'
import { inlineBlocks } from './inline-python.ts'

test('collects inline Python by step', () => {
  const flow = {
    name: 'f',
    framing: { mode: 'python', python: { library: 'x', function: 'a', inlineSource: 'def a(): pass' } },
    stages: [{ id: 'inl', kind: 'python', python: { library: 'x', function: 'inl', inlineSource: 'def transform(f, m):\n    return f' } }],
    sinks: [{ type: 'stdout', encoding: { encoders: [{ type: 'python', python: { inlineSource: 'def e(b): return b' } }] } }],
  }
  assert.deepEqual(inlineBlocks(flow).map(b => b.label), ['Frame', 'Stage inl', 'Destination 1 › Encoder 1'])
  assert.equal(inlineBlocks(flow)[1].source, 'def transform(f, m):\n    return f')
})

test('ignores library references without inline code', () => {
  assert.deepEqual(inlineBlocks({ stages: [{ id: 's', python: { library: 'lib', function: 'f' } }] }), [])
})

test('labels inline Python in top-level decode and parse stages', () => {
  const flow = {
    decode: { decoders: [{ type: 'base64' }, { type: 'python', python: { inlineSource: 'def d(b): return b' } }] },
    stages: [{ id: 'inner', kind: 'parse', parse: { format: 'python', python: { inlineSource: 'def p(b): return {}' }, decoders: [{ type: 'python', python: { inlineSource: 'def u(b): return b' } }] } }],
  }
  assert.deepEqual(inlineBlocks(flow).map(b => b.label), ['Decoder 2', 'Stage inner', 'Stage inner › Decoder 1'])
})
