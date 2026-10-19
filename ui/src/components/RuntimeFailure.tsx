import { Alert } from '@grafana/ui'
import { useRuntimeInfo } from '../runtime-api'
import { t } from '../i18n'

/** The controller's last failure to start a flow's runtime (for example, a
 * Service the Kubernetes API rejected), from the shared runtime cache that
 * polls while the page is open. */
export function useRuntimeError(name: string): string | undefined {
  return useRuntimeInfo(name)?.error || undefined
}

export function RuntimeFailure({ name }: { name: string }) {
  const error = useRuntimeError(name)
  if (!error) return null
  return <Alert severity="error" title={t('gofi.components.runtime-failure.title', "This flow's runtime failed to start")} bottomSpacing={0}>{error}</Alert>
}
