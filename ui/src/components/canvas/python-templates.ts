// Pure helpers for the Python creator: starter templates, per-node config
// defaults from declared function parameters, and saved-test
// expectations. No React or network imports so node --test can load it.
import { t } from '../../i18n/index.ts'

export interface FunctionParamLike { name: string; type?: string; default?: unknown; required?: boolean; description?: string }

export const DEFAULT_LIBRARY = `import json
import gofi


@gofi.decoder("trim")
def trim(data: bytes) -> bytes:
    """Strip surrounding whitespace from each frame before parsing."""
    return data.strip()


@gofi.parser("telemetry")
def parse_telemetry(data: bytes) -> dict:
    """Decode JSON telemetry into fields."""
    return json.loads(data)


@gofi.transformer("enrich")
def enrich(fields: dict, meta: dict, *, threshold: float = 30.0) -> dict:
    """Flag hot readings; threshold is configurable per flow node."""
    fields["hot"] = float(fields.get("temperature", 0)) >= threshold
    print("enriched", fields.get("sensor"), "hot" if fields["hot"] else "ok")
    return fields
`

export interface FunctionTemplate { id: string; readonly label: string; readonly description: string; source: string }

/** Template titles and descriptions resolve in the active language when read
 * (getters), so the list can stay a plain exported array. */
function template(id: string, label: () => string, description: () => string, source: string): FunctionTemplate {
  return { id, get label() { return label() }, get description() { return description() }, source }
}

