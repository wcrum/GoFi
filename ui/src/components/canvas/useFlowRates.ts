import { useEffect, useRef, useState } from 'react'
import type { TrafficObservations } from '../dashboard/useTrafficObservations'
import { RateTracker } from './traffic'

/** Per-flow message rates from the session counters, keyed
 * `<flow>:observed` (into the pipeline), `<flow>:delivered` (observed minus
 * rejected), and `<flow>:rejected`. Idle keys are omitted. */
export function useFlowRates(traffic: TrafficObservations): Record<string, number> {
  const tracker = useRef(new RateTracker())
  const previous = useRef(new Map<string, { observed: number; dropped: number }>())
  const lastTick = useRef(0)
  const [rates, setRates] = useState<Record<string, number>>({})
  // One step per stats snapshot. The controller publishes the counters once a
  // second; a timer that sampled them every 500 ms saw no change on every
  // other tick, so the smoothed rate sagged, the "/s" label and the edge
  // particles flashed, and two quiet ticks in a row made the rate vanish.
  useEffect(() => {
    const now = performance.now()
    if (lastTick.current && now - lastTick.current < 400) return
    for (const [name, observation] of Object.entries(traffic.flows)) {
      const before = previous.current.get(name)
      previous.current.set(name, { observed: observation.observed, dropped: observation.dropped })
      if (!before) continue
      // A runtime restart resets the counters: no negative or giant delta.
      const observed = observation.observed >= before.observed ? observation.observed - before.observed : 0
      const dropped = observation.dropped >= before.dropped ? observation.dropped - before.dropped : 0
      tracker.current.add(`${name}:observed`, observed)
      tracker.current.add(`${name}:delivered`, Math.max(0, observed - dropped))
      tracker.current.add(`${name}:rejected`, dropped)
    }
    lastTick.current = now
    setRates(tracker.current.tick(now))
  }, [traffic])
  return rates
}

/** Tracks a CSS media query, e.g. "(prefers-reduced-motion: reduce)". */
export function useMediaQuery(query: string): boolean {
  const [matches, setMatches] = useState(() => typeof window !== 'undefined' && window.matchMedia(query).matches)
  useEffect(() => {
    const media = window.matchMedia(query)
    const update = () => setMatches(media.matches)
    update()
    media.addEventListener('change', update)
    return () => media.removeEventListener('change', update)
  }, [query])
  return matches
}
