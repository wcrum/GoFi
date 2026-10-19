import { useEffect, useState } from 'react'
import { Alert, Badge, type Column, InteractiveTable, PanelChrome, Stack, Text } from '@grafana/ui'
import { describePythonEnvironment, isUnimplemented, type PythonEnvironment } from '../../sandbox-api'
import { PageLoading } from '../PageLoading'
import { t } from '../../i18n'

type PackageRow = PythonEnvironment['packages'][number] & { id: string }
type DecoratorRow = PythonEnvironment['decorators'][number] & { id: string }
const packageColumns = (): Array<Column<PackageRow>> => [
  { id: 'name', header: t('gofi.python.environment.package-header', 'Package'), sortType: 'string', cell: ({ row: { original } }) => <Text variant="code">{original.name}</Text> },
  { id: 'version', header: t('gofi.python.environment.version-header', 'Version'), cell: ({ row: { original } }) => <Text color="secondary">{original.version}</Text> },
  { id: 'summary', header: t('gofi.python.environment.summary-header', 'Summary'), cell: ({ row: { original } }) => <Text color="secondary">{original.summary || '—'}</Text> },
]
const decoratorColumns = (): Array<Column<DecoratorRow>> => [
  { id: 'signature', header: t('gofi.python.environment.decorator-header', 'Decorator'), cell: ({ row: { original } }) => <Text variant="code">{original.signature || original.name}</Text> },
  { id: 'description', header: t('gofi.common.description', 'Description'), cell: ({ row: { original } }) => <Text color="secondary">{original.description || '—'}</Text> },
]

/** What library code can use: interpreter, packages, SDK decorators, limits. */
export function EnvironmentTab() {
  const [environment, setEnvironment] = useState<PythonEnvironment | null>(null)
  const [error, setError] = useState('')
  useEffect(() => { describePythonEnvironment().then(setEnvironment).catch(reason => setError(isUnimplemented(reason) ? 'unimplemented' : String(reason))) }, [])
  if (error === 'unimplemented') return <Alert severity="info" title={t('gofi.python.environment.unimplemented-title', 'Environment details unavailable')} bottomSpacing={0}>{t('gofi.python.environment.unimplemented-body', 'This GoFi controller does not report its Python environment yet. Workers run the Python standard library with the GoFi SDK and no outbound network.')}</Alert>
  if (error) return <Alert severity="error" title={t('gofi.python.environment.error-title', 'Environment unavailable')} bottomSpacing={0}>{error}</Alert>
  if (!environment) return <PageLoading text={t('gofi.python.environment.loading', 'Inspecting the Python worker...')} />
  return <Stack direction="column" gap={2}>
    <Stack gap={1} wrap="wrap">
      <Badge color="blue" icon="brackets-curly" text={t('gofi.python.environment.python-version', 'Python {{version}}', { version: environment.pythonVersion })} />
      <Badge color="darkgrey" icon="clock-nine" text={t('gofi.python.environment.max-deadline', 'Deadline up to {{seconds}}s', { seconds: environment.maxTimeoutSeconds })} />
      <Badge color="darkgrey" text={t('gofi.python.environment.max-memory', 'Memory up to {{mib}} MiB', { mib: environment.maxMemoryMib })} />
    </Stack>
    <Alert severity="info" title={t('gofi.python.environment.network-title', 'Network')} bottomSpacing={0}>{environment.networkPolicy}</Alert>
    <PanelChrome title={t('gofi.python.environment.decorators-title', 'SDK decorators')} padding="none">
      <InteractiveTable columns={decoratorColumns()} data={environment.decorators.map(item => ({ ...item, id: item.name }))} getRowId={row => row.id} />
    </PanelChrome>
    <PanelChrome title={t('gofi.python.environment.packages-title', 'Installed packages ({{count}})', { count: environment.packages.length })} description={t('gofi.python.environment.packages-description', 'Plus the full Python standard library. Packages cannot be installed at runtime.')} padding="none">
      <InteractiveTable columns={packageColumns()} data={environment.packages.map(item => ({ ...item, id: item.name }))} getRowId={row => row.id} pageSize={20} />
    </PanelChrome>
  </Stack>
}
