import { createContext, useContext, useMemo, useState } from 'react'
import { css } from '@emotion/css'
import type { GrafanaTheme2 } from '@grafana/data'
import { Badge, ConfirmModal, FilterInput, IconButton, InteractiveTable, LinkButton, RadioButtonGroup, Stack, TextLink, useStyles2, type Column } from '@grafana/ui'
import { EmptyState } from '../components/EmptyState'
import { Page } from '../shell/Page'
import { paths, to } from '../router'
import { useGoFi } from '../state'
import { deleteFlow, type RuntimeInfo } from '../api'
import { useRuntimeInfo } from '../runtime-api'
import { useToast } from '../components/Toast'
import { FRAMING_COLOR, PARSER_COLOR, SINK_COLOR } from '../components/stageColors'
import { listenOf, sourceSummary, type FlowDefinition } from '../types'
import { PageLoading } from '../components/PageLoading'
import { RunControls, RunStateBadge } from '../components/operations/RunControls'
import { useFlowStatus } from '../operations-api'
import { useTrafficObservations, type TrafficObservations } from '../components/dashboard/useTrafficObservations'
import type { FlowObservation } from '../components/canvas/topology'
import { formatNumber, t } from '../i18n'

type Protocol = 'all' | 'tcp' | 'udp'
// The table's columns must keep their identity: react-table mounts each cell
// renderer as a component, so columns rebuilt on every counters message
// would remount every cell (and reset what they show). The per-second
// traffic snapshot reaches TrafficCell through context instead.
const TrafficContext = createContext<TrafficObservations | null>(null)
const network = (flow: FlowDefinition) => (listenOf(flow).network || 'tcp').toLowerCase()

