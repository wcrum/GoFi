#!/usr/bin/env node
// Compares every non-English catalog in src/locales with the en-US source:
//
//   node scripts/i18n-check.mjs [--dir src/locales] [--source en-US]
//                               [--namespace gofi] [--allow-missing] [--json]
//
// Reports, per locale: missing keys (in en-US, absent here), empty strings
// (present but untranslated), stale keys (not in en-US any more), and
// placeholder problems ({{name}} / <0> tags that the source does not have,
// which would break interpolation). Plural forms are compared per locale
// using Intl.PluralRules: Ukrainian needs _one/_few/_many/_other, Chinese
// only _other.
//
// Exit 1 on stale keys or unknown placeholders; also on missing/empty keys
// unless --allow-missing is given (use it until translators have caught up).
// Node only, no dependencies; importable (checkLocales) for tests.

import { existsSync, readFileSync, readdirSync } from 'node:fs'
import { join, resolve } from 'node:path'
import { pathToFileURL } from 'node:url'

export const PLURAL_CATEGORIES = ['zero', 'one', 'two', 'few', 'many', 'other']
const PLURAL_RE = new RegExp(`^(.*)_(${PLURAL_CATEGORIES.join('|')})$`)

/** { gofi: { a: { b: 'x' } } } → { 'gofi.a.b': 'x' } */
export function flatten(value, prefix = '', out = {}) {
  if (value && typeof value === 'object' && !Array.isArray(value)) {
    for (const [key, child] of Object.entries(value)) flatten(child, prefix ? `${prefix}.${key}` : key, out)
  } else if (prefix) {
    out[prefix] = value
  }
  return out
}

/** `gofi.x.items_other` → { base: 'gofi.x.items', category: 'other' }; else undefined. */
export function pluralKey(key) {
  const match = PLURAL_RE.exec(key)
  return match ? { base: match[1], category: match[2] } : undefined
}

/** Plural categories a locale needs, in i18next's order. */
export function pluralCategories(locale) {
  try {
    const categories = new Intl.PluralRules(locale).resolvedOptions().pluralCategories
    return PLURAL_CATEGORIES.filter(category => categories.includes(category))
  } catch {
    return ['one', 'other']
  }
}

/** The {{placeholders}} and <tags> a string interpolates, as a sorted unique list. */
export function placeholders(text) {
  if (typeof text !== 'string') return []
  const found = new Set()
  for (const match of text.matchAll(/\{\{\s*([^}]+?)\s*\}\}/g)) found.add(`{{${match[1].trim()}}}`)
  for (const match of text.matchAll(/<\/?([a-zA-Z0-9]+)[^>]*>/g)) found.add(`<${match[1]}>`)
  return [...found].sort()
}

/** The keys a locale's catalog should contain, given the source catalog:
 * plain keys as they are, plural keys expanded to the locale's categories. */
export function expectedKeys(sourceFlat, locale) {
  const expected = new Map() // key → source text to compare placeholders with
  const pluralBases = new Map()
  for (const [key, text] of Object.entries(sourceFlat)) {
    const plural = pluralKey(key)
    if (plural) {
      const base = pluralBases.get(plural.base) ?? {}
      base[plural.category] = text
      pluralBases.set(plural.base, base)
    } else expected.set(key, text)
  }
  for (const [base, forms] of pluralBases) {
    const reference = forms.other ?? Object.values(forms)[0]
    for (const category of pluralCategories(locale)) expected.set(`${base}_${category}`, forms[category] ?? reference)
  }
  return { expected, pluralBases }
}

export function checkLocale(sourceFlat, localeFlat, locale) {
  const { expected, pluralBases } = expectedKeys(sourceFlat, locale)
  const report = { locale, total: expected.size, translated: 0, missing: [], empty: [], stale: [], unknownPlaceholders: [], missingPlaceholders: [] }
  for (const [key, sourceText] of expected) {
    const text = localeFlat[key]
    if (text === undefined) { report.missing.push(key); continue }
    if (typeof text !== 'string' || text.trim() === '') { report.empty.push(key); continue }
    report.translated++
    const want = placeholders(sourceText)
    const have = placeholders(text)
    const unknown = have.filter(token => !want.includes(token))
    const absent = want.filter(token => !have.includes(token))
    if (unknown.length) report.unknownPlaceholders.push({ key, placeholders: unknown })
    // A plural form may legitimately drop {{count}} ("one flow"); only
    // non-plural keys are held to the full set.
    if (absent.length && !pluralKey(key)) report.missingPlaceholders.push({ key, placeholders: absent })
  }
  for (const key of Object.keys(localeFlat)) {
    if (expected.has(key)) continue
    const plural = pluralKey(key)
    // A plural form the locale does not use (e.g. _few left over in zh-Hant)
    // is stale too, but a form of a base that exists in the source is only
    // stale if this locale does not need it: report both as stale.
    if (plural && pluralBases.has(plural.base)) { report.stale.push(key); continue }
    report.stale.push(key)
  }
  return report
}

