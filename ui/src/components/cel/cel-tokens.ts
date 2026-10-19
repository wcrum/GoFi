/** Token kinds for read-only CEL highlighting; they mirror the Monaco CEL
 * language in celLanguage.ts so tables and editors color alike. */
export type CelTokenKind = 'comment' | 'keyword' | 'variable' | 'function' | 'identifier' | 'string' | 'number' | 'operator' | 'delimiter' | 'text'

export interface CelToken { kind: CelTokenKind; text: string }

const KEYWORDS = new Set(['true', 'false', 'null', 'in'])
const VARIABLES = new Set(['fields', 'meta', 'raw'])

const RULES: Array<[RegExp, CelTokenKind | 'word']> = [
  [/^\/\/[^\n]*/, 'comment'],
  [/^[rR]?"(?:[^"\\]|\\.)*"?/, 'string'],
  [/^[rR]?'(?:[^'\\]|\\.)*'?/, 'string'],
  [/^0x[0-9a-fA-F]+[uU]?/, 'number'],
  [/^\d+(?:\.\d+)?(?:[eE][-+]?\d+)?[uU]?/, 'number'],
  [/^[a-zA-Z_]\w*/, 'word'],
  [/^(?:&&|\|\||[=!<>]=?|[-+*/%?:])/, 'operator'],
  [/^[{}()[\].,]/, 'delimiter'],
  [/^\s+/, 'text'],
]

/** Splits a CEL expression into colorable tokens. Never throws: anything
 * unrecognised becomes a one-character text token. */
export function tokenizeCel(source: string): CelToken[] {
  const tokens: CelToken[] = []
  let rest = source
  while (rest) {
    let matched = false
    for (const [pattern, kind] of RULES) {
      const match = pattern.exec(rest)
      if (!match) continue
      const text = match[0]
      rest = rest.slice(text.length)
      if (kind === 'word') {
        tokens.push({ text, kind: KEYWORDS.has(text) ? 'keyword' : VARIABLES.has(text) ? 'variable' : /^\s*\(/.test(rest) ? 'function' : 'identifier' })
      } else tokens.push({ text, kind })
      matched = true
      break
    }
    if (!matched) { tokens.push({ text: rest[0], kind: 'text' }); rest = rest.slice(1) }
  }
  return tokens
}
