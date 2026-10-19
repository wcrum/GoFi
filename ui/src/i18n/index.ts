// i18n facade for the GoFi UI. Import everything from here:
//
//   import { t, Trans, useLocale, formatRelativeTime } from '../i18n'
//
// - t(key, defaultEnglish, values?) / <Trans i18nKey defaults> — strings,
//   keys `gofi.<area>.<component>.<slug>`, English default as fallback.
// - initI18n() — call in main.tsx before the first render.
// - useLocale() → [locale, setLocale]; I18nProvider remounts on change.
// - LOCALES, localeOf(navigator), matchLocale — supported locales/detection.
// - formatNumber/formatDate/formatDateTime/formatTime/formatRelativeTime/
//   formatDuration — Intl formatting in the active locale.
// - translateServerError(code, params) — Connect/server error text.
// - pseudoLocalize / isPseudoEnabled — QA pseudo-locale (?pseudo=1).
//
// See docs/i18n.md for conventions and the extract/check/lint scripts.

export {
  DEFAULT_LOCALE, LOCALES, LOCALE_STORAGE_KEY, PSEUDO_STORAGE_KEY,
  findLocale, isSupportedLocale, localeOf, matchLocale,
  type LocaleDefinition, type NavigatorLike, type SupportedLocale,
} from './locales.ts'
export { getLocale } from './state.ts'
export {
  NAMESPACE, GRAFANA_UI_NAMESPACE, I18nProvider, Trans, clearLocalePreference, enablePseudo, i18nInstance,
  initI18n, resolveInitialLocale, setLocale, t, useLocale, type TFunction,
} from './runtime.ts'
export {
  formatDate, formatDateTime, formatDuration, formatNumber, formatRelativeTime, formatTime, type DurationFormatOptions,
} from './format.ts'
export { translateServerError, type ServerErrorInput } from './server-errors.ts'
export { isPseudoEnabled, pseudoLocalize, PSEUDO_EXPANSION } from './pseudo.ts'
