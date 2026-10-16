import { useCallback, useEffect, useState, type ReactNode } from 'react'
import { css, cx } from '@emotion/css'
import type { GrafanaTheme2 } from '@grafana/data'
import { Alert, Button, ClipboardButton, CodeEditor, type Column, EmptyState, Icon, InteractiveTable, Stack, Text, useStyles2 } from '@grafana/ui'
import '../lib/monaco'
import { analyzeConnectivity, notKubernetes, type Connectivity, type ConnectivityPath, type PolicyFinding } from '../kubernetes-api'
import { manifestHeight } from '../lib/resource'
import type { FlowDefinition } from '../types'
import { PageLoading } from './PageLoading'
import { StatusIndicator } from './StatusIndicator'

const VERDICT = { allowed: { tone: 'success', text: 'Allowed' }, blocked: { tone: 'error', text: 'Blocked' }, unknown: { tone: 'warning', text: 'Unknown' } } as const

/** Every path a flow needs, verified against both sides' UDS Packages and
 * Pepr admission, with the allow entry to add where a path is blocked. */
export function ConnectivityPanel({ flow }: { flow: FlowDefinition }) {
  const styles = useStyles2(getStyles)
  const [result, setResult] = useState<Connectivity | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [unavailable, setUnavailable] = useState(false)
  const [checking, setChecking] = useState(false)
  const flowKey = JSON.stringify(flow)
  const check = useCallback(() => {
    setChecking(true); setError(null)
    analyzeConnectivity(JSON.parse(flowKey) as FlowDefinition).then(setResult)
      .catch(reason => notKubernetes(reason) ? setUnavailable(true) : setError(String(reason)))
      .finally(() => setChecking(false))
  }, [flowKey])
  useEffect(check, [check])

  if (unavailable) return <EmptyState variant="not-found" message="Kubernetes mode only">Connectivity checks verify UDS Packages and policies in a cluster.</EmptyState>
  if (error) return <Alert severity="error" title="Connectivity check failed">{error}</Alert>
  if (!result) return <PageLoading text="Checking connectivity..." />
  const pepr = result.engines.find(e => e.name === 'pepr')
  const blocked = result.paths.filter(p => p.verdict === 'blocked').length
  const columns: Array<Column<ConnectivityPath>> = [
    { id: 'verdict', header: 'Verdict', cell: ({ row: { original } }) => <StatusIndicator tone={VERDICT[original.verdict].tone} text={VERDICT[original.verdict].text} /> },
    { id: 'path', header: 'Path', cell: ({ row: { original } }) => <Stack gap={1} alignItems="center" wrap="wrap">
      <Text>{original.from}</Text><Icon name="arrow-right" /><Text>{original.to}</Text>
    </Stack> },
    { id: 'port', header: 'Port', cell: ({ row: { original } }) => <Text variant="code">{original.port || 'any'}/{original.protocol}</Text> },
    { id: 'summary', header: 'Why', cell: ({ row: { original } }) => <Text color="secondary">{original.summary}</Text> },
  ]
  return <Stack direction="column" gap={2}>
    <div className={styles.header}>
      <Text color="secondary">{result.paths.length} path{result.paths.length === 1 ? '' : 's'} this flow needs outside namespace {result.namespace}{blocked ? ` · ${blocked} blocked` : ''}.</Text>
      <Button variant="secondary" icon={checking ? 'spinner' : 'sync'} disabled={checking} onClick={check}>Check again</Button>
    </div>
    {pepr && <Alert severity={pepr.note?.includes('rejects') || pepr.note?.startsWith('Could not') ? 'error' : 'success'} title="Pepr admission" bottomSpacing={0}>{pepr.note}</Alert>}
    {result.paths.length ? <InteractiveTable columns={columns} data={result.paths} getRowId={row => row.id}
      renderExpandedRow={row => <Stack direction="column" gap={1.5}>
        {row.findings.map((finding, index) => <FindingLine key={index} finding={finding} />)}
        {row.fixes.map((fix, index) => <div key={index} className={styles.fix}>
          <Stack justifyContent="space-between" alignItems="center"><Text weight="medium">{fix.title}</Text><ClipboardButton size="sm" variant="secondary" icon="copy" getText={() => fix.yaml}>Copy</ClipboardButton></Stack>
          <CodeEditor language="yaml" value={fix.yaml} readOnly showLineNumbers={false} showMiniMap={false} height={manifestHeight(fix.yaml)} width="100%" monacoOptions={{ scrollBeyondLastLine: false }} />
        </div>)}
      </Stack>} />
      : <EmptyState variant="completed" message="Nothing to verify">This flow only talks inside namespace {result.namespace}, which GoFi's Package already allows.</EmptyState>}
    {result.limits.map(limit => <Text key={limit} variant="bodySmall" color="secondary">{limit}</Text>)}
  </Stack>
}

const EFFECT_TONE: Record<string, string> = { allows: 'success', isolates: 'error', blocks: 'error', unknown: 'warning' }

/** One finding: effect, the Package as `namespace/name`, then the detail with
 * allow-entry references (`network.allow[3]`) as code and the entry's own
 * description, in parentheses, shown as a quote. */
function FindingLine({ finding }: { finding: PolicyFinding }) {
  const styles = useStyles2(getStyles)
  const tone = EFFECT_TONE[finding.effect] ?? 'neutral'
  const parts = finding.detail.split(/(network\.allow\[\d+\])(?: \(([^)]*)\))?/)
  const rendered: ReactNode[] = []
  for (let i = 0; i < parts.length; i += 3) {
    if (parts[i]) rendered.push(<span key={`t${i}`}>{parts[i]}</span>)
    if (parts[i + 1]) rendered.push(<code key={`c${i}`} className={styles.code}>{parts[i + 1]}</code>)
    if (parts[i + 2]) rendered.push(<span key={`d${i}`} className={styles.quote}>“{parts[i + 2]}”</span>)
  }
  return <div className={styles.finding}>
    <span className={cx(styles.effect, styles[tone as 'success'])}>{finding.effect}</span>
    {finding.namespace && <code className={styles.code}>{finding.namespace}/{finding.name}</code>}
    <span>{rendered}</span>
  </div>
}

