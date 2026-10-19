// Pure helpers for the guided "New function" page: turns plain-language
// answers into library source by picking a starter template and substituting
// the answers into its keyword defaults, docstring, and (for a few goals) its
// body with plain string replacement. No React or network imports so
// node --test can load it. Python text in here is code, not UI copy.
import { insertTemplate, type FunctionTemplate } from '../canvas/python-templates.ts'
import type { FunctionTest, PythonFunctionKind } from '../../sandbox-api.ts'

export type Goal = 'parse' | 'transform' | 'filter' | 'sink' | 'source' | 'ai-classify' | 'ai-enrich' | 'ai-extract' | 'ai-sink'
export const GOALS: Goal[] = ['parse', 'transform', 'filter', 'sink', 'source', 'ai-classify', 'ai-enrich', 'ai-extract', 'ai-sink']
export const AI_GOALS: Goal[] = ['ai-classify', 'ai-enrich', 'ai-extract', 'ai-sink']
export const isAiGoal = (goal: Goal) => AI_GOALS.includes(goal)

export type ParseFormat = 'json' | 'csv' | 'keyvalue' | 'binary'
export type TransformAction = 'uppercase' | 'lowercase' | 'number' | 'rename' | 'default' | 'threshold' | 'describe'
export type SinkKind = 'http' | 'print'
export type SourceKind = 'fetcher' | 'streamer'

export interface WizardAnswers {
  goal: Goal
  /** The registered function name (a Python identifier); empty picks the goal's default. */
  functionName: string
  /** parse: input format; transform/filter/AI: a sample message as JSON. */
  parseFormat: ParseFormat
  sample: string
  /** parse (csv): column names in order. */
  columns: string
  /** transform. */
  field: string
  transformAction: TransformAction
  /** rename: the new name; default: the value; threshold: the number; describe: the words. */
  transformValue: string
  /** filter: the condition in words, e.g. "temperature > 30". */
  condition: string
  /** sink. */
  sinkKind: SinkKind
  url: string
  /** source. */
  sourceKind: SourceKind
  every: string
  /** AI classify. */
  labels: string
  dropLabels: string
  /** AI enrich/extract/sink: the question, the fields to extract, or the instruction. */
  question: string
  outputField: string
}

export const DEFAULT_ANSWERS: WizardAnswers = {
  goal: 'parse', functionName: '', parseFormat: 'json', sample: '', columns: '', field: '', transformAction: 'uppercase', transformValue: '',
  condition: '', sinkKind: 'print', url: 'https://api.example/events', sourceKind: 'fetcher', every: '30s',
  labels: 'normal, suspicious, critical', dropLabels: 'normal', question: '', outputField: 'summary',
}

/** Preferred template ids per goal, best first: the AI templates are being
 * renamed, so both the new and the former ids are accepted. */
export const TEMPLATE_IDS: Record<Goal, string[]> = {
  parse: ['parser'],
  transform: ['transformer'],
  filter: ['transformer'],
  sink: ['sink'],
  source: ['fetcher'],
  'ai-classify': ['ai-classify', 'llm-classify'],
  'ai-enrich': ['ai-enrich', 'llm-enrich'],
  'ai-extract': ['ai-extract', 'llm-enrich'],
  'ai-sink': ['ai-sink', 'llm-sink'],
}

export const GOAL_KIND: Record<Goal, PythonFunctionKind> = {
  parse: 'parser', transform: 'transformer', filter: 'transformer', sink: 'sink', source: 'fetcher',
  'ai-classify': 'transformer', 'ai-enrich': 'transformer', 'ai-extract': 'transformer', 'ai-sink': 'sink',
}

/** First template whose id is in `ids`, in order of preference. */
export function findTemplate(templates: FunctionTemplate[], ids: string[]): FunctionTemplate | undefined {
  for (const id of ids) { const found = templates.find(template => template.id === id); if (found) return found }
  return undefined
}

// ---- String helpers ---------------------------------------------------------

