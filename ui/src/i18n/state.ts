// The active locale, as a tiny external store: formatters read it
// synchronously, React subscribes through useLocale() (useSyncExternalStore).

import { DEFAULT_LOCALE, type SupportedLocale } from './locales.ts'

let current: SupportedLocale = DEFAULT_LOCALE
const listeners = new Set<() => void>()

/** The active locale (BCP-47). en-US until initI18n() has run. */
export function getLocale(): SupportedLocale {
  return current
}

export function setCurrentLocale(locale: SupportedLocale): void {
  if (locale === current) return
  current = locale
  for (const listener of [...listeners]) listener()
}

export function subscribeLocale(listener: () => void): () => void {
  listeners.add(listener)
  return () => { listeners.delete(listener) }
}
