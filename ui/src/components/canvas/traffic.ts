import { formatNumber, t } from '../../i18n/index.ts'

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
      // Rise over a couple of samples, decay over ~4 s: a sender that emits
      // once a second (or once per tick) must read as a steady rate, not as
      // a value that sags and recovers on alternate ticks.
      const previous = this.rates.get(key) ?? 0
      const rate = instant > previous ? 0.6 * instant + 0.4 * previous : previous * Math.pow(0.6, seconds) + instant * (1 - Math.pow(0.6, seconds))
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

const fixed = (value: number, digits: number) => formatNumber(value, { minimumFractionDigits: digits, maximumFractionDigits: digits })

export function formatRate(rate: number): string {
  if (rate >= 1000) return t('gofi.canvas.traffic.rate-thousands-per-second', '{{n}}k/s', { n: fixed(rate / 1000, rate >= 10000 ? 0 : 1) })
  if (rate >= 10) return t('gofi.canvas.traffic.rate-per-second', '{{n}}/s', { n: fixed(Math.round(rate), 0) })
  // Per-minute only well below one a second: a sender at exactly one a
  // second hovers around 1.0, and a threshold there flipped the label
  // between "60/min" and "1.0/s" every sample.
  if (rate >= 0.5) return t('gofi.canvas.traffic.rate-per-second', '{{n}}/s', { n: fixed(rate, 1) })
  return t('gofi.canvas.traffic.rate-per-minute', '{{n}}/min', { n: fixed(rate * 60, rate * 60 >= 10 ? 0 : 1) })
}

/** Particles and travel time for a rate: busier edges carry more, faster
 * dots. The rate is quantized to half-octaves first, so the smoothed rate's
 * small drift between samples leaves the SMIL animation's attributes alone:
 * a changed `dur` restarts every dot, which read as flashing. */
export function particlesFor(rate: number): { count: number; seconds: number } {
  if (rate <= 0) return { count: 0, seconds: 0 }
  const level = Math.round(Math.log2(1 + rate) * 2) / 2
  const quantized = Math.pow(2, level) - 1
  const count = Math.min(5, 1 + Math.floor(level))
  const seconds = Math.round(Math.max(0.7, 2.6 - Math.log10(1 + quantized) * 0.8) * 4) / 4
  return { count, seconds }
}

// Where the runtime reports a drop that is not a stage id.
const DROP_NODE: Record<string, string> = { parser: 'parse', decoder: 'decode', framer: 'frame', listener: 'ingress' }

/** The edges a tapped message traveled: every edge for a delivered message;
 * for a dropped one, only the edges leading to the node that dropped it. */
export function edgesTraveled(edges: Array<{ id: string; source: string; target: string }>, event: { dropped: boolean; droppedBy?: string }): string[] {
  // The dead-letter sink's connection (from its Encode node) carries only
  // rejected messages.
  if (!event.dropped) return edges.filter((edge) => edge.target !== 'deadletter').map((edge) => edge.id)
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
  return edges.filter((edge) => upstream.has(edge.target) || edge.target === 'deadletter').map((edge) => edge.id)
}
