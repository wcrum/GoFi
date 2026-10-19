// Pure helpers shared by the assistant API wrapper and the chat panel. No
// imports, so node --test can load them directly.

export const DEFAULT_MAX_TOOL_STEPS = 8
export const MIN_TOOL_STEPS = 1
export const MAX_TOOL_STEPS = 20

/** The server accepts 1-20; 0/NaN (an unset proto field) means the default. */
export function clampToolSteps(value: number): number {
  if (!Number.isFinite(value) || value <= 0) return DEFAULT_MAX_TOOL_STEPS
  return Math.min(MAX_TOOL_STEPS, Math.max(MIN_TOOL_STEPS, Math.round(value)))
}

/** Pretty-prints a tool's JSON input; anything that is not JSON is shown as-is. */
export function prettyJson(text: string): string {
  const trimmed = text.trim()
  if (!trimmed) return ''
  try { return JSON.stringify(JSON.parse(trimmed), null, 2) } catch { return text }
}

/** Keeps the visible part of a long tool output: the first `limit`
 * characters plus how much was cut. */
export function truncateOutput(text: string, limit = 4000): { text: string; cut: number } {
  if (text.length <= limit) return { text, cut: 0 }
  return { text: text.slice(0, limit), cut: text.length - limit }
}

/** The AbortSignal-friendly page context: the hash route of the current page. */
export function pageContextOf(hash: string): string {
  return hash && hash !== '#' ? hash : '#/'
}

/** The field of an apply tool's input that holds the thing being written. */
const PROPOSAL_CODE_FIELDS: Record<string, string> = { apply_flow: 'flow_yaml', apply_library: 'source', apply_expression: 'expression' }

/** Splits a proposal's input for display: `code` is the flow YAML, library
 * source, or expression (shown as a code block) and `rest` the remaining
 * fields pretty-printed (empty when there are none). Input that is not a
 * JSON object, or a tool without a code field, is all `rest`. */
export function proposalInput(name: string, inputJson: string): { code?: string; rest: string } {
  const field = PROPOSAL_CODE_FIELDS[name]
  let parsed: unknown
  try { parsed = JSON.parse(inputJson) } catch { return { rest: inputJson } }
  if (!field || typeof parsed !== 'object' || parsed === null || Array.isArray(parsed)) return { rest: prettyJson(inputJson) }
  const { [field]: code, ...others } = parsed as Record<string, unknown>
  if (typeof code !== 'string') return { rest: prettyJson(inputJson) }
  return { code, rest: Object.keys(others).length ? JSON.stringify(others, null, 2) : '' }
}

/** Monaco language id for a fenced-code language name; unknown names are plain text. */
export function codeLanguage(name: string): string {
  switch (name.trim().toLowerCase()) {
    case 'yaml': case 'yml': return 'yaml'
    case 'python': case 'py': return 'python'
    case 'json': return 'json'
    case 'cel': return 'cel'
    case 'bash': case 'sh': case 'shell': case 'zsh': return 'shell'
    case 'js': case 'javascript': return 'javascript'
    case 'ts': case 'typescript': return 'typescript'
    case 'xml': case 'html': return 'xml'
    default: return 'plaintext'
  }
}

/** The language of a tool input field holding code, by its name. */
export function fieldLanguage(key: string): string {
  switch (key) {
    case 'flow_yaml': case 'yaml': return 'yaml'
    case 'source': case 'python': return 'python'
    case 'expression': case 'cel': return 'cel'
    default: return 'plaintext'
  }
}

/** A guess at the language of a tool's output (the controller answers in YAML or text). */
export function outputLanguage(output: string): string {
  const trimmed = output.trim()
  if (!trimmed) return 'plaintext'
  if (/^[[{]/.test(trimmed)) { try { JSON.parse(trimmed); return 'json' } catch { /* not JSON */ } }
  const lines = trimmed.split('\n', 5)
  if (lines.every(line => !line.trim() || /^\s*(- |#)/.test(line) || /^\s*[\w.-]+:(\s|$)/.test(line))) return 'yaml'
  return 'plaintext'
}

export interface ToolInputView { fields: string; code: Array<{ key: string; language: string; source: string }> }
/** Splits a tool's JSON input for display: multi-line string fields become
 * code blocks (language by field name); the rest is pretty-printed JSON
 * (empty when nothing is left). Non-object input is all `fields`. */
export function toolInputView(inputJson: string): ToolInputView {
  const trimmed = inputJson.trim()
  if (!trimmed) return { fields: '', code: [] }
  let parsed: unknown
  try { parsed = JSON.parse(trimmed) } catch { return { fields: inputJson, code: [] } }
  if (typeof parsed !== 'object' || parsed === null || Array.isArray(parsed)) return { fields: JSON.stringify(parsed, null, 2), code: [] }
  const rest: Record<string, unknown> = {}
  const code: ToolInputView['code'] = []
  for (const [key, value] of Object.entries(parsed as Record<string, unknown>)) {
    if (typeof value === 'string' && value.includes('\n')) code.push({ key, language: fieldLanguage(key), source: value })
    else rest[key] = value
  }
  return { fields: Object.keys(rest).length ? JSON.stringify(rest, null, 2) : '', code }
}

/** One line for a collapsed tool row: its scalar inputs as `key: value`. */
export function summarizeInput(inputJson: string, limit = 80): string {
  let parsed: unknown
  try { parsed = JSON.parse(inputJson) } catch { return inputJson.trim().slice(0, limit) }
  if (typeof parsed !== 'object' || parsed === null || Array.isArray(parsed)) return String(JSON.stringify(parsed)).slice(0, limit)
  const parts: string[] = []
  for (const [key, value] of Object.entries(parsed as Record<string, unknown>)) {
    if (value === undefined || value === null || value === '') continue
    if (typeof value === 'string') parts.push(`${key}: ${value.includes('\n') ? `${value.split('\n').length} lines` : value}`)
    else if (typeof value === 'number' || typeof value === 'boolean') parts.push(`${key}: ${value}`)
    else if (Array.isArray(value)) parts.push(`${key}: [${value.length}]`)
    else parts.push(`${key}: {…}`)
  }
  const line = parts.join(' · ')
  return line.length > limit ? `${line.slice(0, limit - 1)}…` : line
}
