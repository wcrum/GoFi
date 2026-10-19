/** Reconnect timing shared by every server stream the portal opens. */

export const RECONNECT_BASE_MS = 1000
export const RECONNECT_CAP_MS = 15000

/**
 * Delay before reconnect attempt `attempt` (0-based): exponential from one
 * second to a 15 s cap, with ±50% jitter so every tab of a fleet does not
 * reconnect at the same instant after a controller restart. `random` is
 * injectable for tests (defaults to Math.random).
 */
export function reconnectDelay(attempt: number, random: () => number = Math.random): number {
  const base = Math.min(RECONNECT_CAP_MS, RECONNECT_BASE_MS * 2 ** Math.max(0, Math.min(attempt, 10)))
  return Math.round(base / 2 + random() * base)
}

/** WatchTraffic sends a heartbeat every 15 s when there is no traffic (api/stream.go). */
export const TRAFFIC_HEARTBEAT_MS = 15000
/** WatchFlowStats sends once a second (api/stats.go statsInterval). */
export const STATS_INTERVAL_MS = 1000
/** A stream that stays silent for three heartbeats is treated as dead. */
export const TRAFFIC_IDLE_TIMEOUT_MS = 3 * TRAFFIC_HEARTBEAT_MS
export const STATS_IDLE_TIMEOUT_MS = 10 * STATS_INTERVAL_MS

/**
 * Calls `onIdle` once when `touch()` has not been called for `timeoutMs`.
 * A server stream whose connection died silently (a dev proxy that never
 * closes the response, a NAT that dropped the socket, a controller that was
 * killed mid-stream) never errors on its own: the watchdog cancels the
 * request so the reconnect loop can open a fresh one. The timer starts on
 * construction, so a request that never receives headers is covered too.
 */
export class IdleWatchdog {
  private timer: ReturnType<typeof setTimeout> | undefined
  private readonly timeoutMs: number
  private readonly onIdle: () => void
  constructor(timeoutMs: number, onIdle: () => void) {
    this.timeoutMs = timeoutMs
    this.onIdle = onIdle
    this.touch()
  }
  /** Something arrived: restart the countdown. */
  touch(): void {
    this.stop()
    if (this.timeoutMs > 0) this.timer = setTimeout(() => { this.timer = undefined; this.onIdle() }, this.timeoutMs)
  }
  stop(): void {
    if (this.timer) { clearTimeout(this.timer); this.timer = undefined }
  }
  get armed(): boolean { return this.timer !== undefined }
}

/** Tracks the latest `dropped` counts a stream reports so a view can show
 * how many events it missed since it opened (and since the last look). */
export class DropCounter {
  total = 0
  private seen = 0
  add(dropped: number | bigint | undefined) {
    const value = Number(dropped ?? 0)
    if (value > 0) this.total += value
  }
  /** Drops since the previous call. */
  take(): number {
    const delta = this.total - this.seen
    this.seen = this.total
    return delta
  }
}

/** Refcounted shared resource: the first subscriber opens it, the last one
 * closes it after `linger` ms (so a page change within the grace reuses the
 * open stream instead of reconnecting). */
export function shared<T>(open: (publish: (value: T) => void) => () => void, linger = 2000) {
  const listeners = new Set<(value: T) => void>()
  let stop: (() => void) | undefined
  let closeTimer: ReturnType<typeof setTimeout> | undefined
  let latest: T | undefined
  const publish = (value: T) => { latest = value; for (const listener of listeners) listener(value) }
  return {
    subscribe(listener: (value: T) => void): () => void {
      listeners.add(listener)
      if (closeTimer) { clearTimeout(closeTimer); closeTimer = undefined }
      if (!stop) stop = open(publish)
      else if (latest !== undefined) listener(latest)
      return () => {
        listeners.delete(listener)
        if (listeners.size === 0 && stop && !closeTimer) {
          closeTimer = setTimeout(() => { closeTimer = undefined; if (listeners.size === 0) { stop?.(); stop = undefined; latest = undefined } }, linger)
        }
      }
    },
    /** Open subscribers (for tests). */
    get size() { return listeners.size },
    get open() { return !!stop },
  }
}
