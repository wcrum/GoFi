import { Alert, Badge, Button, type Column, EmptyState, InteractiveTable, Stack, Text } from '@grafana/ui'
import type { LibraryFunction } from '../../sandbox-api'
import { discoverFunctions } from '../canvas/python-templates'

interface Row { id: string; name: string; kind: string; params: LibraryFunction['params']; description: string; registered: boolean; line?: number }

/** Each decorator plugs into one flow step, in pipeline order. */
export const STEP_OF_KIND: Record<string, { step: string; color: 'purple' | 'blue' | 'green' | 'orange' | 'darkgrey' | 'red' }> = {
  framer: { step: 'Frame', color: 'darkgrey' },
  decoder: { step: 'Decode', color: 'purple' },
  parser: { step: 'Parse', color: 'blue' },
  transformer: { step: 'Stages', color: 'green' },
  encoder: { step: 'Encode', color: 'purple' },
  sink: { step: 'Sink', color: 'orange' },
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
    { id: 'name', header: 'Function', cell: ({ row: { original } }) => <Stack gap={1} alignItems="center"><Text variant="code">{original.name}</Text>{!original.registered && <Badge color="darkgrey" text="Detected" tooltip="Found in the editor source; save and launch to register it." />}</Stack> },
    { id: 'kind', header: 'Kind', cell: ({ row: { original } }) => <Badge color={STEP_OF_KIND[original.kind]?.color ?? 'darkgrey'} text={original.kind} /> },
    { id: 'step', header: 'Flow step', cell: ({ row: { original } }) => <Text color="secondary">{STEP_OF_KIND[original.kind]?.step ?? '—'}</Text> },
    { id: 'params', header: 'Parameters', cell: ({ row: { original } }) => original.params.length ? <Stack gap={0.5} wrap="wrap">{original.params.map(param => <Badge key={param.name} color="darkgrey" text={`${param.name}${param.type && param.type !== 'any' ? `: ${param.type}` : ''}${param.default !== undefined && param.default !== null ? ` = ${JSON.stringify(param.default)}` : ''}`} />)}</Stack> : <Text color="secondary">—</Text> },
    { id: 'description', header: 'Description', cell: ({ row: { original } }) => <Text color="secondary">{original.description || '—'}</Text> },
    { id: 'actions', header: '', cell: ({ row: { original } }) => <Stack gap={1} justifyContent="flex-end">
      {original.line && <Button size="sm" variant="secondary" fill="text" icon="arrow-right" onClick={() => onJumpToLine(original.line!)}>Source</Button>}
      <Button size="sm" variant="secondary" fill="outline" icon="play" onClick={() => onRun(original.kind, original.name)}>Run</Button>
    </Stack> },
  ]
  if (!rows.length) return <EmptyState variant="call-to-action" message="No functions yet">Decorate functions with <Text variant="code">@gofi.framer</Text>, <Text variant="code">@gofi.decoder</Text>, <Text variant="code">@gofi.parser</Text>, <Text variant="code">@gofi.transformer</Text>, <Text variant="code">@gofi.encoder</Text>, or <Text variant="code">@gofi.sink</Text>, or insert one from <b>New function</b> on the Source tab.</EmptyState>
  return <Stack direction="column" gap={2}>
    {!saved && <Alert severity="info" title="Source has unsaved changes" bottomSpacing={0}>Detected functions are read from the editor text. Registration and parameters come from the worker after Save and launch.</Alert>}
    <InteractiveTable columns={columns} data={rows} getRowId={row => row.id} />
  </Stack>
}
