import { test } from 'node:test'
import assert from 'node:assert/strict'
import { flowResource, parseFlowResource, toYAML } from './resource.ts'
import { flowKey } from './flow-normalize.ts'

const definition = { name: 'radar', source: { listen: { network: 'tcp', port: 9000 } }, framing: { mode: 'line' }, parser: { format: 'json' }, stages: [], sinks: [{ type: 'stdout' }] }

test('a Flow resource round-trips through YAML', () => {
  const parsed = parseFlowResource(toYAML(flowResource(definition, null)))
  assert.deepEqual(parsed.definition, definition)
})

test('malformed YAML reports its line', () => {
  const parsed = parseFlowResource('apiVersion: gofi.wcrum.dev/v1alpha1\nkind: Flow\nspec: [unclosed\n')
  assert.ok(parsed.error)
  assert.equal(typeof parsed.line, 'number')
})

test('the resource must be a named Flow', () => {
  assert.match(parseFlowResource('apiVersion: v1\nkind: ConfigMap\n').error, /kind must be Flow/)
  assert.match(parseFlowResource('apiVersion: gofi.wcrum.dev/v1alpha1\nkind: Flow\nspec: {}\n').error, /metadata.name/)
})

test('the resource spec is in pipeline order and always uses top-level decode', () => {
  const legacy = { stages: [{ id: 'p', kind: 'parse', parse: { from: 'fields["m"]', format: 'json' } }], python: { library: 'hooks' }, sinks: [{ type: 'stdout' }], parser: { format: 'json', decoders: [{ type: 'gzip' }] }, framing: { mode: 'line' }, source: { listen: { network: 'tcp', port: 9000 } }, name: 'ordered', testCases: [] }
  const resource = flowResource(legacy, null)
  assert.deepEqual(Object.keys(resource.spec), ['source', 'framing', 'decode', 'parser', 'stages', 'sinks', 'testCases', 'python'])
  assert.deepEqual(resource.spec.decode, { decoders: [{ type: 'gzip' }] })
  assert.deepEqual(resource.spec.parser, { format: 'json' })
  const yaml = toYAML(resource)
  assert.ok(yaml.indexOf('decode:') < yaml.indexOf('parser:') && yaml.indexOf('parser:') < yaml.indexOf('stages:'))
})

test('an older resource with parser decoders parses into the current shape', () => {
  const text = 'apiVersion: gofi.wcrum.dev/v1alpha1\nkind: Flow\nmetadata:\n  name: old\nspec:\n  source: {listen: {network: tcp, port: 9000}}\n  framing: {mode: line}\n  parser:\n    format: json\n    decoders: [{type: base64}]\n    maxDecodedBytes: 2048\n  stages: []\n  sinks: [{type: stdout}]\n'
  const { definition: parsed } = parseFlowResource(text)
  assert.deepEqual(parsed.decode, { decoders: [{ type: 'base64' }], maxDecodedBytes: 2048 })
  assert.deepEqual(parsed.parser, { format: 'json' })
})

test('flowKey ignores key order, so a graph edit read back from YAML is not a second change', () => {
  // The canvas builds name first and decode before parser; the parsed resource has name last and decode appended.
  const fromGraph = { name: 'radar', source: { listen: { network: 'tcp', port: 9000 } }, framing: { mode: 'line' }, decode: { decoders: [{ type: 'base64' }] }, parser: { format: 'json' }, stages: [], sinks: [{ type: 'stdout', encoding: { format: 'fields' } }] }
  const parsed = parseFlowResource(toYAML(flowResource(fromGraph, null))).definition
  assert.notEqual(JSON.stringify(parsed), JSON.stringify(fromGraph), 'plain JSON differs by key order')
  assert.equal(flowKey(parsed), flowKey(fromGraph))
  assert.notEqual(flowKey({ ...fromGraph, sinks: [{ type: 'stdout' }] }), flowKey(fromGraph), 'a real change still differs')
})