export const FUNCTION_TEMPLATES: FunctionTemplate[] = [
  template('fetcher', () => t('gofi.canvas.python-templates.fetcher', 'Fetcher (source)'), () => t('gofi.canvas.python-templates.fetcher-description', 'Poll on an interval as a flow source; needs egress rules for network access'), `@gofi.fetcher("poll", every="30s")
def poll(ctx: gofi.SourceContext, *, url: str = "https://api.example/items", limit: int = 100) -> list[gofi.Frame]:
    """Return new items as frames: bytes, str, a dict (JSON), gofi.Frame, or a list of these.

    ctx.state is kept between polls (a cursor). The library needs an egress
    rule for the host it calls.
    """
    import json
    import urllib.request

    since = ctx.state.get("since", 0)
    with urllib.request.urlopen(f"{url}?since={since}&limit={limit}", timeout=10) as response:
        items = json.load(response)
    if items:
        ctx.state["since"] = max(item["id"] for item in items)
    return [gofi.Frame(item, meta={"feed": "items"}) for item in items]
`),
  template('streamer', () => t('gofi.canvas.python-templates.streamer', 'Streamer (source)'), () => t('gofi.canvas.python-templates.streamer-description', 'Yield frames continuously as a flow source'), `from collections.abc import Iterator


@gofi.streamer("ticks")
def ticks(ctx: gofi.SourceContext, *, interval: float = 5.0) -> Iterator[dict]:
    """Yield frames as they arrive. GoFi restarts the generator with backoff if
    it fails, and closes it (finally runs) when the flow stops."""
    count = ctx.state.get("count", 0)
    while not ctx.wait(interval):  # returns True as soon as the flow stops
        count += 1
        ctx.state["count"] = count
        yield {"tick": count}
`),
  template('framer', () => t('gofi.canvas.python-templates.framer', 'Framer'), () => t('gofi.canvas.python-templates.framer-description', 'Split a byte stream into frames (Frame step)'), `@gofi.framer("records")
def records(buffer: bytes, final: bool) -> tuple[list[bytes], bytes]:
    """Return (frames, rest). Called after each read; final is True at end of stream."""
    *frames, rest = buffer.split(b";")
    if final and rest:
        frames, rest = frames + [rest], b""
    return frames, rest
`),
  template('decoder', () => t('gofi.canvas.python-templates.decoder', 'Decoder'), () => t('gofi.canvas.python-templates.decoder-description', 'bytes → bytes before parsing (Decode step)'), `@gofi.decoder("unwrap")
def unwrap(data: bytes) -> bytes:
    """Unwrap, decrypt, or decompress a frame. Return the bytes the next step receives."""
    return data.removeprefix(b"device:")
`),
  template('parser', () => t('gofi.canvas.python-templates.parser', 'Parser'), () => t('gofi.canvas.python-templates.parser-description', 'bytes → fields dict'), `@gofi.parser("my_parser")
def my_parser(data: bytes) -> dict | None:
    """Turn one frame into fields. Return None to drop it."""
    text = data.decode("utf-8", errors="replace")
    key, _, value = text.partition("=")
    return {"key": key.strip(), "value": value.strip()}
`),
  template('transformer', () => t('gofi.canvas.python-templates.transformer', 'Transformer with config'), () => t('gofi.canvas.python-templates.transformer-description', 'Keyword-only parameters become per-node settings'), `@gofi.transformer("enrich")
def enrich(fields: dict, meta: dict, *, threshold: float = 10.0, label: str = "hot") -> dict:
    """Keyword-only parameters are configured on each flow node."""
    if float(fields.get("value", 0)) >= threshold:
        fields["label"] = label
    return fields
`),
  template('encoder', () => t('gofi.canvas.python-templates.encoder', 'Encoder'), () => t('gofi.canvas.python-templates.encoder-description', 'bytes → bytes before a sink sends (Encode step)'), `@gofi.encoder("frame_length")
def frame_length(data: bytes, fields: dict, meta: dict) -> bytes:
    """Prefix the outgoing payload with a big-endian uint32 length."""
    return len(data).to_bytes(4, "big") + data
`),
  template('sink', () => t('gofi.canvas.python-templates.sink', 'Sink'), () => t('gofi.canvas.python-templates.sink-description', 'Print-only unless the library has egress rules'), `@gofi.sink("inspect")
def inspect(fields: dict, meta: dict) -> None:
    """Workers have no outbound network unless the library declares egress rules."""
    print(meta.get("name"), fields)
`),
  template('binary', () => t('gofi.canvas.python-templates.binary', 'Binary struct parser'), () => t('gofi.canvas.python-templates.binary-description', 'Fixed-layout binary frames with struct or construct'), `import struct


@gofi.parser("binary_header")
def binary_header(data: bytes) -> dict | None:
    """Big-endian header: uint16 type, uint32 sequence, float32 value."""
    if len(data) < 10:
        return None
    msg_type, sequence, value = struct.unpack(">HIf", data[:10])
    return {"type": msg_type, "sequence": sequence, "value": round(value, 4), "payload_hex": data[10:].hex()}

# The construct package is also available for declarative layouts:
#   from construct import Struct, Int16ub, Int32ub, Float32b
#   Header = Struct("type" / Int16ub, "sequence" / Int32ub, "value" / Float32b)
`),
  template('msgpack', () => t('gofi.canvas.python-templates.msgpack', 'MessagePack / CBOR decoder'), () => t('gofi.canvas.python-templates.msgpack-description', 'Self-describing binary encodings'), `import cbor2
import msgpack


@gofi.parser("binary_object")
def binary_object(data: bytes) -> dict:
    """Try MessagePack first, then CBOR."""
    try:
        value = msgpack.unpackb(data, raw=False)
    except Exception:
        value = cbor2.loads(data)
    return value if isinstance(value, dict) else {"value": value}
`),
  template('pydantic', () => t('gofi.canvas.python-templates.pydantic', 'Validated parser (pydantic)'), () => t('gofi.canvas.python-templates.pydantic-description', 'Declare the frame shape as a model; frames that do not match are dropped'), `from pydantic import BaseModel, Field, ValidationError


class Reading(BaseModel):
    """The shape of one frame. Unknown keys are ignored; types are coerced."""

    sensor: str = Field(min_length=1)
    temperature: float = Field(ge=-50, le=150)
    unit: str = "C"


@gofi.parser("reading")
def reading(data: bytes) -> dict | None:
    """Parse JSON into a Reading and return its fields; drop frames that do not validate."""
    try:
        return Reading.model_validate_json(data).model_dump()
    except ValidationError as error:
        print("rejected:", error.errors(include_url=False, include_input=False))
        return None
`),
  template('ai-classify', () => t('gofi.canvas.python-templates.ai-classify', 'AI classify / route'), () => t('gofi.canvas.python-templates.ai-classify-description', 'Have the AI pick one label from a list, then drop or route by it; needs the AI endpoint enabled for functions under Administration'), `# Needs "Functions may use the AI endpoint" on under Administration › AI assistant; GoFi supplies the endpoint and egress.
@gofi.transformer("ai_classify")
def ai_classify(fields: dict, meta: dict, *, labels: list = ["normal", "suspicious", "critical"], drop: list = ["normal"], instructions: str = "Classify this event by how urgently an operator must look at it.") -> dict | None:
    """Label each message with one of labels; drop it when the label is in drop.

    labels: the allowed labels; the model picks exactly one
    drop: labels whose messages are dropped (everything else continues)
    instructions: what the labels mean
    """
    fields["label"] = gofi.llm.classify(fields, labels, instructions=instructions)
    return None if fields["label"] in drop else fields
`),
  template('ai-enrich', () => t('gofi.canvas.python-templates.ai-enrich', 'AI enrich'), () => t('gofi.canvas.python-templates.ai-enrich-description', 'Ask the AI about each message and add its answer as a field; needs the AI endpoint enabled for functions under Administration'), `import json


# Needs "Functions may use the AI endpoint" on under Administration › AI assistant; GoFi supplies the endpoint and egress.
@gofi.transformer("ai_enrich")
def ai_enrich(fields: dict, meta: dict, *, prompt: str = "Summarize this event in one sentence: {fields}", output_key: str = "summary") -> dict:
    """Ask the AI about each message and store its answer under output_key.

    prompt: {fields} is the message as JSON; {name} inserts one field
    output_key: field that receives the answer
    """
    fields[output_key] = gofi.llm.chat(prompt.format_map({**fields, "fields": json.dumps(fields)}))
    return fields
`),
  template('ai-extract', () => t('gofi.canvas.python-templates.ai-extract', 'AI extract'), () => t('gofi.canvas.python-templates.ai-extract-description', 'Pull the values of a pydantic model out of each message into a field; needs the AI endpoint enabled for functions under Administration'), `import json

from pydantic import BaseModel


class Extracted(BaseModel):
    """The values to pull out of each message; a value the message does not state stays None."""

    vendor: str | None = None
    amount: float | None = None
    currency: str | None = None


# Needs "Functions may use the AI endpoint" on under Administration › AI assistant; GoFi supplies the endpoint and egress.
@gofi.transformer("ai_extract")
def ai_extract(fields: dict, meta: dict, *, prompt: str = "Extract the vendor, amount, and currency from this event.\\n\\n{fields}", output_key: str = "extracted") -> dict:
    """Pull the Extracted values out of each message and store them under output_key.

    prompt: what to extract; {fields} is the message as JSON
    output_key: field that receives the values (a JSON object)
    """
    fields[output_key] = gofi.llm.extract(prompt.format_map({**fields, "fields": json.dumps(fields)}), Extracted)
    return fields
`),
  template('ai-sink', () => t('gofi.canvas.python-templates.ai-sink', 'AI sink'), () => t('gofi.canvas.python-templates.ai-sink-description', 'Let the AI triage each delivered message and pick an action; your code carries it out. Needs the AI endpoint enabled for functions under Administration'), `import json
from typing import Literal

from pydantic import BaseModel


class Decision(BaseModel):
    """What the model decides for one message; the code below carries it out."""

    action: Literal["ignore", "notify", "ticket"]
    message: str = ""


# Needs "Functions may use the AI endpoint" on under Administration › AI assistant; GoFi supplies the endpoint and egress.
@gofi.sink("ai_notify")
def ai_notify(fields: dict, meta: dict, *, prompt: str = "Triage this event for the on-call operator. Decide whether to ignore it, notify the channel, or open a ticket, and write the message to send: {fields}") -> None:
    """Let the AI decide what to do with each delivered message, then carry it out.

    prompt: {fields} is the message as JSON; {name} inserts one field
    """
    # 1. The model decides. Structured output limits it to the actions in
    #    Decision, so the model never gets free-form control of what happens.
    decision = gofi.llm.extract(prompt.format_map({**fields, "fields": json.dumps(fields)}), Decision)

    # 2. Your code acts. This is the agentic boundary: only what is written
    #    here can touch the world. Calls beyond print need an egress rule on
    #    the library's Network tab, and should be safe to repeat, since a flow
    #    can redeliver a message. Replace the prints with your webhook or
    #    ticket API call.
    if decision["action"] == "ignore":
        return
    if decision["action"] == "ticket":
        print(meta.get("name"), "ticket ->", decision["message"][:200])
        return
    print(meta.get("name"), "notify ->", decision["message"][:200])
`),
]

