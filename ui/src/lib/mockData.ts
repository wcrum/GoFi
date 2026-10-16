import type { FlowDefinition } from '../types'

// Heuristic sample-data generator for mock mode.
//
// This is explicitly a *starting point* the user edits afterward, NOT a
// constraint solver. It makes a single best-effort pass over the definition's
// CEL-bearing fields, extracts `fields["name"]` references, and guesses a
// plausible placeholder value per referenced field:
//
//   - a field compared to a quoted string literal (`fields["x"] == "foo"`)
//     reuses that literal, so at least that one equality passes;
//   - a field compared to a bare number (`fields["x"] > 5`) gets a number a
//     little past the threshold;
//   - everything else gets a short placeholder string derived from the name.
//
// Limitations (intentional — don't try to fix these here, the user tweaks the
// result by hand): it does not understand boolean logic (&&/||/!), operator
// direction beyond a naive nudge, negation, ranges, nested/JSON field access,
// or any cross-field constraint. Two conflicting comparisons on the same field
// just take whichever the regex sees first. It is a scanner, not a solver.

interface Guess {
  value: unknown
}

// Matches a fields access: fields["name"] or fields['name']. Global so we can
// walk every occurrence in a string.
const FIELD_RE = /fields\[["']([^"']+)["']\]/g

/** Collect every string on the definition that may contain CEL / field
 * references worth scanning: each stage's expression/valueExpr/keyExpr, each
 * sink's `when`, and (as bare field names) schema stages' requiredFields. */
function collectExpressions(def: FlowDefinition): { exprs: string[]; bareFields: string[] } {
  const exprs: string[] = []
  const bareFields: string[] = []
  for (const stage of def.stages ?? []) {
    if (stage.expression) exprs.push(stage.expression)
    if (stage.valueExpr) exprs.push(stage.valueExpr)
    if (stage.keyExpr) exprs.push(stage.keyExpr)
    if (stage.kind === 'schema' && stage.requiredFields) bareFields.push(...stage.requiredFields)
  }
  for (const sink of def.sinks ?? []) {
    if (sink.when) exprs.push(sink.when)
  }
  if (def.deadLetterSink?.when) exprs.push(def.deadLetterSink.when)
  return { exprs, bareFields }
}

/** Sniff the substring immediately following a field reference for a simple
 * comparison against a literal, returning a guessed value. Best-effort: only
 * recognizes `== "..."` / `== '...'` (reuse the string) and the numeric
 * comparisons `> < >= <=` against a bare number (nudge past the threshold). */
function sniffLiteral(after: string): Guess | undefined {
  // Quoted-string equality: reuse the literal so the equality passes.
  const strEq = after.match(/^\s*==\s*["']([^"']*)["']/)
  if (strEq) return { value: strEq[1] }

  // Numeric comparison: pick a number a bit past the threshold in the
  // direction that satisfies it.
  const numCmp = after.match(/^\s*(>=|<=|>|<|==)\s*(-?\d+(?:\.\d+)?)/)
  if (numCmp) {
    const op = numCmp[1]
    const n = Number(numCmp[2])
    switch (op) {
      case '>':
      case '>=':
        return { value: op === '>' ? n + 1 : n }
      case '<':
      case '<=':
        return { value: op === '<' ? n - 1 : n }
      case '==':
        return { value: n }
    }
  }
  return undefined
}

/** Short placeholder string for a field we couldn't otherwise constrain. */
function placeholderFor(field: string): string {
  return `sample-${field}`
}

/** Scan every CEL-bearing field in `def` and build a plausible placeholder
 * message object, then serialize it per `def.parser.format`. See the module
 * comment for the heuristic and its limits. */
export function generateSampleRaw(def: FlowDefinition): string {
  const { exprs, bareFields } = collectExpressions(def)
  const fields: Record<string, unknown> = {}

  for (const expr of exprs) {
    FIELD_RE.lastIndex = 0
    let m: RegExpExecArray | null
    while ((m = FIELD_RE.exec(expr)) !== null) {
      const field = m[1]
      const after = expr.slice(m.index + m[0].length)
      const guess = sniffLiteral(after)
      // A literal-derived guess wins over an existing plain placeholder, but
      // don't clobber an already literal-derived value with a placeholder.
      if (guess) {
        fields[field] = guess.value
      } else if (!(field in fields)) {
        fields[field] = placeholderFor(field)
      }
    }
  }

  // Schema-required fields that weren't referenced in any expression still
  // need to be present for the schema stage to pass.
  for (const field of bareFields) {
    if (!(field in fields)) fields[field] = placeholderFor(field)
  }

  // If nothing was discoverable, hand back a single illustrative field so the
  // textarea isn't empty.
  if (Object.keys(fields).length === 0) fields.message = 'sample'

  return serialize(fields, def.parser?.format ?? 'json')
}

function serialize(fields: Record<string, unknown>, format: string): string {
  switch (format) {
    case 'json':
      return JSON.stringify(fields)
    case 'xml': {
      // Naive flat XML: <root><k>v</k>...</root>. Not namespace- or
      // schema-aware — just enough to feed a flat XML parser as a starting
      // point the user refines.
      const body = Object.entries(fields)
        .map(([k, v]) => `<${k}>${String(v)}</${k}>`)
        .join('')
      return `<root>${body}</root>`
    }
    default:
      // Unknown parser format — JSON is a reasonable, human-editable fallback.
      return JSON.stringify(fields)
  }
}