/** A Python string literal; JSON escapes are valid Python escapes. */
export const pythonString = (value: string) => JSON.stringify(value)
export const pythonList = (values: string[]) => `[${values.map(pythonString).join(', ')}]`

/** Comma- or newline-separated words, trimmed, deduplicated, empties dropped. */
export function splitList(text: string): string[] {
  const seen = new Set<string>()
  for (const item of text.split(/[,\n]/)) { const word = item.trim(); if (word) seen.add(word) }
  return [...seen]
}

/** A Python identifier from free text ("Parse JSON!" → parse_json); '' when nothing usable. */
export function pythonIdentifier(text: string): string {
  const slug = text.trim().toLowerCase().replace(/[^a-z0-9_]+/g, '_').replace(/^_+|_+$/g, '')
  return slug.replace(/^(?=\d)/, '_')
}

/** A library name from free text, valid for the editor's rule (lowercase, digits, hyphens). */
export function librarySlug(text: string): string {
  return text.trim().toLowerCase().replace(/[^a-z0-9]+/g, '-').replace(/^-+|-+$/g, '').replace(/^[^a-z]+/, '').slice(0, 40).replace(/-+$/, '')
}

export const LIBRARY_NAME_PATTERN = /^[a-z][a-z0-9-]*[a-z0-9]$/
/** Why a library name is not acceptable; '' when it is. Same rule as flow names, without dots. */
export function libraryNameProblem(name: string): 'required' | 'format' | '' {
  if (!name.trim()) return 'required'
  return LIBRARY_NAME_PATTERN.test(name) && name.length <= 63 ? '' : 'format'
}

/** Replaces the default of keyword parameter `name` in the first def whose
 * signature declares it; a no-op when no such parameter exists. The literal
 * must already be valid Python (use pythonString/pythonList). Handles
 * string, list, number, and bare-word defaults. */
/** Replaces the optional fields of a pydantic model class (`name: type | None = None`
 * lines after its docstring) with one per wanted field; no-op without the class. */
export function setModelFields(source: string, className: string, wanted: string[]): string {
  const names = wanted.map(pythonIdentifier).filter(Boolean)
  if (!names.length) return source
  const pattern = new RegExp(`(class ${className}\\(BaseModel\\):\\n(?:    """[^]*?"""\\n)?\\n?)((?:    \\w+: [^\\n]+\\n)+)`)
  const match = pattern.exec(source)
  if (!match) return source
  const fields = names.map(name => `    ${name}: str | None = None\n`).join('')
  return source.slice(0, match.index) + match[1] + fields + source.slice(match.index + match[0].length)
}

export function setKeywordDefault(source: string, name: string, literal: string): string {
  const pattern = new RegExp(`(\\b${name}\\s*:\\s*[^=,)]+?\\s*=\\s*)("(?:[^"\\\\]|\\\\.)*"|'(?:[^'\\\\]|\\\\.)*'|\\[[^\\]]*\\]|\\{[^}]*\\}|[^,)]+)`)
  return source.replace(pattern, (_match, head: string) => `${head}${literal}`)
}

/** Appends keyword-only parameters to the def of `functionName`, adding the
 * `*` marker when the signature has none yet. `params` are "name: type = default" texts. */
export function addKeywordParams(source: string, functionName: string, params: string[]): string {
  if (!params.length) return source
  const pattern = new RegExp(`^(\\s*(?:async\\s+)?def\\s+${functionName}\\s*\\()([^\\n]*?)(\\)\\s*(?:->[^:\\n]*)?:)`, 'm')
  return source.replace(pattern, (_match, open: string, args: string, close: string) => {
    const hasStar = /(^|,)\s*\*\s*(,|$)/.test(args)
    const existing = args.trim()
    const added = `${hasStar || !existing ? '' : '*, '}${params.join(', ')}`
    return `${open}${existing ? `${existing}, ${added}` : added}${close}`
  })
}

