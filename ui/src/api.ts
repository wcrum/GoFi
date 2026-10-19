import { createClient, ConnectError, Code } from '@connectrpc/connect'
import { createConnectTransport } from '@connectrpc/connect-web'
import { fromJson, toJson, type JsonValue } from '@bufbuild/protobuf'
import { GoFiService, ConfigurationSchema, EndpointSchema, GetModeResponseSchema, RuntimeInfoSchema, KubernetesTargetSchema, RouteHealthSchema } from './gen/gofi/v1/gofi_pb'
import { flowFromProto, flowToProto, jsonValue, simulationFromProto, trafficFromProto } from './rpc-model'
import type { FlowDefinition, SimulateResult, TapEvent, KubernetesTarget } from './types'
import { IdleWatchdog, reconnectDelay, STATS_IDLE_TIMEOUT_MS, TRAFFIC_IDLE_TIMEOUT_MS } from './lib/stream'
import type { FlowStatsSample } from './lib/flow-stats'

// Binary protobuf on the wire: smaller than Connect JSON and no per-message
// JSON parse on streams. Unary responses above 1 KiB arrive gzip-compressed
// (the browser's Accept-Encoding; the server compresses above that size).
export const transport = createConnectTransport({ baseUrl: window.location.origin, useBinaryFormat: true })
const client = createClient(GoFiService, transport)
export interface DeployModeInfo { mode: string; processRole: string; driverKind: string; nodePortMin?: number; nodePortMax?: number }
export interface ConfigurationInfo { source: 'kubernetes' | 'memory' | 'file'; apiVersion: string; kind: string; namespace?: string; path?: string }
export interface DiscoveredEndpoint {
  name: string; namespace?: string; kind: 'kubernetes' | 'docker'; type?: string; host: string; clusterIP?: string; externalAddresses?: string[]; labels?: Record<string, string>; ready?: boolean
  ports: { name?: string; port: number; protocol: string; nodePort?: number; url: string }[]
}
/** `error` is the controller's last failure to start the runtime. */
export interface RuntimeInfo { kind: string; id: string; status: string; startedAt?: string; node?: string; ip?: string; error?: string }

export async function getMode(): Promise<DeployModeInfo> {
  return toJson(GetModeResponseSchema, await client.getMode({}), { alwaysEmitImplicit: true }) as unknown as DeployModeInfo
}
export async function getConfiguration(): Promise<ConfigurationInfo> {
  const { configuration } = await client.getConfiguration({})
  if (!configuration) throw new Error('GoFi returned no configuration information')
  return toJson(ConfigurationSchema, configuration, { alwaysEmitImplicit: true }) as unknown as ConfigurationInfo
}
export async function getEndpoints(): Promise<DiscoveredEndpoint[]> {
  return (await client.listEndpoints({})).endpoints.map((endpoint) => toJson(EndpointSchema, endpoint, { alwaysEmitImplicit: true }) as unknown as DiscoveredEndpoint)
}
export async function listFlows(): Promise<FlowDefinition[]> {
  return (await client.listFlows({})).flows.map(flowFromProto)
}
/** Inventory poll: when nothing changed since `revision`, resolves
 * `unchanged` without definitions, so the poll costs one small round trip. */
export async function listFlowsSince(revision: number): Promise<{ flows?: FlowDefinition[]; revision: number; unchanged: boolean }> {
  const response = await client.listFlows({ ifRevision: BigInt(revision) })
  return { flows: response.unchanged ? undefined : response.flows.map(flowFromProto), revision: Number(response.revision), unchanged: response.unchanged }
}
export async function getFlow(name: string): Promise<FlowDefinition> {
  const { flow } = await client.getFlow({ name })
  if (!flow) throw new Error('Flow not found')
  return flowFromProto(flow)
}
/** Saves a flow. warnings name paths the flow needs that a UDS Package GoFi
 * does not own blocks; the flow is saved regardless. */
export async function saveFlow(definition: FlowDefinition, message = ''): Promise<{ flow: FlowDefinition; warnings: string[] }> {
  // message is recorded on the flow revision the save creates (history-api.ts).
  const { flow, warnings } = await client.upsertFlow({ flow: flowToProto(definition), message })
  if (!flow) throw new Error('GoFi returned no flow')
  return { flow: flowFromProto(flow), warnings }
}
/** Runs every check a save runs (including platform admission) without
 * saving; resolves to the warnings a save would return. */
