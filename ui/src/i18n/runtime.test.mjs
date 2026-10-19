import { test, before } from 'node:test'
import assert from 'node:assert/strict'
import { createElement } from 'react'
import { renderToStaticMarkup } from 'react-dom/server'
import {
  Trans, enablePseudo, formatDuration, formatNumber, formatRelativeTime, getLocale, i18nInstance, initI18n, setLocale, t,
  translateServerError,
} from './index.ts'

// The real runtime: @grafana/i18n's shared i18next instance, our namespace,
// bundles added the way a lazy-loaded locale file would be.
before(async () => {
  await initI18n()
  i18nInstance().addResourceBundle('uk-UA', 'gofi', {
    gofi: {
      test: {
        hello: 'Привіт, {{name}}!',
        flows_one: '{{count}} потік',
        flows_few: '{{count}} потоки',
        flows_many: '{{count}} потоків',
        flows_other: '{{count}} потоку',
        empty: '',
        rich: 'Дивись <0>документацію</0>',
      },
    },
  }, true, true)
  i18nInstance().addResourceBundle('uk-UA', 'grafana-ui', { 'grafana-ui': { pagination: { 'next-page': 'Наступна сторінка' } } }, true, true)
})

test('starts in en-US and returns the English default for unknown keys, interpolated', async () => {
  await setLocale('en-US')
  assert.equal(getLocale(), 'en-US')
  assert.equal(t('gofi.test.nope', 'Hello, {{name}}!', { name: 'Ada' }), 'Hello, Ada!')
  assert.equal(t('gofi.test.hello', 'Hello, {{name}}!', { name: 'Ada' }), 'Hello, Ada!')
})

test('English plurals pick defaultValue_one / the default by count', async () => {
  await setLocale('en-US')
  const flows = count => t('gofi.test.flows', '{{count}} flows', { count, defaultValue_one: '{{count}} flow' })
  assert.equal(flows(1), '1 flow')
  assert.equal(flows(0), '0 flows')
  assert.equal(flows(12), '12 flows')
})

test('switching to uk-UA resolves translations, Ukrainian plural forms and falls back per key', async () => {
  await setLocale('uk-UA')
  assert.equal(getLocale(), 'uk-UA')
  assert.equal(t('gofi.test.hello', 'Hello, {{name}}!', { name: 'Ада' }), 'Привіт, Ада!')
  const flows = count => t('gofi.test.flows', '{{count}} flows', { count, defaultValue_one: '{{count}} flow' })
  assert.equal(flows(1), '1 потік')
  assert.equal(flows(3), '3 потоки')
  assert.equal(flows(5), '5 потоків')
  assert.equal(flows(21), '21 потік')
  // Missing and empty translations fall back to the English default.
  assert.equal(t('gofi.test.missing', 'Only in code'), 'Only in code')
  assert.equal(t('gofi.test.empty', 'Empty means untranslated'), 'Empty means untranslated')
})

test('@grafana/ui keys resolve through the grafana-ui fallback namespace', async () => {
  await setLocale('uk-UA')
  // This is how @grafana/ui calls t(): fixed to our namespace by initPluginTranslations.
  assert.equal(i18nInstance().t('grafana-ui.pagination.next-page', 'next page', { ns: 'gofi' }), 'Наступна сторінка')
  assert.equal(i18nInstance().t('grafana-ui.pagination.previous-page', 'previous page', { ns: 'gofi' }), 'previous page')
})

test('Trans renders translated text with embedded elements', async () => {
  await setLocale('uk-UA')
  const html = renderToStaticMarkup(createElement(Trans, { i18nKey: 'gofi.test.rich', components: [createElement('a', { href: '/docs' })] }, 'See <0>the docs</0>'))
  assert.equal(html, 'Дивись <a href="/docs">документацію</a>')
  await setLocale('en-US')
  const english = renderToStaticMarkup(createElement(Trans, { i18nKey: 'gofi.test.rich', components: [createElement('a', { href: '/docs' })] }, 'See <0>the docs</0>'))
  assert.equal(english, 'See <a href="/docs">the docs</a>')
})

test('setLocale rejects unsupported tags', async () => {
  await assert.rejects(() => setLocale('fr-FR'), /Unsupported locale/)
})

test('formatters follow the active locale', async () => {
  await setLocale('en-US')
  const now = new Date('2026-10-17T12:00:00Z')
  assert.equal(formatNumber(1234567.5), '1,234,567.5')
  assert.equal(formatRelativeTime(new Date(now.getTime() - 5 * 60_000), now), '5 minutes ago')
  assert.equal(formatRelativeTime(new Date(now.getTime() - 86_400_000), now), 'yesterday')
  assert.equal(formatRelativeTime(new Date(now.getTime() + 2 * 3_600_000), now), 'in 2 hours')
  assert.equal(formatRelativeTime(now.toISOString(), now), 'now')
  assert.match(formatDuration(3_723_000), /1 hr.*2 min.*3 sec/)
  assert.match(formatDuration(250), /250 ms/)
  await setLocale('uk-UA')
  assert.match(formatNumber(1234567.5), /^1\s234\s567,5$/) // group separator is a (narrow no-break) space
  assert.equal(formatRelativeTime(new Date(now.getTime() - 5 * 60_000), now), '5 хвилин тому')
  await setLocale('en-US')
})

test('translateServerError maps known messages and status codes', async () => {
  await setLocale('en-US')
  assert.equal(translateServerError('unauthenticated', { message: 'library credential required' }), 'A credential is required to use this library.')
  assert.equal(translateServerError(5, { message: 'flow "radar" does not exist' }), 'Not found. flow "radar" does not exist')
  assert.equal(translateServerError('NotFound'), 'Not found.')
  assert.equal(translateServerError('flow_not_found', { name: 'radar' }), 'Flow "radar" was not found.')
  assert.equal(translateServerError(undefined, { message: 'boom' }), 'boom')
})

// Last: the pseudo post-processor stays registered on the shared instance.
test('pseudo-localization wraps every translated string', async () => {
  await setLocale('en-US')
  enablePseudo()
  const out = t('gofi.test.nope', 'Hello, {{name}}!', { name: 'Ada' })
  assert.ok(out.startsWith('[') && out.endsWith(']'), out)
  assert.ok(!/Hello/.test(out), out)
  assert.ok(out.includes('{{') === false, 'placeholders are interpolated before pseudo-localization')
})
