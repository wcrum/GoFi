import { lazy, Suspense, useEffect, useState } from 'react'
import { css } from '@emotion/css'
import type { GrafanaTheme2 } from '@grafana/data'
import { EmptyState, LinkButton, PanelChrome, TextLink, useStyles2 } from '@grafana/ui'
import { Page } from '../shell/Page'
import { listenOf, sourceSummary } from '../types'
import { paths, to } from '../router'
import { useGoFi } from '../state'
import { listSandboxes } from '../sandbox-api'
import { StatPanel } from '../components/dashboard/StatPanel'
import { useTrafficObservations } from '../components/dashboard/useTrafficObservations'
import { PageLoading } from '../components/PageLoading'

const FlowTopology = lazy(() => import('../components/canvas/FlowTopology'))
const SESSION = 'Counted from the live traffic stream since this page opened. Not processing history; a reconnect keeps, and does not reset, these counts.'

export default function HomePage() {
  const styles = useStyles2(getStyles)
  const { flows, loading } = useGoFi()
  // One all-flow stream feeds the stat panels, the topology, and the table.
  const traffic = useTrafficObservations(flows.map(flow => flow.name))
  const [libraries, setLibraries] = useState<number | null>(null)
  useEffect(() => { listSandboxes().then(result => setLibraries(result.sandboxes.length)).catch(() => setLibraries(null)) }, [])
  const series = (key: 'observed' | 'dropped' | 'parseErrors') => traffic.history.map(bucket => bucket[key])

  return <Page title="Home" icon="home-alt"
    subTitle="GoFi receives TCP and UDP traffic, decodes frames, filters and transforms messages, and routes them to your applications."
    actions={<><LinkButton icon="compass" variant="secondary" href={paths.explore()}>Explore</LinkButton><LinkButton icon="plus" href={paths.newFlow()}>New flow</LinkButton></>}>
    {loading ? <PageLoading text="Loading flows..." />
      : !flows.length ? <EmptyState variant="call-to-action" message="Build your first live flow" button={<LinkButton icon="plus" size="lg" href={paths.newFlow()}>Create flow</LinkButton>}>
        Receive traffic from a NodePort or socket, decode incoming frames, filter with expressions, and send it to your applications.
      </EmptyState>
      : <div className={styles.grid}>
        <div className={styles.stats}>
        <div className={styles.stat}><StatPanel title="Flows" value={flows.length} description="Configured flow resources." /></div>
        <div className={styles.stat}><StatPanel title="Observed" value={traffic.totals.observed} series={series('observed')} description={SESSION} /></div>
        <div className={styles.stat}><StatPanel title="Rejected" value={traffic.totals.dropped} series={series('dropped')} description={SESSION} /></div>
        <div className={styles.stat}><StatPanel title="Parse errors" value={traffic.totals.parseErrors} series={series('parseErrors')} description={SESSION} /></div>
        <div className={styles.stat}><StatPanel title="Python libraries" value={libraries ?? '—'} description="Saved Python function libraries." /></div>
        </div>
        <div className={styles.topology} style={{ height: topologyHeight(flows.length) }}>
          <PanelChrome title="Traffic topology" description="Configured routes from each flow. Delivery to destinations is not confirmed here." padding="none"
            actions={<span className={styles.streamState}>{traffic.state === 'connected' ? 'Stream connected' : traffic.state === 'connecting' ? 'Connecting…' : 'Reconnecting…'}</span>}>
            <Suspense fallback={<PageLoading text="Loading topology..." />}><FlowTopology flows={flows} observations={traffic} /></Suspense>
          </PanelChrome>
        </div>
        <div className={styles.table}>
          <PanelChrome title="Flows" description={`Observed counts: ${SESSION}`}>
            <div className={styles.scroll}>
              <table className={styles.tableEl}>
                <thead><tr><th>Name</th><th>Ingress</th><th data-num>Stages</th><th>Destinations</th><th>Dead letter</th><th data-num>Observed</th></tr></thead>
                <tbody>{flows.map(flow => <tr key={flow.name}>
                  <td><TextLink href={to(paths.flow(flow.name))} inline={false}>{flow.name}</TextLink></td>
                  <td className={styles.mono}>{flow.source.listen ? `${listenOf(flow).network || 'tcp'}:${listenOf(flow).port}` : sourceSummary(flow.source)}</td>
                  <td data-num>{flow.stages.length}</td>
                  <td>{flow.sinks.map(sink => sink.type).join(', ') || '—'}</td>
                  <td>{flow.deadLetterSink?.type ?? '—'}</td>
                  <td data-num>{(traffic.flows[flow.name]?.observed ?? 0).toLocaleString()}</td>
                </tr>)}</tbody>
              </table>
            </div>
          </PanelChrome>
        </div>
      </div>}
  </Page>
}

const getStyles = (theme: GrafanaTheme2) => ({
  grid: css({ display: 'flex', flexDirection: 'column', gap: theme.spacing(1) }),
  stats: css({ display: 'grid', gap: theme.spacing(1), gridTemplateColumns: 'repeat(auto-fit, minmax(160px, 1fr))' }),
  stat: css({ height: 116 }),
  topology: css({ flexShrink: 0 }),
  table: css({}),
  streamState: css({ fontSize: theme.typography.bodySmall.fontSize, color: theme.colors.text.secondary, paddingRight: theme.spacing(1) }),
  scroll: css({ overflowX: 'auto' }),
  tableEl: css({
    width: '100%', borderCollapse: 'collapse', fontSize: theme.typography.bodySmall.fontSize,
    th: { textAlign: 'left', fontWeight: theme.typography.fontWeightMedium, color: theme.colors.text.secondary, padding: theme.spacing(0.75, 1), borderBottom: `1px solid ${theme.colors.border.weak}`, whiteSpace: 'nowrap' },
    td: { padding: theme.spacing(0.75, 1), borderBottom: `1px solid ${theme.colors.border.weak}`, whiteSpace: 'nowrap' },
    'tbody tr:hover': { background: theme.colors.action.hover },
    '& [data-num]': { textAlign: 'right', fontVariantNumeric: 'tabular-nums' },
  }),
  mono: css({ fontFamily: theme.typography.fontFamilyMonospace }),
})

/** Grows with the flow count so auto-fit keeps nodes legible. */
const topologyHeight = (flows: number) => Math.min(900, Math.max(360, flows * 190 + 110))
