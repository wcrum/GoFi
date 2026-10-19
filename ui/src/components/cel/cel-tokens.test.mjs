import { test } from 'node:test'
import assert from 'node:assert/strict'
import { tokenizeCel } from './cel-tokens.ts'

test('tokenizes CEL into colorable kinds', () => {
  const kinds = tokenizeCel('fields["app"] == "payments" && size(raw) > 10 // note')
    .filter(t => t.kind !== 'text').map(t => `${t.kind}:${t.text}`)
  assert.deepEqual(kinds, ['variable:fields', 'delimiter:[', 'string:"app"', 'delimiter:]', 'operator:==', 'string:"payments"', 'operator:&&',
    'function:size', 'delimiter:(', 'variable:raw', 'delimiter:)', 'operator:>', 'number:10', 'comment:// note'])
})

test('round-trips every character, including unknown ones', () => {
  const source = 'meta.x in [1, 2u] ? true : null #'
  assert.equal(tokenizeCel(source).map(t => t.text).join(''), source)
})
