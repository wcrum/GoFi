// The i18next runtime behind the GoFi UI: initialisation, `t`/`Trans` in the
// `gofi` namespace, locale switching, and the React hook that follows it.
//
// @grafana/i18n (used by @grafana/ui for its own strings) owns the shared
// i18next singleton; `initPluginTranslations` sets it up and points its
// `t`/`Trans` at our namespace. We then add resource bundles directly on the
// same i18next instance and switch language with `changeLanguage`, which
// @grafana/ui components pick up as well.

import { createElement, Fragment, useSyncExternalStore, type ReactElement, type ReactNode } from 'react'
import i18next from 'i18next'
import { initPluginTranslations, t as grafanaT, Trans as GrafanaTrans, type TransProps } from '@grafana/i18n'
import enUS from '../locales/en-US/gofi.json' with { type: 'json' }
import { DEFAULT_LOCALE, LOCALE_STORAGE_KEY, findLocale, isSupportedLocale, localeOf, type SupportedLocale } from './locales.ts'
import { isPseudoEnabled, pseudoLocalize } from './pseudo.ts'
import { getLocale, setCurrentLocale, subscribeLocale } from './state.ts'

/** i18next namespace holding GoFi's strings (src/locales/<locale>/gofi.json). */
export const NAMESPACE = 'gofi'
/** Optional namespace with translations for @grafana/ui's own `grafana-ui.*`
 * keys (src/locales/<locale>/grafana-ui.json); see docs/i18n.md. */
export const GRAFANA_UI_NAMESPACE = 'grafana-ui'
const PSEUDO_PROCESSOR = 'gofi-pseudo'

export type TFunction = (key: string, defaultEnglish: string, values?: Record<string, unknown>) => string

type Resources = Record<string, unknown>
type Loader = () => Promise<{ default: Resources }>

// Lazy-loaded locale bundles: only the active locale's JSON is fetched.
// en-US is bundled inline above (it is the source language, so it is tiny
// to ship and never needs a round trip). No `with { type: 'json' }` here:
// Vite serves JSON as a JS module in dev and keeps dynamic-import attributes,
// which would make the browser reject the response. (Under node these
// imports fail and are ignored; tests add bundles through i18nInstance().)
const GOFI_LOADERS: Partial<Record<SupportedLocale, Loader>> = {
  'uk-UA': () => import('../locales/uk-UA/gofi.json'),
  'zh-Hant': () => import('../locales/zh-Hant/gofi.json'),
}
const GRAFANA_UI_LOADERS: Partial<Record<SupportedLocale, Loader>> = {
  'uk-UA': () => import('../locales/uk-UA/grafana-ui.json'),
  'zh-Hant': () => import('../locales/zh-Hant/grafana-ui.json'),
}

function storageGet(key: string): string | null {
  try { return localStorage.getItem(key) } catch { return null }
}
function storageSet(key: string, value: string | null) {
  try {
    if (value === null) localStorage.removeItem(key)
    else localStorage.setItem(key, value)
  } catch { /* storage unavailable: the choice lasts for this page only */ }
}

/** The stored choice if valid, else the first matching browser language. */
export function resolveInitialLocale(): SupportedLocale {
  const stored = storageGet(LOCALE_STORAGE_KEY)
  if (isSupportedLocale(stored)) return stored
  return localeOf(typeof navigator === 'undefined' ? null : navigator)
}

const loaded = new Set<string>()
async function loadBundles(locale: SupportedLocale): Promise<void> {
  if (locale === DEFAULT_LOCALE || loaded.has(locale)) return
  const [gofi, grafanaUi] = await Promise.all([
    GOFI_LOADERS[locale]?.().then(module => module.default, () => undefined),
    GRAFANA_UI_LOADERS[locale]?.().then(module => module.default, () => undefined),
  ])
  if (gofi) i18next.addResourceBundle(locale, NAMESPACE, gofi, true, true)
  // Grafana's files nest everything under a "grafana-ui" object; accept
  // both that and a file that already starts at the component level.
  if (grafanaUi) {
    const nested = (grafanaUi as Record<string, unknown>)[GRAFANA_UI_NAMESPACE]
    i18next.addResourceBundle(locale, GRAFANA_UI_NAMESPACE, nested && typeof nested === 'object' ? { [GRAFANA_UI_NAMESPACE]: nested } : grafanaUi, true, true)
  }
  loaded.add(locale)
}

function applyDocumentLocale(locale: SupportedLocale) {
  if (typeof document === 'undefined') return
  const root = document.documentElement
  root.lang = locale
  root.dir = findLocale(locale)?.dir ?? 'ltr'
}

let ready: Promise<void> | undefined

/** Sets up i18next (through @grafana/i18n, so @grafana/ui shares it), bundles
 * en-US, loads the active locale's JSON and switches to it. Call once before
 * the first render; the en-US path does no network work. */
