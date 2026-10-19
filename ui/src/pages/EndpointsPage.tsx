import { useEffect, useMemo, useState } from 'react'
import { Alert, Badge, ClipboardButton, Column, FilterInput, InteractiveTable, Stack, Text } from '@grafana/ui'
import { EmptyState } from '../components/EmptyState'
import { getEndpoints, type DiscoveredEndpoint } from '../api'
import { Page } from '../shell/Page'
import { PageLoading } from '../components/PageLoading'
import { t } from '../i18n'

type Row = DiscoveredEndpoint & { id: string }
const buildColumns = (): Array<Column<Row>> => [
  { id: 'name', header: t('gofi.pages.endpoints.column-name', 'Name'), sortType: 'string', cell: ({ row: { original } }) => <Text weight="medium">{original.name}</Text> },
  { id: 'type', header: t('gofi.pages.endpoints.column-type', 'Type'), cell: ({ row: { original } }) => <Badge color={original.kind === 'kubernetes' ? 'blue' : 'purple'} text={original.type ?? original.kind} /> },
  { id: 'namespace', header: t('gofi.pages.endpoints.column-namespace', 'Namespace'), sortType: 'string', cell: ({ row: { original } }) => <Text color="secondary">{original.namespace || '—'}</Text> },
  { id: 'ports', header: t('gofi.pages.endpoints.column-destination-urls', 'Destination URLs'), cell: ({ row: { original } }) => <Stack direction="column" gap={0.5}>{original.ports.map(port => <Stack key={`${port.protocol}-${port.port}`} gap={1} alignItems="center">
    <Text variant="code">{port.url}</Text>{port.nodePort ? <Badge color="darkgrey" text={t('gofi.pages.endpoints.node-port', 'NodePort {{port}}', { port: port.nodePort })} /> : null}
    <ClipboardButton size="sm" fill="text" variant="secondary" icon="copy" getText={() => port.url} aria-label={t('gofi.pages.endpoints.copy-url', 'Copy {{url}}', { url: port.url })} />
  </Stack>)}</Stack> },
  { id: 'external', header: t('gofi.pages.endpoints.column-external', 'External'), cell: ({ row: { original } }) => <Text color="secondary">{original.externalAddresses?.join(', ') || '—'}</Text> },
]

export default function EndpointsPage() {
  const [endpoints, setEndpoints] = useState<DiscoveredEndpoint[] | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [query, setQuery] = useState('')
  const columns = useMemo(buildColumns, [])
  useEffect(() => { getEndpoints().then(setEndpoints).catch(reason => setError(String(reason))) }, [])
  const rows = useMemo(() => (endpoints ?? [])
    .filter(endpoint => !query || JSON.stringify(endpoint).toLowerCase().includes(query.toLowerCase()))
    .map((endpoint, index) => ({ ...endpoint, id: `${endpoint.namespace ?? ''}/${endpoint.name}/${index}` })), [endpoints, query])
  return <Page title={t('gofi.pages.endpoints.title', 'Endpoints')} icon="adjust-circle" subTitle={t('gofi.pages.endpoints.subtitle', 'Containers, Services, and hosts GoFi can discover. Use a destination URL to route traffic directly to an application.')}>
    {error ? <Alert severity="error" title={t('gofi.pages.endpoints.inventory-unavailable', 'Endpoint inventory unavailable')}>{error}</Alert>
      : !endpoints ? <PageLoading text={t('gofi.pages.endpoints.discovering', 'Discovering endpoints...')} />
      : !endpoints.length ? <EmptyState variant="not-found" message={t('gofi.pages.endpoints.empty-title', 'No discoverable endpoints')}>{t('gofi.pages.endpoints.empty-body', 'Configure a destination URL directly in a flow sink.')}</EmptyState>
      : <Stack direction="column" gap={2}>
        <FilterInput value={query} onChange={setQuery} placeholder={t('gofi.pages.endpoints.search-placeholder', 'Search endpoints')} />
        <InteractiveTable columns={columns} data={rows} getRowId={row => row.id} pageSize={25} />
      </Stack>}
  </Page>
}
