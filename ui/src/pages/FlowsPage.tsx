import { useEffect, useMemo, useState } from 'react'
import { css } from '@emotion/css'
import type { GrafanaTheme2 } from '@grafana/data'
import { Badge, ConfirmModal, EmptyState, FilterInput, IconButton, InteractiveTable, LinkButton, RadioButtonGroup, Stack, TextLink, useStyles2, type Column } from '@grafana/ui'
import { Page } from '../shell/Page'
import { paths, to } from '../router'
import { useGoFi } from '../state'
import { deleteFlow, getRuntimeStatus, type RuntimeInfo } from '../api'
import { useToast } from '../components/Toast'
import { FRAMING_COLOR, PARSER_COLOR, SINK_COLOR } from '../components/stageColors'
import { listenOf, sourceSummary, type FlowDefinition } from '../types'
import { PageLoading } from '../components/PageLoading'

type Protocol = 'all' | 'tcp' | 'udp'
const network = (flow: FlowDefinition) => (listenOf(flow).network || 'tcp').toLowerCase()

export default function FlowsPage() {
  const styles = useStyles2(getStyles)
  const addToast = useToast()
  const { flows, loading, reload } = useGoFi()
  const [query, setQuery] = useState('')
  const [protocol, setProtocol] = useState<Protocol>('all')
  const [pendingDelete, setPendingDelete] = useState<string | null>(null)

  const filtered = useMemo(() => flows.filter(flow => {
    if (protocol !== 'all' && network(flow) !== protocol) return false
    const needle = query.trim().toLowerCase()
    if (!needle) return true
    return [flow.name, String(listenOf(flow).port), ...flow.sinks.map(sink => sink.type), flow.deadLetterSink?.type ?? ''].some(value => value.toLowerCase().includes(needle))
  }), [flows, protocol, query])

  const columns = useMemo<Array<Column<FlowDefinition>>>(() => [
    { id: 'name', header: 'Name', sortType: 'string', cell: ({ row }) => <TextLink href={to(paths.flow(row.original.name))} inline={false} weight="medium">{row.original.name}</TextLink> },
    { id: 'source', header: 'Source', cell: ({ row: { original } }) => <span className={styles.mono}>
      {original.source.listen ? `listen ${network(original)}:${listenOf(original).port}` : sourceSummary(original.source)}
      {listenOf(original).service && <span className={styles.secondary}> · {listenOf(original).service!.type}{listenOf(original).service!.nodePort ? ` :${listenOf(original).service!.nodePort}` : ''}</span>}
    </span> },
    { id: 'pipeline', header: 'Pipeline', cell: ({ row: { original } }) => <Stack gap={0.5} alignItems="center" wrap="wrap">
      <Badge text={original.framing.mode} color={FRAMING_COLOR[original.framing.mode] ?? 'darkgrey'} />
      <span className={styles.secondary}>→</span>
      <Badge text={original.parser.format || 'none'} color={PARSER_COLOR[original.parser.format] ?? 'darkgrey'} />
      {original.stages.length > 0 && <><span className={styles.secondary}>→</span><Badge text={`${original.stages.length} stage${original.stages.length === 1 ? '' : 's'}`} color="darkgrey" /></>}
    </Stack> },
    { id: 'destinations', header: 'Destinations', cell: ({ row: { original } }) => <Stack gap={0.5} wrap="wrap">
      {original.sinks.map((sink, index) => <Badge key={index} text={sink.type} color={SINK_COLOR[sink.type] ?? 'darkgrey'} />)}
      {original.deadLetterSink && <Badge text={`dead letter: ${original.deadLetterSink.type}`} color="orange" />}
      {!original.sinks.length && !original.deadLetterSink && <span className={styles.secondary}>—</span>}
    </Stack> },
    { id: 'runtime', header: 'Runtime', cell: ({ row: { original } }) => <RuntimeCell name={original.name} /> },
    { id: 'actions', header: '', disableGrow: true, cell: ({ row: { original } }) => <Stack gap={0.5} justifyContent="flex-end" alignItems="center">
      <LinkButton size="sm" fill="text" variant="secondary" icon="sitemap" tooltip="Flow" aria-label={`Open flow for ${original.name}`} href={paths.flow(original.name)} />
      <LinkButton size="sm" fill="text" variant="secondary" icon="document-info" tooltip="Live traffic" aria-label={`Live traffic for ${original.name}`} href={paths.flow(original.name, 'traffic')} />
      <LinkButton size="sm" fill="text" variant="secondary" icon="pen" tooltip="Edit" aria-label={`Edit ${original.name}`} href={paths.flow(original.name, 'edit')} />
      <IconButton name="trash-alt" variant="destructive" tooltip="Delete" aria-label={`Delete ${original.name}`} onClick={() => setPendingDelete(original.name)} />
    </Stack> },
  ], [styles])

  async function confirmDelete() {
    const name = pendingDelete
    setPendingDelete(null)
    if (!name) return
    try { await deleteFlow(name); await reload(); addToast(`Deleted flow ${name}`, 'success') }
    catch (error) { addToast(String(error)) }
  }

  return <Page title="Flows" icon="exchange-alt"
    subTitle="Each flow receives TCP or UDP traffic on a port, decodes frames, applies processing stages, and routes messages to destinations."
    actions={<LinkButton icon="plus" href={paths.newFlow()}>New flow</LinkButton>}>
    {loading ? <PageLoading text="Loading flows..." />
      : !flows.length ? <EmptyState variant="call-to-action" message="You haven't created any flows yet" button={<LinkButton icon="plus" size="lg" href={paths.newFlow()}>Create flow</LinkButton>}>
        A flow opens a TCP or UDP port and turns incoming frames into routed messages.
      </EmptyState>
      : <>
        <div className={styles.toolbar}>
          <div className={styles.search}><FilterInput value={query} onChange={setQuery} placeholder="Search by name, port, or destination type" aria-label="Search flows" /></div>
          <RadioButtonGroup<Protocol> aria-label="Protocol" value={protocol} onChange={setProtocol} options={[{ label: 'All', value: 'all' }, { label: 'TCP', value: 'tcp' }, { label: 'UDP', value: 'udp' }]} />
        </div>
        {filtered.length
          ? <InteractiveTable columns={columns} data={filtered} getRowId={row => row.name} pageSize={25} initialSortBy={[{ id: 'name', desc: false }]} />
          : <EmptyState variant="not-found" message="No flows match your filters" button={<LinkButton variant="secondary" onClick={() => { setQuery(''); setProtocol('all') }}>Clear filters</LinkButton>} />}
      </>}
    <ConfirmModal isOpen={pendingDelete !== null} title="Delete flow" body={`Delete flow "${pendingDelete}"? Its port closes and traffic stops being routed.`}
      confirmText="Delete" onConfirm={confirmDelete} onDismiss={() => setPendingDelete(null)} />
  </Page>
}

