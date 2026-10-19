import { useCallback, useSyncExternalStore } from 'react'
import { getRuntimeStatus, type RuntimeInfo } from './api'

/** A flow's runtime as the controller last reported it; `null` when it has
 * not started or this deployment mode reports none. */
export type RuntimeInfoState = RuntimeInfo | null

// One cache of runtime info per flow, shared by every view that shows it
// (the flows table, a flow page's failure banner). A value, once known, is
// kept in place across remounts and polls, so cells never fall back to a
// placeholder after their first value; polling runs only while subscribed.
interface Entry { info?: RuntimeInfoState; loadedAt: number; inflight?: Promise<RuntimeInfoState> }
const POLL_MS = 5000
const FRESH_MS = 1000
const entries = new Map<string, Entry>()
const listeners = new Map<string, Set<() => void>>()
let timer: number | undefined

const entryOf = (name: string): Entry => { let entry = entries.get(name); if (!entry) { entry = { loadedAt: 0 }; entries.set(name, entry) } return entry }
const emit = (name: string) => listeners.get(name)?.forEach(listener => listener())

/** Fetches (or joins the in-flight fetch of) a flow's runtime info and caches it. */
export function loadRuntimeInfo(name: string): Promise<RuntimeInfoState> {
  const entry = entryOf(name)
  if (entry.inflight) return entry.inflight
  entry.inflight = getRuntimeStatus(name).then(info => info, () => null).then(info => {
    entry.info = info; entry.loadedAt = Date.now(); entry.inflight = undefined
    emit(name)
    return info
  })
  return entry.inflight
}

/** The cached runtime info; undefined until first loaded. */
export const peekRuntimeInfo = (name: string): RuntimeInfoState | undefined => entries.get(name)?.info

function poll() {
  if (document.visibilityState === 'hidden') return
  for (const name of listeners.keys()) void loadRuntimeInfo(name)
}

export function subscribeRuntimeInfo(name: string, listener: () => void): () => void {
  let set = listeners.get(name)
  if (!set) { set = new Set(); listeners.set(name, set) }
  set.add(listener)
  if (Date.now() - entryOf(name).loadedAt > FRESH_MS) void loadRuntimeInfo(name)
  if (timer === undefined) timer = window.setInterval(poll, POLL_MS)
  return () => {
    set.delete(listener)
    if (!set.size) listeners.delete(name)
    if (!listeners.size && timer !== undefined) { clearInterval(timer); timer = undefined }
  }
}

/** A flow's runtime info, polled while something shows it. */
export function useRuntimeInfo(name: string): RuntimeInfoState | undefined {
  const subscribe = useCallback((listener: () => void) => subscribeRuntimeInfo(name, listener), [name])
  const get = useCallback(() => peekRuntimeInfo(name), [name])
  return useSyncExternalStore(subscribe, get)
}
