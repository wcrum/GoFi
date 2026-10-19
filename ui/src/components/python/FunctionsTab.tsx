import { Alert, Badge, Button, type Column, InteractiveTable, Stack, Text } from '@grafana/ui'
import { EmptyState } from '../EmptyState'
import type { LibraryFunction } from '../../sandbox-api'
import { discoverFunctions } from '../canvas/python-templates'
import { t, Trans } from '../../i18n'

interface Row { id: string; name: string; kind: string; params: LibraryFunction['params']; description: string; registered: boolean; line?: number }

/** Each decorator plugs into one flow step, in pipeline order. */
export const STEP_OF_KIND: Record<string, { color: 'purple' | 'blue' | 'green' | 'orange' | 'darkgrey' | 'red' }> = {
  fetcher: { color: 'red' },
  streamer: { color: 'red' },
  framer: { color: 'darkgrey' },
  decoder: { color: 'purple' },
  parser: { color: 'blue' },
  transformer: { color: 'green' },
  encoder: { color: 'purple' },
  sink: { color: 'orange' },
}

/** The flow step a decorator kind plugs into, or undefined for unknown kinds. */
export function stepOfKind(kind: string): string | undefined {
  switch (kind) {
    case 'fetcher':
    case 'streamer': return t('gofi.python.functions.step-source', 'Source')
    case 'framer': return t('gofi.python.functions.step-frame', 'Frame')
    case 'decoder': return t('gofi.python.functions.step-decode', 'Decode')
    case 'parser': return t('gofi.python.functions.step-parse', 'Parse')
    case 'transformer': return t('gofi.python.functions.step-stages', 'Stages')
    case 'encoder': return t('gofi.python.functions.step-encode', 'Encode')
    case 'sink': return t('gofi.python.functions.step-sink', 'Sink')
    default: return undefined
  }
}

/** Registered functions of the saved library; functions only found in unsaved
 * source are listed as detected until the library is saved and launched. */
export function FunctionsTab({ registry, source, saved, onRun, onJumpToLine }: { library: string; registry: LibraryFunction[]; source: string; saved: boolean; onRun: (kind: string, name: string) => void; onJumpToLine: (line: number) => void }) {
  const detected = discoverFunctions(source)
  const rows: Row[] = [
    ...registry.map(fn => ({ id: `r:${fn.kind}:${fn.name}`, name: fn.name, kind: fn.kind, params: fn.params, description: fn.description, registered: true, line: detected.find(item => item.kind === fn.kind && item.name === fn.name)?.line })),
    ...detected.filter(fn => !registry.some(item => item.kind === fn.kind && item.name === fn.name)).map(fn => ({ id: `d:${fn.kind}:${fn.name}`, name: fn.name, kind: fn.kind, params: [], description: '', registered: false, line: fn.line })),
  ]
  const columns: Array<Column<Row>> = [
    { id: 'name', header: t('gofi.common.function-header', 'Function'), cell: ({ row: { original } }) => <Stack gap={1} alignItems="center"><Text variant="code">{original.name}</Text>{!original.registered && <Badge color="darkgrey" text={t('gofi.python.functions.detected', 'Detected')} tooltip={t('gofi.python.functions.detected-tooltip', 'Found in the editor source; save and launch to register it.')} />}</Stack> },
    { id: 'kind', header: t('gofi.python.functions.kind-header', 'Kind'), cell: ({ row: { original } }) => <Badge color={STEP_OF_KIND[original.kind]?.color ?? 'darkgrey'} text={original.kind} /> },
    { id: 'step', header: t('gofi.python.functions.step-header', 'Flow step'), cell: ({ row: { original } }) => <Text color="secondary">{stepOfKind(original.kind) ?? '—'}</Text> },
    { id: 'params', header: t('gofi.python.functions.params-header', 'Parameters'), cell: ({ row: { original } }) => original.params.length ? <Stack gap={0.5} wrap="wrap">{original.params.map(param => <Badge key={param.name} color="darkgrey" text={`${param.name}${param.type && param.type !== 'any' ? `: ${param.type}` : ''}${param.default !== undefined && param.default !== null ? ` = ${JSON.stringify(param.default)}` : ''}`} />)}</Stack> : <Text color="secondary">—</Text> },
    { id: 'description', header: t('gofi.common.description', 'Description'), cell: ({ row: { original } }) => <Text color="secondary">{original.description || '—'}</Text> },
    { id: 'actions', header: '', cell: ({ row: { original } }) => <Stack gap={1} justifyContent="flex-end">
      {original.line && <Button size="sm" variant="secondary" fill="text" icon="arrow-right" onClick={() => onJumpToLine(original.line!)}>{t('gofi.python.functions.source', 'Source')}</Button>}
      {/* Streamers run only as a flow source. */}
      {original.kind !== 'streamer' && <Button size="sm" variant="secondary" fill="outline" icon="play" onClick={() => onRun(original.kind, original.name)}>{t('gofi.common.run', 'Run')}</Button>}
    </Stack> },
  ]
  if (!rows.length) return <EmptyState variant="call-to-action" message={t('gofi.python.functions.empty-title', 'No functions yet')}>
    <Trans i18nKey="gofi.python.functions.empty-body">Decorate functions with <Text variant="code">@gofi.fetcher</Text>, <Text variant="code">@gofi.streamer</Text>, <Text variant="code">@gofi.framer</Text>, <Text variant="code">@gofi.decoder</Text>, <Text variant="code">@gofi.parser</Text>, <Text variant="code">@gofi.transformer</Text>, <Text variant="code">@gofi.encoder</Text>, or <Text variant="code">@gofi.sink</Text>, or insert one from <b>New function</b> on the Source tab.</Trans>
  </EmptyState>
  return <Stack direction="column" gap={2}>
    {!saved && <Alert severity="info" title={t('gofi.python.functions.unsaved-title', 'Source has unsaved changes')} bottomSpacing={0}>{t('gofi.python.functions.unsaved-body', 'Detected functions are read from the editor text. Registration and parameters come from the worker after Save and launch.')}</Alert>}
    <InteractiveTable columns={columns} data={rows} getRowId={row => row.id} />
  </Stack>
}
