import { createClient, ConnectError, Code } from '@connectrpc/connect'
import { createConnectTransport } from '@connectrpc/connect-web'
import { fromJson, toJson, type JsonValue } from '@bufbuild/protobuf'
import { GoFiService, ConfigurationSchema, EndpointSchema, GetModeResponseSchema, RuntimeInfoSchema, KubernetesTargetSchema } from './gen/gofi/v1/gofi_pb'
import { listenerFromProto, listenerToProto, simulationFromProto, trafficFromProto } from './rpc-model'
import type { ListenerDefinition, SimulateResult, TapEvent, KubernetesTarget } from './types'

const client = createClient(GoFiService, createConnectTransport({ baseUrl: window.location.origin }))
export interface DeployModeInfo { mode: string; processRole: string; driverKind: string }
export interface ConfigurationInfo { source: 'kubernetes' | 'memory' | 'file'; apiVersion: string; kind: string; namespace?: string; path?: string }
export interface DiscoveredEndpoint {
  name: string; namespace?: string; kind: 'kubernetes' | 'docker'; type?: string; host: string; clusterIP?: string; externalAddresses?: string[]; labels?: Record<string, string>; ready?: boolean
  ports: { name?: string; port: number; protocol: string; nodePort?: number; url: string }[]
}
export interface RuntimeInfo { kind: string; id: string; status: string; startedAt?: string; node?: string; ip?: string }

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
export async function listListeners(): Promise<ListenerDefinition[]> {
  return (await client.listListeners({})).listeners.map(listenerFromProto)
}
export async function getListener(name: string): Promise<ListenerDefinition> {
  const { listener } = await client.getListener({ name })
  if (!listener) throw new Error('Listener not found')
  return listenerFromProto(listener)
}
export async function createListener(definition: ListenerDefinition): Promise<ListenerDefinition> {
  const { listener } = await client.upsertListener({ listener: listenerToProto(definition) })
  if (!listener) throw new Error('GoFi returned no listener')
  return listenerFromProto(listener)
}
export async function deleteListener(name: string): Promise<void> {
  try { await client.deleteListener({ name }) }
  catch (error) { if (ConnectError.from(error).code !== Code.NotFound) throw error }
}
export async function getRuntimeStatus(name: string): Promise<RuntimeInfo> {
  const { runtime } = await client.getRuntimeStatus({ name })
  if (!runtime) throw new Error('Runtime status unavailable')
  return toJson(RuntimeInfoSchema, runtime) as unknown as RuntimeInfo
}
export async function simulateListener(listener: ListenerDefinition, raw: string): Promise<SimulateResult> {
  const { result } = await client.simulate({ listener: listenerToProto(listener), raw })
  if (!result) throw new Error('GoFi returned no simulation result')
  return simulationFromProto(result)
}

// Every live view uses the generated server-streaming RPC. Cancellation closes
// both the active request and any reconnect timer; no background stream remains.
function watchTraffic(name: string, onMessage: (listener: string, event: TapEvent) => void, onError?: (event: Event) => void, onOpen?: () => void): () => void {
  const abort = new AbortController()
  let retryTimer: ReturnType<typeof setTimeout> | undefined
  let finishRetry: (() => void) | undefined
  const run = async () => {
    let delay = 1000
    while (!abort.signal.aborted) {
      try {
        for await (const response of client.watchTraffic({ name }, { signal: abort.signal, onHeader: () => { delay = 1000; onOpen?.() } })) {
          if (response.event && !abort.signal.aborted) onMessage(response.listener, trafficFromProto(response.event))
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
export function streamListener(name: string, onMessage: (event: TapEvent) => void, onError?: (event: Event) => void, onOpen?: () => void): () => void {
  return watchTraffic(name, (_listener, event) => onMessage(event), onError, onOpen)
}
export function streamAllListeners(onMessage: (listener: string, event: TapEvent) => void, onError?: (event: Event) => void, onOpen?: () => void): () => void {
  return watchTraffic('', onMessage, onError, onOpen)
}

export interface ReplayDelivery { endpoint?: DiscoveredEndpoint; sent: boolean; error?: string }
export async function resolveTargets(target: KubernetesTarget, network: 'tcp' | 'udp'): Promise<DiscoveredEndpoint[]> {
  const response = await client.resolveTargets({ target: fromJson(KubernetesTargetSchema, target as unknown as JsonValue), network })
  return response.endpoints.map(endpoint => toJson(EndpointSchema, endpoint, { alwaysEmitImplicit: true }) as unknown as DiscoveredEndpoint)
}
export async function replayTraffic({ target, network, url, raw }: { target?: KubernetesTarget; network: 'tcp' | 'udp'; url?: string; raw: Uint8Array }): Promise<ReplayDelivery[]> {
  const response = await client.replay({ target: target ? fromJson(KubernetesTargetSchema, target as unknown as JsonValue) : undefined, network, url, raw })
  return response.deliveries.map(delivery => ({ endpoint: delivery.endpoint ? toJson(EndpointSchema, delivery.endpoint, { alwaysEmitImplicit: true }) as unknown as DiscoveredEndpoint : undefined, sent: delivery.sent, error: delivery.error || undefined }))
}
