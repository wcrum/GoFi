import { t } from '../../../i18n/index.ts'

// Client-side mirrors of the Go checks in spec/zenoh.go, so the forms flag
// what the server would reject. The server remains authoritative.

/** Mirrors spec.ValidateZenohOrigin: an http(s) origin without a path, query,
 * fragment, or credentials. */
export function zenohOriginProblem(value: string): string | undefined {
  if (!value) return t('gofi.common.required', 'Required.')
  try {
    const url = new URL(value)
    if (url.protocol !== 'http:' && url.protocol !== 'https:') return t('gofi.canvas.zenoh-validation.origin-scheme', 'Use the router REST origin, http:// or https:// (not the native 7447 port).')
    if (url.username || url.password) return t('gofi.canvas.zenoh-validation.origin-credentials', 'Put credentials under Authentication, not in the URL.')
    if ((url.pathname && url.pathname !== '/') || url.search || url.hash || value.includes('?') || value.includes('#')) return t('gofi.canvas.zenoh-validation.origin-only', 'Use the origin only, without a path or query.')
    return undefined
  } catch { return t('gofi.canvas.zenoh-validation.origin-absolute', 'Enter an absolute URL such as http://zenoh-router:8000.') }
}

/** Mirrors spec.ZenohKeyPath. Subscriptions allow whole * and ** chunks. */
export function zenohKeyProblem(key: string, wildcards: boolean): string | undefined {
  if (!key) return t('gofi.common.required', 'Required.')
  const segments = key.split('/')
  for (let i = 0; i < segments.length; i++) {
    const segment = segments[i]
    if (!segment || segment === '.' || segment === '..') return t('gofi.canvas.zenoh-validation.key-segments', 'Segments must be nonempty and cannot be . or ..')
    if (segment === '*' || segment === '**') {
      if (!wildcards) return t('gofi.canvas.zenoh-validation.key-no-wildcards', 'Use one concrete key without wildcards.')
      if (segment === '**' && segments[i - 1] === '**') return t('gofi.canvas.zenoh-validation.key-double-wildcard', 'Use ** once, not **/**.')
      continue
    }
    if (/[*$?#@\\%]/.test(segment) || /\p{Cc}/u.test(segment)) {
      return wildcards ? t('gofi.canvas.zenoh-validation.key-whole-wildcards', 'Wildcards must be whole * or ** segments; @ (admin space) and query syntax are not allowed.') : t('gofi.canvas.zenoh-validation.key-concrete', 'Use a concrete key without wildcards, query syntax, or @ admin paths.')
    }
  }
  return undefined
}
