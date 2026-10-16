/** Smoothed per-key message rates for animating live traffic on edges.
 * Counts are added as messages arrive; `tick` turns them into msgs/second
 * with an exponential moving average so bursts fade out instead of blinking. */
export class RateTracker {
  private counts = new Map<string, number>()
  private rates = new Map<string, number>()
  private last = performance.now()

  add(key: string, count = 1) {
    this.counts.set(key, (this.counts.get(key) ?? 0) + count)
  }

  /** Advances the window and returns current rates (keys with ~0 rate are omitted). */
  tick(now = performance.now()): Record<string, number> {
    const seconds = Math.max((now - this.last) / 1000, 0.05)
    this.last = now
    const keys = new Set([...this.counts.keys(), ...this.rates.keys()])
    const out: Record<string, number> = {}
    for (const key of keys) {
      const instant = (this.counts.get(key) ?? 0) / seconds
      // Rise quickly, decay over ~2 s.
      const previous = this.rates.get(key) ?? 0
      const rate = instant > previous ? 0.6 * instant + 0.4 * previous : previous * Math.pow(0.35, seconds) + instant * (1 - Math.pow(0.35, seconds))
      if (rate < 0.05) {
        this.rates.delete(key)
        continue
      }
      this.rates.set(key, rate)
      out[key] = rate
    }
    this.counts.clear()
    return out
  }
}

export function formatRate(rate: number): string {
  if (rate >= 1000) return `${(rate / 1000).toFixed(rate >= 10000 ? 0 : 1)}k/s`
  if (rate >= 10) return `${Math.round(rate)}/s`
  if (rate >= 1) return `${rate.toFixed(1)}/s`
  return `${(rate * 60).toFixed(rate * 60 >= 10 ? 0 : 1)}/min`
}

/** Particles and travel time for a rate: busier edges carry more, faster dots. */
export function particlesFor(rate: number): { count: number; seconds: number } {
  if (rate <= 0) return { count: 0, seconds: 0 }
  const count = Math.min(5, 1 + Math.floor(Math.log2(1 + rate)))
  const seconds = Math.max(0.7, 2.6 - Math.log10(1 + rate) * 0.8)
  return { count, seconds }
}

// Where the runtime reports a drop that is not a stage id.
const DROP_NODE: Record<string, string> = { parser: 'parse', decoder: 'decode', framer: 'frame', listener: 'ingress' }

/** The edges a tapped message traveled: every edge for a delivered message;
 * for a dropped one, only the edges leading to the node that dropped it. */
export function edgesTraveled(edges: Array<{ id: string; source: string; target: string }>, event: { dropped: boolean; droppedBy?: string }): string[] {
  if (!event.dropped) return edges.map((edge) => edge.id)
  const stop = DROP_NODE[event.droppedBy ?? ''] ?? event.droppedBy ?? 'ingress'
  const upstream = new Set([stop])
  for (let changed = true; changed;) {
    changed = false
    for (const edge of edges) {
      if (upstream.has(edge.target) && !upstream.has(edge.source)) {
        upstream.add(edge.source)
        changed = true
      }
    }
  }
  return edges.filter((edge) => upstream.has(edge.target)).map((edge) => edge.id)
}
