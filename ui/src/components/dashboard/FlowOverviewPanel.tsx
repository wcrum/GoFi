import { useEffect, useState, type ReactNode } from 'react'
import { css } from '@emotion/css'
import type { GrafanaTheme2, IconName } from '@grafana/data'
import { Badge, Divider, Icon, IconButton, LinkButton, Stack, Text, useStyles2 } from '@grafana/ui'
import { ERROR_CLASS_TEXT, getRouteHealth, getRuntimeStatus, routeLabel, type RouteHealth, type RuntimeInfo } from '../../api'
import { paths } from '../../router'
import { listenOf, sourceSummary, sourceTypeOf, subscribeKindOf, type FlowDefinition, type SinkCfg } from '../../types'
import { codecChain } from '../../lib/codec'
import { pythonRefLabel } from '../../lib/python-ref'
import { FRAMING_COLOR, PARSER_COLOR, SINK_COLOR, STAGE_KIND_COLOR } from '../stageColors'
import { StatusIndicator, type StatusTone } from '../StatusIndicator'
import { formatRate } from '../canvas/traffic'
import { useFlowRates } from '../canvas/useFlowRates'
import { emptyObservation, type TrafficObservations } from './useTrafficObservations'
import { CelCode } from '../cel/CelCode'
import { formatNumber, t } from '../../i18n'

const sourceKinds = (): Record<ReturnType<typeof sourceTypeOf>, { label: string; icon: IconName }> => ({
  listen: { label: t('gofi.dashboard.flow-overview.source-listen', 'Listen'), icon: 'signal' },
  fetch: { label: t('gofi.dashboard.flow-overview.source-fetch', 'Fetch'), icon: 'sync' },
  subscribe: { label: t('gofi.dashboard.flow-overview.source-subscribe', 'Subscribe'), icon: 'rss' },
  python: { label: 'Python', icon: 'brackets-curly' },
})
/** Runtime and route checks only run for the one flow shown here. */
const REFRESH_MS = 10_000

interface Props {
  flow: FlowDefinition
  traffic: TrafficObservations
  /** Omit when a host (a Drawer) already renders the title and close button. */
  onClose?: () => void
}

/** General information about one flow: source, runtime, live counters,
 * pipeline shape, and links to its pages. */
