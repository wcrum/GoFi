import { useEffect, useMemo, useState } from 'react'
import { Alert, Badge, ClipboardButton, Column, EmptyState, FilterInput, InteractiveTable, Stack, Text } from '@grafana/ui'
import { getEndpoints, type DiscoveredEndpoint } from '../api'
import { Page } from '../shell/Page'
import { PageLoading } from '../components/PageLoading'

type Row = DiscoveredEndpoint & { id: string }
const columns: Array<Column<Row>> = [
  { id: 'name', header: 'Name', sortType: 'string', cell: ({ row: { original } }) => <Text weight="medium">{original.name}</Text> },
  { id: 'type', header: 'Type', cell: ({ row: { original } }) => <Badge color={original.kind === 'kubernetes' ? 'blue' : 'purple'} text={original.type ?? original.kind} /> },
  { id: 'namespace', header: 'Namespace', sortType: 'string', cell: ({ row: { original } }) => <Text color="secondary">{original.namespace || '—'}</Text> },
  { id: 'ports', header: 'Destination URLs', cell: ({ row: { original } }) => <Stack direction="column" gap={0.5}>{original.ports.map(port => <Stack key={`${port.protocol}-${port.port}`} gap={1} alignItems="center">
    <Text variant="code">{port.url}</Text>{port.nodePort ? <Badge color="darkgrey" text={`NodePort ${port.nodePort}`} /> : null}
    <ClipboardButton size="sm" fill="text" variant="secondary" icon="copy" getText={() => port.url} aria-label={`Copy ${port.url}`} />
  </Stack>)}</Stack> },
  { id: 'external', header: 'External', cell: ({ row: { original } }) => <Text color="secondary">{original.externalAddresses?.join(', ') || '—'}</Text> },
]

export default function EndpointsPage() {
  const [endpoints, setEndpoints] = useState<DiscoveredEndpoint[] | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [query, setQuery] = useState('')
  useEffect(() => { getEndpoints().then(setEndpoints).catch(reason => setError(String(reason))) }, [])
  const rows = useMemo(() => (endpoints ?? [])
    .filter(endpoint => !query || JSON.stringify(endpoint).toLowerCase().includes(query.toLowerCase()))
    .map((endpoint, index) => ({ ...endpoint, id: `${endpoint.namespace ?? ''}/${endpoint.name}/${index}` })), [endpoints, query])
  return <Page title="Endpoints" icon="adjust-circle" subTitle="Containers, Services, and hosts GoFi can discover. Use a destination URL to route traffic directly to an application.">
    {error ? <Alert severity="error" title="Endpoint inventory unavailable">{error}</Alert>
      : !endpoints ? <PageLoading text="Discovering endpoints..." />
      : !endpoints.length ? <EmptyState variant="not-found" message="No discoverable endpoints">Configure a destination URL directly in a flow sink.</EmptyState>
      : <Stack direction="column" gap={2}>
        <FilterInput value={query} onChange={setQuery} placeholder="Search endpoints" />
        <InteractiveTable columns={columns} data={rows} getRowId={row => row.id} pageSize={25} />
      </Stack>}
  </Page>
}
