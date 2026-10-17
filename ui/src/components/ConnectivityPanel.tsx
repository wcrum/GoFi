import { useCallback, useEffect, useState, type ReactNode } from 'react'
import { css, cx } from '@emotion/css'
import type { GrafanaTheme2 } from '@grafana/data'
import { Alert, Button, ClipboardButton, CodeEditor, type Column, Icon, InteractiveTable, Stack, Text, useStyles2 } from '@grafana/ui'
import { EmptyState } from './EmptyState'
import '../lib/monaco'
import { ERROR_CLASS_TEXT } from '../api'
import { analyzeConnectivity, notKubernetes, type Connectivity, type ConnectivityPath, type PolicyFinding, type SuggestedFix } from '../kubernetes-api'
import { manifestHeight } from '../lib/resource'
import type { FlowDefinition } from '../types'
import { PageLoading } from './PageLoading'
import { PackageChangeDrawer } from './PackageChangeDrawer'
import { StatusIndicator } from './StatusIndicator'

const VERDICT = { allowed: { tone: 'success', text: 'Allowed' }, blocked: { tone: 'error', text: 'Blocked' }, unknown: { tone: 'warning', text: 'Unknown' } } as const

/** The verdict a person reads: a Package verdict, sharpened by what the
 * running flow saw (a blocked path that is failing is "Not allowed"). */
function verdictOf(path: ConnectivityPath) {
  if (path.diagnosis === 'not-allowed') return { tone: 'error', text: 'Not allowed' } as const
  if (path.diagnosis === 'mismatch') return { tone: 'warning', text: 'Blocked, yet working' } as const
  return VERDICT[path.verdict]
}

/** Every path a flow needs, verified against both sides' UDS Packages and
 * Pepr admission and joined with what the running flow observed, with the
 * allow entry to add where a path is blocked: applied by GoFi when it is
 * GoFi's own Package, copied to its owner otherwise. */
