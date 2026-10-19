import { useEffect, useMemo, useState } from 'react'
import { css, cx } from '@emotion/css'
import type { GrafanaTheme2 } from '@grafana/data'
import { Alert, Badge, CodeEditor, type Column, Combobox, FilterInput, InteractiveTable, RadioButtonGroup, Stack, Text, TextLink, useStyles2 } from '@grafana/ui'
import { EmptyState } from '../components/EmptyState'
import '../lib/monaco'
import { describeCluster, listClusterNetworking, notKubernetes, type ClusterInfo, type ClusterNetworking, type GatewayInfo, type NetworkingObject } from '../kubernetes-api'
import { Page, type PageTab } from '../shell/Page'
import { PageLoading } from '../components/PageLoading'
import { StatusIndicator } from '../components/StatusIndicator'
import { manifestHeight } from '../lib/resource'
import { paths, to, type ClusterTab } from '../router'
import { useGoFi } from '../state'
import { t } from '../i18n'

const ENGINE_COLORS: Record<string, 'blue' | 'purple' | 'green' | 'orange' | 'darkgrey'> = { kubernetes: 'blue', istio: 'purple', 'gateway-api': 'green', uds: 'orange', metallb: 'darkgrey' }
const ENGINE_LABELS: Record<string, string> = { kubernetes: 'Kubernetes', istio: 'Istio', 'gateway-api': 'Gateway API', uds: 'UDS', metallb: 'MetalLB' }

/** Kubernetes mode: what the cluster offers for networking and every object
 * that shapes traffic (NetworkPolicies, Istio, Gateway API, UDS, MetalLB). */
export default function ClusterPage({ tab }: { tab: ClusterTab }) {
  const [info, setInfo] = useState<ClusterInfo | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [notCluster, setNotCluster] = useState(false)
  // Other drivers have no cluster: do not ask (the menu hides this page too).
  const { mode } = useGoFi()
  const kubernetes = mode?.driverKind === 'kubernetes'
  const unavailable = notCluster || (!!mode && !kubernetes)
  useEffect(() => {
    if (!kubernetes) return
    describeCluster().then(setInfo).catch(reason => notKubernetes(reason) ? setNotCluster(true) : setError(String(reason)))
  }, [kubernetes])
  const tabs: PageTab[] = [
    { label: t('gofi.pages.cluster.tab-overview', 'Overview'), href: paths.cluster(), active: tab === 'overview', icon: 'cloud' },
    { label: t('gofi.pages.cluster.tab-networking', 'Networking'), href: paths.cluster('networking'), active: tab === 'networking', icon: 'sitemap' },
  ]
  const subTitle = info
    ? (info.clusterWide
      ? t('gofi.pages.cluster.subtitle-cluster-wide', 'Kubernetes {{version}} · GoFi namespace {{namespace}} · cluster-wide discovery', { version: info.kubernetesVersion ?? '', namespace: info.namespace })
      : t('gofi.pages.cluster.subtitle-namespace-only', 'Kubernetes {{version}} · GoFi namespace {{namespace}} · this namespace only', { version: info.kubernetesVersion ?? '', namespace: info.namespace }))
    : t('gofi.pages.cluster.subtitle', 'Networking capabilities and policies in this cluster.')
  return <Page title={t('gofi.pages.cluster.title', 'Cluster')} icon="cloud" subTitle={subTitle} tabs={unavailable ? undefined : tabs}>
    {unavailable ? <EmptyState variant="not-found" message={t('gofi.pages.cluster.kubernetes-only-title', 'Kubernetes mode only')}>{t('gofi.pages.cluster.kubernetes-only-body', 'GoFi is not running with the Kubernetes driver, so there is no cluster to describe.')}</EmptyState>
      : error ? <Alert severity="error" title={t('gofi.pages.cluster.discovery-failed', 'Cluster discovery failed')}>{error}</Alert>
      : !info ? <PageLoading text={t('gofi.pages.cluster.discovering', 'Discovering cluster...')} />
      : tab === 'networking' ? <NetworkingTab />
      : <OverviewTab info={info} />}
  </Page>
}