/** How the template picker groups FUNCTION_TEMPLATES; every template id
 * belongs to exactly one group (the test checks). */
export const TEMPLATE_GROUPS: ReadonlyArray<{ id: string; readonly label: string; ids: readonly string[] }> = [
  { id: 'sources', get label() { return t('gofi.canvas.python-templates.group-sources', 'Sources') }, ids: ['fetcher', 'streamer'] },
  { id: 'steps', get label() { return t('gofi.canvas.python-templates.group-steps', 'Pipeline steps') }, ids: ['framer', 'decoder', 'parser', 'transformer', 'encoder', 'sink'] },
  { id: 'parsers', get label() { return t('gofi.canvas.python-templates.group-parsers', 'Binary and typed parsers') }, ids: ['binary', 'msgpack', 'pydantic'] },
  { id: 'ai', get label() { return t('gofi.canvas.python-templates.group-ai', 'AI') }, ids: ['ai-classify', 'ai-enrich', 'ai-extract', 'ai-sink'] },
]

/** Appends a template to existing source, adding `import gofi` when missing. */
export function insertTemplate(source: string, template: string): string {
  const base = source.trimEnd()
  const needsImport = !/^\s*import\s+gofi\b/m.test(base) && !/^\s*from\s+gofi\s+import\b/m.test(base)
  const header = needsImport ? (base ? `import gofi\n${base}` : 'import gofi') : base
  return `${header}\n\n\n${template.trimEnd()}\n`
}

