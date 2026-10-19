import { useEffect, useState } from 'react'
import { streamFlowStats } from '../../api'
import type { FlowObservation } from '../canvas/topology'
import { RollingHistory, emptyBucket, sampleDelta, totals, type FlowStatsSample, type SecondBucket } from '../../lib/flow-stats'
import { shared } from '../../lib/stream'

export const HISTORY_SECONDS = 60
export type { SecondBucket }
export interface TrafficObservations {
  state: FlowObservation['state']
  /** Per-flow runtime counters, keyed by flow name. */
  flows: Record<string, FlowObservation>
  totals: SecondBucket
  /** Oldest-first per-second counts for the last HISTORY_SECONDS seconds. */
  history: SecondBucket[]
}

export const emptyObservation = (): FlowObservation => ({ state: 'connecting', observed: 0, dropped: 0, parseErrors: 0, lastSeen: 0, up: false })
const emptySnapshot = (): TrafficObservations => ({ state: 'connecting', flows: {}, totals: emptyBucket(), history: Array.from({ length: HISTORY_SECONDS }, emptyBucket) })

// One WatchFlowStats stream per browser tab, shared by every dashboard view
// (home stats, topology, flow overview, flows table). A message a second
// carries every flow's cumulative counters; views derive totals, per-second
// history, and rates from it. No traffic events reach these views.
const flowStats = shared<TrafficObservations>((publish) => {
  const previous = new Map<string, FlowStatsSample>()
  const observations = new Map<string, FlowObservation>()
  const history = new RollingHistory(HISTORY_SECONDS)
  let state: FlowObservation['state'] = 'connecting'
  let latest: FlowStatsSample[] = []
  let lastAt = 0
  const snapshot = (): TrafficObservations => ({
    state,
    flows: Object.fromEntries([...observations].map(([name, observation]) => [name, { ...observation, state }])),
    totals: totals(latest),
    history: history.series(Math.floor(Date.now() / 1000)),
  })
  const stop = streamFlowStats('', (samples) => {
    // Bucketed by the browser's clock, not the message's server timestamp:
    // the 60-second window is drawn against Date.now(), so a controller whose
    // clock differs from the viewer's put the newest buckets outside the
    // window (the sparkline filled partway, then went blank). The server's
    // `at` is not needed here; the stream is one message a second.
    const at = Date.now()
    const second = Math.floor(at / 1000)
    const seen = new Set<string>()
    for (const sample of samples) {
      seen.add(sample.flow)
      history.add(second, sampleDelta(previous.get(sample.flow), sample))
      previous.set(sample.flow, sample)
      observations.set(sample.flow, { state, observed: sample.observed, dropped: sample.rejected, parseErrors: sample.parseErrors, lastSeen: sample.up ? at : observations.get(sample.flow)?.lastSeen ?? 0, up: sample.up })
    }
    for (const name of observations.keys()) if (!seen.has(name)) { observations.delete(name); previous.delete(name) }
    latest = samples
    lastAt = at
    publish(snapshot())
  }, {
    onOpen: () => { state = 'connected'; publish(snapshot()) },
    onError: () => { state = 'reconnecting'; publish(snapshot()) },
  })
  // Keep the 60-second window moving while the stream is quiet.
  const tick = window.setInterval(() => { if (Date.now() - lastAt > 1500) publish(snapshot()) }, 1000)
  return () => { stop(); window.clearInterval(tick) }
}, 5000)

/**
 * The shared per-flow counters for a view. `names` limits the flows reported
 * (the inventory); unknown flows read as empty observations. Pass
 * `enabled = false` when a parent already owns the data and hands it down.
 */
export function useTrafficObservations(names: string[], enabled = true): TrafficObservations {
  const namesKey = JSON.stringify([...names].sort())
  const [snapshot, setSnapshot] = useState<TrafficObservations>(emptySnapshot)
  useEffect(() => {
    if (!enabled) return
    const wanted = new Set<string>(JSON.parse(namesKey) as string[])
    return flowStats.subscribe(value => {
      const flows: Record<string, FlowObservation> = {}
      for (const name of wanted) flows[name] = value.flows[name] ?? { ...emptyObservation(), state: value.state }
      setSnapshot({ ...value, flows })
    })
  }, [enabled, namesKey])
  return snapshot
}
