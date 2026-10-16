import { useEffect, useState } from 'react'
import { Alert, Badge, type Column, InteractiveTable, PanelChrome, Stack, Text } from '@grafana/ui'
import { describePythonEnvironment, isUnimplemented, type PythonEnvironment } from '../../sandbox-api'
import { PageLoading } from '../PageLoading'

type PackageRow = PythonEnvironment['packages'][number] & { id: string }
type DecoratorRow = PythonEnvironment['decorators'][number] & { id: string }
const packageColumns: Array<Column<PackageRow>> = [
  { id: 'name', header: 'Package', sortType: 'string', cell: ({ row: { original } }) => <Text variant="code">{original.name}</Text> },
  { id: 'version', header: 'Version', cell: ({ row: { original } }) => <Text color="secondary">{original.version}</Text> },
  { id: 'summary', header: 'Summary', cell: ({ row: { original } }) => <Text color="secondary">{original.summary || '—'}</Text> },
]
const decoratorColumns: Array<Column<DecoratorRow>> = [
  { id: 'signature', header: 'Decorator', cell: ({ row: { original } }) => <Text variant="code">{original.signature || original.name}</Text> },
  { id: 'description', header: 'Description', cell: ({ row: { original } }) => <Text color="secondary">{original.description || '—'}</Text> },
]

/** What library code can use: interpreter, packages, SDK decorators, limits. */
export function EnvironmentTab() {
  const [environment, setEnvironment] = useState<PythonEnvironment | null>(null)
  const [error, setError] = useState('')
  useEffect(() => { describePythonEnvironment().then(setEnvironment).catch(reason => setError(isUnimplemented(reason) ? 'unimplemented' : String(reason))) }, [])
  if (error === 'unimplemented') return <Alert severity="info" title="Environment details unavailable" bottomSpacing={0}>This GoFi controller does not report its Python environment yet. Workers run the Python standard library with the GoFi SDK and no outbound network.</Alert>
  if (error) return <Alert severity="error" title="Environment unavailable" bottomSpacing={0}>{error}</Alert>
  if (!environment) return <PageLoading text="Inspecting the Python worker..." />
  return <Stack direction="column" gap={2}>
    <Stack gap={1} wrap="wrap">
      <Badge color="blue" icon="brackets-curly" text={`Python ${environment.pythonVersion}`} />
      <Badge color="darkgrey" icon="clock-nine" text={`Deadline up to ${environment.maxTimeoutSeconds}s`} />
      <Badge color="darkgrey" text={`Memory up to ${environment.maxMemoryMib} MiB`} />
    </Stack>
    <Alert severity="info" title="Network" bottomSpacing={0}>{environment.networkPolicy}</Alert>
    <PanelChrome title="SDK decorators" padding="none">
      <InteractiveTable columns={decoratorColumns} data={environment.decorators.map(item => ({ ...item, id: item.name }))} getRowId={row => row.id} />
    </PanelChrome>
    <PanelChrome title={`Installed packages (${environment.packages.length})`} description="Plus the full Python standard library. Packages cannot be installed at runtime." padding="none">
      <InteractiveTable columns={packageColumns} data={environment.packages.map(item => ({ ...item, id: item.name }))} getRowId={row => row.id} pageSize={20} />
    </PanelChrome>
  </Stack>
}
