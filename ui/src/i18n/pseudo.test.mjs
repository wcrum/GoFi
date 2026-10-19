import { test } from 'node:test'
import assert from 'node:assert/strict'
import { isPseudoEnabled, pseudoLocalize } from './pseudo.ts'

test('pseudoLocalize brackets, accents and lengthens by about 35%', () => {
  const out = pseudoLocalize('Save flow')
  assert.ok(out.startsWith('[') && out.endsWith(']'), out)
  assert.ok(!/[A-Za-z]/.test(out), `no plain ASCII letters left: ${out}`)
  // 8 letters → 3 tildes of padding
  assert.equal(out, '[Šáṽé ƒĺóŵ ~~~]')
})

test('pseudoLocalize keeps placeholders, element tags and entities intact', () => {
  const out = pseudoLocalize('Hello {{name}}, see <0>docs</0> &amp; <strong>more</strong>')
  assert.match(out, /\{\{name\}\}/)
  assert.match(out, /<0>.*<\/0>/)
  assert.match(out, /<strong>.*<\/strong>/)
  assert.match(out, /&amp;/)
  assert.ok(!/Hello|docs|more/.test(out), out)
})

test('pseudoLocalize leaves strings without letters alone', () => {
  assert.equal(pseudoLocalize('123'), '123')
  assert.equal(pseudoLocalize(''), '')
  assert.equal(pseudoLocalize('—'), '—')
})

test('isPseudoEnabled reads the query flag first, then localStorage', () => {
  const storage = value => ({ getItem: key => (key === 'gofi.i18n.pseudo' ? value : null) })
  assert.equal(isPseudoEnabled({ location: { search: '?pseudo=1' }, storage: storage(null) }), true)
  assert.equal(isPseudoEnabled({ location: { search: '?a=b&pseudo=true' }, storage: storage(null) }), true)
  assert.equal(isPseudoEnabled({ location: { search: '?pseudo=0' }, storage: storage('1') }), false)
  assert.equal(isPseudoEnabled({ location: { search: '' }, storage: storage('1') }), true)
  assert.equal(isPseudoEnabled({ location: { search: '' }, storage: storage(null) }), false)
  assert.equal(isPseudoEnabled({ location: null, storage: null }), false)
})
