import { useEffect, useState } from 'react'
import { css, cx } from '@emotion/css'
import type { GrafanaTheme2 } from '@grafana/data'
import { Alert, Button, Drawer, Stack, Text, useStyles2 } from '@grafana/ui'
import { applyUdsPackage, planUdsPackage, type PackagePlan } from '../kubernetes-api'
import { lineDiff } from '../lib/line-diff'
import { PageLoading } from './PageLoading'
import { StatusIndicator } from './StatusIndicator'
import { useToast } from './Toast'

/** Reviews GoFi's own UDS Package change (live → as GoFi would apply it for
 * the saved flows) with Pepr's verdict, then applies exactly that plan. GoFi
 * never writes another application's Package. */
export function PackageChangeDrawer({ onClose, onApplied }: { onClose: () => void; onApplied: () => void }) {
  const styles = useStyles2(getStyles)
  const addToast = useToast()
  const [plan, setPlan] = useState<PackagePlan | null>(null)
  const [error, setError] = useState('')
  const [applying, setApplying] = useState(false)
  const load = () => { setError(''); planUdsPackage().then(setPlan).catch(reason => setError(String(reason))) }
  useEffect(load, [])

  async function apply() {
    if (!plan) return
    setApplying(true); setError('')
    try {
      const next = await applyUdsPackage(plan.digest)
      setPlan(next)
      addToast(`Applied Package ${next.namespace}/${next.name}. UDS regenerates its policies shortly.`, 'success')
      onApplied()
    } catch (reason) {
      setError(String(reason))
      load() // a stale plan is refused: show the current one
    } finally {
      setApplying(false)
    }
  }

  const rejected = plan?.admission && !plan.admission.accepted
  const canApply = !!plan && plan.managed && !plan.error && !plan.inSync && !rejected
  return <Drawer title="Review GoFi's UDS Package" subtitle={plan?.name ? `${plan.namespace}/${plan.name} · live → as GoFi applies it for the saved flows` : undefined} size="md" onClose={onClose}>
    {!plan && !error ? <PageLoading text="Planning Package change..." /> : <Stack direction="column" gap={2}>
      {error && <Alert severity="error" title="Could not apply" bottomSpacing={0}>{error}</Alert>}
      {plan?.error && <Alert severity="error" title="No plan" bottomSpacing={0}>{plan.error}</Alert>}
      {plan && !plan.managed && <Alert severity="info" title="GoFi does not manage this Package" bottomSpacing={0}>uds.manageAllows is off, so GoFi only shows the change. Add the entries to additionalNetworkAllow in the uds-gofi-config chart values.</Alert>}
      {plan && !plan.error && <>
        <Stack gap={2} alignItems="center" wrap="wrap">
          {plan.inSync ? <StatusIndicator tone="success" text="Live Package is up to date" /> : <StatusIndicator tone="warning" text="Live Package differs" />}
          {plan.admission && <StatusIndicator tone={rejected ? 'error' : 'success'} text={rejected ? 'Pepr rejects it' : 'Pepr accepts it'} tooltip={plan.admission.error} />}
        </Stack>
        {rejected && <Alert severity="error" title="Pepr admission rejects this Package" bottomSpacing={0}>{plan.admission?.error}</Alert>}
        <Text variant="bodySmall" color="secondary">Only GoFi's own Package is written. Paths blocked by another application's Package still need that application's owner.</Text>
        <DiffView before={plan.liveYaml} after={plan.desiredYaml} />
        <Stack gap={1}>
          <Button icon={applying ? 'spinner' : 'check'} disabled={!canApply || applying} onClick={() => void apply()}>{applying ? 'Applying...' : 'Apply Package'}</Button>
          <Button variant="secondary" onClick={onClose}>Close</Button>
        </Stack>
      </>}
    </Stack>}
  </Drawer>

}

/** Unified line diff of two YAML documents. */
function DiffView({ before, after }: { before: string; after: string }) {
  const styles = useStyles2(getStyles)
  const lines = lineDiff(before, after)
  return <pre className={styles.diff} aria-label="Package change">
    {lines.map((line, index) => <div key={index} className={cx(styles.line, line.kind === 'added' && styles.added, line.kind === 'removed' && styles.removed)}>
      <span className={styles.sign} aria-hidden="true">{line.kind === 'added' ? '+' : line.kind === 'removed' ? '−' : ' '}</span>{line.text}
    </div>)}
  </pre>
}

const getStyles = (theme: GrafanaTheme2) => ({
  diff: css({ margin: 0, padding: theme.spacing(1, 0), fontFamily: theme.typography.fontFamilyMonospace, fontSize: theme.typography.bodySmall.fontSize, background: theme.colors.background.secondary, border: `1px solid ${theme.colors.border.weak}`, borderRadius: theme.shape.radius.default, overflowX: 'auto' }),
  line: css({ padding: theme.spacing(0, 1.5), whiteSpace: 'pre' }),
  sign: css({ display: 'inline-block', width: theme.spacing(2), color: theme.colors.text.secondary, userSelect: 'none' }),
  added: css({ background: theme.colors.success.transparent, color: theme.colors.success.text }),
  removed: css({ background: theme.colors.error.transparent, color: theme.colors.error.text }),
})
