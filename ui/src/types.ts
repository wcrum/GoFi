export interface PythonFunctionRef {
  library: string
  function: string
  /** Python authored directly in a flow stage (folded into a managed library). */
  inlineSource?: string
  /** Per-node configuration passed as keyword arguments. */
  config?: Record<string, unknown>
}
/** Link to a CEL library entry; generation 0/undefined follows the latest. */
export interface ExpressionRef { name: string; generation?: number }

export type ServiceType = 'ClusterIP' | 'NodePort' | 'LoadBalancer' | 'Gateway'
/** `port` maps a Service port different from the listen port; the portal leaves it unset. */
export interface LoadBalancerOptions { class?: string; addressPool?: string; ip?: string }
/** Attach to a Gateway API Gateway; an empty listener asks GoFi to add gofi-<flow>. */
export interface GatewayExposure { namespace: string; name: string; listener?: string; port?: number }
export interface ServiceExposure {
  type: ServiceType; port?: number; nodePort?: number
  externalTrafficPolicy?: 'Cluster' | 'Local'; annotations?: Record<string, string>
  loadBalancer?: LoadBalancerOptions; gateway?: GatewayExposure
  /** Limit in-cluster senders to these workloads (UDS Packages). */
  allowFrom?: PeerRef[]
}
export interface PeerRef { namespace: string; package?: string; selector?: Record<string, string> }
/** Default Kubernetes NodePort range (kube-apiserver --service-node-port-range). */
export const NODE_PORT_RANGE = { min: 30000, max: 32767 } as const

/** `listen`: senders connect to a GoFi TCP/UDP port. */
export interface ListenSource {
  network: string
  port: number
  /** Kubernetes Service exposure for the port (Kubernetes mode only). */
  service?: ServiceExposure
}

/** `fetch`: GoFi polls an HTTP endpoint; `split` ("$.items[*]") makes one frame per element. */
export interface HTTPFetch { url: string; method?: string; headers?: Record<string, string>; auth?: HTTPAuth; every?: string; timeout?: string; body?: string }
export interface FetchSource { http?: HTTPFetch; split?: string }
export interface NATSSubscribe { url: string; subject: string; queue?: string; auth?: NATSAuth }
export interface MQTTSubscribe { url: string; topic: string; qos?: number; clientId?: string; username?: string; password?: string }
/** A WebSocket or SSE stream; `send` (WebSocket only) is sent after connecting. */
export interface StreamSubscribe { url: string; headers?: Record<string, string>; auth?: HTTPAuth; send?: string }
/** `subscribe`: exactly one stream is set. Each message or event is one frame. */
export interface SubscribeSource { nats?: NATSSubscribe; mqtt?: MQTTSubscribe; websocket?: StreamSubscribe; sse?: StreamSubscribe }
export type SubscribeKind = keyof SubscribeSource

/** How data arrives in a flow. Exactly one member is set (docs/flows.md);
 * Python sources are planned. */
export interface FlowSource { listen?: ListenSource; fetch?: FetchSource; subscribe?: SubscribeSource }
export type SourceType = 'listen' | 'fetch' | 'subscribe' | 'python'

/** The configured source's type. */
export function sourceTypeOf(source: FlowSource): SourceType {
  return source.fetch ? 'fetch' : source.subscribe ? 'subscribe' : 'listen'
}
export function subscribeKindOf(subscribe: SubscribeSource): SubscribeKind {
  return subscribe.mqtt ? 'mqtt' : subscribe.websocket ? 'websocket' : subscribe.sse ? 'sse' : 'nats'
}
/** One-line description of a source, e.g. "TCP :9000" or "NATS sensors.>". */
export function sourceSummary(source: FlowSource): string {
  if (source.fetch) return `Fetch ${source.fetch.http?.url || '—'}${source.fetch.http?.every ? ` every ${source.fetch.http.every}` : ''}`
  if (source.subscribe) {
    const kind = subscribeKindOf(source.subscribe)
    const s = source.subscribe
    const detail = kind === 'nats' ? s.nats?.subject : kind === 'mqtt' ? s.mqtt?.topic : kind === 'websocket' ? s.websocket?.url : s.sse?.url
    return `${kind === 'sse' ? 'SSE' : kind === 'nats' ? 'NATS' : kind === 'mqtt' ? 'MQTT' : 'WebSocket'} ${detail || '—'}`
  }
  const listen = source.listen ?? { network: 'tcp', port: 0 }
  return `${(listen.network || 'tcp').toUpperCase()} :${listen.port || '—'}`
}

