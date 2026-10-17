import { createClient, ConnectError, Code } from '@connectrpc/connect'
import { createConnectTransport } from '@connectrpc/connect-web'
import { fromJson, toJson, type JsonValue } from '@bufbuild/protobuf'
import { GoFiService, ConfigurationSchema, EndpointSchema, GetModeResponseSchema, RuntimeInfoSchema, KubernetesTargetSchema, RouteHealthSchema } from './gen/gofi/v1/gofi_pb'
import { flowFromProto, flowToProto, jsonValue, simulationFromProto, trafficFromProto } from './rpc-model'
import type { FlowDefinition, SimulateResult, TapEvent, KubernetesTarget } from './types'

const client = createClient(GoFiService, createConnectTransport({ baseUrl: window.location.origin }))
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
export async function getFlow(name: string): Promise<FlowDefinition> {
  const { flow } = await client.getFlow({ name })
  if (!flow) throw new Error('Flow not found')
  return flowFromProto(flow)
}
/** Saves a flow. warnings name paths the flow needs that a UDS Package GoFi
 * does not own blocks; the flow is saved regardless. */
export async function saveFlow(definition: FlowDefinition): Promise<{ flow: FlowDefinition; warnings: string[] }> {
  const { flow, warnings } = await client.upsertFlow({ flow: flowToProto(definition) })
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
function watchTraffic(name: string, onMessage: (flow: string, event: TapEvent) => void, onError?: (event: Event) => void, onOpen?: () => void): () => void {
  const abort = new AbortController()
  let retryTimer: ReturnType<typeof setTimeout> | undefined
  let finishRetry: (() => void) | undefined
  const run = async () => {
    let delay = 1000
    while (!abort.signal.aborted) {
      try {
        for await (const response of client.watchTraffic({ name }, { signal: abort.signal, onHeader: () => { delay = 1000; onOpen?.() } })) {
          if (response.event && !abort.signal.aborted) onMessage(response.flow, trafficFromProto(response.event))
        }
      } catch (error) {
        if (abort.signal.aborted) return
        onError?.(new Event('error'))
        // A failed request is retried with a cap, preserving session counters.
        if (ConnectError.from(error).code === Code.PermissionDenied || ConnectError.from(error).code === Code.Unauthenticated) return
      }
      if (abort.signal.aborted) return
      await new Promise<void>((resolve) => { finishRetry = resolve; retryTimer = setTimeout(resolve, delay) })
      finishRetry = undefined
      retryTimer = undefined
      delay = Math.min(delay * 2, 10000)
    }
  }
  void run()
  return () => { abort.abort(); if (retryTimer) clearTimeout(retryTimer); finishRetry?.() }
}
export function streamFlow(name: string, onMessage: (event: TapEvent) => void, onError?: (event: Event) => void, onOpen?: () => void): () => void {
  return watchTraffic(name, (_flow, event) => onMessage(event), onError, onOpen)
}
export function streamAllFlows(onMessage: (flow: string, event: TapEvent) => void, onError?: (event: Event) => void, onOpen?: () => void): () => void {
  return watchTraffic('', onMessage, onError, onOpen)
}

export interface ReplayDelivery { endpoint?: DiscoveredEndpoint; sent: boolean; error?: string }
export async function resolveTargets(target: KubernetesTarget, network: 'tcp' | 'udp'): Promise<DiscoveredEndpoint[]> {
  const response = await client.resolveTargets({ target: fromJson(KubernetesTargetSchema, jsonValue(target)), network })
  return response.endpoints.map(endpoint => toJson(EndpointSchema, endpoint, { alwaysEmitImplicit: true }) as unknown as DiscoveredEndpoint)
}
export async function replayTraffic({ target, network, url, raw }: { target?: KubernetesTarget; network: 'tcp' | 'udp'; url?: string; raw: Uint8Array }): Promise<ReplayDelivery[]> {
  const response = await client.replay({ target: target ? fromJson(KubernetesTargetSchema, jsonValue(target)) : undefined, network, url, raw })
  return response.deliveries.map(delivery => ({ endpoint: delivery.endpoint ? toJson(EndpointSchema, delivery.endpoint, { alwaysEmitImplicit: true }) as unknown as DiscoveredEndpoint : undefined, sent: delivery.sent, error: delivery.error || undefined }))
}
