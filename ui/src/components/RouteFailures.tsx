import { useEffect, useRef, useState } from 'react'
import { Alert, Stack, Text, TextLink } from '@grafana/ui'
import { ERROR_CLASS_TEXT, getRouteHealth, routeLabel, type RouteHealth } from '../api'
import { formatDate, t } from '../i18n'

export const failingRoutes = (routes: RouteHealth[]): RouteHealth[] => routes.filter(route => route.failing)

/** Routes whose last attempt failed, polled while the page is open. `initial`
 * is the route health a page loaded up front: shown at once, next read on the
 * first poll. */
export function useFailingRoutes(name: string, intervalMs = 10000, initial?: RouteHealth[]): RouteHealth[] {
  const [failing, setFailing] = useState<RouteHealth[]>(() => initial ? failingRoutes(initial) : [])
  const seeded = useRef(initial !== undefined)
  useEffect(() => {
    let cancelled = false
    const check = () => getRouteHealth(name).then(routes => { if (!cancelled) setFailing(failingRoutes(routes)) }).catch(() => { if (!cancelled) setFailing([]) })
    if (seeded.current) seeded.current = false
    else check()
    const timer = setInterval(check, intervalMs)
    return () => { cancelled = true; clearInterval(timer) }
  }, [name, intervalMs])
  return failing
}

/** Says when a route this flow sends on (or pulls from) is not available.
 * In Kubernetes, connectivityHref leads to whether a UDS Package is why. */
export function RouteFailures({ name, connectivityHref, initial }: { name: string; connectivityHref?: string; initial?: RouteHealth[] }) {
  const failing = useFailingRoutes(name, undefined, initial)
  if (!failing.length) return null
  const title = failing.length === 1
    ? t('gofi.components.route-failures.title-one', '{{route}} is not reachable', { route: routeLabel(failing[0].route) })
    : t('gofi.components.route-failures.title-many', '{{count}} routes are not reachable', { count: failing.length })
  return <Alert severity="warning" title={title} bottomSpacing={0}>
    <Stack direction="column" gap={0.5}>
      {failing.map(route => {
        const failures = t('gofi.components.route-failures.failure-count', '{{count}} failures', { count: route.failures, defaultValue_one: '{{count}} failure' })
        const summary = route.lastFailureAt
          ? t('gofi.components.route-failures.summary-with-last', '{{status}} · {{failures}}, last {{time}}', { status: ERROR_CLASS_TEXT[route.errorClass ?? ''] ?? t('gofi.components.route-failures.failed', 'failed'), failures, time: formatDate(route.lastFailureAt, { timeStyle: 'medium' }) })
          : t('gofi.components.route-failures.summary', '{{status}} · {{failures}}', { status: ERROR_CLASS_TEXT[route.errorClass ?? ''] ?? t('gofi.components.route-failures.failed', 'failed'), failures })
        return <Text key={route.route} variant="bodySmall">
          <Text weight="medium">{routeLabel(route.route)}</Text>: {summary}
          {route.lastError && <Text color="secondary"> · {route.lastError}</Text>}
        </Text>
      })}
      {connectivityHref && <TextLink href={connectivityHref} inline={false}>{t('gofi.components.route-failures.check-uds', 'Check whether a network policy Package blocks it')}</TextLink>}
    </Stack>
  </Alert>
}
