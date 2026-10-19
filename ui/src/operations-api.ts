import { useEffect, useSyncExternalStore } from 'react'
import { createClient } from '@connectrpc/connect'
import { createConnectTransport } from '@connectrpc/connect-web'
import { timestampDate, type Timestamp } from '@bufbuild/protobuf/wkt'
import { OperationsService, type Bulletin as ProtoBulletin, type FlowStatus as ProtoFlowStatus } from './gen/gofi/v1/operations_pb'

const client = createClient(OperationsService, createConnectTransport({ baseUrl: window.location.origin, useBinaryFormat: true }))

/** Observed run state, as the controller derives it. */
export type FlowPhase = 'running' | 'starting' | 'stopping' | 'stopped' | 'crashed' | 'failed' | 'invalid' | 'unknown'

export interface FlowStatus {
  name: string
  desired: 'running' | 'stopped'
  phase: FlowPhase
  restarts: number
  lastRestartAt?: Date
  runtimeStatus: string
  startedAt?: Date
  message: string
  /** Open (unacknowledged) error and warning bulletins. */
  problems: number
  errors: number
  warnings: number
}

export interface Bulletin {
  id: string
  flow: string
  severity: 'error' | 'warning' | 'info'
  /** "source", "framer", "parser", "stage:<id>", "sink:<index>", "deadLetter", "runtime", "tap". */
  component: string
  message: string
  count: number
  firstSeen?: Date
  lastSeen?: Date
  acknowledged: boolean
}

const date = (value?: Timestamp) => value ? timestampDate(value) : undefined

export function statusFromProto(status: ProtoFlowStatus): FlowStatus {
  return {
    name: status.name, desired: status.desired === 'stopped' ? 'stopped' : 'running', phase: (status.phase || 'unknown') as FlowPhase,
    restarts: status.restarts, lastRestartAt: date(status.lastRestartAt), runtimeStatus: status.runtimeStatus, startedAt: date(status.startedAt),
    message: status.message, problems: status.problems, errors: status.errors, warnings: status.warnings,
  }
}
export function bulletinFromProto(b: ProtoBulletin): Bulletin {
  return {
    id: b.id, flow: b.flow, severity: (b.severity || 'info') as Bulletin['severity'], component: b.component, message: b.message,
    count: Number(b.count), firstSeen: date(b.firstSeen), lastSeen: date(b.lastSeen), acknowledged: b.acknowledged,
  }
}

export async function startFlow(name: string): Promise<FlowStatus | undefined> {
  const { status } = await client.startFlow({ name }); statuses.refresh()
  return status && statusFromProto(status)
}
export async function stopFlow(name: string): Promise<FlowStatus | undefined> {
  const { status } = await client.stopFlow({ name }); statuses.refresh()
  return status && statusFromProto(status)
}
export async function restartFlow(name: string): Promise<FlowStatus | undefined> {
  const { status } = await client.restartFlow({ name }); statuses.refresh()
  return status && statusFromProto(status)
}
export async function listBulletins(flow: string, includeAcknowledged = true): Promise<Bulletin[]> {
  return (await client.listBulletins({ flow, includeAcknowledged })).bulletins.map(bulletinFromProto)
}
export async function acknowledgeBulletins(flow: string, ids: string[] = []): Promise<void> {
  await client.acknowledgeBulletins({ flow, ids }); statuses.refresh()
}
export async function clearBulletins(flow: string, ids: string[] = []): Promise<void> {
  await client.clearBulletins({ flow, ids }); statuses.refresh()
}

/** One poller shared by every component showing run state: it polls only
 * while something is subscribed, and the controller caches what it reads
 * from runtimes, so open tabs never fan out to flow Pods. */
interface StatusSnapshot { byName: Record<string, FlowStatus>; problems: number; loaded: boolean; error?: string }
const POLL_MS = 5000
function createStatusStore() {
  let snapshot: StatusSnapshot = { byName: {}, problems: 0, loaded: false }
  const listeners = new Set<() => void>()
  let timer: number | undefined
  let inflight: Promise<void> | undefined
  let loadedAt = 0
  const FRESH_MS = 1000
  const emit = () => listeners.forEach(listener => listener())
  function refresh(): Promise<void> {
    if (inflight) return inflight
    inflight = (async () => {
      try {
        const response = await client.listFlowStatus({})
        const byName: Record<string, FlowStatus> = {}
        for (const status of response.flows) byName[status.name] = statusFromProto(status)
        snapshot = { byName, problems: response.problems, loaded: true }
        loadedAt = Date.now()
      } catch (error) {
        snapshot = { ...snapshot, loaded: true, error: String(error) }
      } finally {
        inflight = undefined
        emit()
      }
    })()
    return inflight
  }
  return {
    refresh: () => { void refresh() },
    /** Resolves once the statuses are (re)read; joins a read in flight. */
    load: () => refresh(),
    subscribe(listener: () => void) {
      listeners.add(listener)
      if (listeners.size === 1) {
        // A page that just loaded the statuses as part of its initial data need not read them again.
        if (Date.now() - loadedAt > FRESH_MS) void refresh()
        timer = window.setInterval(() => { if (document.visibilityState !== 'hidden') void refresh() }, POLL_MS)
      }
      return () => {
        listeners.delete(listener)
        if (!listeners.size && timer !== undefined) { clearInterval(timer); timer = undefined }
      }
    },
    get: () => snapshot,
  }
}
const statuses = createStatusStore()

/** Every flow's run state and open problem counts (shared, polled). */
export function useFlowStatuses(): StatusSnapshot {
  return useSyncExternalStore(statuses.subscribe, statuses.get)
}
/** One flow's run state. */
export function useFlowStatus(name: string): FlowStatus | undefined {
  return useFlowStatuses().byName[name]
}
/** Refresh the shared run state now (after an operation or save). */
export const refreshFlowStatuses = statuses.refresh
/** Read the shared run state and resolve with it (for a page's initial load);
 * components subscribing right after find it already there. */
export const loadFlowStatuses = async (): Promise<StatusSnapshot> => { await statuses.load(); return statuses.get() }

/** Re-read statuses whenever a flow's definition changes. */
export function useRefreshOn(key: unknown) {
  useEffect(() => { statuses.refresh() }, [key])
}