export function FlowOverviewPanel({ flow, traffic, onClose }: Props) {
  const styles = useStyles2(getStyles)
  const name = flow.name
  const observation = traffic.flows[name] ?? emptyObservation()
  const rates = useFlowRates(traffic)
  const { runtime, routes } = useRuntime(name)
  const type = sourceTypeOf(flow.source)
  const source = sourceKinds()[type]
  const stageKinds = Object.entries(flow.stages.reduce<Record<string, number>>((counts, stage) => ({ ...counts, [stage.kind]: (counts[stage.kind] ?? 0) + 1 }), {}))
  const failing = routes?.filter((route) => route.failing) ?? []
  const tests = flow.testCases?.length ?? 0
  const none = <Text color="secondary">{t('gofi.common.none', 'None')}</Text>

  return <div className={styles.root}>
    {onClose && <div className={styles.header}>
      <Icon name={source.icon} className={styles.sourceIcon} />
      <div className={styles.headerText}><Text element="h4" truncate>{name}</Text></div>
      <IconButton name="times" aria-label={t('gofi.dashboard.flow-overview.close-details', 'Close flow details')} tooltip={t('gofi.dashboard.flow-overview.close-tooltip', 'Close (Esc)')} onClick={onClose} />
    </div>}

    <Section title={t('gofi.dashboard.flow-overview.section-source', 'Source')}>
      <Row label={t('gofi.dashboard.flow-overview.row-type', 'Type')}><Stack gap={1} alignItems="center"><Icon name={source.icon} size="sm" />{source.label}{flow.source.subscribe && ` · ${subscribeKindOf(flow.source.subscribe).toUpperCase()}`}</Stack></Row>
      <Row label={type === 'listen' ? t('gofi.dashboard.flow-overview.row-listen', 'Listen') : type === 'fetch' ? t('gofi.dashboard.flow-overview.row-url', 'URL') : t('gofi.dashboard.flow-overview.row-stream', 'Stream')} mono>{type === 'listen' ? `${(listenOf(flow).network || 'tcp').toUpperCase()} :${listenOf(flow).port || '—'}` : sourceSummary(flow.source).replace(/^\S+ /, '')}</Row>
      {type === 'listen' && <Row label={t('gofi.dashboard.flow-overview.row-exposure', 'Exposure')}>{exposure(flow)}</Row>}
    </Section>

    <Section title={t('gofi.dashboard.flow-overview.section-runtime', 'Runtime')}>
      <Row label={t('gofi.dashboard.flow-overview.row-status', 'Status')}>{runtime === undefined ? <Text color="secondary">{t('gofi.dashboard.flow-overview.runtime-checking', 'Checking…')}</Text> : runtime === null ? <Text color="secondary">{t('gofi.dashboard.flow-overview.runtime-not-reported', 'Not reported')}</Text>
        : <StatusIndicator tone={runtimeTone(runtime.status)} text={t('gofi.dashboard.flow-overview.runtime-badge', '{{kind}}: {{status}}', { kind: runtime.kind, status: runtime.status || t('gofi.dashboard.flow-overview.runtime-status-unknown', 'unknown') })} tooltip={runtime.error} />}</Row>
      {runtime?.node && <Row label={t('gofi.dashboard.flow-overview.row-node', 'Node')} mono>{runtime.node}{runtime.ip ? ` · ${runtime.ip}` : ''}</Row>}
      {runtime?.error && <p className={styles.error}>{runtime.error}</p>}
      {routes && routes.length > 0 && <Row label={t('gofi.dashboard.flow-overview.row-routes', 'Routes')}>{failing.length
        ? <StatusIndicator tone="error" text={t('gofi.dashboard.flow-overview.routes-failing', '{{failing}} of {{total}} failing', { failing: failing.length, total: routes.length })} tooltip={failing.map((route) => `${routeLabel(route.route)}: ${route.errorClass ? ERROR_CLASS_TEXT[route.errorClass] ?? route.errorClass : route.lastError ?? t('gofi.dashboard.flow-overview.route-failing', 'failing')}`).join('\n')} />
        : <StatusIndicator tone="success" text={t('gofi.dashboard.flow-overview.routes-healthy', '{{count}} healthy', { count: routes.length })} />}</Row>}
    </Section>

    <Section title={t('gofi.dashboard.flow-overview.section-live-traffic', 'Live traffic')} description={t('gofi.dashboard.flow-overview.live-traffic-description', 'Counted since this page opened.')}>
      <div className={styles.stats}>
        <Stat label={t('gofi.dashboard.flow-overview.stat-observed', 'Observed')} value={formatNumber(observation.observed)} />
        <Stat label={t('gofi.dashboard.flow-overview.stat-rate', 'Rate')} value={formatRate(rates[`${name}:observed`] ?? 0)} />
        <Stat label={t('gofi.dashboard.flow-overview.stat-rejected', 'Rejected')} value={formatNumber(observation.dropped)} tone={observation.dropped ? 'error' : undefined} />
        <Stat label={t('gofi.dashboard.flow-overview.stat-parse-errors', 'Parse errors')} value={formatNumber(observation.parseErrors)} tone={observation.parseErrors ? 'warning' : undefined} />
      </div>
    </Section>

    <Section title={t('gofi.dashboard.flow-overview.section-pipeline', 'Pipeline')}>
      <Row label={t('gofi.dashboard.flow-overview.row-framing', 'Framing')}><Badge text={flow.framing.mode} color={FRAMING_COLOR[flow.framing.mode] ?? 'darkgrey'} /></Row>
      {(flow.decode?.decoders.length ?? 0) > 0 && <Row label={t('gofi.dashboard.flow-overview.row-decoders', 'Decoders')} mono>{codecChain(flow.decode!.decoders, flow.python?.library)}</Row>}
      <Row label={t('gofi.dashboard.flow-overview.row-parser', 'Parser')}>{flow.parser.python ? <span className={styles.mono}>{pythonRefLabel(flow.parser.python, flow.python?.library)}</span> : <Badge text={flow.parser.format || 'raw'} color={PARSER_COLOR[flow.parser.format] ?? 'darkgrey'} />}</Row>
      <Row label={t('gofi.dashboard.flow-overview.row-stages', 'Stages')}>{flow.stages.length ? <Stack gap={0.5} wrap="wrap" alignItems="center"><span>{flow.stages.length}</span>{stageKinds.map(([kind, count]) => <Badge key={kind} text={count > 1 ? `${kind} ×${count}` : kind} color={STAGE_KIND_COLOR[kind] ?? 'darkgrey'} />)}</Stack> : none}</Row>
    </Section>

    <Section title={t('gofi.dashboard.flow-overview.section-destinations', 'Destinations ({{count}})', { count: flow.sinks.length })}>
      {flow.sinks.length ? <ul className={styles.sinks}>{flow.sinks.map((sink, index) => <SinkItem key={index} sink={sink} />)}</ul> : <Text color="secondary">{t('gofi.dashboard.flow-overview.no-destinations', 'No destinations configured.')}</Text>}
      <Row label={t('gofi.dashboard.flow-overview.row-dead-letter', 'Dead letter')}>{flow.deadLetterSink ? <Stack gap={1} alignItems="center"><Badge text={flow.deadLetterSink.type} color={SINK_COLOR[flow.deadLetterSink.type] ?? 'darkgrey'} /><span className={styles.mono}>{sinkTarget(flow.deadLetterSink)}</span></Stack> : none}</Row>
    </Section>

    <Section title={t('gofi.dashboard.flow-overview.section-tests', 'Tests')}>
      <Row label={t('gofi.dashboard.flow-overview.row-test-cases', 'Test cases')}>{tests ? tests : none}</Row>
    </Section>

    <Divider spacing={1} />
    <div className={styles.actions}>
      <LinkButton icon="arrow-right" href={paths.flow(name)}>{t('gofi.dashboard.flow-overview.open-flow', 'Open flow')}</LinkButton>
      <LinkButton icon="exchange-alt" variant="secondary" href={paths.flow(name, 'traffic')}>{t('gofi.dashboard.flow-overview.live-traffic', 'Live traffic')}</LinkButton>
      <LinkButton icon="compass" variant="secondary" href={paths.explore(name)}>{t('gofi.common.explore', 'Explore')}</LinkButton>
      <LinkButton icon="pen" variant="secondary" href={paths.flow(name, 'edit')}>{t('gofi.common.edit', 'Edit')}</LinkButton>
    </div>
  </div>
}