function readJson(path) {
  return JSON.parse(readFileSync(path, 'utf8'))
}

/** Checks every locale folder under `dir` (except the source) that has a
 * <namespace>.json. Returns { source, reports, errors, warnings }. */
export function checkLocales(dir, { source = 'en-US', namespace = 'gofi', allowMissing = false } = {}) {
  const sourcePath = join(dir, source, `${namespace}.json`)
  if (!existsSync(sourcePath)) throw new Error(`source catalog not found: ${sourcePath} (run pnpm i18n:extract)`)
  const sourceFlat = flatten(readJson(sourcePath))
  const reports = []
  for (const locale of readdirSync(dir, { withFileTypes: true }).filter(entry => entry.isDirectory()).map(entry => entry.name).sort()) {
    if (locale === source) continue
    const path = join(dir, locale, `${namespace}.json`)
    if (!existsSync(path)) { reports.push({ locale, missingFile: path }); continue }
    reports.push(checkLocale(sourceFlat, flatten(readJson(path)), locale))
  }
  let errors = 0
  let warnings = 0
  for (const report of reports) {
    if (report.missingFile) { errors++; continue }
    errors += report.stale.length + report.unknownPlaceholders.length
    const untranslated = report.missing.length + report.empty.length
    if (allowMissing) warnings += untranslated
    else errors += untranslated
    warnings += report.missingPlaceholders.length
  }
  return { source, sourceKeys: Object.keys(sourceFlat).length, reports, errors, warnings }
}

function formatReport(result, allowMissing) {
  const lines = [`i18n-check: source ${result.source} has ${result.sourceKeys} keys`]
  for (const report of result.reports) {
    if (report.missingFile) { lines.push(`  ${report.locale}: ERROR catalog not found at ${report.missingFile}`); continue }
    const untranslated = report.missing.length + report.empty.length
    const pct = report.total ? Math.round((report.translated / report.total) * 100) : 100
    lines.push(`  ${report.locale}: ${report.translated}/${report.total} translated (${pct}%)`)
    const list = (label, items, severity) => {
      if (!items.length) return
      lines.push(`    ${severity} ${items.length} ${label}:`)
      for (const item of items.slice(0, 50)) lines.push(`      ${typeof item === 'string' ? item : `${item.key}  ${item.placeholders.join(' ')}`}`)
      if (items.length > 50) lines.push(`      … ${items.length - 50} more`)
    }
    const untranslatedSeverity = allowMissing ? 'warning' : 'ERROR'
    list('missing keys', report.missing, untranslatedSeverity)
    list('empty translations', report.empty, untranslatedSeverity)
    list('stale keys (not in source)', report.stale, 'ERROR')
    list('unknown placeholders (not in source)', report.unknownPlaceholders, 'ERROR')
    list('placeholders dropped from source', report.missingPlaceholders, 'warning')
    if (!untranslated && !report.stale.length && !report.unknownPlaceholders.length) lines.push('    ok')
  }
  lines.push(`${result.errors} error(s), ${result.warnings} warning(s)`)
  return lines.join('\n')
}

function parseArgs(argv) {
  const options = { dir: 'src/locales', source: 'en-US', namespace: 'gofi', allowMissing: false, json: false }
  for (let i = 0; i < argv.length; i++) {
    const arg = argv[i]
    if (arg === '--allow-missing') options.allowMissing = true
    else if (arg === '--json') options.json = true
    else if (arg === '--dir') options.dir = argv[++i]
    else if (arg === '--source') options.source = argv[++i]
    else if (arg === '--namespace') options.namespace = argv[++i]
    else if (arg === '--help' || arg === '-h') { console.log('usage: i18n-check.mjs [--dir src/locales] [--source en-US] [--namespace gofi] [--allow-missing] [--json]'); process.exit(0) }
    else throw new Error(`unknown argument: ${arg}`)
  }
  return options
}

export function main(argv = process.argv.slice(2)) {
  const options = parseArgs(argv)
  const result = checkLocales(resolve(options.dir), options)
  console.log(options.json ? JSON.stringify(result, null, 2) : formatReport(result, options.allowMissing))
  return result.errors > 0 ? 1 : 0
}

if (process.argv[1] && import.meta.url === pathToFileURL(resolve(process.argv[1])).href) {
  try {
    process.exit(main())
  } catch (error) {
    console.error(`i18n-check: ${error.message}`)
    process.exit(2)
  }
}
