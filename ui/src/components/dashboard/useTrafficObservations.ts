import { useEffect, useRef, useState } from 'react'
import { streamAllFlows } from '../../api'
import type { FlowObservation } from '../canvas/topology'

export const HISTORY_SECONDS = 60
export interface SecondBucket { observed: number; dropped: number; parseErrors: number }
export interface TrafficObservations {
  state: FlowObservation['state']
  /** Per-flow session counters, keyed by flow name. */
  flows: Record<string, FlowObservation>
  totals: SecondBucket
  /** Oldest-first per-second counts for the last HISTORY_SECONDS seconds. */
  history: SecondBucket[]
}

const emptyBucket = (): SecondBucket => ({ observed: 0, dropped: 0, parseErrors: 0 })
export const emptyObservation = (): FlowObservation => ({ state: 'connecting', observed: 0, dropped: 0, parseErrors: 0, lastSeen: 0 })

/**
 * One all-flow ConnectRPC traffic stream for a view. Counters are session
 * values: they start when the view mounts and are not processing history, and a
 * reconnect keeps (does not reset) them. Pass `enabled = false` when a parent
 * already owns the stream and hands observations down.
 */
export function useTrafficObservations(names: string[], enabled = true): TrafficObservations {
  const namesKey = JSON.stringify([...names].sort())
  const counters = useRef(new Map<string, FlowObservation>())
  const totals = useRef(emptyBucket())
  const buckets = useRef<{ second: number; bucket: SecondBucket }[]>([])
  const connection = useRef<FlowObservation['state']>('connecting')
  const [snapshot, setSnapshot] = useState<TrafficObservations>({ state: 'connecting', flows: {}, totals: emptyBucket(), history: Array.from({ length: HISTORY_SECONDS }, emptyBucket) })

  // Reconcile names without resetting counters when the inventory polls.
  useEffect(() => {
    const current = new Set<string>(JSON.parse(namesKey))
    for (const name of counters.current.keys()) if (!current.has(name)) counters.current.delete(name)
    for (const name of current) if (!counters.current.has(name)) counters.current.set(name, emptyObservation())
  }, [namesKey])

  useEffect(() => {
    if (!enabled) return
    const bucketFor = (second: number) => {
      const last = buckets.current[buckets.current.length - 1]
      if (last?.second === second) return last.bucket
      const entry = { second, bucket: emptyBucket() }
      buckets.current.push(entry)
      if (buckets.current.length > HISTORY_SECONDS * 2) buckets.current.splice(0, buckets.current.length - HISTORY_SECONDS)
      return entry.bucket
    }
    const stop = streamAllFlows((name, event) => {
      const observation = counters.current.get(name)
      if (!observation) return
      const bucket = bucketFor(Math.floor(Date.now() / 1000))
      observation.observed++; totals.current.observed++; bucket.observed++
      if (event.dropped) { observation.dropped++; totals.current.dropped++; bucket.dropped++ }
      if (event.meta?.parseError) { observation.parseErrors++; totals.current.parseErrors++; bucket.parseErrors++ }
      observation.lastSeen = Date.now()
    }, () => { connection.current = 'reconnecting' }, () => { connection.current = 'connected' })
    const flush = () => {
      const now = Math.floor(Date.now() / 1000)
      const bySecond = new Map(buckets.current.map(entry => [entry.second, entry.bucket]))
      setSnapshot({
        state: connection.current,
        flows: Object.fromEntries([...counters.current].map(([name, observation]) => [name, { ...observation, state: connection.current }])),
        totals: { ...totals.current },
        history: Array.from({ length: HISTORY_SECONDS }, (_, index) => ({ ...(bySecond.get(now - HISTORY_SECONDS + 1 + index) ?? emptyBucket()) })),
      })
    }
    flush()
    const timer = window.setInterval(flush, 1000)
    return () => { stop(); window.clearInterval(timer) }
  }, [enabled])
  return snapshot
}
