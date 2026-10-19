#!/usr/bin/env node
// Finds user-visible strings in src/**/*.tsx that bypass t()/<Trans>:
//
//   node scripts/i18n-lint.mjs [--dir src] [--strict] [--max N] [--json] [--quiet]
//
// Reported:
//   jsx-text     text between JSX tags            <h3>Theme</h3>
//   jsx-prop     string props among PROP_NAMES    <Button tooltip="Save" />, placeholder={'…'}
//   object-prop  the same property names in object literals  { label: 'Flows', value: 'flows' }
// A string counts when it contains letters and is not inside a call (t(),
// any helper) or a <Trans> element. Skipped: `// i18n-ignore` on the same or
// previous line (or `{/* i18n-ignore */}`), and text made only of allowlisted
// terms (scripts/i18n-allowlist.json: brand names, acronyms, key names).
//
// Prints file:line:col per hit plus counts by category and area. Exit code 0
// unless --strict (or --max N is exceeded): CI only reports for now.
// Node only; parses with the TypeScript compiler API already in devDependencies.

import { readFileSync, readdirSync, statSync, existsSync } from 'node:fs'
import { createRequire } from 'node:module'
import { join, relative, resolve, dirname } from 'node:path'
import { fileURLToPath, pathToFileURL } from 'node:url'

/** The TypeScript compiler API (typescript 5.x). The project's own
 * `typescript` is 7.x (the native compiler, no JS API), so fall back to the
 * copy i18next-parser depends on, which the i18n toolchain needs anyway. */
function loadTypeScript() {
  const requireHere = createRequire(import.meta.url)
  const candidates = [
    () => requireHere('typescript'),
    () => createRequire(requireHere.resolve('i18next-parser'))('typescript'),
  ]
  for (const load of candidates) {
    try {
      const candidate = load()
      if (typeof candidate?.createSourceFile === 'function') return candidate
    } catch { /* try the next */ }
  }
  throw new Error('no TypeScript compiler API found: install typescript 5.x or i18next-parser')
}
const ts = loadTypeScript()

export const PROP_NAMES = new Set(['label', 'placeholder', 'title', 'tooltip', 'aria-label', 'description', 'text', 'message', 'confirmText', 'subTitle'])
const IGNORE_TOKEN = 'i18n-ignore'
const TRANSLATION_CALLS = new Set(['t', 'Trans'])

const here = dirname(fileURLToPath(import.meta.url))

export function loadAllowlist(path = join(here, 'i18n-allowlist.json')) {
  if (!existsSync(path)) return { terms: [], patterns: [] }
  const parsed = JSON.parse(readFileSync(path, 'utf8'))
  return { terms: parsed.terms ?? [], patterns: (parsed.patterns ?? []).map(pattern => new RegExp(pattern, 'u')) }
}

/** True when `text` has no letters left after removing allowlisted terms. */
export function isAllowlisted(text, allowlist) {
  const trimmed = text.trim()
  if (!trimmed) return true
  if (allowlist.patterns.some(pattern => pattern.test(trimmed))) return true
  let rest = trimmed
  for (const term of [...allowlist.terms].sort((a, b) => b.length - a.length)) rest = rest.split(term).join(' ')
  return !/\p{L}/u.test(rest)
}

const hasLetters = text => /\p{L}/u.test(text)

/** Walks every file under dir with one of the extensions, skipping tests and generated code. */
export function listFiles(dir, extensions = ['.tsx'], out = []) {
  for (const entry of readdirSync(dir).sort()) {
    const path = join(dir, entry)
    if (statSync(path).isDirectory()) {
      if (entry === 'gen' || entry === 'node_modules' || entry === 'locales') continue
      listFiles(path, extensions, out)
    } else if (extensions.some(ext => path.endsWith(ext)) && !/\.test\.[cm]?[jt]sx?$/.test(path)) out.push(path)
  }
  return out
}

/** String literals reachable through ?:, ??, ||, template literals and
 * parentheses, stopping at calls (t(...), formatX(...)) and JSX. */
function bareStrings(node) {
  if (!node) return []
  if (ts.isStringLiteral(node) || ts.isNoSubstitutionTemplateLiteral(node)) return [{ node, text: node.text }]
  if (ts.isTemplateExpression(node)) {
    const text = [node.head.text, ...node.templateSpans.map(span => span.literal.text)].join(' ')
    return hasLetters(text) ? [{ node, text }] : []
  }
  if (ts.isParenthesizedExpression(node) || ts.isAsExpression(node) || ts.isSatisfiesExpression?.(node)) return bareStrings(node.expression)
  if (ts.isConditionalExpression(node)) return [...bareStrings(node.whenTrue), ...bareStrings(node.whenFalse)]
  if (ts.isBinaryExpression(node)) {
    const kind = node.operatorToken.kind
    if (kind === ts.SyntaxKind.QuestionQuestionToken || kind === ts.SyntaxKind.BarBarToken || kind === ts.SyntaxKind.AmpersandAmpersandToken) {
      return [...bareStrings(node.left), ...bareStrings(node.right)]
    }
    if (kind === ts.SyntaxKind.PlusToken) return [...bareStrings(node.left), ...bareStrings(node.right)]
  }
  return []
}

function isTransElement(node) {
  const tag = ts.isJsxElement(node) ? node.openingElement.tagName : ts.isJsxSelfClosingElement(node) ? node.tagName : undefined
  return !!tag && ts.isIdentifier(tag) && tag.text === 'Trans'
}