export default function FlowsPage() {
  const styles = useStyles2(getStyles)
  const addToast = useToast()
  const { flows, loading, reload } = useGoFi()
  const [query, setQuery] = useState('')
  const [protocol, setProtocol] = useState<Protocol>('all')
  const [pendingDelete, setPendingDelete] = useState<string | null>(null)
  // One shared counters stream for the whole table (no per-row polling).
  const traffic = useTrafficObservations(useMemo(() => flows.map(flow => flow.name), [flows]))

  const filtered = useMemo(() => flows.filter(flow => {
    if (protocol !== 'all' && network(flow) !== protocol) return false
    const needle = query.trim().toLowerCase()
    if (!needle) return true
    return [flow.name, String(listenOf(flow).port), ...flow.sinks.map(sink => sink.type), flow.deadLetterSink?.type ?? ''].some(value => value.toLowerCase().includes(needle))
  }), [flows, protocol, query])

  const columns = useMemo<Array<Column<FlowDefinition>>>(() => [
    { id: 'name', header: t('gofi.pages.flows.column-name', 'Name'), sortType: 'string', cell: ({ row }) => <TextLink href={to(paths.flow(row.original.name))} inline={false} weight="medium">{row.original.name}</TextLink> },
    { id: 'source', header: t('gofi.pages.flows.column-source', 'Source'), cell: ({ row: { original } }) => <span className={styles.mono}>
      {original.source.listen ? t('gofi.pages.flows.listen-summary', 'listen {{network}}:{{port}}', { network: network(original), port: listenOf(original).port }) : sourceSummary(original.source)}
      {listenOf(original).service && <span className={styles.secondary}> · {listenOf(original).service!.type}{listenOf(original).service!.nodePort ? ` :${listenOf(original).service!.nodePort}` : ''}</span>}
    </span> },
    { id: 'pipeline', header: t('gofi.pages.flows.column-pipeline', 'Pipeline'), cell: ({ row: { original } }) => <Stack gap={0.5} alignItems="center" wrap="wrap">
      <Badge text={original.framing.mode} color={FRAMING_COLOR[original.framing.mode] ?? 'darkgrey'} />
      <span className={styles.secondary}>→</span>
      <Badge text={original.parser.format || t('gofi.pages.flows.parser-none', 'none')} color={PARSER_COLOR[original.parser.format] ?? 'darkgrey'} />
      {original.stages.length > 0 && <><span className={styles.secondary}>→</span><Badge text={t('gofi.pages.flows.stage-count', '{{count}} stages', { count: original.stages.length, defaultValue_one: '{{count}} stage' })} color="darkgrey" /></>}
    </Stack> },
    { id: 'destinations', header: t('gofi.pages.flows.column-destinations', 'Destinations'), cell: ({ row: { original } }) => <Stack gap={0.5} wrap="wrap">
      {original.sinks.map((sink, index) => <Badge key={index} text={sink.type} color={SINK_COLOR[sink.type] ?? 'darkgrey'} />)}
      {original.deadLetterSink && <Badge text={t('gofi.pages.flows.dead-letter-badge', 'dead letter: {{type}}', { type: original.deadLetterSink.type })} color="orange" />}
      {!original.sinks.length && !original.deadLetterSink && <span className={styles.secondary}>—</span>}
    </Stack> },
    { id: 'state', header: t('gofi.pages.flows.column-state', 'State'), cell: ({ row: { original } }) => <RunStateBadge name={original.name} /> },
    { id: 'problems', header: t('gofi.pages.flows.column-problems', 'Problems'), cell: ({ row: { original } }) => <ProblemsCell name={original.name} /> },
    { id: 'runtime', header: t('gofi.pages.flows.column-runtime', 'Runtime'), cell: ({ row: { original } }) => <RuntimeCell name={original.name} /> },
    { id: 'traffic', header: t('gofi.pages.flows.column-traffic', 'Traffic'), cell: ({ row: { original } }) => <TrafficCell name={original.name} /> },
    { id: 'actions', header: '', disableGrow: true, cell: ({ row: { original } }) => <Stack gap={0.5} justifyContent="flex-end" alignItems="center">
      <RunControls name={original.name} compact />
      <LinkButton size="sm" fill="text" variant="secondary" icon="sitemap" tooltip={t('gofi.pages.flows.action-flow', 'Flow')} aria-label={t('gofi.pages.flows.action-open-flow-for', 'Open flow for {{name}}', { name: original.name })} href={paths.flow(original.name)} />
      <LinkButton size="sm" fill="text" variant="secondary" icon="document-info" tooltip={t('gofi.pages.flows.action-live-traffic', 'Live traffic')} aria-label={t('gofi.pages.flows.action-live-traffic-for', 'Live traffic for {{name}}', { name: original.name })} href={paths.flow(original.name, 'traffic')} />
      <LinkButton size="sm" fill="text" variant="secondary" icon="pen" tooltip={t('gofi.common.edit', 'Edit')} aria-label={t('gofi.pages.flows.action-edit-flow', 'Edit {{name}}', { name: original.name })} href={paths.flow(original.name, 'edit')} />
      <IconButton name="trash-alt" variant="destructive" tooltip={t('gofi.common.delete', 'Delete')} aria-label={t('gofi.pages.flows.action-delete-flow', 'Delete {{name}}', { name: original.name })} onClick={() => setPendingDelete(original.name)} />
    </Stack> },
  ], [styles])

  async function confirmDelete() {
    const name = pendingDelete
    setPendingDelete(null)
    if (!name) return
    try { await deleteFlow(name); await reload(); addToast(t('gofi.pages.flows.deleted-flow', 'Deleted flow {{name}}', { name }), 'success') }
    catch (error) { addToast(String(error)) }
  }

  return <Page title={t('gofi.pages.flows.title', 'Flows')} icon="exchange-alt"
    subTitle={t('gofi.pages.flows.subtitle', 'Each flow receives TCP or UDP traffic on a port, decodes frames, applies processing stages, and routes messages to destinations.')}
    actions={<LinkButton icon="plus" href={paths.newFlow()}>{t('gofi.common.new-flow', 'New flow')}</LinkButton>}>
    {loading ? <PageLoading text={t('gofi.pages.flows.loading-flows', 'Loading flows...')} />
      : !flows.length ? <EmptyState variant="call-to-action" message={t('gofi.pages.flows.empty-title', "You haven't created any flows yet")} button={<LinkButton icon="plus" size="lg" href={paths.newFlow()}>{t('gofi.common.create-flow', 'Create flow')}</LinkButton>}>
      </EmptyState>
      : <>
        <div className={styles.toolbar}>
          <div className={styles.search}><FilterInput value={query} onChange={setQuery} placeholder={t('gofi.pages.flows.search-placeholder', 'Search by name, port, or destination type')} aria-label={t('gofi.pages.flows.search-flows', 'Search flows')} /></div>
          <RadioButtonGroup<Protocol> aria-label={t('gofi.pages.flows.protocol', 'Protocol')} value={protocol} onChange={setProtocol} options={[{ label: t('gofi.common.all', 'All'), value: 'all' }, { label: 'TCP', value: 'tcp' }, { label: 'UDP', value: 'udp' }]} />
        </div>
        {filtered.length
          ? <TrafficContext.Provider value={traffic}><InteractiveTable columns={columns} data={filtered} getRowId={row => row.name} pageSize={25} initialSortBy={[{ id: 'name', desc: false }]} /></TrafficContext.Provider>
          : <EmptyState variant="not-found" message={t('gofi.pages.flows.no-match', 'No flows match your filters')} button={<LinkButton variant="secondary" onClick={() => { setQuery(''); setProtocol('all') }}>{t('gofi.pages.flows.clear-filters', 'Clear filters')}</LinkButton>} />}
      </>}
    <ConfirmModal isOpen={pendingDelete !== null} title={t('gofi.pages.flows.delete-title', 'Delete flow')} body={t('gofi.pages.flows.delete-body', 'Delete flow "{{name}}"? Its port closes and traffic stops being routed.', { name: pendingDelete })}
      confirmText={t('gofi.common.delete', 'Delete')} onConfirm={confirmDelete} onDismiss={() => setPendingDelete(null)} />
  </Page>
}

