import { useEffect, useState } from 'react'
import { Alert } from '@grafana/ui'
import { getRuntimeStatus } from '../api'

/** The controller's last failure to start a flow's runtime (for example, a
 * Service the Kubernetes API rejected), polled while the page is open. */
export function useRuntimeError(name: string, intervalMs = 5000): string | undefined {
  const [error, setError] = useState<string>()
  useEffect(() => {
    let cancelled = false
    const check = () => getRuntimeStatus(name).then(info => { if (!cancelled) setError(info.error || undefined) }).catch(() => { if (!cancelled) setError(undefined) })
    check()
    const timer = setInterval(check, intervalMs)
    return () => { cancelled = true; clearInterval(timer) }
  }, [name, intervalMs])
  return error
}

export function RuntimeFailure({ name }: { name: string }) {
  const error = useRuntimeError(name)
  if (!error) return null
  return <Alert severity="error" title="This flow's runtime failed to start" bottomSpacing={0}>{error}</Alert>
}