export const INLINE_STARTER = `def transform(fields: dict, meta: dict) -> dict | None:
    """Return the updated fields, or None to drop the message."""
    print("received", fields)
    return fields
`

/** Config values for a function: declared defaults overlaid with saved values. */
export function configWithDefaults(params: FunctionParamLike[], saved?: Record<string, unknown>): Record<string, unknown> {
  const config: Record<string, unknown> = {}
  for (const param of params) if (param.default !== undefined && param.default !== null) config[param.name] = param.default
  return { ...config, ...(saved ?? {}) }
}

/** Parses one config input according to the declared parameter type. */
export function coerceParam(type: string | undefined, text: string): unknown {
  switch (type) {
    case 'int': { const value = Number.parseInt(text, 10); if (Number.isNaN(value)) throw new Error(t('gofi.canvas.python-templates.expected-integer', 'expected an integer')); return value }
    case 'float': { const value = Number(text); if (text.trim() === '' || Number.isNaN(value)) throw new Error(t('gofi.canvas.python-templates.expected-number', 'expected a number')); return value }
    case 'bool': return text === 'true'
    case 'list': case 'dict': case 'any': {
      const value: unknown = JSON.parse(text)
      if (type === 'list' && !Array.isArray(value)) throw new Error(t('gofi.canvas.python-templates.expected-json-list', 'expected a JSON list'))
      if (type === 'dict' && (value === null || typeof value !== 'object' || Array.isArray(value))) throw new Error(t('gofi.canvas.python-templates.expected-json-object', 'expected a JSON object'))
      return value
    }
    default: return text
  }
}

/** Parameters required but absent from the config. */
export function missingParams(params: FunctionParamLike[], config: Record<string, unknown>): string[] {
  return params.filter(param => param.required && (config[param.name] === undefined || config[param.name] === '')).map(param => param.name)
}

export interface TestOutcome { fields?: Record<string, unknown>; drop?: boolean; error?: string }
export interface TestExpectation { expectFields?: Record<string, unknown>; expectDrop?: boolean }

