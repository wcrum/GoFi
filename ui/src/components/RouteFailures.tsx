import { useEffect, useState } from 'react'
import { Alert, Stack, Text, TextLink } from '@grafana/ui'
import { ERROR_CLASS_TEXT, getRouteHealth, routeLabel, type RouteHealth } from '../api'

/** Routes whose last attempt failed, polled while the page is open. */
export function useFailingRoutes(name: string, intervalMs = 10000): RouteHealth[] {
  const [failing, setFailing] = useState<RouteHealth[]>([])
  useEffect(() => {
    let cancelled = false
    const check = () => getRouteHealth(name).then(routes => { if (!cancelled) setFailing(routes.filter(route => route.failing)) }).catch(() => { if (!cancelled) setFailing([]) })
    check()
    const timer = setInterval(check, intervalMs)
    return () => { cancelled = true; clearInterval(timer) }
  }, [name, intervalMs])
  return failing
}

/** Says when a route this flow sends on (or pulls from) is not available.
 * In Kubernetes, connectivityHref leads to whether a UDS Package is why. */
export function RouteFailures({ name, connectivityHref }: { name: string; connectivityHref?: string }) {
  const failing = useFailingRoutes(name)
  if (!failing.length) return null
  const title = failing.length === 1 ? `${routeLabel(failing[0].route)} is not reachable` : `${failing.length} routes are not reachable`
  return <Alert severity="warning" title={title} bottomSpacing={0}>
    <Stack direction="column" gap={0.5}>
      {failing.map(route => <Text key={route.route} variant="bodySmall">
        <Text weight="medium">{routeLabel(route.route)}</Text>: {ERROR_CLASS_TEXT[route.errorClass ?? ''] ?? 'failed'} · {route.failures} failure{route.failures === 1 ? '' : 's'}{route.lastFailureAt ? `, last ${new Date(route.lastFailureAt).toLocaleTimeString()}` : ''}
        {route.lastError && <Text color="secondary"> · {route.lastError}</Text>}
      </Text>)}
      {connectivityHref && <TextLink href={connectivityHref} inline={false}>Check whether a UDS Package blocks it</TextLink>}
    </Stack>
  </Alert>
}
