// Supported locales and browser-language detection. Pure: no DOM, no
// i18next, so it can be unit-tested in node and imported anywhere.

export type SupportedLocale = 'en-US' | 'uk-UA' | 'zh-Hant'

export interface LocaleDefinition {
  /** BCP-47 tag, also the folder name under src/locales. */
  tag: SupportedLocale
  /** Name in the language itself, shown in pickers. */
  nativeName: string
  /** Name in English, for logs and the palette's search keywords. */
  englishName: string
  /** Writing direction, mirrored to <html dir>. */
  dir: 'ltr' | 'rtl'
}

/** The source language: `t()` defaults are written in it. */
export const DEFAULT_LOCALE: SupportedLocale = 'en-US'

/** localStorage key holding the user's explicit choice (absent = detect). */
export const LOCALE_STORAGE_KEY = 'gofi.locale'
/** localStorage key ('1') or `?pseudo=1` query flag enabling pseudo-localization. */
export const PSEUDO_STORAGE_KEY = 'gofi.i18n.pseudo'

export const LOCALES: readonly LocaleDefinition[] = [
  { tag: 'en-US', nativeName: 'English (US)', englishName: 'English (United States)', dir: 'ltr' },
  { tag: 'uk-UA', nativeName: 'Українська', englishName: 'Ukrainian', dir: 'ltr' },
  { tag: 'zh-Hant', nativeName: '繁體中文（台灣）', englishName: 'Traditional Chinese (Taiwan)', dir: 'ltr' },
]

export function findLocale(tag: string | null | undefined): LocaleDefinition | undefined {
  return tag ? LOCALES.find(locale => locale.tag === tag) : undefined
}

export function isSupportedLocale(tag: string | null | undefined): tag is SupportedLocale {
  return !!findLocale(tag)
}

interface LocaleParts { language: string; script?: string }

/** `zh-TW` → { zh, Hant }, `uk` → { uk, Cyrl }: the likely script lets a
 * region-tagged language match a script-tagged one. */
function maximize(tag: string): LocaleParts | undefined {
  try {
    const locale = new Intl.Locale(tag.trim()).maximize()
    return { language: locale.language.toLowerCase(), script: locale.script }
  } catch {
    // Not a well-formed tag (or no Intl.Locale): a plain split still finds
    // the language, and Traditional Chinese by its common regions.
    const parts = tag.trim().toLowerCase().split(/[-_]/).filter(Boolean)
    if (!parts[0]) return undefined
    const language = parts[0]
    let script = parts.find(part => /^[a-z]{4}$/.test(part))
    if (language === 'zh' && !script) script = parts.some(part => ['tw', 'hk', 'mo'].includes(part)) ? 'hant' : 'hans'
    return { language, script: script ? script[0].toUpperCase() + script.slice(1) : undefined }
  }
}

const SUPPORTED_PARTS = new Map<SupportedLocale, LocaleParts | undefined>()
function supportedParts(locale: SupportedLocale): LocaleParts | undefined {
  if (!SUPPORTED_PARTS.has(locale)) SUPPORTED_PARTS.set(locale, maximize(locale))
  return SUPPORTED_PARTS.get(locale)
}

/** The supported locale a single language tag maps to, if any: `zh-TW`,
 * `zh-Hant-TW`, `zh-HK` → zh-Hant; `uk`, `uk-UA` → uk-UA; `en-GB` → en-US;
 * `zh-CN`, `fr` → undefined. */
export function matchLocale(tag: string | null | undefined): SupportedLocale | undefined {
  if (!tag) return undefined
  if (isSupportedLocale(tag)) return tag
  const wanted = maximize(tag)
  if (!wanted) return undefined
  for (const { tag: candidate } of LOCALES) {
    const have = supportedParts(candidate)
    if (have && have.language === wanted.language && (!have.script || !wanted.script || have.script === wanted.script)) return candidate
  }
  return undefined
}

export interface NavigatorLike { languages?: readonly string[]; language?: string }

/** The first browser language (navigator.languages in preference order,
 * then navigator.language) that maps to a supported locale; en-US otherwise. */
export function localeOf(nav: NavigatorLike | null | undefined): SupportedLocale {
  if (!nav) return DEFAULT_LOCALE
  const candidates = [...(nav.languages ?? []), ...(nav.language ? [nav.language] : [])]
  for (const candidate of candidates) {
    const match = matchLocale(candidate)
    if (match) return match
  }
  return DEFAULT_LOCALE
}
