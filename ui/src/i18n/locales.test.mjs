import { test } from 'node:test'
import assert from 'node:assert/strict'
import { DEFAULT_LOCALE, LOCALES, findLocale, isSupportedLocale, localeOf, matchLocale } from './locales.ts'

test('the supported locales are en-US, uk-UA and zh-Hant with native names', () => {
  assert.deepEqual(LOCALES.map(locale => locale.tag), ['en-US', 'uk-UA', 'zh-Hant'])
  assert.equal(findLocale('uk-UA')?.nativeName, 'Українська')
  assert.equal(findLocale('zh-Hant')?.nativeName, '繁體中文（台灣）')
  assert.equal(DEFAULT_LOCALE, 'en-US')
  assert.ok(isSupportedLocale('zh-Hant'))
  assert.ok(!isSupportedLocale('zh-Hans'))
})

test('matchLocale maps browser tags onto supported locales', () => {
  assert.equal(matchLocale('en'), 'en-US')
  assert.equal(matchLocale('en-GB'), 'en-US')
  assert.equal(matchLocale('uk'), 'uk-UA')
  assert.equal(matchLocale('uk-UA'), 'uk-UA')
  assert.equal(matchLocale('zh-TW'), 'zh-Hant')
  assert.equal(matchLocale('zh-Hant-TW'), 'zh-Hant')
  assert.equal(matchLocale('zh-Hant'), 'zh-Hant')
  assert.equal(matchLocale('zh-HK'), 'zh-Hant')
  assert.equal(matchLocale('ZH-tw'), 'zh-Hant')
})

test('matchLocale rejects unsupported languages and scripts', () => {
  assert.equal(matchLocale('zh-CN'), undefined)
  assert.equal(matchLocale('zh-Hans'), undefined)
  assert.equal(matchLocale('zh'), undefined)
  assert.equal(matchLocale('ru'), undefined)
  assert.equal(matchLocale('fr-FR'), undefined)
  assert.equal(matchLocale(''), undefined)
  assert.equal(matchLocale(null), undefined)
  assert.equal(matchLocale('not a tag!'), undefined)
})

test('localeOf takes the first matching navigator language, else en-US', () => {
  assert.equal(localeOf({ languages: ['fr-FR', 'zh-TW', 'en-US'] }), 'zh-Hant')
  assert.equal(localeOf({ languages: ['zh-CN', 'uk'] }), 'uk-UA')
  assert.equal(localeOf({ languages: ['de-DE'], language: 'uk-UA' }), 'uk-UA')
  assert.equal(localeOf({ languages: [], language: 'ja' }), 'en-US')
  assert.equal(localeOf({ language: 'en-AU' }), 'en-US')
  assert.equal(localeOf(undefined), 'en-US')
  assert.equal(localeOf({}), 'en-US')
})