export interface Framing {
  /** 'line' | 'delimited' | 'length-prefix' | 'raw' | 'python' (a @gofi.framer). */
  mode: string
  delimiter?: string
  prefixLen?: number
  maxBytes?: number
  python?: PythonFunctionRef
}

/** One decoding or encoding step: a built-in codec or a Python function. */
export interface CodecStep { type: string; python?: PythonFunctionRef }
export const BUILTIN_CODECS = ['base64', 'base64url', 'base32', 'hex', 'url', 'gzip', 'zlib', 'deflate', 'zstd', 'snappy', 'lz4', 'brotli', 'bzip2'] as const

/** How a message becomes the bytes a sink sends: a format, then encoders in order. */
export interface Encoding { format?: 'wrapped' | 'fields' | 'raw'; encoders?: CodecStep[] }

export interface ParserCfg {
  python?: PythonFunctionRef
  format: string
  decoders?: CodecStep[]
  maxDecodedBytes?: number
  errorPolicy?: 'continue' | 'drop'
}

export interface StageSpec {
  python?: PythonFunctionRef
  id: string
  kind: 'filter' | 'transform' | 'rate-limit' | 'dedup' | 'schema' | 'python'
  // kind === 'filter'
  type?: string // 'cel' or 'regex'
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
  /** Library-linked expression for this kind; a literal expression wins. */
  expressionRef?: ExpressionRef
  /** CEL evaluation errors: 'pass' (default) or 'drop'. */
  onError?: 'pass' | 'drop'
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

export interface AsterCfg {
  topic: string
  scope: string
  priority?: 'routine' | 'priority' | 'immediate' | 'flash'
  logicalKey?: string
  logicalKeyField?: string
  token?: string
  tokenFile?: string
  timeoutSeconds?: number
}

/** `delivery` all (default): every ready Pod gets each message; one: load-balanced. */
export interface KubernetesTarget { namespace?: string; selector: Record<string, string>; port: number; delivery?: 'all' | 'one' }

export interface SinkCfg {
  labels?: Record<string, string>
  kubernetes?: KubernetesTarget
  python?: PythonFunctionRef
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
  whenRef?: ExpressionRef
  /** Serialize and encode before sending; unset keeps the sink's default payload. */
  encoding?: Encoding
  delimiter?: string
  aster?: AsterCfg
}

export interface TestCase {
  id: string
  name: string
  raw: string // raw frame text fed through Simulate
}

/** A Flow: where data comes from, framing/decoding, stages, and sinks. */
export interface FlowDefinition {
  name: string
  source: FlowSource
  framing: Framing
  parser: ParserCfg
  stages: StageSpec[]
  sinks: SinkCfg[]
  deadLetterSink?: SinkCfg
  testCases?: TestCase[]
}

/** The flow's listen source, with defaults when unset. */
export function listenOf(flow: Pick<FlowDefinition, 'source'>): ListenSource {
  return flow.source.listen ?? { network: 'tcp', port: 0 }
}

// --- Mock-mode simulation contract (GoFiService.Simulate) ---
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
  stdout?: string // Python stages: captured print output
  durationUs?: string // int64 as a protobuf JSON string
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
  deadLetter: boolean // true only if dropped AND the flow has a deadLetterSink configured
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
