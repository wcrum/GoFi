// Locale-aware number, date, relative-time and duration formatting via Intl,
// following the active locale by default. Use these instead of
// toLocaleString()/toFixed() or hand-built "5 minutes ago" strings.

import { getLocale } from './state.ts'

type DateInput = number | Date | string

function toDate(value: DateInput): Date {
  return value instanceof Date ? value : new Date(value)
}

export function formatNumber(value: number, options?: Intl.NumberFormatOptions, locale: string = getLocale()): string {
  try {
    return new Intl.NumberFormat(locale, options).format(value)
  } catch {
    return String(value)
  }
}

/** A date (no time) unless `options` say otherwise, e.g. { dateStyle: 'medium', timeStyle: 'short' }. */
export function formatDate(value: DateInput, options?: Intl.DateTimeFormatOptions, locale: string = getLocale()): string {
  const date = toDate(value)
  if (Number.isNaN(date.getTime())) return String(value)
  try {
    return new Intl.DateTimeFormat(locale, options).format(date)
  } catch {
    return date.toISOString()
  }
}

/** Date and time, medium/short: "Oct 17, 2026, 3:04 PM" in en-US. Replaces Date#toLocaleString(). */
export function formatDateTime(value: DateInput, locale: string = getLocale()): string {
  return formatDate(value, { dateStyle: 'medium', timeStyle: 'short' }, locale)
}

/** Time of day with seconds: "3:04:05 PM". Replaces Date#toLocaleTimeString(). */
export function formatTime(value: DateInput, locale: string = getLocale()): string {
  return formatDate(value, { timeStyle: 'medium' }, locale)
}

const RELATIVE_UNITS: Array<[Intl.RelativeTimeFormatUnit, number]> = [
  ['year', 365 * 24 * 60 * 60 * 1000],
  ['month', 30 * 24 * 60 * 60 * 1000],
  ['week', 7 * 24 * 60 * 60 * 1000],
  ['day', 24 * 60 * 60 * 1000],
  ['hour', 60 * 60 * 1000],
  ['minute', 60 * 1000],
  ['second', 1000],
]

/** `value` relative to `now`: "3 minutes ago", "yesterday", "in 2 hours"
 * (Intl.RelativeTimeFormat, numeric: 'auto'). ISO strings are accepted. */
export function formatRelativeTime(
  value: DateInput,
  now: number | Date = Date.now(),
  options: Intl.RelativeTimeFormatOptions = { numeric: 'auto' },
  locale: string = getLocale(),
): string {
  const target = toDate(value).getTime()
  const base = now instanceof Date ? now.getTime() : now
  if (Number.isNaN(target)) return String(value)
  const diff = target - base
  const abs = Math.abs(diff)
  try {
    const rtf = new Intl.RelativeTimeFormat(locale, options)
    for (const [unit, ms] of RELATIVE_UNITS) {
      if (abs >= ms || unit === 'second') return rtf.format(Math.round(diff / ms), unit)
    }
  } catch {
    // Intl.RelativeTimeFormat unavailable: an absolute date is still correct.
  }
  return formatDateTime(target, locale)
}

export interface DurationFormatOptions { style?: 'long' | 'short' | 'narrow' | 'digital' }

interface DurationParts { days: number; hours: number; minutes: number; seconds: number; milliseconds: number }

function splitDuration(durationMs: number): DurationParts {
  const total = Math.max(0, Math.floor(durationMs))
  const seconds = Math.floor(total / 1000)
  return {
    days: Math.floor(seconds / 86400),
    hours: Math.floor((seconds % 86400) / 3600),
    minutes: Math.floor((seconds % 3600) / 60),
    seconds: seconds % 60,
    milliseconds: total % 1000,
  }
}

type DurationFormatCtor = new (locale: string, options?: DurationFormatOptions) => { format(parts: Partial<DurationParts>): string }

/** A duration in milliseconds: "1 hr, 5 min, 3 sec" (short, the default).
 * Sub-second durations show milliseconds. Uses Intl.DurationFormat where the
 * browser has it, else per-unit Intl.NumberFormat. */
export function formatDuration(durationMs: number, options: DurationFormatOptions = { style: 'short' }, locale: string = getLocale()): string {
  const parts = splitDuration(durationMs)
  const shown: Partial<DurationParts> = {}
  for (const unit of ['days', 'hours', 'minutes', 'seconds'] as const) if (parts[unit]) shown[unit] = parts[unit]
  if (Object.keys(shown).length === 0) {
    if (parts.milliseconds) shown.milliseconds = parts.milliseconds
    else shown.seconds = 0
  }
  const DurationFormat = (Intl as unknown as { DurationFormat?: DurationFormatCtor }).DurationFormat
  if (DurationFormat) {
    try {
      return new DurationFormat(locale, options).format(shown)
    } catch {
      // fall through to the manual join
    }
  }
  const unitStyle = options.style === 'long' ? 'long' : options.style === 'narrow' ? 'narrow' : 'short'
  const UNIT_NAMES: Record<keyof DurationParts, string> = { days: 'day', hours: 'hour', minutes: 'minute', seconds: 'second', milliseconds: 'millisecond' }
  return (Object.keys(shown) as Array<keyof DurationParts>)
    .map(unit => formatNumber(shown[unit]!, { style: 'unit', unit: UNIT_NAMES[unit], unitDisplay: unitStyle }, locale))
    .join(' ')
}