/** 'pass' | 'fail' | 'recorded' (no expectations). Expected fields must match by subset. */
export function evaluateTest(expectation: TestExpectation, outcome: TestOutcome): { status: 'pass' | 'fail' | 'recorded'; reasons: string[] } {
  if (outcome.error) return { status: 'fail', reasons: [outcome.error] }
  const reasons: string[] = []
  const hasFields = !!expectation.expectFields && Object.keys(expectation.expectFields).length > 0
  if (expectation.expectDrop !== undefined && !!outcome.drop !== expectation.expectDrop) reasons.push(expectation.expectDrop ? t('gofi.canvas.python-templates.expected-drop', 'expected the message to be dropped') : t('gofi.canvas.python-templates.unexpected-drop', 'message was dropped'))
  if (hasFields) for (const [key, value] of Object.entries(expectation.expectFields!)) {
    if (JSON.stringify(outcome.fields?.[key]) !== JSON.stringify(value)) reasons.push(t('gofi.canvas.python-templates.field-mismatch', '{{key}}: expected {{expected}}, got {{actual}}', { key, expected: JSON.stringify(value), actual: JSON.stringify(outcome.fields?.[key]) }))
  }
  if (expectation.expectDrop === undefined && !hasFields) return { status: 'recorded', reasons }
  return { status: reasons.length ? 'fail' : 'pass', reasons }
}

/** Library name suggested when promoting an inline stage: valid sandbox name. */
export function suggestLibraryName(flow: string, stageId: string): string {
  const slug = `${flow}-${stageId}`.toLowerCase().replace(/[^a-z0-9-]+/g, '-').replace(/^-+|-+$/g, '').replace(/^[^a-z]+/, '')
  return (slug || 'functions').slice(0, 40).replace(/-+$/, '')
}