function OverviewTab({ info }: { info: ClusterInfo }) {
  const styles = useStyles2(getStyles)
  const gatewayColumns = useMemo(buildGatewayColumns, [])
  const lbColumns = useMemo(buildLbColumns, [])
  const range = { min: info.nodePortMin, max: info.nodePortMax }
  const nodePortSummary = info.manageGatewayListeners
    ? (info.manageUdsAllows
      ? t('gofi.pages.cluster.node-port-summary-listeners-allows', 'NodePort range {{min}}–{{max}}. GoFi may add Gateway listeners and maintains network allows for flows.', range)
      : t('gofi.pages.cluster.node-port-summary-listeners-no-allows', 'NodePort range {{min}}–{{max}}. GoFi may add Gateway listeners and does not maintain network allows for flows.', range))
    : (info.manageUdsAllows
      ? t('gofi.pages.cluster.node-port-summary-no-listeners-allows', 'NodePort range {{min}}–{{max}}. GoFi may not add Gateway listeners and maintains network allows for flows.', range)
      : t('gofi.pages.cluster.node-port-summary-no-listeners-no-allows', 'NodePort range {{min}}–{{max}}. GoFi may not add Gateway listeners and does not maintain network allows for flows.', range))
  return <Stack direction="column" gap={3}>
    {info.warnings.length > 0 && <Alert severity="warning" title={t('gofi.pages.cluster.warnings-title', 'Some cluster details could not be read')} bottomSpacing={0}>{info.warnings.map(w => <div key={w}>{w}</div>)}</Alert>}
    <section className={styles.section}>
      <Text element="h3" variant="h5">{t('gofi.pages.cluster.capabilities', 'Capabilities')}</Text>
      <div className={styles.grid}>
        {[...info.capabilities].sort((a, b) => Number(b.present) - Number(a.present)).map(capability => <div key={capability.id} className={cx(styles.tile, !capability.present && styles.absent)}>
          <div className={styles.tileHeader}>
            <span className={cx(styles.dot, capability.present ? styles.present : styles.missing)} aria-hidden="true" />
            <Text weight="medium">{capability.name}</Text>
            <span className={styles.version}>{capability.present ? (capability.version || t('gofi.pages.cluster.capability-installed', 'installed')) : t('gofi.pages.cluster.capability-not-detected', 'not detected')}</span>
          </div>
          {capability.present && capability.detail && <Text variant="bodySmall" color="secondary">{capability.detail}</Text>}
          {capability.present && !!capability.evidence?.length && <div className={styles.evidence}>{capability.evidence.join(' · ')}</div>}
        </div>)}
      </div>
    </section>
    <section className={styles.section}>
      <Text element="h3" variant="h5">{t('gofi.pages.cluster.gateways', 'Gateways')}</Text>
      {info.gateways.length ? <InteractiveTable columns={gatewayColumns} data={info.gateways.map(gw => ({ ...gw, id: `${gw.namespace}/${gw.name}` }))} getRowId={row => row.id} />
        : <Text color="secondary">{t('gofi.pages.cluster.no-gateways', 'No Gateway API Gateways.')}</Text>}
    </section>
    <section className={styles.section}>
      <Text element="h3" variant="h5">{t('gofi.pages.cluster.load-balancers', 'Load balancers')}</Text>
      <Stack direction="column" gap={1}>
        {info.addressPools.length > 0 && <Text color="secondary">{t('gofi.pages.cluster.address-pools', 'Address pools: {{pools}}', { pools: info.addressPools.map(pool => pool.autoAssign === false
          ? t('gofi.pages.cluster.address-pool-manual', '{{name}} ({{addresses}}, manual)', { name: pool.name, addresses: (pool.addresses ?? []).join(', ') })
          : t('gofi.pages.cluster.address-pool', '{{name}} ({{addresses}})', { name: pool.name, addresses: (pool.addresses ?? []).join(', ') })).join(' · ') })}</Text>}
        {info.loadBalancers.length ? <InteractiveTable columns={lbColumns} data={info.loadBalancers.map(lb => ({ ...lb, id: `${lb.namespace}/${lb.name}` }))} getRowId={row => row.id} />
          : <Text color="secondary">{t('gofi.pages.cluster.no-load-balancers', 'No LoadBalancer Services.')}</Text>}
        <Text variant="bodySmall" color="secondary">{nodePortSummary}</Text>
      </Stack>
    </section>
  </Stack>
}

