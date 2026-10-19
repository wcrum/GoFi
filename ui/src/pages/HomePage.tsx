import { lazy, Suspense, useCallback, useEffect, useState } from 'react'
import { css } from '@emotion/css'
import type { GrafanaTheme2 } from '@grafana/data'
import { Drawer, LinkButton, PanelChrome, TextLink, useStyles2, useTheme2 } from '@grafana/ui'
import { useLocation } from 'react-router-dom-v5-compat'
import { EmptyState } from '../components/EmptyState'
import { Page } from '../shell/Page'
import { listenOf, sourceSummary } from '../types'
import { navigate, paths, to } from '../router'
import { useGoFi } from '../state'
import { listSandboxes } from '../sandbox-api'
import { StatPanel } from '../components/dashboard/StatPanel'
import { useTrafficObservations } from '../components/dashboard/useTrafficObservations'
import { PageLoading } from '../components/PageLoading'
import { FlowOverviewPanel } from '../components/dashboard/FlowOverviewPanel'
import { useMediaQuery } from '../components/canvas/useFlowRates'
import { formatNumber, t } from '../i18n'

const FlowTopology = lazy(() => import('../components/canvas/FlowTopology'))
const sessionNote = () => t('gofi.pages.home.session-note', 'Runtime counters since each flow started, refreshed once a second. The sparkline shows the last 60 seconds of this session; a runtime restart resets its counters.')

