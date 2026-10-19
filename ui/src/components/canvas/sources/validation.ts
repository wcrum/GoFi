import { t } from '../../../i18n/index.ts'

/** Field checks shared by the source forms. Each returns an error message,
 * or undefined when the value is acceptable. */

export const durationProblem = (value: string | undefined, min = 0): string | undefined => {
  if (!value) return undefined
  const match = /^(\d+(?:\.\d+)?)(ms|s|m|h)$/.exec(value.trim())
  if (!match) return t('gofi.canvas.validation.duration-format', 'Use a duration such as 500ms, 10s, 5m, or 1h.')
  const seconds = Number(match[1]) * ({ ms: 0.001, s: 1, m: 60, h: 3600 } as const)[match[2] as 'ms' | 's' | 'm' | 'h']
  return seconds < min ? t('gofi.canvas.validation.duration-min', 'Must be at least {{min}}s.', { min }) : undefined
}

export const splitProblem = (value: string | undefined): string | undefined =>
  !value || /^\$(\.[^.[\]*$]+)*\[\*\]$/.test(value.trim()) ? undefined : t('gofi.canvas.validation.split-format', 'Use $[*] or a path such as $.items[*].')

export const urlProblem = (value: string, schemes: string[]): string | undefined => {
  if (!value) return t('gofi.common.required', 'Required.')
  try {
    const url = new URL(value)
    return schemes.includes(url.protocol.replace(':', '')) && url.host ? undefined : t('gofi.canvas.validation.url-scheme', 'Use {{schemes}}.', { schemes: schemes.map((s) => `${s}://`).join(', ') })
  } catch { return t('gofi.canvas.validation.url-absolute', 'Enter an absolute URL.') }
}

/** Mirrors spec validation for an S3 bucket name. */
export const bucketProblem = (value: string): string | undefined =>
  !value ? t('gofi.common.required', 'Required.') : /^[a-z0-9][a-z0-9._-]{1,61}[a-z0-9]$/.test(value) ? undefined : t('gofi.canvas.validation.bucket-format', 'Use 3–63 lowercase letters, digits, dots, or hyphens.')

export const rfc3339Problem = (value: string | undefined): string | undefined =>
  !value || /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(\.\d+)?(Z|[+-]\d{2}:\d{2})$/.test(value.trim()) ? undefined : t('gofi.canvas.validation.rfc3339-format', 'Use an RFC 3339 time such as 2026-01-02T15:04:05Z.')

/** The parameter each SQL driver binds the cursor value with. */
export const SQL_PLACEHOLDERS = { postgres: '$1', mysql: '?', sqlserver: '@p1' } as const

/** A typo guard only (mirrors spec): a read-only database user is the boundary. */
export const sqlQueryProblem = (query: string, driver: keyof typeof SQL_PLACEHOLDERS, cursorColumn?: string): string | undefined => {
  if (!query.trim()) return t('gofi.common.required', 'Required.')
  if (/^\s*(insert|update|delete|merge|upsert|replace|create|alter|drop|truncate|grant|revoke|call|exec|execute|copy|load|lock|vacuum|set)\b/i.test(query)) return t('gofi.canvas.validation.sql-read-only', 'Use a read-only query (SELECT or WITH).')
  if (cursorColumn && !query.includes(SQL_PLACEHOLDERS[driver])) return t('gofi.canvas.validation.sql-bind-cursor', 'Bind the cursor value with {{placeholder}} and ORDER BY {{column}}.', { placeholder: SQL_PLACEHOLDERS[driver], column: cursorColumn })
  return undefined
}
