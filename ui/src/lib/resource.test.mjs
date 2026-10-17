import { test } from 'node:test'
import assert from 'node:assert/strict'
import { flowResource, parseFlowResource, toYAML } from './resource.ts'

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