function SinkItem({ sink }: { sink: SinkCfg }) {
  const styles = useStyles2(getStyles)
  const when = sink.when || sink.whenRef?.name
  return <li className={styles.sink}>
    <Stack gap={1} alignItems="center"><Badge text={sink.type} color={SINK_COLOR[sink.type] ?? 'darkgrey'} /><span className={styles.mono} title={sinkTarget(sink)}>{sinkTarget(sink)}</span></Stack>
    <span className={styles.when}>{when ? <>{t('gofi.dashboard.flow-overview.sink-when', 'When')} <CelCode className={styles.mono} expression={when} /></> : t('gofi.dashboard.flow-overview.sink-always', 'Always')}</span>
  </li>
}

function Section({ title, description, children }: { title: string; description?: string; children: ReactNode }) {
  const styles = useStyles2(getStyles)
  return <section className={styles.section}>
    <div className={styles.sectionTitle}><Text element="h6" weight="medium">{title}</Text>{description && <Text variant="bodySmall" color="secondary">{description}</Text>}</div>
    {children}
  </section>
}

function Row({ label, mono, children }: { label: string; mono?: boolean; children: ReactNode }) {
  const styles = useStyles2(getStyles)
  return <div className={styles.row}><span className={styles.label}>{label}</span><span className={mono ? styles.valueMono : styles.value}>{children}</span></div>
}

function Stat({ label, value, tone }: { label: string; value: string; tone?: 'error' | 'warning' }) {
  const styles = useStyles2(getStyles)
  return <div className={styles.stat}><span className={styles.label}>{label}</span><span className={tone === 'error' ? styles.statError : tone === 'warning' ? styles.statWarning : styles.statValue}>{value}</span></div>
}

/** Runtime and route health for one flow, refreshed every REFRESH_MS. */
function useRuntime(name: string) {
  const [runtime, setRuntime] = useState<RuntimeInfo | null | undefined>(undefined)
  const [routes, setRoutes] = useState<RouteHealth[] | null>(null)
  useEffect(() => {
    let cancelled = false
    setRuntime(undefined)
    setRoutes(null)
    const check = () => {
      getRuntimeStatus(name).then((info) => { if (!cancelled) setRuntime(info) }).catch(() => { if (!cancelled) setRuntime(null) })
      getRouteHealth(name).then((result) => { if (!cancelled) setRoutes(result) }).catch(() => { if (!cancelled) setRoutes(null) })
    }
    check()
    const timer = window.setInterval(check, REFRESH_MS)
    return () => { cancelled = true; window.clearInterval(timer) }
  }, [name])
  return { runtime, routes }
}

function runtimeTone(status: string | undefined): StatusTone {
  const value = (status || '').toLowerCase()
  if (['running', 'ready', 'active'].includes(value)) return 'success'
  if (['error', 'failed', 'crashloopbackoff'].includes(value)) return 'error'
  if (['pending', 'starting', 'creating', 'containercreating'].includes(value)) return 'warning'
  return 'neutral'
}