export async function validateFlow(definition: FlowDefinition): Promise<string[]> {
  return (await client.upsertFlow({ flow: flowToProto(definition), validateOnly: true })).warnings
}
export async function deleteFlow(name: string): Promise<void> {
  try { await client.deleteFlow({ name }) }
  catch (error) { if (ConnectError.from(error).code !== Code.NotFound) throw error }
}
export async function getRuntimeStatus(name: string): Promise<RuntimeInfo> {
  const { runtime } = await client.getRuntimeStatus({ name })
  if (!runtime) throw new Error('Runtime status unavailable')
  return toJson(RuntimeInfoSchema, runtime) as unknown as RuntimeInfo
}
/** One outbound route as the flow's runtime observed it. route is
 * "egress-sink-<i>", "egress-deadletter", or "egress-source". */
export interface RouteHealth { route: string; successes: number; failures: number; lastSuccessAt?: string; lastFailureAt?: string; lastError?: string; errorClass?: string; failing: boolean }
/** Protobuf JSON carries int64 counts as strings. */
export const routeHealthFromJson = (json: RouteHealth): RouteHealth => ({ ...json, successes: Number(json.successes ?? 0), failures: Number(json.failures ?? 0), failing: !!json.failing })
export async function getRouteHealth(name: string): Promise<RouteHealth[]> {
  const { routes } = await client.getRouteHealth({ name })
  return routes.map(route => routeHealthFromJson(toJson(RouteHealthSchema, route, { alwaysEmitImplicit: true }) as unknown as RouteHealth))
}
/** A route's name for people: "Destination 2", "Dead letter", "Source". */
export function routeLabel(route: string): string {
  const sink = /^egress-sink-(\d+)$/.exec(route)
  return sink ? `Destination ${Number(sink[1]) + 1}` : route === 'egress-deadletter' ? 'Dead letter' : route === 'egress-source' ? 'Source' : route
}
/** How a failure looked on the socket, in a few words. */
export const ERROR_CLASS_TEXT: Record<string, string> = {
  denied: 'denied by Istio (RBAC)', forbidden: '403 Forbidden', http: 'HTTP error', dns: 'name does not resolve', refused: 'connection refused',
  reset: 'connection reset', timeout: 'timed out', unreachable: 'network unreachable', tls: 'TLS handshake failed', other: 'failed',
}
export async function simulateFlow(flow: FlowDefinition, raw: string): Promise<SimulateResult> {
  const { result } = await client.simulate({ flow: flowToProto(flow), raw })
  if (!result) throw new Error('GoFi returned no simulation result')
  return simulationFromProto(result)
}

// Every live view uses the generated server-streaming RPC. Cancellation closes
// both the active request and any reconnect timer; no background stream remains.
export interface StreamHandlers {
  /** The stream is down: a transport failure, or the server closed it. Fires
   * again for every failed reconnect attempt, until `onOpen`. */
  onError?: (event: Event) => void
  /** Response headers arrived: the stream is live again. */
  onOpen?: () => void
  /** Events the controller skipped for this stream because the browser fell
   * behind (reported on the next event or heartbeat). */
  onDropped?: (count: number) => void
}

/**
 * Runs a server stream until cancelled, reconnecting after every failure or
 * server-side close with jittered exponential backoff (so a fleet of tabs
 * never reconnects in lockstep). Only an authorization failure ends the loop.
 * `open` receives the signal for one attempt and an `onHeader` callback.
 *
 * Each attempt has its own AbortController, watched by an idle timer: a
 * connection that dies without closing (the dev proxy keeps a response whose
 * upstream vanished open forever; so can a NAT or a load balancer) never
 * errors on its own, so after `idleTimeoutMs` of silence the attempt is
 * cancelled and the next one opens. The server heartbeats keep a healthy
 * quiet stream well inside that window.
 */
