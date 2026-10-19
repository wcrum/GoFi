import { test } from 'node:test'
import assert from 'node:assert/strict'
import { mkdtempSync, mkdirSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { checkLocale, checkLocales, expectedKeys, flatten, placeholders, pluralCategories, pluralKey } from './i18n-check.mjs'

const source = {
  gofi: {
    shell: { nav: { flows: 'Flows', hello: 'Hello {{name}}', rich: 'See <0>docs</0>' } },
    count: { flows_one: '{{count}} flow', flows_other: '{{count}} flows' },
  },
}

test('flatten, pluralKey, placeholders', () => {
  assert.deepEqual(flatten(source), {
    'gofi.shell.nav.flows': 'Flows', 'gofi.shell.nav.hello': 'Hello {{name}}', 'gofi.shell.nav.rich': 'See <0>docs</0>',
    'gofi.count.flows_one': '{{count}} flow', 'gofi.count.flows_other': '{{count}} flows',
  })
  assert.deepEqual(pluralKey('gofi.count.flows_other'), { base: 'gofi.count.flows', category: 'other' })
  assert.equal(pluralKey('gofi.shell.nav.flows'), undefined)
  assert.deepEqual(placeholders('Hello {{ name }}, <0>{{count}}</0> &amp; <b>x</b>'), ['<0>', '<b>', '{{count}}', '{{name}}'])
})

test('plural categories are per locale: uk needs four forms, zh-Hant one', () => {
  assert.deepEqual(pluralCategories('uk-UA'), ['one', 'few', 'many', 'other'])
  assert.deepEqual(pluralCategories('zh-Hant'), ['other'])
  assert.deepEqual(pluralCategories('en-US'), ['one', 'other'])
  const uk = [...expectedKeys(flatten(source), 'uk-UA').expected.keys()].filter(key => key.startsWith('gofi.count.'))
  assert.deepEqual(uk, ['gofi.count.flows_one', 'gofi.count.flows_few', 'gofi.count.flows_many', 'gofi.count.flows_other'])
  const zh = [...expectedKeys(flatten(source), 'zh-Hant').expected.keys()].filter(key => key.startsWith('gofi.count.'))
  assert.deepEqual(zh, ['gofi.count.flows_other'])
})

test('checkLocale reports missing, empty, stale and placeholder problems', () => {
  const uk = flatten({
    gofi: {
      shell: { nav: { flows: 'Потоки', hello: '', rich: 'Дивись <0>документацію</0>', gone: 'Застаріле' } },
      count: { flows_one: '{{count}} потік', flows_few: '{{count}} потоки', flows_other: '{{n}} потоку' },
    },
  })
  const report = checkLocale(flatten(source), uk, 'uk-UA')
  assert.deepEqual(report.missing, ['gofi.count.flows_many'])
  assert.deepEqual(report.empty, ['gofi.shell.nav.hello'])
  assert.deepEqual(report.stale, ['gofi.shell.nav.gone'])
  assert.deepEqual(report.unknownPlaceholders, [{ key: 'gofi.count.flows_other', placeholders: ['{{n}}'] }])
  // flows, rich, flows_one, flows_few, flows_other (bad placeholder but translated)
  assert.equal(report.translated, 5)
  assert.equal(report.total, 7)
})

test('a complete catalog passes; zh-Hant may omit _one', () => {
  const zh = flatten({
    gofi: { shell: { nav: { flows: '流程', hello: '你好 {{name}}', rich: '請參閱<0>文件</0>' } }, count: { flows_other: '{{count}} 個流程' } },
  })
  const report = checkLocale(flatten(source), zh, 'zh-Hant')
  assert.deepEqual(report.missing, [])
  assert.deepEqual(report.empty, [])
  assert.deepEqual(report.stale, [])
  assert.deepEqual(report.unknownPlaceholders, [])
  assert.deepEqual(report.missingPlaceholders, [])
})

test('checkLocales walks a locales directory and counts errors by --allow-missing', () => {
  const dir = mkdtempSync(join(tmpdir(), 'gofi-i18n-'))
  for (const [locale, catalog] of [
    ['en-US', source],
    ['uk-UA', { gofi: { shell: { nav: { flows: 'Потоки', hello: '', rich: 'Дивись <0>документацію</0>' } }, count: { flows_one: 'a', flows_few: 'b', flows_many: 'c', flows_other: 'd' } } }],
    ['zh-Hant', { gofi: { shell: { nav: { flows: '流程', hello: '你好 {{name}}', rich: '請參閱<0>文件</0>', extra: '多餘' } }, count: { flows_other: 'x' } } }],
  ]) {
    mkdirSync(join(dir, locale))
    writeFileSync(join(dir, locale, 'gofi.json'), JSON.stringify(catalog))
  }
  const lenient = checkLocales(dir, { allowMissing: true })
  assert.equal(lenient.sourceKeys, 5)
  assert.equal(lenient.reports.length, 2)
  // zh-Hant's stale key is an error regardless; uk-UA's empty string is only a warning.
  assert.equal(lenient.errors, 1)
  assert.equal(lenient.warnings, 1)
  const strict = checkLocales(dir, { allowMissing: false })
  assert.equal(strict.errors, 2)
  assert.equal(strict.warnings, 0)
  assert.throws(() => checkLocales(join(dir, 'nope')), /source catalog not found/)
})