/** Wraps an inline `def transform(...)` snippet as a decorated library function. */
export function promoteInlineSource(source: string, functionName: string): string {
  const name = functionName.replace(/[^A-Za-z0-9_]/g, '_').replace(/^[^A-Za-z_]/, '_') || 'transform'
  const renamed = source.replace(/^def\s+transform\s*\(/m, `def ${name}(`)
  return `@gofi.transformer("${name}")\n${renamed.trimEnd()}\n`
}

/** `line` is the decorator; `defLine` is the `def` that receives the input. */
/** Why a snippet cannot be promoted into `librarySource` as `functionName`:
 * the library already registers that transformer, or defines a function of
 * that name (which the appended one would shadow). Empty when it is safe. */
export function promotionConflict(librarySource: string, functionName: string): string {
  const name = functionName.replace(/[^A-Za-z0-9_]/g, '_').replace(/^[^A-Za-z_]/, '_') || 'transform'
  if (discoverFunctions(librarySource).some(fn => fn.kind === 'transformer' && fn.name === name)) return t('gofi.canvas.python-templates.conflict-transformer', 'already registers a transformer named "{{name}}"', { name })
  if (new RegExp(`^(?:async\\s+)?def\\s+${name}\\s*\\(`, 'm').test(librarySource)) return t('gofi.canvas.python-templates.conflict-function', 'already defines a function named "{{name}}"', { name })
  return ''
}

export type StepKind = 'fetcher' | 'streamer' | 'framer' | 'decoder' | 'parser' | 'transformer' | 'encoder' | 'sink'
export interface DiscoveredFunction { name: string; kind: StepKind; line: number; defLine: number }

/** Static scan of decorated functions in unsaved source (text only, never executed). */
export function discoverFunctions(source: string): DiscoveredFunction[] {
  const lines = source.split('\n')
  const found: DiscoveredFunction[] = []
  for (let index = 0; index < lines.length; index++) {
    const decorator = /^@gofi\.(fetcher|streamer|framer|decoder|parser|transformer|encoder|sink)\b\s*(?:\((.*)\))?\s*$/.exec(lines[index].trim())
    if (!decorator) continue
    let defLine = index + 1
    while (defLine < lines.length && (lines[defLine].trim().startsWith('@') || !lines[defLine].trim())) defLine++
    const definition = /^(?:async\s+)?def\s+([A-Za-z_]\w*)\s*\(/.exec(lines[defLine]?.trim() ?? '')
    if (!definition) continue
    const [, decoratorName, args = ''] = decorator
    const explicit = /name\s*=\s*["']([^"']+)["']/.exec(args)?.[1]
    const positional = /^\s*["']([^"']+)["']/.exec(args)?.[1]
    found.push({ name: positional ?? explicit ?? definition[1], kind: decoratorName as StepKind, line: index + 1, defLine: defLine + 1 })
  }
  return found
}

// ---- Live worker logs -------------------------------------------------------

export interface LogEntryLike { time?: string; stream: string; line: string; invocationId?: string; function?: string; kind?: string; level?: string }
export type LogSeverity = 'error' | 'warn' | 'info' | 'system'

/** stderr and error-level system lines are errors; stdout is info. */
export function logSeverity(entry: LogEntryLike): LogSeverity {
  if (entry.stream === 'stderr') return 'error'
  if (entry.stream === 'system') return entry.level === 'error' ? 'error' : entry.level === 'warn' ? 'warn' : 'system'
  return 'info'
}

export const shortId = (id?: string) => id ? id.slice(0, 8) : ''

/** Appends entries and keeps only the newest `max`. */
export function appendBounded<T>(list: T[], entries: T[], max: number): T[] {
  const next = list.concat(entries)
  return next.length > max ? next.slice(next.length - max) : next
}

/** Text filter over line/function/invocation, optionally limited to one run. */
export function matchesLogFilter(entry: LogEntryLike, query: string, onlyInvocation?: string): boolean {
  if (onlyInvocation && entry.invocationId !== onlyInvocation) return false
  if (!query) return true
  const needle = query.toLowerCase()
  return [entry.line, entry.function, entry.invocationId, entry.stream].some(value => value?.toLowerCase().includes(needle))
}

/** HH:MM:SS.mmm (UTC) for an RFC 3339 timestamp; '' when absent. */
export function logTime(time?: string): string {
  if (!time) return ''
  const match = /T(\d{2}:\d{2}:\d{2})(?:\.(\d{1,3}))?/.exec(time)
  return match ? `${match[1]}.${(match[2] ?? '0').padEnd(3, '0')}` : ''
}

/** A unique client-chosen id correlating one run with its log lines. */
export function newRunId(): string {
  const bytes = new Uint8Array(8)
  globalThis.crypto.getRandomValues(bytes)
  return `run-${Array.from(bytes, byte => byte.toString(16).padStart(2, '0')).join('')}`
}

// ---- Source highlights ------------------------------------------------------

/** What the editor marks: where the input enters, the lines it executed, and
 * the line that raised. All lines are 1-based source lines. */
export interface SourceHighlights { entry?: { line: number; label: string; preview?: string }; executed?: number[]; error?: number }

/** A short, single-line preview of the input delivered to a function. */
export function inputPreview(input: { raw?: string; fields?: Record<string, unknown> }, max = 48): string {
  const keys = input.fields ? Object.keys(input.fields) : []
  const text = keys.length ? `{${keys.slice(0, 4).join(', ')}${keys.length > 4 ? ', …' : ''}}` : (input.raw ?? '').replace(/\s+/g, ' ').trim()
  if (!text) return ''
  return text.length > max ? `${text.slice(0, max - 1)}…` : text
}

/** Collapses executed lines into sorted, contiguous [start, end] ranges. */
export function lineRanges(lines: number[]): [number, number][] {
  const sorted = [...new Set(lines.filter(line => Number.isInteger(line) && line > 0))].sort((a, b) => a - b)
  const ranges: [number, number][] = []
  for (const line of sorted) {
    const last = ranges[ranges.length - 1]
    if (last && line === last[1] + 1) last[1] = line
    else ranges.push([line, line])
  }
  return ranges
}

/** Maps lines of a wrapped snippet back to the snippet: subtracts the lines
 * added above it and drops anything outside the snippet. */
export function unwrapLines(lines: number[], offset: number, snippetLines: number): number[] {
  return lines.map(line => line - offset).filter(line => line >= 1 && line <= snippetLines)
}
