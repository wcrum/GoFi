import { useEffect, useMemo, useState } from 'react'
import { css, cx } from '@emotion/css'
import type { GrafanaTheme2 } from '@grafana/data'
import { Alert, Badge, CodeEditor, type Column, Combobox, EmptyState, FilterInput, InteractiveTable, RadioButtonGroup, Stack, Text, TextLink, useStyles2 } from '@grafana/ui'
import '../lib/monaco'
import { describeCluster, listClusterNetworking, notKubernetes, type ClusterInfo, type ClusterNetworking, type GatewayInfo, type NetworkingObject } from '../kubernetes-api'
import { Page, type PageTab } from '../shell/Page'
import { PageLoading } from '../components/PageLoading'
import { StatusIndicator } from '../components/StatusIndicator'
import { manifestHeight } from '../lib/resource'
import { paths, to, type ClusterTab } from '../router'

const ENGINE_COLORS: Record<string, 'blue' | 'purple' | 'green' | 'orange' | 'darkgrey'> = { kubernetes: 'blue', istio: 'purple', 'gateway-api': 'green', uds: 'orange', metallb: 'darkgrey' }
const ENGINE_LABELS: Record<string, string> = { kubernetes: 'Kubernetes', istio: 'Istio', 'gateway-api': 'Gateway API', uds: 'UDS', metallb: 'MetalLB' }

/** Kubernetes mode: what the cluster offers for networking and every object
 * that shapes traffic (NetworkPolicies, Istio, Gateway API, UDS, MetalLB). */
export default function ClusterPage({ tab }: { tab: ClusterTab }) {
  const [info, setInfo] = useState<ClusterInfo | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [unavailable, setUnavailable] = useState(false)
  useEffect(() => { describeCluster().then(setInfo).catch(reason => notKubernetes(reason) ? setUnavailable(true) : setError(String(reason))) }, [])
  const tabs: PageTab[] = [
    { label: 'Overview', href: paths.cluster(), active: tab === 'overview', icon: 'cloud' },
    { label: 'Networking', href: paths.cluster('networking'), active: tab === 'networking', icon: 'sitemap' },
  ]
  const subTitle = info ? `Kubernetes ${info.kubernetesVersion ?? ''} · GoFi namespace ${info.namespace}${info.clusterWide ? ' · cluster-wide discovery' : ' · this namespace only'}` : 'Networking capabilities and policies in this cluster.'
  return <Page title="Cluster" icon="cloud" subTitle={subTitle} tabs={unavailable ? undefined : tabs}>
    {unavailable ? <EmptyState variant="not-found" message="Kubernetes mode only">GoFi is not running with the Kubernetes driver, so there is no cluster to describe.</EmptyState>
      : error ? <Alert severity="error" title="Cluster discovery failed">{error}</Alert>
      : !info ? <PageLoading text="Discovering cluster..." />
      : tab === 'networking' ? <NetworkingTab />
      : <OverviewTab info={info} />}
  </Page>
}

function OverviewTab({ info }: { info: ClusterInfo }) {
  const styles = useStyles2(getStyles)
  return <Stack direction="column" gap={3}>
    {info.warnings.length > 0 && <Alert severity="warning" title="Some cluster details could not be read" bottomSpacing={0}>{info.warnings.map(w => <div key={w}>{w}</div>)}</Alert>}
    <section className={styles.section}>
      <Text element="h3" variant="h5">Capabilities</Text>
      <div className={styles.grid}>
        {[...info.capabilities].sort((a, b) => Number(b.present) - Number(a.present)).map(capability => <div key={capability.id} className={cx(styles.tile, !capability.present && styles.absent)}>
          <div className={styles.tileHeader}>
            <span className={cx(styles.dot, capability.present ? styles.present : styles.missing)} aria-hidden="true" />
            <Text weight="medium">{capability.name}</Text>
            <span className={styles.version}>{capability.present ? (capability.version || 'installed') : 'not detected'}</span>
          </div>
          {capability.present && capability.detail && <Text variant="bodySmall" color="secondary">{capability.detail}</Text>}
          {capability.present && !!capability.evidence?.length && <div className={styles.evidence}>{capability.evidence.join(' · ')}</div>}
        </div>)}
      </div>
    </section>
    <section className={styles.section}>
      <Text element="h3" variant="h5">Gateways</Text>
      {info.gateways.length ? <InteractiveTable columns={gatewayColumns} data={info.gateways.map(gw => ({ ...gw, id: `${gw.namespace}/${gw.name}` }))} getRowId={row => row.id} />
        : <Text color="secondary">No Gateway API Gateways.</Text>}
    </section>
    <section className={styles.section}>
      <Text element="h3" variant="h5">Load balancers</Text>
      <Stack direction="column" gap={1}>
        {info.addressPools.length > 0 && <Text color="secondary">Address pools: {info.addressPools.map(pool => `${pool.name} (${(pool.addresses ?? []).join(', ')}${pool.autoAssign === false ? ', manual' : ''})`).join(' · ')}</Text>}
        {info.loadBalancers.length ? <InteractiveTable columns={lbColumns} data={info.loadBalancers.map(lb => ({ ...lb, id: `${lb.namespace}/${lb.name}` }))} getRowId={row => row.id} />
          : <Text color="secondary">No LoadBalancer Services.</Text>}
        <Text variant="bodySmall" color="secondary">NodePort range {info.nodePortMin}–{info.nodePortMax}. GoFi {info.manageGatewayListeners ? 'may' : 'may not'} add Gateway listeners and {info.manageUdsAllows ? 'maintains' : 'does not maintain'} UDS network allows for flows.</Text>
      </Stack>
    </section>
  </Stack>
}