type GatewayRow = GatewayInfo & { id: string }
const buildGatewayColumns = (): Array<Column<GatewayRow>> => [
  { id: 'name', header: t('gofi.pages.cluster.column-gateway', 'Gateway'), cell: ({ row: { original } }) => <Stack direction="column" gap={0}><Text weight="medium">{original.name}</Text><Text variant="bodySmall" color="secondary">{original.namespace}</Text></Stack> },
  { id: 'impl', header: t('gofi.pages.cluster.column-implementation', 'Implementation'), cell: ({ row: { original } }) => <Stack gap={1} alignItems="center"><Text>{original.implementation || original.className || '—'}</Text>{original.waypoint && <Badge color="purple" text={t('gofi.pages.cluster.waypoint', 'waypoint')} />}</Stack> },
  { id: 'state', header: t('gofi.pages.cluster.column-state', 'State'), cell: ({ row: { original } }) => <StatusIndicator tone={original.programmed ? 'success' : 'warning'} text={original.programmed ? t('gofi.pages.cluster.programmed', 'Programmed') : t('gofi.pages.cluster.not-programmed', 'Not programmed')} /> },
  { id: 'addresses', header: t('gofi.pages.cluster.column-addresses', 'Addresses'), cell: ({ row: { original } }) => <Text variant="code">{original.addresses?.join(', ') || '—'}</Text> },
  { id: 'listeners', header: t('gofi.pages.cluster.column-listeners', 'Listeners'), cell: ({ row: { original } }) => <Stack direction="column" gap={0.5}>{(original.listeners ?? []).map(l => <Stack key={l.name} gap={1} alignItems="center">
    <Text variant="code">{l.protocol} {l.port}</Text><Text color="secondary">{t('gofi.pages.cluster.listener-summary', '{{name}} · routes from {{allowedRoutes}} · {{count}} attached', { name: l.name, allowedRoutes: l.allowedRoutes, count: l.attachedRoutes ?? 0 })}</Text>{l.managedByGofi && <Badge color="green" text="GoFi" />}
  </Stack>)}</Stack> },
  { id: 'routes', header: t('gofi.pages.cluster.column-route-kinds', 'Route kinds'), cell: ({ row: { original } }) => <Text color="secondary">{original.routeKinds?.join(', ') || '—'}</Text> },
]
type LBRow = ClusterInfo['loadBalancers'][number] & { id: string }
const buildLbColumns = (): Array<Column<LBRow>> => [
  { id: 'name', header: t('gofi.pages.cluster.column-service', 'Service'), cell: ({ row: { original } }) => <Stack direction="column" gap={0}><Text weight="medium">{original.name}</Text><Text variant="bodySmall" color="secondary">{original.namespace}</Text></Stack> },
  { id: 'class', header: t('gofi.pages.cluster.column-class', 'Class'), cell: ({ row: { original } }) => <Text color="secondary">{original.className || t('gofi.pages.cluster.class-default', 'default')}</Text> },
  { id: 'addresses', header: t('gofi.pages.cluster.column-addresses', 'Addresses'), cell: ({ row: { original } }) => <Text variant="code">{original.addresses?.join(', ') || t('gofi.pages.cluster.addresses-pending', 'pending')}</Text> },
  { id: 'ports', header: t('gofi.pages.cluster.column-ports', 'Ports'), cell: ({ row: { original } }) => <Text variant="code">{original.ports?.join(', ') || '—'}</Text> },
]

