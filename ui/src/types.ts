import { t } from './i18n/index.ts'

export interface PythonFunctionRef {
  /** Optional when the flow sets a default library (FlowDefinition.python). */
  library?: string
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

/** TLS for a `quic` listener or sink, as inline PEM. A listener presents
 * `cert`/`key` and, with `requireClientCert`, verifies clients against `ca`;
 * a sink verifies the server against `ca` (or `serverName`) and may present
 * `cert`/`key`. `alpn` defaults to ["gofi/1"] on both ends. */
export interface TLSConfig { cert?: string; key?: string; ca?: string; insecureSkipVerify?: boolean; serverName?: string; alpn?: string[]; requireClientCert?: boolean }

/** `listen`: senders connect to a GoFi TCP, UDP, or QUIC (TLS over UDP) port. */
export interface ListenSource {
  network: string // 'tcp' | 'udp' | 'quic'
  port: number
  /** QUIC only: the listener's certificate and client-auth policy. */
  tls?: TLSConfig
  /** Kubernetes Service exposure for the port (Kubernetes mode only). */
  service?: ServiceExposure
}

/** `fetch`: GoFi polls an HTTP endpoint; `split` ("$.items[*]") makes one frame per element. */
export interface HTTPFetch { url: string; method?: string; headers?: Record<string, string>; auth?: HTTPAuth; every?: string; timeout?: string; body?: string }
/** `fetch.s3`: lists a bucket prefix each poll; every new object is one frame. Omit keys for ambient credentials. */
export interface S3Fetch {
  endpoint?: string; region?: string; bucket: string; prefix?: string; glob?: string
  accessKeyId?: string; secretAccessKey?: string; sessionToken?: string
  pathStyle?: boolean; insecureSkipVerify?: boolean; tlsCa?: string
  since?: string; maxObjects?: number; deleteAfter?: boolean; moveTo?: string; every?: string; timeout?: string
}
/** `fetch.sql`: runs a read-only query each poll; every row is one JSON frame. */
export interface SQLFetch {
  driver: 'postgres' | 'mysql' | 'sqlserver'; dsn?: string; host?: string; port?: number; database?: string
  username?: string; password?: string; tls?: '' | 'disable' | 'require' | 'verify-full'; params?: Record<string, string>
  query: string; cursorColumn?: string; cursorStart?: string; maxRows?: number; every?: string; timeout?: string
}
/** Exactly one of `http`, `s3`, or `sql` is set. */
export interface FetchSource { http?: HTTPFetch; s3?: S3Fetch; sql?: SQLFetch; split?: string }
export type FetchKind = 'http' | 's3' | 'sql'
export function fetchKindOf(fetch: FetchSource): FetchKind {
  return fetch.s3 ? 's3' : fetch.sql ? 'sql' : 'http'
}
export interface NATSSubscribe { url: string; subject: string; queue?: string; auth?: NATSAuth }
export interface MQTTSubscribe { url: string; topic: string; qos?: number; clientId?: string; username?: string; password?: string }
/** A WebSocket or SSE stream; `send` (WebSocket only) is sent after connecting. */
export interface StreamSubscribe { url: string; headers?: Record<string, string>; auth?: HTTPAuth; send?: string }
/** `subscribe`: exactly one stream is set. Each message or event is one frame. */
export interface SubscribeSource {
  nats?: NATSSubscribe; mqtt?: MQTTSubscribe; websocket?: StreamSubscribe; sse?: StreamSubscribe; zenoh?: ZenohSubscribe
  kafka?: KafkaSubscribe; amqp?: AMQPSubscribe; jetstream?: JetStreamSubscribe; redis?: RedisSubscribe
}
/** TLS for a broker connection: Kafka needs `enabled`; AMQP and Redis turn it on with amqps:// and rediss://. PEM inline. */
export interface SourceTLS { enabled?: boolean; ca?: string; cert?: string; key?: string; insecureSkipVerify?: boolean }
/** `subscribe.kafka`: consumer group; offsets commit after each record's pipeline run. */
export interface KafkaSubscribe {
  brokers: string[]; topics: string[]; group?: string; startOffset?: '' | 'latest' | 'earliest'; clientId?: string
  tls?: SourceTLS; sasl?: { mechanism: 'PLAIN' | 'SCRAM-SHA-256' | 'SCRAM-SHA-512'; username: string; password: string }
}
/** `subscribe.amqp`: a queue, or an exchange bound to a temporary queue; acked after the pipeline run. */
export interface AMQPSubscribe { url: string; queue?: string; exchange?: string; routingKey?: string; prefetch?: number; username?: string; password?: string; tls?: SourceTLS }
/** `subscribe.jetstream`: durable pull consumer with explicit acks. */
export interface JetStreamSubscribe { url: string; stream: string; consumer?: string; subject?: string; deliverPolicy?: '' | 'all' | 'last' | 'new' | 'last_per_subject'; ackWait?: string; auth?: NATSAuth }
/** `subscribe.redis`: a stream with a consumer group (XREADGROUP/XACK), or Pub/Sub channels. */
export interface RedisSubscribe {
  url: string; stream?: string; group?: string; consumer?: string; start?: '' | 'latest' | 'earliest'; field?: string
  channels?: string[]; pattern?: boolean; username?: string; password?: string; tls?: SourceTLS
}
/** One-line summary of a broker subscribe source, or undefined for other sources. */
export function brokerSummary(s: SubscribeSource): string | undefined {
  if (s.kafka) return `Kafka ${s.kafka.topics?.join(', ') || '—'}`
  if (s.amqp) return `AMQP ${s.amqp.queue || (s.amqp.exchange ? `${s.amqp.exchange} ${s.amqp.routingKey ?? ''}`.trim() : '—')}`
  if (s.jetstream) return `JetStream ${s.jetstream.stream || '—'}${s.jetstream.subject ? ` ${s.jetstream.subject}` : ''}`
  if (s.redis) return `Redis ${s.redis.stream ? t('gofi.types.broker-summary.redis-stream', 'stream {{stream}}', { stream: s.redis.stream }) : s.redis.channels?.join(', ') || '—'}`
  return undefined
}
/** A Zenoh key expression through the router's REST plugin (docs/zenoh.md).
 * `kinds` defaults to PUT only; `query` also reads stored values on connect. */
export interface ZenohSubscribe { url: string; keyExpr: string; headers?: Record<string, string>; auth?: HTTPAuth; kinds?: Array<'PUT' | 'DELETE'>; query?: boolean }
export type SubscribeKind = keyof SubscribeSource

/** `source.python`: a @gofi.fetcher (polled every `every`) or @gofi.streamer
 * in a saved library. It runs in that library's isolated worker, under the
 * library's egress allowlist. `config` is passed as keyword arguments. */
export interface PythonSource { library: string; function: string; config?: Record<string, unknown>; every?: string }
/** How data arrives in a flow. Exactly one member is set (docs/flows.md). */
export interface FlowSource { listen?: ListenSource; fetch?: FetchSource; subscribe?: SubscribeSource; python?: PythonSource }
export type SourceType = 'listen' | 'fetch' | 'subscribe' | 'python'

/** The configured source's type. */
export function sourceTypeOf(source: FlowSource): SourceType {
  return source.fetch ? 'fetch' : source.subscribe ? 'subscribe' : source.python ? 'python' : 'listen'
}
export function subscribeKindOf(subscribe: SubscribeSource): SubscribeKind {
  if (subscribe.kafka) return 'kafka'
  if (subscribe.amqp) return 'amqp'
  if (subscribe.jetstream) return 'jetstream'
  if (subscribe.redis) return 'redis'
  return subscribe.zenoh ? 'zenoh' : subscribe.mqtt ? 'mqtt' : subscribe.websocket ? 'websocket' : subscribe.sse ? 'sse' : 'nats'
}
/** `summary`, followed by the poll interval when one is set ("… every 10s"). */
const withEvery = (summary: string, every?: string) => every ? t('gofi.types.source-summary.with-every', '{{summary}} every {{every}}', { summary, every }) : summary

/** One-line description of a source, e.g. "TCP :9000" or "NATS sensors.>".
 * Protocol and brand names stay literal; connecting words are localized. */
export function sourceSummary(source: FlowSource): string {
  if (source.python) return withEvery(`Python ${source.python.library || '—'}.${source.python.function || '—'}`, source.python.every)
  if (source.fetch?.s3) return withEvery(`S3 s3://${source.fetch.s3.bucket || '—'}/${source.fetch.s3.prefix ?? ''}`, source.fetch.s3.every)
  if (source.fetch?.sql) return withEvery(`SQL ${source.fetch.sql.driver} ${source.fetch.sql.host || source.fetch.sql.database || 'DSN'}`, source.fetch.sql.every)
  if (source.fetch) return withEvery(t('gofi.types.source-summary.fetch', 'Fetch {{url}}', { url: source.fetch.http?.url || '—' }), source.fetch.http?.every)
  if (source.subscribe) {
    const kind = subscribeKindOf(source.subscribe)
    const s = source.subscribe
    if (kind === 'zenoh') return `Zenoh ${s.zenoh?.keyExpr || '—'}`
    const broker = brokerSummary(s)
    if (broker) return broker
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
  /** @deprecated Legacy location of FlowDefinition.decode; read-only (normalizeFlow moves it). */
  decoders?: CodecStep[]
  /** @deprecated Legacy location of FlowDefinition.decode.maxDecodedBytes. */
  maxDecodedBytes?: number
  errorPolicy?: 'continue' | 'drop'
}

/** Byte decoding applied to every frame before the parser, in order. */
export interface DecodeCfg { decoders: CodecStep[]; maxDecodedBytes?: number }

/** Flow-wide Python defaults: function refs without a library use this one. */
export interface FlowPython { library: string }

export type ParseFormat = 'json' | 'xml' | 'text' | 'python'
/** A `parse` stage: parse a value (a field, or the raw frame) into fields. */
export interface ParseStageCfg {
  /** CEL expression for the input; empty or 'raw' parses the raw frame. */
  from?: string
  decoders?: CodecStep[]
  format: ParseFormat
  python?: PythonFunctionRef
  /** Field to put the result under; empty merges at the top level. */
  into?: string
  mode?: 'merge' | 'replace'
  onError?: 'continue' | 'drop'
}

export interface StageSpec {
  python?: PythonFunctionRef
  id: string
  kind: 'filter' | 'transform' | 'rate-limit' | 'dedup' | 'schema' | 'python' | 'parse'
  // kind === 'parse'
  parse?: ParseStageCfg
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
  // QUIC-specific (url quic://host:port)
  tls?: TLSConfig
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

/** A Flow, in pipeline order: where data comes from, framing, decoding,
 * parsing, stages, and sinks. */
export interface FlowDefinition {
  name: string
  source: FlowSource
  framing: Framing
  decode?: DecodeCfg
  parser: ParserCfg
  stages: StageSpec[]
  sinks: SinkCfg[]
  deadLetterSink?: SinkCfg
  testCases?: TestCase[]
  python?: FlowPython
  /** Declared run state; empty or absent means running. */
  state?: 'running' | 'stopped' | ''
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
  /** raw or a string value was cut to the runtime's per-event limit. */
  truncated?: boolean
}
