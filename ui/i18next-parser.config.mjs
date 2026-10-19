// i18next-parser configuration: `pnpm i18n:extract`.
//
// Scans src for t('gofi.<area>.<component>.<slug>', 'English default', values?)
// and <Trans i18nKey=... defaults=...> and writes one catalog per locale,
// src/locales/<locale>/gofi.json, nested by '.' (gofi → area → component →
// slug). en-US receives the English defaults; the other locales keep their
// existing translations, get '' for new keys, and lose keys no longer in code.
// When an English default changes, the other locales' entries for that key
// are reset to '' so the stale translation is redone (resetDefaultValueLocale).

const SOURCE_LOCALE = 'en-US'

export default {
  locales: [SOURCE_LOCALE, 'uk-UA', 'zh-Hant'],
  input: ['src/**/*.{ts,tsx}', '!src/**/*.test.*', '!src/gen/**', '!src/locales/**'],
  output: 'src/locales/$LOCALE/$NAMESPACE.json',
  defaultNamespace: 'gofi',
  // Keys are dotted paths; ':' never separates a namespace.
  keySeparator: '.',
  namespaceSeparator: false,
  pluralSeparator: '_',
  contextSeparator: '_',
  defaultValue: (locale, _namespace, _key, value) => (locale === SOURCE_LOCALE ? value ?? '' : ''),
  resetDefaultValueLocale: SOURCE_LOCALE,
  keepRemoved: false,
  createOldCatalogs: false,
  sort: true,
  indentation: 2,
  lineEnding: 'lf',
  verbose: false,
  failOnWarnings: false,
  lexers: {
    ts: [{ lexer: 'JavascriptLexer', functions: ['t'] }],
    tsx: [{ lexer: 'JsxLexer', functions: ['t'], componentFunctions: ['Trans'] }],
    default: ['JavascriptLexer'],
  },
}
