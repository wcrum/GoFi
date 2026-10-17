import { lazy, Suspense, useState } from 'react'
import { Alert, Button, Checkbox, Combobox, Field, Icon, Input, Modal, RadioButtonGroup, Stack, Text, TextArea } from '@grafana/ui'
import { applySandbox, listSandboxes, validatePythonSource, type LibraryFunction, type PythonRunResult, type SourceDiagnostic } from '../../sandbox-api'
import type { PythonFunctionRef } from '../../types'
import { configWithDefaults, INLINE_STARTER, inputPreview, missingParams, newRunId, promoteInlineSource, promotionConflict, suggestLibraryName, unwrapLines, type SourceHighlights } from '../canvas/python-templates'
import type { RevealRequest } from '../PythonSourceEditor'
import { ParamsForm } from './ParamsForm'
import { RunResultView } from './RunResultView'
import { objectInput, runPython } from './run'
import { useLogReadiness } from './useLogReadiness'
import { WorkerLogsPanel } from './WorkerLogsPanel'
import { PageLoading } from '../PageLoading'

const PythonSourceEditor = lazy(() => import('../PythonSourceEditor'))
type Mode = 'library' | 'inline'

/** Python transformer stage: a library function with per-node configuration,
 * or inline source the controller runs in the flow's managed worker. */
export function PythonStageEditor({ stageId, value, functions, registryError, readOnly, onChange }: { stageId: string; value?: PythonFunctionRef; functions: LibraryFunction[]; registryError: string | null; readOnly?: boolean; onChange: (value: PythonFunctionRef | undefined) => void }) {
  const mode: Mode = value?.inlineSource !== undefined ? 'inline' : 'library'
  const transformers = functions.filter(fn => fn.kind === 'transformer')
  const selected = value && mode === 'library' ? transformers.find(fn => fn.library === value.library && fn.name === value.function) : undefined
  const switchMode = (next: Mode) => onChange(next === 'inline' ? { library: '', function: '', inlineSource: INLINE_STARTER } : undefined)
  return <Stack direction="column" gap={2}>
    <RadioButtonGroup fullWidth size="sm" disabled={readOnly} value={mode} onChange={switchMode} options={[{ value: 'library', label: 'Library function', icon: 'book' }, { value: 'inline', label: 'Inline code', icon: 'brackets-curly' }]} />
    {mode === 'library' ? <>
      <Field label="Transformer" noMargin>
        <Combobox disabled={readOnly} value={value ? `${value.library}/${value.function}` : ''} placeholder="Choose a transformer..."
          options={[...transformers.map(fn => ({ value: `${fn.library}/${fn.name}`, label: `${fn.library}.${fn.name}`, description: fn.description || undefined })),
            ...(value && !selected && value.library ? [{ value: `${value.library}/${value.function}`, label: `${value.library}.${value.function} (unavailable)` }] : [])]}
          onChange={option => { if (!option) return; const [library, ...rest] = option.value.split('/'); const fn = transformers.find(item => item.library === library && item.name === rest.join('/')); onChange({ library, function: rest.join('/'), config: fn ? configWithDefaults(fn.params) : undefined }) }} />
      </Field>
      {selected && <Field label="Configuration" description="Passed to this node's call as keyword arguments." noMargin>
        <ParamsForm params={selected.params} value={value?.config ?? configWithDefaults(selected.params)} disabled={readOnly} onChange={config => onChange({ ...value!, config })} />
      </Field>}
      {selected && missingParams(selected.params, value?.config ?? {}).length > 0 && <Text color="warning" variant="bodySmall">Required: {missingParams(selected.params, value?.config ?? {}).join(', ')}</Text>}
      {registryError && <Text color="warning" variant="bodySmall">Python function registry unavailable.</Text>}
      {!transformers.length && !registryError && <Text color="secondary" variant="bodySmall">No transformers registered yet. Write one inline, or launch a library under Python libraries.</Text>}
    </> : <InlineEditor stageId={stageId} value={value!} readOnly={readOnly} onChange={onChange} />}
  </Stack>
}

