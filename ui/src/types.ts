export interface Source {
  port: number
  network: string
}

export interface Framing {
  mode: string
  delimiter?: string
  prefixLen?: number
  maxBytes?: number
}

export interface ParserCfg {
  format: string
}

export interface StageSpec {
  id: string
  kind: 'filter' | 'transform' | 'rate-limit' | 'dedup' | 'schema'
  // kind === 'filter'
  type?: string // 'cel' (only type today)
  expression?: string
  // kind === 'transform'
  transformOp?: 'set' | 'rename' | 'remove'
  field?: string // target field (all ops)
  newField?: string // 'rename': destination field name
  valueExpr?: string // 'set': CEL expression producing the new value
  // kind === 'rate-limit'
  maxPerSecond?: number
  // kind === 'dedup'
  keyExpr?: string // CEL expression producing the dedup key
  window?: string // Go duration string e.g. "30s"; blank = backend defaults to 30s
  // kind === 'schema'
  requiredFields?: string[]
}

export interface HTTPAuth {
  type: 'bearer' | 'basic' | 'apiKey'
  token?: string // bearer
  username?: string // basic
  password?: string // basic
  headerName?: string // apiKey: header name, defaults to "X-API-Key" if omitted
  apiKey?: string // apiKey: header value
}

export interface NATSAuth {
  username?: string
  password?: string
  token?: string
  nkeySeed?: string // raw NKey seed string
  creds?: string // full inline .creds file content (multi-line, JWT + seed blocks)
  tlsCert?: string // inline client cert PEM (multi-line)
  tlsKey?: string // inline client key PEM (multi-line)
  tlsCa?: string // inline CA cert PEM (multi-line)
  insecureSkipVerify?: boolean
}

export interface SinkCfg {
  type: string
  url?: string
  subject?: string
  // HTTP-specific
  method?: string // 'GET' | 'POST' | 'PUT' | 'PATCH' | 'DELETE'
  headers?: Record<string, string>
  contentType?: string
  bodyMode?: 'wrapped' | 'raw'
  auth?: HTTPAuth
  // NATS-specific
  natsAuth?: NATSAuth
  // optional CEL expression; empty/absent = always send (fan-out); set = conditional (routing)
  when?: string
}

export interface TestCase {
  id: string
  name: string
  raw: string // raw frame text fed through Simulate
}

export interface ListenerDefinition {
  name: string
  source: Source
  framing: Framing
  parser: ParserCfg
  stages: StageSpec[]
  sinks: SinkCfg[]
  deadLetterSink?: SinkCfg
  testCases?: TestCase[]
}

// --- Mock-mode simulation contract (POST /api/simulate) ---
// Mirrors the backend's dry-run trace shape exactly. A Simulate call is
// side-effect-free: it never dispatches to a real sink, so `sent` on a
// SinkOutcome reflects only whether the sink *would* have received the
// message, not an actual delivery.

export interface StageTrace {
  id: string
  kind: string
  type?: string
  pass: boolean
  error?: string
  fields: Record<string, unknown> // snapshot of message Fields AFTER this stage ran
}

export interface SinkOutcome {
  index: number
  type: string
  sent: boolean
  reason?: string // present when sent === false, e.g. "when: false" or "when eval error: ..."
}

export interface SimulateResult {
  parseError?: string
  parsedFields: Record<string, unknown> // snapshot right after parsing, before any stage
  stages: StageTrace[]
  dropped: boolean
  droppedBy?: string
  deadLetter: boolean // true only if dropped AND the listener has a deadLetterSink configured
  sinks?: SinkOutcome[] // only present/non-empty when NOT dropped
}

export interface TapEvent {
  ts: string
  dropped: boolean
  droppedBy?: string
  fields: Record<string, unknown>
  meta: Record<string, string>
  raw: string
}