export function lintSource(fileName, source, allowlist) {
  const file = ts.createSourceFile(fileName, source, ts.ScriptTarget.Latest, true, ts.ScriptKind.TSX)
  const lines = source.split('\n')
  const hits = []
  // `// i18n-ignore` (or `{/* i18n-ignore */}`) on the same line, or alone
  // on the line above, silences a hit.
  const standaloneComment = /^\s*\{?\s*(\/\/.*|\/\*.*\*\/)\s*\}?\s*$/
  const ignored = line => {
    if ((lines[line] ?? '').includes(IGNORE_TOKEN)) return true
    const previous = lines[line - 1] ?? ''
    return previous.includes(IGNORE_TOKEN) && standaloneComment.test(previous)
  }

  const report = (node, text, category) => {
    const clean = text.replace(/\s+/g, ' ').trim()
    if (!hasLetters(clean) || isAllowlisted(clean, allowlist)) return
    const { line, character } = file.getLineAndCharacterOfPosition(node.getStart(file))
    if (ignored(line)) return
    hits.push({ file: fileName, line: line + 1, column: character + 1, category, text: clean.length > 80 ? `${clean.slice(0, 77)}...` : clean })
  }

  const visit = node => {
    if (ts.isCallExpression(node)) {
      const callee = node.expression
      const name = ts.isIdentifier(callee) ? callee.text : ts.isPropertyAccessExpression(callee) ? callee.name.text : ''
      // Anything inside t('…', '…') is a key or an English default.
      if (TRANSLATION_CALLS.has(name)) return
    }
    if (isTransElement(node)) return
    if (ts.isJsxText(node)) {
      report(node, node.text, 'jsx-text')
    } else if (ts.isJsxAttribute(node) && node.initializer) {
      const name = ts.isIdentifier(node.name) ? node.name.text : node.name.getText(file)
      if (PROP_NAMES.has(name)) {
        const value = ts.isJsxExpression(node.initializer) ? node.initializer.expression : node.initializer
        for (const found of bareStrings(value)) report(found.node, found.text, 'jsx-prop')
      }
    } else if (ts.isPropertyAssignment(node)) {
      const name = ts.isIdentifier(node.name) || ts.isStringLiteral(node.name) ? node.name.text : ''
      if (PROP_NAMES.has(name)) for (const found of bareStrings(node.initializer)) report(found.node, found.text, 'object-prop')
    }
    ts.forEachChild(node, visit)
  }
  visit(file)
  return hits
}

/** Top-level folder under src, for the per-area summary (shell, pages, components/canvas, …). */
function areaOf(path) {
  const parts = path.split(/[\\/]/)
  const index = parts.indexOf('src')
  const rest = parts.slice(index + 1, -1)
  return rest.length === 0 ? '(root)' : rest[0] === 'components' && rest[1] ? `components/${rest[1]}` : rest[0]
}

export function lintDirectory(dir, allowlist = loadAllowlist()) {
  const hits = []
  for (const path of listFiles(dir)) hits.push(...lintSource(path, readFileSync(path, 'utf8'), allowlist))
  return hits
}

export function summarize(hits) {
  const byCategory = {}
  const byArea = {}
  for (const hit of hits) {
    byCategory[hit.category] = (byCategory[hit.category] ?? 0) + 1
    const area = areaOf(hit.file)
    byArea[area] = (byArea[area] ?? 0) + 1
  }
  return { total: hits.length, byCategory, byArea }
}

function parseArgs(argv) {
  const options = { dir: 'src', strict: false, max: undefined, json: false, quiet: false }
  for (let i = 0; i < argv.length; i++) {
    const arg = argv[i]
    if (arg === '--strict') options.strict = true
    else if (arg === '--json') options.json = true
    else if (arg === '--quiet') options.quiet = true
    else if (arg === '--max') options.max = Number(argv[++i])
    else if (arg === '--dir') options.dir = argv[++i]
    else if (arg === '--help' || arg === '-h') { console.log('usage: i18n-lint.mjs [--dir src] [--strict] [--max N] [--json] [--quiet]'); process.exit(0) }
    else throw new Error(`unknown argument: ${arg}`)
  }
  return options
}

export function main(argv = process.argv.slice(2)) {
  const options = parseArgs(argv)
  const root = resolve(options.dir)
  const hits = lintDirectory(root)
  const summary = summarize(hits)
  if (options.json) {
    console.log(JSON.stringify({ hits, summary }, null, 2))
  } else {
    if (!options.quiet) for (const hit of hits) console.log(`${relative(process.cwd(), hit.file)}:${hit.line}:${hit.column}  [${hit.category}]  ${JSON.stringify(hit.text)}`)
    const areas = Object.entries(summary.byArea).sort((a, b) => b[1] - a[1]).map(([area, count]) => `${area} ${count}`).join(', ')
    const categories = Object.entries(summary.byCategory).map(([category, count]) => `${category} ${count}`).join(', ')
    console.log(`i18n-lint: ${summary.total} hardcoded string(s)${summary.total ? ` (${categories})` : ''}${areas ? `\n  by area: ${areas}` : ''}`)
  }
  if (options.strict && summary.total > 0) return 1
  if (options.max !== undefined && summary.total > options.max) { console.log(`i18n-lint: ${summary.total} exceeds --max ${options.max}`); return 1 }
  return 0
}

if (process.argv[1] && import.meta.url === pathToFileURL(resolve(process.argv[1])).href) {
  try {
    process.exit(main())
  } catch (error) {
    console.error(`i18n-lint: ${error.message}`)
    process.exit(2)
  }
}