/** Replaces `every="..."` in a decorator call. */
export function setDecoratorEvery(source: string, every: string): string {
  return source.replace(/(every\s*=\s*)("[^"]*"|'[^']*')/, (_match, head: string) => `${head}${pythonString(every)}`)
}

/** Renames the function registered under `kind`: its decorator name and its def. */
export function renameFunction(source: string, kind: PythonFunctionKind, newName: string): string {
  const decorator = new RegExp(`^@gofi\\.${kind}\\(\\s*(?:name\\s*=\\s*)?["']([^"']+)["']`, 'm')
  const match = decorator.exec(source)
  if (!match) return source
  const oldName = match[1]
  const renamed = source.replace(decorator, found => found.replace(new RegExp(`(["'])${oldName}(["'])$`), `$1${newName}$2`))
  // The def may use a different Python name than the registered one.
  const defPattern = new RegExp(`(^(?:\\s*)(?:async\\s+)?def\\s+)([A-Za-z_]\\w*)(?=\\s*\\()`, 'm')
  const afterDecorator = renamed.slice(renamed.indexOf(`@gofi.${kind}(`))
  const def = defPattern.exec(afterDecorator)
  if (!def) return renamed
  const defName = def[2]
  return renamed.replace(new RegExp(`(^(?:\\s*)(?:async\\s+)?def\\s+)${defName}(?=\\s*\\()`, 'm'), `$1${newName}`)
}

/** Replaces the first line of the docstring of `functionName` (the summary) and
 * keeps the rest of it; a no-op when the function has no docstring. */
export function setDocstringSummary(source: string, functionName: string, summary: string): string {
  const def = new RegExp(`^\\s*(?:async\\s+)?def\\s+${functionName}\\s*\\([^\\n]*$`, 'm').exec(source)
  if (!def) return source
  const start = def.index + def[0].length
  const docstring = /^(\s*"""\s*)([^\n"]*)/.exec(source.slice(start))
  if (!docstring) return source
  const line = summary.replace(/["\\]/g, '').replace(/\s+/g, ' ').trim()
  return source.slice(0, start) + docstring[1] + line + source.slice(start + docstring[0].length)
}

/** Replaces the body of `functionName` after its docstring (or its signature
 * when it has none) with `body`, indented 4 spaces. The body ends where the
 * indentation returns to column 0. */
export function replaceBody(source: string, functionName: string, body: string): string {
  const lines = source.split('\n')
  const defIndex = lines.findIndex(line => new RegExp(`^(?:async\\s+)?def\\s+${functionName}\\s*\\(`).test(line))
  if (defIndex < 0) return source
  let bodyStart = defIndex + 1
  if (/^\s+"""/.test(lines[bodyStart] ?? '')) {
    // Single- or multi-line docstring.
    const single = /^\s+""".*"""\s*$/.test(lines[bodyStart])
    bodyStart++
    if (!single) { while (bodyStart < lines.length && !/"""\s*$/.test(lines[bodyStart])) bodyStart++; bodyStart++ }
  }
  let bodyEnd = bodyStart
  while (bodyEnd < lines.length && (lines[bodyEnd] === '' || /^\s/.test(lines[bodyEnd]))) bodyEnd++
  // Keep blank lines that separate this function from the next definition.
  while (bodyEnd > bodyStart && lines[bodyEnd - 1] === '') bodyEnd--
  const indented = body.trimEnd().split('\n').map(line => (line ? `    ${line}` : ''))
  return [...lines.slice(0, bodyStart), ...indented, ...lines.slice(bodyEnd)].join('\n')
}

/** Adds `import name` at the top of a snippet when it is not imported yet. */
export function ensureImport(source: string, name: string): string {
  if (new RegExp(`^\\s*(?:import\\s+${name}\\b|from\\s+${name}\\s+import\\b)`, 'm').test(source)) return source
  return `import ${name}\n\n${source}`
}

/** One-line, bounded preview of a sample for a docstring. */
export function sampleLine(sample: string, max = 80): string {
  const text = sample.replace(/\s+/g, ' ').trim()
  return text.length > max ? `${text.slice(0, max - 1)}…` : text
}

// ---- Plain-language condition -----------------------------------------------

const OPERATORS: [RegExp, string][] = [
  [/^(>=|is at least|at least|greater than or equal to)$/i, '>='],
  [/^(<=|is at most|at most|less than or equal to)$/i, '<='],
  [/^(>|is greater than|greater than|above|over|more than|exceeds)$/i, '>'],
  [/^(<|is less than|less than|below|under)$/i, '<'],
  [/^(!=|is not|isn't|not equal to|does not equal)$/i, '!='],
  [/^(==|=|is|equals|equal to)$/i, '=='],
  [/^(contains|includes|has)$/i, 'in'],
]

/** Translates "field op value" (e.g. "temperature > 30", "level is error",
 * "message contains timeout") into a Python expression over `fields`; null
 * when the words do not fit that shape. Numbers compare as floats. */
export function conditionToPython(words: string): string | null {
  const text = words.trim().replace(/\s+/g, ' ')
  const match = /^([A-Za-z_][\w.]*)\s+(.+?)\s+("[^"]*"|'[^']*'|\S+)$/.exec(text)
  if (!match) return null
  const [, field, opWords, rawValue] = match
  const operator = OPERATORS.find(([pattern]) => pattern.test(opWords))?.[1]
  if (!operator) return null
  const quoted = /^(["']).*\1$/.test(rawValue) ? rawValue.slice(1, -1) : null
  const number = quoted === null && rawValue !== '' && !Number.isNaN(Number(rawValue)) ? Number(rawValue) : null
  const get = `fields.get(${pythonString(field)})`
  if (operator === 'in') return `${pythonString(quoted ?? rawValue)} in str(${get} or "")`
  if (number !== null) return `float(${get} or 0) ${operator} ${number}`
  const literal = /^(true|false)$/i.test(rawValue) && quoted === null ? (rawValue.toLowerCase() === 'true' ? 'True' : 'False') : pythonString(quoted ?? rawValue)
  return `${get} ${operator} ${literal}`
}

// ---- Composition ------------------------------------------------------------

export const DEFAULT_FUNCTION_NAMES: Record<Goal, string> = {
  parse: 'parse', transform: 'transform', filter: 'keep', sink: 'deliver', source: 'poll',
  'ai-classify': 'classify', 'ai-enrich': 'enrich', 'ai-extract': 'extract', 'ai-sink': 'handle',
}

export const functionNameOf = (answers: WizardAnswers) => pythonIdentifier(answers.functionName) || DEFAULT_FUNCTION_NAMES[answers.goal]

export interface Composition {
  source: string
  /** The template the source started from; undefined when none matched (the fallback body is used). */
  templateId?: string
  /** A sample test saved with the library, when the answers include a usable sample. */
  test?: FunctionTest
}

const FALLBACK: Record<PythonFunctionKind, string> = {
  parser: '@gofi.parser("parse")\ndef parse(data: bytes) -> dict | None:\n    """Turn one frame into fields. Return None to drop it."""\n    return {"raw": data.decode("utf-8", errors="replace")}\n',
  transformer: '@gofi.transformer("transform")\ndef transform(fields: dict, meta: dict) -> dict | None:\n    """Return the updated fields, or None to drop the message."""\n    return fields\n',
  sink: '@gofi.sink("deliver")\ndef deliver(fields: dict, meta: dict) -> None:\n    """Deliver one message."""\n    print(meta.get("name"), fields)\n',
  fetcher: '@gofi.fetcher("poll", every="30s")\ndef poll(ctx: gofi.SourceContext, *, url: str = "https://api.example/items") -> list[gofi.Frame]:\n    """Return new items as frames."""\n    return []\n',
  streamer: 'from collections.abc import Iterator\n\n\n@gofi.streamer("ticks")\ndef ticks(ctx: gofi.SourceContext, *, interval: float = 5.0) -> Iterator[dict]:\n    """Yield frames as they arrive."""\n    while not ctx.wait(interval):\n        yield {}\n',
  framer: '', decoder: '', encoder: '',
}

function parseBody(format: ParseFormat, columns: string[]): string {
  switch (format) {
    case 'json': return 'return json.loads(data)'
    case 'csv': return `text = data.decode("utf-8", errors="replace").strip()\nif not text:\n    return None\nvalues = next(csv.reader([text]))\nreturn dict(zip(COLUMNS, values))`
    // KEY=VALUE pairs: a regex tokenizer (newlines, commas, and quoted values
    // are fine) and a pydantic model that types and checks the fields.
    case 'keyvalue': return `pairs = {m.group(1): m.group(3) if m.group(3) is not None else m.group(4) if m.group(4) is not None else m.group(5) for m in PAIR.finditer(data.decode("utf-8", errors="replace"))}\nif not pairs:\n    return None\ntry:\n    return Fields.model_validate(pairs).model_dump()\nexcept ValidationError as error:\n    return {**pairs, "_invalid": error.errors()[0]["msg"]}`
    case 'binary': return ''
  }
}

/** A pydantic model for KEY=VALUE input, its fields inferred from the sample
 * (int, float, bool, else str; every field optional). Unknown keys are kept
 * (extra="allow"), so the model only ever adds typing, never drops data. */
export function keyValueModel(sample: string): string {
  const pair = /([A-Za-z_][\w.-]*)=("([^"]*)"|'([^']*)'|([^\s,]+))/g
  const lines: string[] = []
  const seen = new Set<string>()
  for (const match of sample.matchAll(pair)) {
    const key = match[1]
    const value = match[3] ?? match[4] ?? match[5] ?? ''
    const name = pythonIdentifier(key)
    if (!name || seen.has(name)) continue
    seen.add(name)
    const type = /^(true|false)$/i.test(value) ? 'bool' : /^-?\d+$/.test(value) ? 'int' : /^-?\d+\.\d+$/.test(value) ? 'float' : 'str'
    lines.push(name === key ? `    ${name}: ${type} | None = None` : `    ${name}: ${type} | None = Field(default=None, alias=${pythonString(key)})`)
  }
  const aliased = lines.some(line => line.includes('Field('))
  const body = lines.length ? lines.join('\n') : '    # Add the fields you expect, e.g. host: str | None = None'
  return `class Fields(BaseModel):\n    """The fields a message carries; pydantic turns "31.5" into a float and "true" into a bool. Unknown keys stay as text."""\n\n    model_config = ConfigDict(extra="allow"${aliased ? ', populate_by_name=True' : ''})\n\n${body}`
}

function transformBody(action: TransformAction, field: string, value: string): string | null {
  const key = pythonString(field || 'value')
  switch (action) {
    case 'uppercase': return `if ${key} in fields:\n    fields[${key}] = str(fields[${key}]).upper()\nreturn fields`
    case 'lowercase': return `if ${key} in fields:\n    fields[${key}] = str(fields[${key}]).lower()\nreturn fields`
    case 'number': return `try:\n    fields[${key}] = float(fields.get(${key}, 0))\nexcept (TypeError, ValueError):\n    fields[f"{${key}}_error"] = "not a number"\nreturn fields`
    case 'rename': return `if ${key} in fields:\n    fields[${pythonString(pythonIdentifier(value) || 'renamed')}] = fields.pop(${key})\nreturn fields`
    case 'default': return `fields.setdefault(${key}, ${/^-?\d+(\.\d+)?$/.test(value.trim()) ? value.trim() : pythonString(value)})\nreturn fields`
    case 'threshold': return null // the template's own threshold/label body, with the defaults substituted
    case 'describe': return `# TODO: ${sampleLine(value || 'describe the change', 120)}\nreturn fields`
  }
}

/** Composes a complete library source for the answers from the templates.
 * The result always parses as a module starting with `import gofi`. */
export function composeSource(answers: WizardAnswers, templates: FunctionTemplate[]): Composition {
  const kind = answers.goal === 'source' ? answers.sourceKind : GOAL_KIND[answers.goal]
  const ids = answers.goal === 'source' ? [answers.sourceKind] : TEMPLATE_IDS[answers.goal]
  const template = findTemplate(templates, ids)
  let snippet = template?.source ?? FALLBACK[kind]
  const name = functionNameOf(answers)
  snippet = renameFunction(snippet, kind, name)
  let test: FunctionTest | undefined
  const sampleFields = parseSampleFields(answers.sample)

  switch (answers.goal) {
    case 'parse': {
      const columns = splitList(answers.columns)
      const body = parseBody(answers.parseFormat, columns)
      if (body) snippet = replaceBody(snippet, name, body)
      if (answers.parseFormat === 'json') snippet = ensureImport(snippet, 'json')
      if (answers.parseFormat === 'csv') snippet = `import csv\n\nCOLUMNS = ${pythonList(columns.length ? columns : ['column_1', 'column_2'])}\n\n\n${snippet}`
      if (answers.parseFormat === 'keyvalue') snippet = `import re\n\nfrom pydantic import BaseModel, ConfigDict, Field, ValidationError\n\nPAIR = re.compile(r'([A-Za-z_][\\w.\\-]*)=("([^"]*)"|\\'([^\\']*)\\'|([^\\s,]+))')\n\n\n${keyValueModel(answers.sample)}\n\n\n${snippet}`
      const label = { json: 'Parse one JSON object per frame into fields.', csv: 'Parse one CSV row per frame into COLUMNS.', keyvalue: 'Parse KEY=VALUE pairs (any whitespace, newlines, or commas between them; values may be quoted) into fields typed by the Fields model.', binary: 'Parse a fixed-layout binary frame; adjust the struct format to your layout.' }[answers.parseFormat]
      snippet = setDocstringSummary(snippet, name, answers.sample.trim() ? `${label} Example input: ${sampleLine(answers.sample)}` : label)
      if (answers.sample.trim() && answers.parseFormat !== 'binary') test = { name: 'Sample input', function: name, kind: 'parser', raw: answers.sample }
      break
    }
    case 'transform': {
      const body = transformBody(answers.transformAction, answers.field, answers.transformValue)
      if (body) snippet = replaceBody(snippet, name, body)
      if (answers.transformAction === 'threshold') {
        const threshold = Number(answers.transformValue)
        if (!Number.isNaN(threshold) && answers.transformValue.trim()) snippet = setKeywordDefault(snippet, 'threshold', String(threshold))
        snippet = snippet.replace(/fields\.get\("value", 0\)/, `fields.get(${pythonString(answers.field || 'value')}, 0)`)
      }
      snippet = setDocstringSummary(snippet, name, transformSummary(answers))
      if (sampleFields) test = { name: 'Sample message', function: name, kind: 'transformer', fields: sampleFields }
      break
    }
    case 'filter': {
      const expression = conditionToPython(answers.condition)
      const comment = answers.condition.trim() ? `# Keep a message when: ${sampleLine(answers.condition, 120)}` : '# Keep a message when the condition below holds'
      snippet = replaceBody(snippet, name, expression ? `${comment}\nkeep = ${expression}\nreturn fields if keep else None` : `${comment}\nkeep = True  # TODO: translate the condition into Python\nreturn fields if keep else None`)
      // The transformer template's threshold/label parameters no longer apply.
      snippet = snippet.replace(/,\s*\*\s*,\s*threshold: float = [^,)]+,\s*label: str = [^)]+\)/, ')')
      snippet = setDocstringSummary(snippet, name, answers.condition.trim() ? `Keep messages where ${sampleLine(answers.condition)}; drop the rest.` : 'Keep messages that match the condition; drop the rest.')
      if (sampleFields) test = { name: 'Sample message', function: name, kind: 'transformer', fields: sampleFields }
      break
    }
    case 'sink': {
      if (answers.sinkKind === 'http') {
        snippet = addKeywordParams(snippet, name, [`url: str = ${pythonString(answers.url || 'https://api.example/events')}`, 'timeout: float = 10.0'])
        snippet = replaceBody(snippet, name, `request = urllib.request.Request(url, data=json.dumps(fields).encode(), headers={"Content-Type": "application/json"}, method="POST")\nwith urllib.request.urlopen(request, timeout=timeout) as response:\n    if response.status >= 300:\n        raise RuntimeError(f"{url} answered {response.status}")`)
        snippet = ensureImport(ensureImport(snippet, 'urllib.request'), 'json')
        snippet = setDocstringSummary(snippet, name, `POST each message as JSON to ${answers.url || 'url'}. The library needs an egress rule for that host; a failure counts as a delivery failure.`)
      } else {
        snippet = setDocstringSummary(snippet, name, 'Print each delivered message to the worker log.')
      }
      if (sampleFields) test = { name: 'Sample message', function: name, kind: 'sink', fields: sampleFields }
      break
    }
    case 'source': {
      if (answers.sourceKind === 'fetcher') {
        if (answers.url.trim()) snippet = setKeywordDefault(snippet, 'url', pythonString(answers.url.trim()))
        if (answers.every.trim()) snippet = setDecoratorEvery(snippet, answers.every.trim())
        snippet = setDocstringSummary(snippet, name, `Poll ${answers.url.trim() || 'the endpoint'} every ${answers.every.trim() || '30s'} and return new items as frames.`)
      } else {
        const interval = Number(answers.every)
        if (!Number.isNaN(interval) && answers.every.trim()) snippet = setKeywordDefault(snippet, 'interval', String(interval))
        snippet = setDocstringSummary(snippet, name, 'Yield frames continuously while the flow runs.')
      }
      break
    }
    case 'ai-classify': {
      const labels = splitList(answers.labels)
      const drop = splitList(answers.dropLabels).filter(label => labels.includes(label))
      if (labels.length) snippet = setKeywordDefault(snippet, 'labels', pythonList(labels))
      snippet = setKeywordDefault(snippet, 'drop', pythonList(drop))
      if (answers.question.trim()) snippet = setKeywordDefault(snippet, 'instructions', pythonString(answers.question.trim()))
      snippet = setDocstringSummary(snippet, name, `Label each message as one of ${labels.join(', ') || 'the labels'}${drop.length ? ` and drop ${drop.join(', ')}` : ''}.`)
      if (sampleFields) test = { name: 'Sample message', function: name, kind: 'transformer', fields: sampleFields }
      break
    }
    case 'ai-enrich': {
      const output = pythonIdentifier(answers.outputField) || 'summary'
      if (answers.question.trim()) snippet = setKeywordDefault(snippet, 'prompt', pythonString(`${answers.question.trim()}\n\n{fields}`))
      snippet = setKeywordDefault(snippet, 'output_key', pythonString(output))
      snippet = setDocstringSummary(snippet, name, `Ask the model "${sampleLine(answers.question || 'the question', 60)}" about each message and store the answer in ${output}.`)
      if (sampleFields) test = { name: 'Sample message', function: name, kind: 'transformer', fields: sampleFields }
      break
    }
    case 'ai-extract': {
      const wanted = splitList(answers.question)
      const output = pythonIdentifier(answers.outputField) || 'extracted'
      const prompt = `Extract ${wanted.length ? wanted.join(', ') : 'the key facts'} from this event. Answer with a JSON object with exactly those keys; use null when a value is absent.\n\n{fields}`
      // The gofi.llm template declares a pydantic model and an `instructions`
      // keyword; the older enrich-style template takes a prompt. Each
      // substitution is a no-op where the keyword does not exist.
      snippet = setKeywordDefault(snippet, 'instructions', pythonString(`Extract ${wanted.length ? wanted.join(', ') : 'the key facts'} from this event; use null when a value is absent.`))
      snippet = setModelFields(snippet, 'Extracted', wanted)
      snippet = setKeywordDefault(snippet, 'prompt', pythonString(prompt))
      snippet = setKeywordDefault(snippet, 'system', pythonString('You extract structured data. Reply with JSON only.'))
      snippet = setKeywordDefault(snippet, 'output_key', pythonString(output))
      snippet = setKeywordDefault(snippet, 'fields', pythonList(wanted))
      snippet = setDocstringSummary(snippet, name, `Extract ${wanted.join(', ') || 'structured fields'} from each message into ${output}.`)
      if (sampleFields) test = { name: 'Sample message', function: name, kind: 'transformer', fields: sampleFields }
      break
    }
    case 'ai-sink': {
      if (answers.question.trim()) snippet = setKeywordDefault(snippet, 'prompt', pythonString(`${answers.question.trim()}\n\n{fields}`))
      snippet = setDocstringSummary(snippet, name, `Send each delivered message to the model with: ${sampleLine(answers.question || 'the instruction', 60)}`)
      if (sampleFields) test = { name: 'Sample message', function: name, kind: 'sink', fields: sampleFields }
      break
    }
  }
  return { source: insertTemplate('', snippet), templateId: template?.id, test }
}

function transformSummary(answers: WizardAnswers): string {
  const field = answers.field || 'value'
  switch (answers.transformAction) {
    case 'uppercase': return `Upper-case ${field}.`
    case 'lowercase': return `Lower-case ${field}.`
    case 'number': return `Convert ${field} to a number.`
    case 'rename': return `Rename ${field} to ${pythonIdentifier(answers.transformValue) || 'renamed'}.`
    case 'default': return `Set ${field} to ${answers.transformValue || 'a default'} when it is missing.`
    case 'threshold': return `Label the message when ${field} reaches the threshold; both are per-node settings.`
    case 'describe': return sampleLine(answers.transformValue || `Change ${field}.`)
  }
}

/** The sample as a JSON object, or undefined when it is not one. */
export function parseSampleFields(sample: string): Record<string, unknown> | undefined {
  if (!sample.trim()) return undefined
  try { const value: unknown = JSON.parse(sample); return value && typeof value === 'object' && !Array.isArray(value) ? value as Record<string, unknown> : undefined } catch { return undefined }
}

/** A suggested library name for the answers. */
export function suggestName(answers: WizardAnswers): string {
  switch (answers.goal) {
    case 'parse': return `parse-${answers.parseFormat === 'keyvalue' ? 'key-value' : answers.parseFormat}`
    case 'transform': return librarySlug(`${answers.transformAction}-${answers.field || 'fields'}`)
    case 'filter': return librarySlug(`filter-${answers.condition.split(/\s+/)[0] || 'messages'}`)
    case 'sink': return answers.sinkKind === 'http' ? 'http-sink' : 'print-sink'
    case 'source': return answers.sourceKind === 'fetcher' ? 'poll-source' : 'stream-source'
    case 'ai-classify': return 'ai-classify'
    case 'ai-enrich': return 'ai-enrich'
    case 'ai-extract': return 'ai-extract'
    case 'ai-sink': return 'ai-sink'
  }
}

/** The message that opens GoFiBot with the generated source and the intent. */
export function assistantMessage(answers: WizardAnswers, source: string): string {
  const intent: Record<Goal, string> = {
    parse: `parse ${answers.parseFormat} input into fields${answers.sample.trim() ? ` (example input: ${sampleLine(answers.sample)})` : ''}`,
    transform: transformSummary(answers).replace(/\.$/, '').toLowerCase(),
    filter: `keep only messages where ${answers.condition.trim() || 'a condition holds'}`,
    sink: answers.sinkKind === 'http' ? `POST each message as JSON to ${answers.url}` : 'print each message',
    source: answers.sourceKind === 'fetcher' ? `poll ${answers.url} every ${answers.every}` : 'stream frames continuously',
    'ai-classify': `classify each message as one of ${answers.labels}${answers.dropLabels.trim() ? ` and drop ${answers.dropLabels}` : ''}`,
    'ai-enrich': `ask "${answers.question.trim()}" about each message and store the answer in ${answers.outputField}`,
    'ai-extract': `extract ${answers.question.trim()} from each message into ${answers.outputField}`,
    'ai-sink': `send each delivered message to the model with "${answers.question.trim()}"`,
  }
  return `I am building a GoFi Python function to ${intent[answers.goal]}. Here is the generated starter; please review it and refine it so it does exactly that, keeping the @gofi decorator and signature:\n\n\`\`\`python\n${source.trimEnd()}\n\`\`\``
}