function InlineEditor({ stageId, value, readOnly, onChange }: { stageId: string; value: PythonFunctionRef; readOnly?: boolean; onChange: (value: PythonFunctionRef) => void }) {
  const source = value.inlineSource ?? ''
  const [diagnostics, setDiagnostics] = useState<SourceDiagnostic[]>([])
  const [validity, setValidity] = useState<'valid' | 'invalid' | undefined>()
  const [reveal, setReveal] = useState<RevealRequest | null>(null)
  const [fields, setFields] = useState('{"sensor":"alpha","temperature":28}')
  const [result, setResult] = useState<PythonRunResult | null>(null)
  const [busy, setBusy] = useState<'validate' | 'run' | null>(null)
  const [error, setError] = useState('')
  const [promoteOpen, setPromoteOpen] = useState(false)
  const [trace, setTrace] = useState(false)
  const [runId, setRunId] = useState<string | null>(null)
  const readiness = useLogReadiness()
  // Inline snippets define `transform`; run them by wrapping as a decorated function.
  const runnable = `import gofi\n\n${promoteInlineSource(source, 'transform')}`
  const offset = 3 // lines added above the user's snippet in `runnable`
  const snippetLines = source.split('\n').length
  const entryLine = Math.max(1, source.split('\n').findIndex(line => /^\s*(?:async\s+)?def\s+transform\s*\(/.test(line)) + 1)
  const highlights: SourceHighlights = { entry: { line: result?.entryLine ? Math.max(1, result.entryLine - offset) : entryLine, label: `transform · stage ${stageId}`, preview: inputPreview({ fields: safeFields(fields) }) },
    executed: result ? unwrapLines(result.executedLines, offset, snippetLines) : undefined, error: result?.errorLocation?.line }
  async function validate() {
    setBusy('validate'); setError('')
    try { const validation = await validatePythonSource(source); setDiagnostics(validation.diagnostics); setValidity(validation.valid ? 'valid' : 'invalid'); if (validation.valid) setError('') }
    catch (reason) { setError(String(reason)) } finally { setBusy(null) }
  }
  async function run() {
    setBusy('run'); setError(''); setResult(null)
    try {
      const sample = objectInput(fields, 'Sample fields')
      const id = newRunId()
      setRunId(id)
      await readiness.ready(false)
      const { result: outcome } = await runPython({ source: runnable }, { kind: 'transformer', name: 'transform', raw: '', fields: sample, meta: { listener: 'inline-test', stage: stageId }, runId: id, traceLines: trace })
      setResult(outcome.errorLocation ? { ...outcome, errorLocation: { ...outcome.errorLocation, line: Math.max(1, outcome.errorLocation.line - offset) } } : outcome)
    } catch (reason) { setError(reason instanceof Error ? reason.message : String(reason)) } finally { setBusy(null) }
  }
  return <Stack direction="column" gap={1.5}>
    <Text color="secondary" variant="bodySmall">Define <Text variant="code">transform(fields, meta)</Text>. Return the fields, or <Text variant="code">None</Text> to drop. GoFi runs it in this listener's isolated worker.</Text>
    <Suspense fallback={<PageLoading text="Loading editor..." />}>
      <PythonSourceEditor source={source} onChange={inlineSource => { onChange({ ...value, inlineSource }); setDiagnostics([]); setValidity(undefined); setResult(null) }} diagnostics={diagnostics} disabled={readOnly} height="220px" reveal={reveal} ariaLabel="Inline Python source" highlights={highlights} validity={validity} />
    </Suspense>
    {validity === 'valid' && <Text color="success" variant="bodySmall"><Icon name="check-circle" /> Validation passed</Text>}
    {diagnostics.map((diagnostic, index) => <Text key={index} color="error" variant="bodySmall">{diagnostic.line}:{diagnostic.column} {diagnostic.code} {diagnostic.message}</Text>)}
    <Field label="Sample fields (JSON)" noMargin><TextArea rows={2} value={fields} aria-label="Inline sample fields" onChange={event => setFields(event.currentTarget.value)} /></Field>
    <Stack gap={1} wrap="wrap">
      <Button size="sm" variant="secondary" icon="check" disabled={!!busy || !source.trim()} onClick={() => void validate()}>{busy === 'validate' ? 'Validating...' : 'Validate'}</Button>
      <Button size="sm" icon={busy === 'run' ? 'spinner' : 'play'} disabled={!!busy || !source.trim()} onClick={() => void run()}>Run with sample</Button>
      {!readOnly && <Button size="sm" variant="secondary" fill="outline" icon="book" onClick={() => setPromoteOpen(true)}>Promote to library</Button>}
      <Checkbox label="Trace lines" value={trace} onChange={event => setTrace(event.currentTarget.checked)} />
    </Stack>
    {runId && <WorkerLogsPanel compact height={110} title="Live output" target={{ runId }} highlightInvocation={runId} onState={readiness.onState} emptyText={busy === 'run' ? 'Waiting for output...' : 'No output.'} />}
    {!busy && diagnostics.length === 0 && error === '' && result === null && <Text color="secondary" variant="bodySmall">Runs in a temporary isolated worker; nothing is saved or sent.</Text>}
    {error && <Alert severity="error" title="Inline Python" bottomSpacing={0}>{error}</Alert>}
    {result && <RunResultView result={result} compact onJumpToLine={(line, column) => setReveal({ line, column, nonce: Date.now() })} />}
    {promoteOpen && <PromoteModal stageId={stageId} source={source} onClose={() => setPromoteOpen(false)} onPromoted={ref => { setPromoteOpen(false); onChange(ref) }} />}
  </Stack>
}

/** Moves an inline snippet into a named library (new or existing) and
 * switches the stage to reference it. */
function PromoteModal({ stageId, source, onClose, onPromoted }: { stageId: string; source: string; onClose: () => void; onPromoted: (ref: PythonFunctionRef) => void }) {
  const [library, setLibrary] = useState(suggestLibraryName('flow', stageId))
  const [name, setName] = useState(stageId.replace(/[^A-Za-z0-9_]/g, '_'))
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  async function promote() {
    setBusy(true); setError('')
    try {
      const { sandboxes } = await listSandboxes()
      const existing = sandboxes.find(item => item.name === library)
      if (existing?.managedBy) throw new Error(`${library} is managed by a flow; choose another library`)
      const conflict = existing ? promotionConflict(existing.sourceCode, name) : ''
      if (conflict) throw new Error(`${library} ${conflict}; choose another function name`)
      const fn = promoteInlineSource(source, name)
      const sourceCode = existing ? `${existing.sourceCode.trimEnd()}\n\n\n${fn}` : `import gofi\n\n\n${fn}`
      await applySandbox(existing ? { ...existing, sourceCode } : { name: library, sourceCode, timeoutSeconds: 5, memoryMib: 128 })
      onPromoted({ library, function: name.replace(/[^A-Za-z0-9_]/g, '_') })
    } catch (reason) { setError(reason instanceof Error ? reason.message : String(reason)) } finally { setBusy(false) }
  }
  return <Modal title="Promote to library" isOpen onDismiss={onClose}>
    <Stack direction="column" gap={2}>
      <Text color="secondary">Adds this snippet as a <Text variant="code">@gofi.transformer</Text> to a library and switches the stage to reference it. Existing libraries are appended to and relaunched.</Text>
      <Field label="Library" description="New or existing library name" noMargin><Input value={library} onChange={event => setLibrary(event.currentTarget.value)} /></Field>
      <Field label="Function name" noMargin><Input value={name} onChange={event => setName(event.currentTarget.value)} /></Field>
      {error && <Alert severity="error" title="Promotion failed" bottomSpacing={0}>{error}</Alert>}
    </Stack>
    <Modal.ButtonRow>
      <Button variant="secondary" fill="outline" onClick={onClose}>Cancel</Button>
      <Button icon={busy ? 'spinner' : 'book'} disabled={busy || !library || !name} onClick={() => void promote()}>Promote</Button>
    </Modal.ButtonRow>
  </Modal>
}

function safeFields(text: string): Record<string, unknown> | undefined {
  try { return objectInput(text, 'Sample fields') } catch { return undefined }
}