function exposure(flow: FlowDefinition): string {
  const listen = listenOf(flow)
  const service = listen.service
  if (!service) return t('gofi.dashboard.flow-overview.exposure-socket-only', 'Socket only')
  if (service.type === 'NodePort') return t('gofi.dashboard.flow-overview.exposure-node-port', 'NodePort {{nodePort}} → {{port}}', { nodePort: service.nodePort ?? t('gofi.dashboard.flow-overview.exposure-node-port-auto', 'auto'), port: service.port ?? listen.port })
  if (service.type === 'Gateway') return `Gateway ${service.gateway ? `${service.gateway.namespace}/${service.gateway.name}` : ''}`.trim()
  return `${service.type} :${service.port ?? listen.port}`
}

function sinkTarget(sink: SinkCfg): string {
  if (sink.kubernetes) return t('gofi.dashboard.flow-overview.sink-pods', 'Pods {{namespace}} :{{port}}', { namespace: sink.kubernetes.namespace || t('gofi.dashboard.flow-overview.sink-current-namespace', '(current namespace)'), port: sink.kubernetes.port })
  if (sink.python) return pythonRefLabel(sink.python)
  if (sink.aster) return `${sink.aster.scope} / ${sink.aster.topic}`
  if (sink.type === 'stdout') return t('gofi.dashboard.flow-overview.sink-process-output', 'Process output')
  return [sink.url, sink.subject].filter(Boolean).join(' · ') || t('gofi.dashboard.flow-overview.sink-not-configured', 'Not configured')
}

const getStyles = (theme: GrafanaTheme2) => ({
  root: css({ display: 'flex', flexDirection: 'column', gap: theme.spacing(2), padding: theme.spacing(2), minWidth: 0 }),
  header: css({ display: 'flex', alignItems: 'center', gap: theme.spacing(1), minWidth: 0 }),
  headerText: css({ flex: 1, minWidth: 0, h4: { margin: 0 } }),
  sourceIcon: css({ color: theme.colors.primary.text, flexShrink: 0 }),
  section: css({ display: 'flex', flexDirection: 'column', gap: theme.spacing(0.75) }),
  sectionTitle: css({ display: 'flex', alignItems: 'baseline', justifyContent: 'space-between', gap: theme.spacing(1), h6: { margin: 0 } }),
  // The label column is 96px for short labels and grows (up to 45%) for
  // long translations such as «Прослуховування», so they never run into the value.
  row: css({ display: 'grid', gridTemplateColumns: 'fit-content(45%) minmax(0, 1fr)', gap: theme.spacing(1), alignItems: 'center', minHeight: 24, fontSize: theme.typography.bodySmall.fontSize }),
  label: css({ minWidth: 96, color: theme.colors.text.secondary, fontSize: theme.typography.bodySmall.fontSize }),
  value: css({ minWidth: 0, overflowWrap: 'anywhere' }),
  valueMono: css({ minWidth: 0, overflowWrap: 'anywhere', fontFamily: theme.typography.fontFamilyMonospace }),
  mono: css({ fontFamily: theme.typography.fontFamilyMonospace, fontSize: theme.typography.bodySmall.fontSize, minWidth: 0, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }),
  error: css({ margin: 0, fontSize: theme.typography.bodySmall.fontSize, color: theme.colors.error.text, overflowWrap: 'anywhere' }),
  stats: css({ display: 'grid', gridTemplateColumns: 'repeat(2, minmax(0, 1fr))', gap: theme.spacing(1) }),
  stat: css({
    display: 'flex', flexDirection: 'column', gap: theme.spacing(0.25), padding: theme.spacing(1),
    border: `1px solid ${theme.colors.border.weak}`, borderRadius: theme.shape.radius.default, background: theme.colors.background.secondary,
  }),
  statValue: css({ fontSize: theme.typography.h4.fontSize, fontVariantNumeric: 'tabular-nums' }),
  statError: css({ fontSize: theme.typography.h4.fontSize, fontVariantNumeric: 'tabular-nums', color: theme.colors.error.text }),
  statWarning: css({ fontSize: theme.typography.h4.fontSize, fontVariantNumeric: 'tabular-nums', color: theme.colors.warning.text }),
  sinks: css({ listStyle: 'none', margin: 0, padding: 0, display: 'flex', flexDirection: 'column', gap: theme.spacing(0.75) }),
  sink: css({ display: 'flex', flexDirection: 'column', gap: theme.spacing(0.25), minWidth: 0 }),
  when: css({ fontSize: theme.typography.bodySmall.fontSize, color: theme.colors.text.secondary, overflowWrap: 'anywhere' }),
  actions: css({ display: 'flex', flexWrap: 'wrap', gap: theme.spacing(1) }),
})