export function ConnectivityPanel({ flow }: { flow: FlowDefinition }) {
  const styles = useStyles2(getStyles)
  const [result, setResult] = useState<Connectivity | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [unavailable, setUnavailable] = useState(false)
  const [checking, setChecking] = useState(false)
  const [reviewing, setReviewing] = useState(false)
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
  const notAllowed = result.paths.filter(p => p.diagnosis === 'not-allowed')
  const unreachable = result.paths.filter(p => p.diagnosis === 'unreachable')
  const applicable = notAllowed.flatMap(p => p.fixes).some(fix => fix.action === 'apply-gofi-package')
  const columns: Array<Column<ConnectivityPath>> = [
    { id: 'verdict', header: 'Verdict', cell: ({ row: { original } }) => { const v = verdictOf(original); return <StatusIndicator tone={v.tone} text={v.text} /> } },
    { id: 'path', header: 'Path', cell: ({ row: { original } }) => <Stack gap={1} alignItems="center" wrap="wrap">
      <Text>{original.from}</Text><Icon name="arrow-right" /><Text>{original.to}</Text>
    </Stack> },
    { id: 'port', header: 'Port', cell: ({ row: { original } }) => <Text variant="code">{original.port || 'any'}/{original.protocol}</Text> },
    { id: 'observed', header: 'Observed', cell: ({ row: { original } }) => <Observed path={original} /> },
    { id: 'summary', header: 'Why', cell: ({ row: { original } }) => <Text color="secondary">{original.summary}</Text> },
  ]
  return <Stack direction="column" gap={2}>
    <div className={styles.header}>
      <Text color="secondary">{result.paths.length} path{result.paths.length === 1 ? '' : 's'} this flow needs outside namespace {result.namespace}{blocked ? ` · ${blocked} blocked` : ''}.</Text>
      <Button variant="secondary" icon={checking ? 'spinner' : 'sync'} disabled={checking} onClick={check}>Check again</Button>
    </div>
    {notAllowed.length > 0 && <Alert severity="error" bottomSpacing={0}
      title={notAllowed.length === 1 ? `${notAllowed[0].to} is not allowed` : `${notAllowed.length} routes are not allowed`}
      buttonContent={applicable ? 'Review Package change' : undefined} onRemove={applicable ? () => setReviewing(true) : undefined}>
      GoFi tried to send and failed, and a UDS Package does not allow the path. {applicable
        ? 'GoFi can update its own Package: review the change before applying it.'
        : 'The Package belongs to the application on the other side; expand the path for the entry its owner needs to add.'}
    </Alert>}
    {unreachable.length > 0 && <Alert severity="warning" bottomSpacing={0} title={`${unreachable.length} route${unreachable.length === 1 ? ' fails' : 's fail'}, but not because of UDS`}>
      The Packages allow {unreachable.length === 1 ? 'this path' : 'these paths'}, yet sends fail. Check that the destination is up and that nothing outside Kubernetes (a firewall, DNS) is in the way.
    </Alert>}
    {pepr && <Alert severity={pepr.note?.includes('rejects') || pepr.note?.startsWith('Could not') ? 'error' : 'success'} title="Pepr admission" bottomSpacing={0}>{pepr.note}</Alert>}
    {result.paths.length ? <InteractiveTable columns={columns} data={result.paths} getRowId={row => row.id}
      renderExpandedRow={row => <Stack direction="column" gap={1.5}>
        {row.observed?.lastError && <Text variant="bodySmall" color="secondary">Last error: <Text variant="code">{row.observed.lastError}</Text></Text>}
        {row.findings.map((finding, index) => <FindingLine key={index} finding={finding} />)}
        {row.fixes.map((fix, index) => <FixBlock key={index} fix={fix} onReview={() => setReviewing(true)} />)}
      </Stack>} />
      : <EmptyState variant="completed" message="Nothing to verify">This flow only talks inside namespace {result.namespace}, which GoFi's Package already allows.</EmptyState>}
    {result.limits.map(limit => <Text key={limit} variant="bodySmall" color="secondary">{limit}</Text>)}
    {reviewing && <PackageChangeDrawer onClose={() => setReviewing(false)} onApplied={check} />}
  </Stack>
}

/** What the running flow saw on the path: nothing, working, or failing and how. */
function Observed({ path }: { path: ConnectivityPath }) {
  const observed = path.observed
  if (!observed) return <Text color="secondary">—</Text>
  if (observed.failing) return <StatusIndicator tone="error" text={ERROR_CLASS_TEXT[observed.errorClass ?? ''] ?? 'Failing'}
    tooltip={`${observed.failures} failed, ${observed.successes} succeeded${observed.lastFailureAt ? ` · last failure ${new Date(observed.lastFailureAt).toLocaleString()}` : ''}`} />
  return <StatusIndicator tone="success" text={`${observed.successes} sent`} tooltip={observed.failures ? `${observed.failures} earlier failure${observed.failures === 1 ? '' : 's'}` : undefined} />
}

/** A suggested allow entry: who owns it, and either GoFi applies it or it is
 * copied for its owner. */
function FixBlock({ fix, onReview }: { fix: SuggestedFix; onReview: () => void }) {
  const styles = useStyles2(getStyles)
  return <div className={styles.fix}>
    <Stack justifyContent="space-between" alignItems="center" wrap="wrap">
      <Text weight="medium">{fix.title}</Text>
      <Stack gap={1}>
        <ClipboardButton size="sm" variant="secondary" icon="copy" getText={() => fix.yaml}>Copy</ClipboardButton>
        {fix.action === 'apply-gofi-package' && <Button size="sm" icon="eye" onClick={onReview}>Review and apply</Button>}
      </Stack>
    </Stack>
    {fix.note && <Text variant="bodySmall" color="secondary">{fix.note}</Text>}
    <CodeEditor language="yaml" value={fix.yaml} readOnly showLineNumbers={false} showMiniMap={false} height={manifestHeight(fix.yaml)} width="100%" monacoOptions={{ scrollBeyondLastLine: false }} />
  </div>
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