/** Runtime counters from the shared stats stream: up/down and totals. The
 * cell keeps one size whatever it shows, so a value updating in place never
 * shifts the row. */
function TrafficCell({ name }: { name: string }) {
  const styles = useStyles2(getStyles)
  const traffic = useContext(TrafficContext)
  const observation: FlowObservation | undefined = traffic?.flows[name]
  const state = traffic?.state ?? 'connecting'
  return <span className={styles.cell}>{
    state !== 'connected' && !observation?.up ? <span className={styles.secondary}>{state === 'reconnecting' ? t('gofi.pages.flows.traffic-reconnecting', 'reconnecting…') : '…'}</span>
    : !observation?.up ? <Badge text={t('gofi.pages.flows.traffic-no-runtime', 'no runtime')} color="darkgrey" tooltip={t('gofi.pages.flows.traffic-no-runtime-tooltip', 'The runtime has not reported counters in the last few seconds.')} />
    : <TrafficBadge observation={observation} />
  }</span>
}
function TrafficBadge({ observation }: { observation: FlowObservation }) {
  const tone = observation.parseErrors || observation.dropped ? 'orange' : 'green'
  const observed = formatNumber(observation.observed)
  const dropped = formatNumber(observation.dropped)
  return <Badge color={tone} text={observation.dropped
    ? t('gofi.pages.flows.traffic-observed-rejected', '{{observed}} observed · {{rejected}} rejected', { observed, rejected: dropped })
    : t('gofi.pages.flows.traffic-observed', '{{observed}} observed', { observed })}
    tooltip={t('gofi.pages.flows.traffic-tooltip', 'Since the runtime started: {{observed}} observed, {{rejected}} rejected, {{parseErrors}} parse errors.', { observed, rejected: dropped, parseErrors: formatNumber(observation.parseErrors) })} />
}

/** Open problems of a flow, linking to its Problems tab. */
function ProblemsCell({ name }: { name: string }) {
  const styles = useStyles2(getStyles)
  const status = useFlowStatus(name)
  return <span className={styles.cell}>{!status?.problems
    ? <span className={styles.secondary}>—</span>
    : <a href={paths.flow(name, 'problems')} aria-label={t('gofi.pages.flows.open-problems-in', '{{count}} open problems in {{name}}', { count: status.problems, name, defaultValue_one: '{{count}} open problem in {{name}}' })}><Badge text={String(status.problems)} icon={status.errors ? 'exclamation-circle' : 'exclamation-triangle'} color={status.errors ? 'red' : 'orange'} /></a>}
  </span>
}

/** The runtime from the shared cache: "…" only until its first value, then
 * updated in place by the poll (never reset by a re-render). */
function RuntimeCell({ name }: { name: string }) {
  const styles = useStyles2(getStyles)
  const runtime = useRuntimeInfo(name)
  return <span className={styles.cell}>{
    runtime === undefined ? <span className={styles.secondary}>…</span>
    : !runtime ? <span className={styles.secondary}>—</span>
    : <RuntimeBadge runtime={runtime} />
  }</span>
}
function RuntimeBadge({ runtime }: { runtime: RuntimeInfo }) {
  const status = (runtime.status || 'unknown').toLowerCase()
  const color = ['running', 'ready', 'active'].includes(status) ? 'green' : ['error', 'failed', 'crashloopbackoff'].includes(status) ? 'red' : 'darkgrey'
  return <Badge text={t('gofi.pages.flows.runtime-badge', '{{kind}}: {{status}}', { kind: runtime.kind, status: runtime.status || t('gofi.pages.flows.runtime-status-unknown', 'unknown') })} color={color} tooltip={runtime.error} />
}

const getStyles = (theme: GrafanaTheme2) => ({
  toolbar: css({ display: 'flex', flexWrap: 'wrap', gap: theme.spacing(1), alignItems: 'center', marginBottom: theme.spacing(2) }),
  search: css({ flex: '1 1 280px', minWidth: 0 }),
  mono: css({ fontFamily: theme.typography.fontFamilyMonospace, fontSize: theme.typography.bodySmall.fontSize }),
  secondary: css({ color: theme.colors.text.secondary }),
  // Reserved so a placeholder, a badge, or a longer badge all take the same height and at least the same width.
  cell: css({ display: 'inline-flex', alignItems: 'center', minHeight: theme.spacing(2.75), minWidth: '7em' }),
})
