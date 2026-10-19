import type { TapEvent } from '../../types'

/** Pure helpers behind LogsExplorer: batching, precomputed search text, and
 * row windowing. No React here so node tests can cover them. */

export type Level = 'error' | 'warn' | 'info' | 'debug' | 'trace' | 'unknown'
export type VolumeLevel = 'error' | 'warn' | 'info' | 'debug' | 'unknown'
export const VOLUME_LEVELS: VolumeLevel[] = ['error', 'warn', 'info', 'debug', 'unknown']

export interface CapturedEvent {
  id: number
  flow: string
  event: TapEvent
  level: Level
  /** Lowercased searchable text, computed once when the event arrives. */
  search: string
}

export function level(event: TapEvent): Level {
  const value = String(event.fields.level ?? event.fields.severity ?? '').toLowerCase()
  if (['fatal', 'critical', 'error', 'err'].includes(value)) return 'error'
  if (['warning', 'warn'].includes(value)) return 'warn'
  if (value === 'debug' || value === 'trace' || value === 'info') return value
  return event.meta.parseError ? 'error' : event.dropped ? 'warn' : 'info'
}
export const volumeLevel = (value: Level): VolumeLevel => value === 'trace' ? 'debug' : value

/** Everything a search can match, lowercased once. */
export function searchText(flow: string, event: TapEvent): string {
  return `${flow} ${event.ts} ${event.droppedBy || ''} ${event.raw} ${JSON.stringify(event.fields)} ${JSON.stringify(event.meta)}`.toLowerCase()
}

export function capture(id: number, flow: string, event: TapEvent): CapturedEvent {
  return { id, flow, event, level: level(event), search: searchText(flow, event) }
}

export interface Filters { query: string; severity: string; status: string }

export function matches(item: CapturedEvent, filters: Filters, query = filters.query.toLowerCase()): boolean {
  const { event } = item
  if (filters.severity !== 'all' && item.level !== filters.severity) return false
  if (filters.status === 'accepted' && event.dropped || filters.status === 'rejected' && !event.dropped || filters.status === 'parse-error' && !event.meta.parseError) return false
  return !query || item.search.includes(query)
}

/** Appends a batch to a bounded buffer, oldest first, keeping at most max. */
export function appendBounded<T>(buffer: T[], batch: T[], max: number): T[] {
  if (!batch.length) return buffer
  const next = buffer.length + batch.length <= max ? buffer.concat(batch) : buffer.concat(batch).slice(-max)
  return next
}

/**
 * Collects events between flushes so the view re-renders at most once per
 * frame (or interval) instead of once per event. `schedule` defaults to
 * requestAnimationFrame when available, else a 250 ms timer.
 */
export class Batcher<T> {
  private pending: T[] = []
  private scheduled = false
  private readonly flush: (batch: T[]) => void
  private readonly schedule: (run: () => void) => void
  constructor(flush: (batch: T[]) => void, schedule: (run: () => void) => void = defaultSchedule) { this.flush = flush; this.schedule = schedule }
  push(item: T) {
    this.pending.push(item)
    if (this.scheduled) return
    this.scheduled = true
    this.schedule(() => { this.scheduled = false; const batch = this.pending; this.pending = []; if (batch.length) this.flush(batch) })
  }
  /** Drops anything not yet flushed. */
  clear() { this.pending = [] }
  get size() { return this.pending.length }
}

export const FLUSH_INTERVAL_MS = 250

function defaultSchedule(run: () => void) {
  if (typeof requestAnimationFrame === 'function') {
    // One frame, but never faster than the interval under a flood.
    let fired = false
    const once = () => { if (!fired) { fired = true; run() } }
    requestAnimationFrame(once)
    setTimeout(once, FLUSH_INTERVAL_MS)
  } else setTimeout(run, FLUSH_INTERVAL_MS)
}

export interface Window { start: number; end: number; top: number; bottom: number }

/**
 * Which rows [start, end) to render for a scroll position, given a fixed
 * collapsed row height and the measured extra height of expanded rows.
 * `top`/`bottom` are spacer heights so the scrollbar reflects every row.
 */
export function windowRows(count: number, scrollTop: number, viewport: number, rowHeight: number, extra: Map<number, number>, overscan = 8): Window {
  if (count === 0) return { start: 0, end: 0, top: 0, bottom: 0 }
  const height = (index: number) => rowHeight + (extra.get(index) ?? 0)
  let offset = 0, start = 0
  while (start < count && offset + height(start) <= scrollTop) { offset += height(start); start++ }
  start = Math.max(0, start - overscan)
  let top = 0
  for (let index = 0; index < start; index++) top += height(index)
  let end = start, visible = top
  while (end < count && visible < scrollTop + viewport) { visible += height(end); end++ }
  end = Math.min(count, end + overscan)
  let bottom = 0
  for (let index = end; index < count; index++) bottom += height(index)
  return { start, end, top, bottom }
}