type GatewayRow = GatewayInfo & { id: string }
const gatewayColumns: Array<Column<GatewayRow>> = [
  { id: 'name', header: 'Gateway', cell: ({ row: { original } }) => <Stack direction="column" gap={0}><Text weight="medium">{original.name}</Text><Text variant="bodySmall" color="secondary">{original.namespace}</Text></Stack> },
  { id: 'impl', header: 'Implementation', cell: ({ row: { original } }) => <Stack gap={1} alignItems="center"><Text>{original.implementation || original.className || '—'}</Text>{original.waypoint && <Badge color="purple" text="waypoint" />}</Stack> },
  { id: 'state', header: 'State', cell: ({ row: { original } }) => <StatusIndicator tone={original.programmed ? 'success' : 'warning'} text={original.programmed ? 'Programmed' : 'Not programmed'} /> },
  { id: 'addresses', header: 'Addresses', cell: ({ row: { original } }) => <Text variant="code">{original.addresses?.join(', ') || '—'}</Text> },
  { id: 'listeners', header: 'Listeners', cell: ({ row: { original } }) => <Stack direction="column" gap={0.5}>{(original.listeners ?? []).map(l => <Stack key={l.name} gap={1} alignItems="center">
    <Text variant="code">{l.protocol} {l.port}</Text><Text color="secondary">{l.name} · routes from {l.allowedRoutes} · {l.attachedRoutes ?? 0} attached</Text>{l.managedByGofi && <Badge color="green" text="GoFi" />}
  </Stack>)}</Stack> },
  { id: 'routes', header: 'Route kinds', cell: ({ row: { original } }) => <Text color="secondary">{original.routeKinds?.join(', ') || '—'}</Text> },
]
type LBRow = ClusterInfo['loadBalancers'][number] & { id: string }
const lbColumns: Array<Column<LBRow>> = [
  { id: 'name', header: 'Service', cell: ({ row: { original } }) => <Stack direction="column" gap={0}><Text weight="medium">{original.name}</Text><Text variant="bodySmall" color="secondary">{original.namespace}</Text></Stack> },
  { id: 'class', header: 'Class', cell: ({ row: { original } }) => <Text color="secondary">{original.className || 'default'}</Text> },
  { id: 'addresses', header: 'Addresses', cell: ({ row: { original } }) => <Text variant="code">{original.addresses?.join(', ') || 'pending'}</Text> },
  { id: 'ports', header: 'Ports', cell: ({ row: { original } }) => <Text variant="code">{original.ports?.join(', ') || '—'}</Text> },
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
  if (error) return <Alert severity="error" title="Networking inventory unavailable">{error}</Alert>
  if (!data) return <PageLoading text="Reading networking objects..." />
  const columns: Array<Column<ObjectRow>> = [
    { id: 'kind', header: 'Kind', cell: ({ row: { original } }) => <Stack direction="column" gap={0.5}><Text weight="medium">{original.kind}</Text><Badge color={ENGINE_COLORS[original.engine] ?? 'darkgrey'} text={ENGINE_LABELS[original.engine] ?? original.engine} /></Stack> },
    { id: 'name', header: 'Name', cell: ({ row: { original } }) => <Stack direction="column" gap={0}><Text>{original.name}</Text><Text variant="bodySmall" color="secondary">{original.namespace || 'cluster'}</Text></Stack> },
    { id: 'summary', header: 'Summary', cell: ({ row: { original } }) => <Text color="secondary">{original.summary}</Text> },
    { id: 'flows', header: 'Affects flows', cell: ({ row: { original } }) => original.affectedFlows.length ? <Stack gap={0.5} wrap="wrap">{original.affectedFlows.map(flow => <TextLink key={flow} href={to(paths.flow(flow))} inline={false}>{flow}</TextLink>)}</Stack> : <Text color="secondary">—</Text> },
  ]
  const engines = Array.from(new Set(data.objects.map(o => o.engine)))
  return <Stack direction="column" gap={2}>
    {data.warnings.length > 0 && <Alert severity="warning" title="Some objects could not be read" bottomSpacing={0}>{data.warnings.map(w => <div key={w}>{w}</div>)}</Alert>}
    <div className={styles.toolbar}>
      <div className={styles.search}><FilterInput value={query} onChange={setQuery} placeholder="Search kinds, names, rules, flows" /></div>
      <div className={styles.namespace}><Combobox value={namespace} onChange={option => setNamespace(option?.value ?? '')} options={[{ value: '', label: 'All namespaces' }, ...data.namespaces.map(ns => ({ value: ns, label: ns }))]} /></div>
      <RadioButtonGroup value={engine} onChange={setEngine} options={[{ value: '', label: 'All' }, ...engines.map(e => ({ value: e, label: ENGINE_LABELS[e] ?? e }))]} />
    </div>
    {rows.length ? <InteractiveTable columns={columns} data={rows} getRowId={row => row.id} pageSize={50}
      renderExpandedRow={row => <Stack direction="column" gap={1}>
        {row.details.length > 0 && <ul className={styles.details}>{row.details.map((detail, index) => <li key={index}><Text variant="code">{detail}</Text></li>)}</ul>}
        <CodeEditor language="yaml" value={row.yaml} readOnly showLineNumbers showMiniMap={false} height={manifestHeight(row.yaml)} width="100%" monacoOptions={{ scrollBeyondLastLine: false }} />
      </Stack>} />
      : <EmptyState variant="not-found" message="No matching networking objects" />}
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