export default function HomePage() {
  const styles = useStyles2(getStyles)
  const { flows, loading } = useGoFi()
  const SESSION = sessionNote()
  // One all-flow stream feeds the stat panels, the topology, and the table.
  const traffic = useTrafficObservations(flows.map(flow => flow.name))
  const [libraries, setLibraries] = useState<number | null>(null)
  useEffect(() => { listSandboxes().then(result => setLibraries(result.sandboxes.filter(library => !library.managedBy?.startsWith('flow:')).length)).catch(() => setLibraries(null)) }, [])
  const series = (key: 'observed' | 'dropped' | 'parseErrors') => traffic.history.map(bucket => bucket[key])
  // The focused flow lives in the URL (#/?flow=<name>) so it is linkable.
  const requested = new URLSearchParams(useLocation().search).get('flow')
  const selectedFlow = flows.find(flow => flow.name === requested) ?? null
  const select = useCallback((name: string | null) => navigate(name ? `#/?flow=${encodeURIComponent(name)}` : paths.home(), { replace: true }), [])
  const wide = useMediaQuery(`(min-width: ${useTheme2().breakpoints.values.lg}px)`)
  useEffect(() => {
    if (!selectedFlow) return
    const onKey = (event: KeyboardEvent) => {
      if (event.key !== 'Escape' || event.defaultPrevented) return
      const target = event.target as HTMLElement | null
      if (target?.closest('input, textarea, select, [contenteditable="true"], [role="dialog"]')) return
      select(null)
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [selectedFlow, select])

  return <Page title={t('gofi.pages.home.title', 'Home')} icon="home-alt"
    subTitle={t('gofi.pages.home.subtitle', 'GoFi receives TCP and UDP traffic, decodes frames, filters and transforms messages, and routes them to your applications.')}
    actions={<><LinkButton icon="compass" variant="secondary" href={paths.explore()}>{t('gofi.common.explore', 'Explore')}</LinkButton><LinkButton icon="plus" href={paths.newFlow()}>{t('gofi.common.new-flow', 'New flow')}</LinkButton></>}>
    {loading ? <PageLoading text={t('gofi.pages.home.loading-flows', 'Loading flows...')} />
      : !flows.length ? <EmptyState variant="call-to-action" message={t('gofi.pages.home.empty-title', 'Build your first live flow')} button={<LinkButton icon="plus" size="lg" href={paths.newFlow()}>{t('gofi.common.create-flow', 'Create flow')}</LinkButton>}>
        {t('gofi.pages.home.empty-body', 'Receive traffic from a NodePort or socket, decode incoming frames, filter with expressions, and send it to your applications.')}
      </EmptyState>
      : <div className={styles.grid}>
        <div className={styles.stats}>
        <div className={styles.stat}><StatPanel title={t('gofi.pages.home.stat-flows', 'Flows')} value={flows.length} description={t('gofi.pages.home.stat-flows-description', 'Configured flow resources.')} /></div>
        <div className={styles.stat}><StatPanel title={t('gofi.pages.home.stat-observed', 'Observed')} value={traffic.totals.observed} series={series('observed')} description={SESSION} /></div>
        <div className={styles.stat}><StatPanel title={t('gofi.pages.home.stat-rejected', 'Rejected')} value={traffic.totals.dropped} series={series('dropped')} description={SESSION} /></div>
        <div className={styles.stat}><StatPanel title={t('gofi.pages.home.stat-parse-errors', 'Parse errors')} value={traffic.totals.parseErrors} series={series('parseErrors')} description={SESSION} /></div>
        <div className={styles.stat}><StatPanel title={t('gofi.pages.home.stat-functions', 'Functions')} value={libraries ?? '—'} description={t('gofi.pages.home.stat-python-libraries-description', 'Saved Python function libraries.')} /></div>
        </div>
        <div className={styles.topologyRow} style={{ height: topologyHeight(flows.length) }}>
        <div className={styles.topology}>
          <PanelChrome title={t('gofi.pages.home.topology-title', 'Traffic topology')} description={t('gofi.pages.home.topology-description', 'Configured routes from each flow. Delivery to destinations is not confirmed here.')} padding="none"
            actions={<span className={styles.streamState}>{traffic.state === 'connected' ? t('gofi.pages.home.stream-connected', 'Stream connected') : traffic.state === 'connecting' ? t('gofi.pages.home.stream-connecting', 'Connecting…') : t('gofi.pages.home.stream-reconnecting', 'Reconnecting…')}</span>}>
            <Suspense fallback={<PageLoading text={t('gofi.pages.home.loading-topology', 'Loading topology...')} />}><FlowTopology flows={flows} observations={traffic} selected={selectedFlow?.name ?? null} onSelect={select} onSelectFlow={select} /></Suspense>
          </PanelChrome>
        </div>
        {selectedFlow && wide && <aside className={styles.sidebar} aria-label={t('gofi.pages.home.flow-sidebar-label', 'Flow {{name}}', { name: selectedFlow.name })}>
          <FlowOverviewPanel key={selectedFlow.name} flow={selectedFlow} traffic={traffic} onClose={() => select(null)} />
        </aside>}
        </div>
        {selectedFlow && !wide && <Drawer title={selectedFlow.name} size="sm" onClose={() => select(null)}>
          <FlowOverviewPanel key={selectedFlow.name} flow={selectedFlow} traffic={traffic} />
        </Drawer>}
        <div className={styles.table}>
          <PanelChrome title={t('gofi.pages.home.flows-table-title', 'Flows')} description={t('gofi.pages.home.flows-table-description', 'Observed counts: {{note}}', { note: SESSION })}>
            <div className={styles.scroll}>
              <table className={styles.tableEl}>
                <thead><tr><th>{t('gofi.pages.home.column-name', 'Name')}</th><th>{t('gofi.pages.home.column-ingress', 'Ingress')}</th><th data-num>{t('gofi.pages.home.column-stages', 'Stages')}</th><th>{t('gofi.pages.home.column-destinations', 'Destinations')}</th><th>{t('gofi.pages.home.column-dead-letter', 'Dead letter')}</th><th data-num>{t('gofi.pages.home.column-observed', 'Observed')}</th></tr></thead>
                <tbody>{flows.map(flow => <tr key={flow.name}>
                  <td><TextLink href={to(paths.flow(flow.name))} inline={false}>{flow.name}</TextLink></td>
                  <td className={styles.mono}>{flow.source.listen ? `${listenOf(flow).network || 'tcp'}:${listenOf(flow).port}` : sourceSummary(flow.source)}</td>
                  <td data-num>{flow.stages.length}</td>
                  <td>{flow.sinks.map(sink => sink.type).join(', ') || '—'}</td>
                  <td>{flow.deadLetterSink?.type ?? '—'}</td>
                  <td data-num>{formatNumber(traffic.flows[flow.name]?.observed ?? 0)}</td>
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
  // PanelChrome titles cannot wrap, so a tile must be wide enough for the
  // longest stat title («Помилки розбору» plus its info icon): 186px, which
  // stacks the tiles in one column on phones and keeps five across on desktop.
  stats: css({ display: 'grid', gap: theme.spacing(1), gridTemplateColumns: 'repeat(auto-fit, minmax(186px, 1fr))' }),
  stat: css({ height: 116 }),
  topologyRow: css({ display: 'flex', gap: theme.spacing(1), flexShrink: 0, minHeight: 0 }),
  topology: css({ flex: 1, minWidth: 0 }),
  sidebar: css({
    width: 380, flexShrink: 0, overflowY: 'auto', background: theme.colors.background.primary,
    border: `1px solid ${theme.colors.border.weak}`, borderRadius: theme.shape.radius.default,
    [theme.transitions.handleMotion('no-preference')]: { animation: 'gofi-sidebar-in 180ms ease-out' },
    '@keyframes gofi-sidebar-in': { from: { opacity: 0, transform: 'translateX(8px)' }, to: { opacity: 1, transform: 'none' } },
  }),
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
