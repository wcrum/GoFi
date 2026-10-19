// Pseudo-localization for QA: every translated string comes back bracketed,
// accented and ~35% longer, so untranslated text and layouts that truncate
// stand out. Enabled with `?pseudo=1` or localStorage 'gofi.i18n.pseudo'='1'.

import { PSEUDO_STORAGE_KEY } from './locales.ts'

const ACCENTS: Record<string, string> = {
  a: 'á', b: 'ƀ', c: 'ç', d: 'ď', e: 'é', f: 'ƒ', g: 'ĝ', h: 'ĥ', i: 'í', j: 'ĵ', k: 'ķ', l: 'ĺ', m: 'ɱ',
  n: 'ñ', o: 'ó', p: 'þ', q: 'ɋ', r: 'ŕ', s: 'š', t: 'ţ', u: 'ú', v: 'ṽ', w: 'ŵ', x: 'ẍ', y: 'ý', z: 'ž',
  A: 'Á', B: 'Ɓ', C: 'Ç', D: 'Ď', E: 'É', F: 'Ƒ', G: 'Ĝ', H: 'Ĥ', I: 'Í', J: 'Ĵ', K: 'Ķ', L: 'Ĺ', M: 'Ṃ',
  N: 'Ñ', O: 'Ó', P: 'Þ', Q: 'Ɋ', R: 'Ŕ', S: 'Š', T: 'Ţ', U: 'Ú', V: 'Ṽ', W: 'Ŵ', X: 'Ẍ', Y: 'Ý', Z: 'Ž',
}

/** Growth factor: German and Ukrainian run roughly a third longer than English. */
export const PSEUDO_EXPANSION = 0.35

// Segments left untouched: i18next placeholders, Trans element tags
// (<0>…</0>, <strong>…), nested lookups and HTML entities.
const PROTECTED = /(\{\{[^}]*\}\}|<\/?[^>]*>|\$t\([^)]*\)|&[a-zA-Z#0-9]+;)/g

/** `Save flow` → `[Šáṽé ƒĺóŵ ~~]`. Placeholders and tags survive, so the
 * interpolated/parsed result still works; strings without letters are
 * returned as they are. */
export function pseudoLocalize(text: string): string {
  if (!text || !/[A-Za-z]/.test(text)) return text
  let letters = 0
  const accented = text.split(PROTECTED).map((segment, index) => {
    if (index % 2 === 1) return segment // a protected match
    return segment.replace(/[A-Za-z]/g, letter => { letters++; return ACCENTS[letter] ?? letter })
  }).join('')
  const padding = '~'.repeat(Math.ceil(letters * PSEUDO_EXPANSION))
  return `[${accented}${padding ? ` ${padding}` : ''}]`
}

export interface PseudoEnvironment {
  location?: { search?: string } | null
  storage?: { getItem(key: string): string | null } | null
}

/** Whether pseudo-localization is on, from `?pseudo=1` (this page load) or
 * localStorage 'gofi.i18n.pseudo' = '1' (sticky). `?pseudo=0` turns the
 * sticky flag off for this load. */
export function isPseudoEnabled(env: PseudoEnvironment = {
  location: typeof location === 'undefined' ? null : location,
  storage: typeof localStorage === 'undefined' ? null : localStorage,
}): boolean {
  try {
    const query = new URLSearchParams(env.location?.search ?? '').get('pseudo')
    if (query !== null) return query === '1' || query === 'true'
    const stored = env.storage?.getItem(PSEUDO_STORAGE_KEY)
    return stored === '1' || stored === 'true'
  } catch {
    return false
  }
}
