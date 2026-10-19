/**
 * Flow counters as WatchFlowStats streams them: cumulative since the runtime
 * started, once a second, for every flow. These helpers turn the samples
 * into what dashboards show: per-flow totals, per-second deltas, and a
 * bounded history, with a runtime restart (counters falling) handled as a
 * reset rather than a negative rate.
 */

export interface FlowStatsSample {
  flow: string
  up: boolean
  observed: number
  rejected: number
  parseErrors: number
  delivered: number
  sinkFailures: number
  tapDropped: number
}

export interface SecondBucket { observed: number; dropped: number; parseErrors: number }
export const emptyBucket = (): SecondBucket => ({ observed: 0, dropped: 0, parseErrors: 0 })

/** Per-second deltas since the previous sample of the same flow. */
export function sampleDelta(previous: FlowStatsSample | undefined, current: FlowStatsSample): SecondBucket {
  if (!previous) return emptyBucket()
  const step = (before: number, after: number) => after >= before ? after - before : after // a restart resets the counters
  return { observed: step(previous.observed, current.observed), dropped: step(previous.rejected, current.rejected), parseErrors: step(previous.parseErrors, current.parseErrors) }
}

export interface StatsHistory {
  /** Oldest-first buckets for the last `seconds` seconds. */
  buckets: SecondBucket[]
  seconds: number
}

/** A rolling per-second history keyed by wall-clock second. */
export class RollingHistory {
  private readonly entries = new Map<number, SecondBucket>()
  readonly seconds: number
  constructor(seconds: number) { this.seconds = seconds }
  add(second: number, delta: SecondBucket) {
    const bucket = this.entries.get(second) ?? emptyBucket()
    bucket.observed += delta.observed; bucket.dropped += delta.dropped; bucket.parseErrors += delta.parseErrors
    this.entries.set(second, bucket)
    for (const key of this.entries.keys()) if (key <= second - this.seconds * 2) this.entries.delete(key)
  }
  /** Oldest-first buckets ending at `now` (a wall-clock second). */
  series(now: number): SecondBucket[] {
    return Array.from({ length: this.seconds }, (_, index) => ({ ...(this.entries.get(now - this.seconds + 1 + index) ?? emptyBucket()) }))
  }
}

/** Sums the per-flow totals of one stats message. */
export function totals(samples: FlowStatsSample[]): SecondBucket {
  const sum = emptyBucket()
  for (const sample of samples) { sum.observed += sample.observed; sum.dropped += sample.rejected; sum.parseErrors += sample.parseErrors }
  return sum
}