const getStyles = (theme: GrafanaTheme2) => ({
  finding: css({ display: 'flex', alignItems: 'baseline', gap: theme.spacing(1), flexWrap: 'wrap', fontSize: theme.typography.bodySmall.fontSize, color: theme.colors.text.primary }),
  code: css({ fontFamily: theme.typography.fontFamilyMonospace, fontSize: '0.95em', padding: theme.spacing(0, 0.5), borderRadius: theme.shape.radius.default, background: theme.colors.background.secondary, border: `1px solid ${theme.colors.border.weak}`, margin: theme.spacing(0, 0.25) }),
  quote: css({ color: theme.colors.text.secondary, fontStyle: 'italic', marginRight: theme.spacing(0.5) }),
  success: css({ color: theme.colors.success.text, borderColor: theme.colors.success.border }),
  error: css({ color: theme.colors.error.text, borderColor: theme.colors.error.border }),
  warning: css({ color: theme.colors.warning.text, borderColor: theme.colors.warning.border }),
  neutral: css({ color: theme.colors.text.secondary }),
  header: css({ display: 'flex', justifyContent: 'space-between', alignItems: 'center', gap: theme.spacing(2), flexWrap: 'wrap' }),
  effect: css({ fontFamily: theme.typography.fontFamilyMonospace, fontSize: 11, textTransform: 'uppercase', letterSpacing: 0.4, padding: theme.spacing(0, 0.5), border: '1px solid', borderRadius: theme.shape.radius.default, flexShrink: 0 }),
  fix: css({ display: 'flex', flexDirection: 'column', gap: theme.spacing(1), padding: theme.spacing(1.5), border: `1px solid ${theme.colors.border.weak}`, borderRadius: theme.shape.radius.default }),
})