function runStream<T>(
  open: (signal: AbortSignal, onHeader: () => void) => AsyncIterable<T>,
  onResponse: (response: T) => void,
  handlers: StreamHandlers,
  idleTimeoutMs: number,
): () => void {
  const abort = new AbortController()
  let retryTimer: ReturnType<typeof setTimeout> | undefined
  let finishRetry: (() => void) | undefined
  const run = async () => {
    let attempt = 0
    while (!abort.signal.aborted) {
      const request = new AbortController()
      const cancel = () => request.abort()
      abort.signal.addEventListener('abort', cancel, { once: true })
      const watchdog = new IdleWatchdog(idleTimeoutMs, cancel)
      try {
        for await (const response of open(request.signal, () => { attempt = 0; watchdog.touch(); handlers.onOpen?.() })) {
          if (abort.signal.aborted) break
          watchdog.touch()
          onResponse(response)
        }
        if (abort.signal.aborted) return
        // The server ended the stream (shutdown, drain): the view is
        // disconnected until the next attempt opens.
        handlers.onError?.(new Event('close'))
      } catch (error) {
        if (abort.signal.aborted) return
        handlers.onError?.(new Event('error'))
        const code = ConnectError.from(error).code
        if (code === Code.PermissionDenied || code === Code.Unauthenticated) return
      } finally {
        watchdog.stop()
        abort.signal.removeEventListener('abort', cancel)
      }
      if (abort.signal.aborted) return
      await new Promise<void>((resolve) => { finishRetry = resolve; retryTimer = setTimeout(resolve, reconnectDelay(attempt++)) })
      finishRetry = undefined
      retryTimer = undefined
    }
  }
  void run()
  return () => { abort.abort(); if (retryTimer) clearTimeout(retryTimer); finishRetry?.() }
}

function watchTraffic(name: string, onMessage: (flow: string, event: TapEvent) => void, handlers: StreamHandlers = {}): () => void {
  return runStream(
    (signal, onHeader) => client.watchTraffic({ name }, { signal, onHeader }),
    (response) => {
      if (response.dropped) handlers.onDropped?.(Number(response.dropped))
      if (response.event) onMessage(response.flow, trafficFromProto(response.event))
    },
    handlers,
    TRAFFIC_IDLE_TIMEOUT_MS,
  )
}
const legacyHandlers = (onError?: (event: Event) => void, onOpen?: () => void, onDropped?: (count: number) => void): StreamHandlers => ({ onError, onOpen, onDropped })
export function streamFlow(name: string, onMessage: (event: TapEvent) => void, onError?: (event: Event) => void, onOpen?: () => void, onDropped?: (count: number) => void): () => void {
  return watchTraffic(name, (_flow, event) => onMessage(event), legacyHandlers(onError, onOpen, onDropped))
}
export function streamAllFlows(onMessage: (flow: string, event: TapEvent) => void, onError?: (event: Event) => void, onOpen?: () => void, onDropped?: (count: number) => void): () => void {
  return watchTraffic('', onMessage, legacyHandlers(onError, onOpen, onDropped))
}

/** One message per second with every flow's cumulative counters (or the
 * named flow's). Dashboards and status views use this instead of the event
 * stream. Reconnects like watchTraffic. */
export function streamFlowStats(name: string, onMessage: (samples: FlowStatsSample[], at: number) => void, handlers: StreamHandlers = {}): () => void {
  return runStream(
    (signal, onHeader) => client.watchFlowStats({ name }, { signal, onHeader }),
    (response) => {
      if (response.heartbeat && !response.flows.length) return
      onMessage(response.flows.map(flow => ({
        flow: flow.flow, up: flow.up, observed: Number(flow.observed), rejected: Number(flow.rejected), parseErrors: Number(flow.parseErrors),
        delivered: Number(flow.delivered), sinkFailures: Number(flow.sinkFailures), tapDropped: Number(flow.tapDropped),
      })), response.at ? Number(response.at.seconds) * 1000 + Math.floor(response.at.nanos / 1e6) : Date.now())
    },
    handlers,
    STATS_IDLE_TIMEOUT_MS,
  )
}

export interface ReplayDelivery { endpoint?: DiscoveredEndpoint; sent: boolean; error?: string }
export async function resolveTargets(target: KubernetesTarget, network: 'tcp' | 'udp' | 'quic'): Promise<DiscoveredEndpoint[]> {
  const response = await client.resolveTargets({ target: fromJson(KubernetesTargetSchema, jsonValue(target)), network })
  return response.endpoints.map(endpoint => toJson(EndpointSchema, endpoint, { alwaysEmitImplicit: true }) as unknown as DiscoveredEndpoint)
}
export async function replayTraffic({ target, network, url, raw }: { target?: KubernetesTarget; network: 'tcp' | 'udp' | 'quic'; url?: string; raw: Uint8Array }): Promise<ReplayDelivery[]> {
  const response = await client.replay({ target: target ? fromJson(KubernetesTargetSchema, jsonValue(target)) : undefined, network, url, raw })
  return response.deliveries.map(delivery => ({ endpoint: delivery.endpoint ? toJson(EndpointSchema, delivery.endpoint, { alwaysEmitImplicit: true }) as unknown as DiscoveredEndpoint : undefined, sent: delivery.sent, error: delivery.error || undefined }))
}
