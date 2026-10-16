// Pure helpers for the Python creator: starter templates, per-node config
// defaults from declared function parameters, and saved-test
// expectations. No React or network imports so node --test can load it.

export interface FunctionParamLike { name: string; type?: string; default?: unknown; required?: boolean; description?: string }

export const DEFAULT_LIBRARY = `import json
import gofi


@gofi.decoder("trim")
def trim(data: bytes):
    """Strip surrounding whitespace from each frame before parsing."""
    return data.strip()


@gofi.parser("telemetry")
def parse_telemetry(data: bytes):
    """Decode JSON telemetry into fields."""
    return json.loads(data)


@gofi.transformer("enrich")
def enrich(fields, meta, *, threshold: float = 30.0):
    """Flag hot readings; threshold is configurable per flow node."""
    fields["hot"] = float(fields.get("temperature", 0)) >= threshold
    print("enriched", fields.get("sensor"), "hot" if fields["hot"] else "ok")
    return fields
`

export interface FunctionTemplate { id: string; label: string; description: string; source: string }

export const FUNCTION_TEMPLATES: FunctionTemplate[] = [
  {
    id: 'framer', label: 'Framer', description: 'Split a byte stream into frames (Frame step)',
    source: `@gofi.framer("records")
def records(buffer: bytes, final: bool):
    """Return (frames, rest). Called after each read; final is True at end of stream."""
    *frames, rest = buffer.split(b";")
    if final and rest:
        frames, rest = frames + [rest], b""
    return frames, rest
`,
  },
  {
    id: 'decoder', label: 'Decoder', description: 'bytes → bytes before parsing (Decode step)',
    source: `@gofi.decoder("unwrap")
def unwrap(data: bytes):
    """Unwrap, decrypt, or decompress a frame. Return the bytes the next step receives."""
    return data.removeprefix(b"device:")
`,
  },
  {
    id: 'parser', label: 'Parser', description: 'bytes → fields dict',
    source: `@gofi.parser("my_parser")
def my_parser(data: bytes):
    """Turn one frame into fields. Return None to drop it."""
    text = data.decode("utf-8", errors="replace")
    key, _, value = text.partition("=")
    return {"key": key.strip(), "value": value.strip()}
`,
  },
  {
    id: 'transformer', label: 'Transformer with config', description: 'Keyword-only parameters become per-node settings',
    source: `@gofi.transformer("enrich")
def enrich(fields, meta, *, threshold: float = 10.0, label: str = "hot"):
    """Keyword-only parameters are configured on each flow node."""
    if float(fields.get("value", 0)) >= threshold:
        fields["label"] = label
    return fields
`,
  },
  {
    id: 'encoder', label: 'Encoder', description: 'bytes → bytes before a sink sends (Encode step)',
    source: `@gofi.encoder("frame_length")
def frame_length(data: bytes, fields, meta):
    """Prefix the outgoing payload with a big-endian uint32 length."""
    return len(data).to_bytes(4, "big") + data
`,
  },
  {
    id: 'sink', label: 'Sink', description: 'Print-only unless the library has egress rules',
    source: `@gofi.sink("inspect")
def inspect(fields, meta):
    """Workers have no outbound network unless the library declares egress rules."""
    print(meta.get("name"), fields)
`,
  },
  {
    id: 'binary', label: 'Binary struct parser', description: 'Fixed-layout binary frames with struct or construct',
    source: `import struct


@gofi.parser("binary_header")
def binary_header(data: bytes):
    """Big-endian header: uint16 type, uint32 sequence, float32 value."""
    if len(data) < 10:
        return None
    msg_type, sequence, value = struct.unpack(">HIf", data[:10])
    return {"type": msg_type, "sequence": sequence, "value": round(value, 4), "payload_hex": data[10:].hex()}

# The construct package is also available for declarative layouts:
#   from construct import Struct, Int16ub, Int32ub, Float32b
#   Header = Struct("type" / Int16ub, "sequence" / Int32ub, "value" / Float32b)
`,
  },
  {
    id: 'msgpack', label: 'MessagePack / CBOR decoder', description: 'Self-describing binary encodings',
    source: `import cbor2
import msgpack


@gofi.parser("binary_object")
def binary_object(data: bytes):
    """Try MessagePack first, then CBOR."""
    try:
        value = msgpack.unpackb(data, raw=False)
    except Exception:
        value = cbor2.loads(data)
    return value if isinstance(value, dict) else {"value": value}
`,
  },
]

/** Appends a template to existing source, adding `import gofi` when missing. */
export function insertTemplate(source: string, template: string): string {
  const base = source.trimEnd()
  const needsImport = !/^\s*import\s+gofi\b/m.test(base) && !/^\s*from\s+gofi\s+import\b/m.test(base)
  const header = needsImport ? (base ? `import gofi\n${base}` : 'import gofi') : base
  return `${header}\n\n\n${template.trimEnd()}\n`
}

export const INLINE_STARTER = `def transform(fields, meta):
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
    case 'int': { const value = Number.parseInt(text, 10); if (Number.isNaN(value)) throw new Error('expected an integer'); return value }
    case 'float': { const value = Number(text); if (text.trim() === '' || Number.isNaN(value)) throw new Error('expected a number'); return value }
    case 'bool': return text === 'true'
    case 'list': case 'dict': case 'any': {
      const value: unknown = JSON.parse(text)
      if (type === 'list' && !Array.isArray(value)) throw new Error('expected a JSON list')
      if (type === 'dict' && (value === null || typeof value !== 'object' || Array.isArray(value))) throw new Error('expected a JSON object')
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
  if (expectation.expectDrop !== undefined && !!outcome.drop !== expectation.expectDrop) reasons.push(expectation.expectDrop ? 'expected the message to be dropped' : 'message was dropped')
  if (hasFields) for (const [key, value] of Object.entries(expectation.expectFields!)) {
    if (JSON.stringify(outcome.fields?.[key]) !== JSON.stringify(value)) reasons.push(`${key}: expected ${JSON.stringify(value)}, got ${JSON.stringify(outcome.fields?.[key])}`)
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
  if (discoverFunctions(librarySource).some(fn => fn.kind === 'transformer' && fn.name === name)) return `already registers a transformer named "${name}"`
  if (new RegExp(`^(?:async\\s+)?def\\s+${name}\\s*\\(`, 'm').test(librarySource)) return `already defines a function named "${name}"`
  return ''
}

export type StepKind = 'framer' | 'decoder' | 'parser' | 'transformer' | 'encoder' | 'sink'
export interface DiscoveredFunction { name: string; kind: StepKind; line: number; defLine: number }

/** Static scan of decorated functions in unsaved source (text only, never executed). */
export function discoverFunctions(source: string): DiscoveredFunction[] {
  const lines = source.split('\n')
  const found: DiscoveredFunction[] = []
  for (let index = 0; index < lines.length; index++) {
    const decorator = /^@gofi\.(framer|decoder|parser|transformer|encoder|sink)\b\s*(?:\((.*)\))?\s*$/.exec(lines[index].trim())
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