function RuntimeCell({ name }: { name: string }) {
  const styles = useStyles2(getStyles)
  const [runtime, setRuntime] = useState<RuntimeInfo | null | undefined>(undefined)
  useEffect(() => {
    let cancelled = false
    // The runtime may not have started, or this mode may not report one.
    getRuntimeStatus(name).then(info => { if (!cancelled) setRuntime(info) }).catch(() => { if (!cancelled) setRuntime(null) })
    return () => { cancelled = true }
  }, [name])
  if (runtime === undefined) return <span className={styles.secondary}>…</span>
  if (!runtime) return <span className={styles.secondary}>—</span>
  const status = (runtime.status || 'unknown').toLowerCase()
  const color = ['running', 'ready', 'active'].includes(status) ? 'green' : ['error', 'failed', 'crashloopbackoff'].includes(status) ? 'red' : 'darkgrey'
  return <Badge text={`${runtime.kind}: ${runtime.status || 'unknown'}`} color={color} tooltip={runtime.error} />
}

const getStyles = (theme: GrafanaTheme2) => ({
  toolbar: css({ display: 'flex', flexWrap: 'wrap', gap: theme.spacing(1), alignItems: 'center', marginBottom: theme.spacing(2) }),
  search: css({ flex: '1 1 280px', minWidth: 0 }),
  mono: css({ fontFamily: theme.typography.fontFamilyMonospace, fontSize: theme.typography.bodySmall.fontSize }),
  secondary: css({ color: theme.colors.text.secondary }),
})