type ObjectRow = NetworkingObject & { id: string }
function NetworkingTab() {
  const styles = useStyles2(getStyles)
  const [data, setData] = useState<ClusterNetworking | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [namespace, setNamespace] = useState('')
  const [engine, setEngine] = useState('')
  const [query, setQuery] = useState('')
  useEffect(() => { listClusterNetworking().then(setData).catch(reason => setError(String(reason))) }, [])
  const rows = useMemo(() => (data?.objects ?? [])
    .filter(o => (!namespace || o.namespace === namespace) && (!engine || o.engine === engine) && (!query || JSON.stringify([o.kind, o.name, o.summary, o.details, o.affectedFlows]).toLowerCase().includes(query.toLowerCase())))
    .map(o => ({ ...o, id: `${o.apiVersion}/${o.kind}/${o.namespace}/${o.name}` })), [data, namespace, engine, query])
  if (error) return <Alert severity="error" title={t('gofi.pages.cluster.networking-unavailable', 'Networking inventory unavailable')}>{error}</Alert>
  if (!data) return <PageLoading text={t('gofi.pages.cluster.reading-networking', 'Reading networking objects...')} />
  const columns: Array<Column<ObjectRow>> = [
    { id: 'kind', header: t('gofi.pages.cluster.column-kind', 'Kind'), cell: ({ row: { original } }) => <Stack direction="column" gap={0.5}><Text weight="medium">{original.kind}</Text><Badge color={ENGINE_COLORS[original.engine] ?? 'darkgrey'} text={ENGINE_LABELS[original.engine] ?? original.engine} /></Stack> },
    { id: 'name', header: t('gofi.pages.cluster.column-name', 'Name'), cell: ({ row: { original } }) => <Stack direction="column" gap={0}><Text>{original.name}</Text><Text variant="bodySmall" color="secondary">{original.namespace || t('gofi.pages.cluster.scope-cluster', 'cluster')}</Text></Stack> },
    { id: 'summary', header: t('gofi.pages.cluster.column-summary', 'Summary'), cell: ({ row: { original } }) => <Text color="secondary">{original.summary}</Text> },
    { id: 'flows', header: t('gofi.pages.cluster.column-affects-flows', 'Affects flows'), cell: ({ row: { original } }) => original.affectedFlows.length ? <Stack gap={0.5} wrap="wrap">{original.affectedFlows.map(flow => <TextLink key={flow} href={to(paths.flow(flow))} inline={false}>{flow}</TextLink>)}</Stack> : <Text color="secondary">—</Text> },
  ]
  const engines = Array.from(new Set(data.objects.map(o => o.engine)))
  return <Stack direction="column" gap={2}>
    {data.warnings.length > 0 && <Alert severity="warning" title={t('gofi.pages.cluster.objects-warnings-title', 'Some objects could not be read')} bottomSpacing={0}>{data.warnings.map(w => <div key={w}>{w}</div>)}</Alert>}
    <div className={styles.toolbar}>
      <div className={styles.search}><FilterInput value={query} onChange={setQuery} placeholder={t('gofi.pages.cluster.networking-search-placeholder', 'Search kinds, names, rules, flows')} /></div>
      <div className={styles.namespace}><Combobox value={namespace} onChange={option => setNamespace(option?.value ?? '')} options={[{ value: '', label: t('gofi.pages.cluster.all-namespaces', 'All namespaces') }, ...data.namespaces.map(ns => ({ value: ns, label: ns }))]} /></div>
      <RadioButtonGroup value={engine} onChange={setEngine} options={[{ value: '', label: t('gofi.common.all', 'All') }, ...engines.map(e => ({ value: e, label: ENGINE_LABELS[e] ?? e }))]} />
    </div>
    {rows.length ? <InteractiveTable columns={columns} data={rows} getRowId={row => row.id} pageSize={50}
      renderExpandedRow={row => <Stack direction="column" gap={1}>
        {row.details.length > 0 && <ul className={styles.details}>{row.details.map((detail, index) => <li key={index}><Text variant="code">{detail}</Text></li>)}</ul>}
        <CodeEditor language="yaml" value={row.yaml} readOnly showLineNumbers showMiniMap={false} height={manifestHeight(row.yaml)} width="100%" monacoOptions={{ scrollBeyondLastLine: false }} />
      </Stack>} />
      : <EmptyState variant="not-found" message={t('gofi.pages.cluster.no-matching-objects', 'No matching networking objects')} />}
  </Stack>
}

const getStyles = (theme: GrafanaTheme2) => ({
  section: css({ display: 'flex', flexDirection: 'column', gap: theme.spacing(1) }),
  grid: css({ display: 'grid', gridTemplateColumns: 'repeat(auto-fill, minmax(300px, 1fr))', gap: theme.spacing(1) }),
  tile: css({ display: 'flex', flexDirection: 'column', gap: theme.spacing(0.5), padding: theme.spacing(1.5, 2), background: theme.colors.background.secondary, border: `1px solid ${theme.colors.border.weak}`, borderRadius: theme.shape.radius.default }),
  absent: css({ background: 'transparent', padding: theme.spacing(1, 2), opacity: 0.75 }),
  tileHeader: css({ display: 'flex', alignItems: 'center', gap: theme.spacing(1) }),
  dot: css({ width: 8, height: 8, borderRadius: theme.shape.radius.circle, flexShrink: 0 }),
  present: css({ background: theme.colors.success.main }),
  missing: css({ background: theme.colors.text.disabled }),
  version: css({ marginLeft: 'auto', fontFamily: theme.typography.fontFamilyMonospace, fontSize: theme.typography.bodySmall.fontSize, color: theme.colors.text.secondary }),
  evidence: css({ fontSize: theme.typography.bodySmall.fontSize, color: theme.colors.text.disabled }),
  toolbar: css({ display: 'flex', flexWrap: 'wrap', gap: theme.spacing(1), alignItems: 'center' }),
  search: css({ flex: '1 1 280px', minWidth: 0 }),
  namespace: css({ width: 240 }),
  details: css({ margin: 0, paddingLeft: theme.spacing(2), display: 'flex', flexDirection: 'column', gap: theme.spacing(0.5) }),
})