export function initI18n(): Promise<void> {
  ready ??= (async () => {
    const locale = resolveInitialLocale()
    await initPluginTranslations(NAMESPACE)
    i18next.addResourceBundle(DEFAULT_LOCALE, NAMESPACE, enUS as Resources, true, true)
    // The default instance was initialised by @grafana/i18n with i18next's
    // defaults; these are read at translate time, so setting them now works.
    i18next.options.fallbackLng = [DEFAULT_LOCALE]
    // React escapes on render; i18next's own HTML escaping would show
    // `fields[&quot;x&quot;]` literally. Grafana core disables it the same way.
    i18next.options.interpolation = { ...i18next.options.interpolation, escapeValue: false }
    // Re-run the interpolator's init so <Trans> (which bypasses t) is unescaped
    // too; at runtime init takes the whole options object (see i18next.js).
    ;(i18next.services.interpolator as unknown as { init(options: unknown, reset?: boolean): void }).init(i18next.options, true)
    i18next.options.fallbackNS = [GRAFANA_UI_NAMESPACE]
    if (isPseudoEnabled()) enablePseudo()
    await loadBundles(locale)
    await i18next.changeLanguage(locale)
    applyDocumentLocale(locale)
    setCurrentLocale(locale)
  })()
  return ready
}

/** Registers the pseudo-localization post-processor on the shared instance. */
export function enablePseudo(): void {
  i18next.use({ type: 'postProcessor', name: PSEUDO_PROCESSOR, process: (value: string) => pseudoLocalize(value) })
  const current = i18next.options.postProcess
  const list = Array.isArray(current) ? current : typeof current === 'string' ? [current] : []
  if (!list.includes(PSEUDO_PROCESSOR)) i18next.options.postProcess = [...list, PSEUDO_PROCESSOR]
}

/** Switches the UI language without a reload: persists the choice, loads the
 * locale's bundles if needed, tells i18next, updates <html lang/dir> and
 * notifies useLocale() subscribers (I18nProvider remounts the app tree). */
export async function setLocale(locale: SupportedLocale): Promise<void> {
  if (!isSupportedLocale(locale)) throw new Error(`Unsupported locale: ${locale}`)
  storageSet(LOCALE_STORAGE_KEY, locale)
  if (ready) await ready
  await loadBundles(locale)
  if (i18next.isInitialized) await i18next.changeLanguage(locale)
  applyDocumentLocale(locale)
  setCurrentLocale(locale)
}

/** Forgets the explicit choice: the browser language decides again. */
export async function clearLocalePreference(): Promise<void> {
  storageSet(LOCALE_STORAGE_KEY, null)
  await setLocale(localeOf(typeof navigator === 'undefined' ? null : navigator))
  storageSet(LOCALE_STORAGE_KEY, null)
}

/** [activeLocale, setLocale]: re-renders the caller when the locale changes. */
export function useLocale(): [SupportedLocale, (locale: SupportedLocale) => Promise<void>] {
  const locale = useSyncExternalStore(subscribeLocale, getLocale, getLocale)
  return [locale, setLocale]
}

/** Remounts its children when the locale changes, so every `t()` call in the
 * tree re-evaluates (plain `t()` results are not reactive; `Trans` is).
 * Place it below the router and theme providers so they keep their state. */
export function I18nProvider({ children }: { children: ReactNode }): ReactElement {
  const [locale] = useLocale()
  return createElement(Fragment, { key: locale }, children)
}

// English-rules fallback used when i18next is not initialised (tests of
// modules that call t() at import time, or a failed init).
function interpolate(template: string, values?: Record<string, unknown>): string {
  if (!values) return template
  let text = template
  if (typeof values.count === 'number') {
    const variant = values.count === 1 ? values.defaultValue_one : values.defaultValue_other
    if (typeof variant === 'string') text = variant
  }
  return text.replace(/\{\{\s*([\w.]+)\s*\}\}/g, (match, name: string) => {
    const value = values[name]
    return value === undefined || value === null ? match : String(value)
  })
}

/** Translate a GoFi UI string. Keys are `gofi.<area>.<component>.<slug>`;
 * `defaultEnglish` is the source text (and the fallback when the active
 * locale lacks the key). `values` interpolate `{{name}}`; pass `count` for
 * plurals (with `defaultValue_one`/`defaultValue_other` defaults). */
export const t: TFunction = (key, defaultEnglish, values) => {
  try {
    // Per-call: i18next's interpolator caches escapeValue at init, before we
    // could set it, and React escapes on render anyway.
    const translated = grafanaT(key, defaultEnglish, { ...values, ns: NAMESPACE, interpolation: { escapeValue: false } })
    if (typeof translated === 'string' && translated.length > 0) return translated
  } catch {
    // i18next not initialised; fall through to the English default.
  }
  return interpolate(defaultEnglish, values)
}

/** `Trans` scoped to the `gofi` namespace, for strings with embedded
 * elements: <Trans i18nKey="gofi.x.y" components={{ b: <strong /> }}>Hello <b>{{name}}</b></Trans>. */
export function Trans(props: TransProps): ReactElement {
  return createElement(GrafanaTrans, { ...props, ns: props.ns ?? NAMESPACE } as TransProps)
}

/** For tests and diagnostics: the shared i18next instance. */
export function i18nInstance(): typeof i18next {
  return i18next
}
