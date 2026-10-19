import { lazy, Suspense, useState } from 'react'
import { css } from '@emotion/css'
import type { GrafanaTheme2 } from '@grafana/data'
import { Alert, Button, Checkbox, Combobox, Field, Icon, Input, Modal, RadioButtonGroup, Stack, Text, TextArea, useStyles2 } from '@grafana/ui'
import { applySandbox, listSandboxes, validatePythonSource, type LibraryFunction, type PythonRunResult, type SourceDiagnostic } from '../../sandbox-api'
import type { PythonFunctionRef } from '../../types'
import { configWithDefaults, INLINE_STARTER, inputPreview, missingParams, newRunId, promoteInlineSource, promotionConflict, suggestLibraryName, unwrapLines, type SourceHighlights } from '../canvas/python-templates'
import type { RevealRequest } from '../PythonSourceEditor'
import { ParamsForm } from './ParamsForm'
import { RunResultView } from './RunResultView'
import { objectInput, runPython } from './run'
import { useLogReadiness } from './useLogReadiness'
import { WorkerLogsPanel } from './WorkerLogsPanel'
import { functionLabel, usePythonDefault } from '../canvas/python-functions'
import { refLibrary, storedRef } from '../../lib/python-ref'
import { PageLoading } from '../PageLoading'
import { CodeView } from '../CodeView'
import { t, Trans } from '../../i18n'

const PythonSourceEditor = lazy(() => import('../PythonSourceEditor'))
type Mode = 'library' | 'inline'

/** Python transformer stage: a library function with per-node configuration,
 * or inline source the controller runs in the flow's managed worker. */
export function PythonStageEditor({ stageId, value, functions, registryError, readOnly, onChange }: { stageId: string; value?: PythonFunctionRef; functions: LibraryFunction[]; registryError: string | null; readOnly?: boolean; onChange: (value: PythonFunctionRef | undefined) => void }) {
  const mode: Mode = value?.inlineSource !== undefined ? 'inline' : 'library'
  const defaultLibrary = usePythonDefault()
  const transformers = functions.filter(fn => fn.kind === 'transformer')
  const library = refLibrary(value, defaultLibrary)
  const selected = value && mode === 'library' ? transformers.find(fn => fn.library === library && fn.name === value.function) : undefined
  const switchMode = (next: Mode) => onChange(next === 'inline' ? { library: '', function: '', inlineSource: INLINE_STARTER } : undefined)
  const missing = selected ? missingParams(selected.params, value?.config ?? {}) : []
  const styles = useStyles2(getStyles)
  return <Stack direction="column" gap={2}>
    {/* Mode labels («Функція з бібліотеки») exceed half of a narrow inspector: let them wrap instead of truncating. */}
    <div className={styles.modes}><RadioButtonGroup fullWidth size="sm" disabled={readOnly} value={mode} onChange={switchMode} options={[{ value: 'library', label: t('gofi.python.stage-editor.mode-library', 'Library function') }, { value: 'inline', label: t('gofi.python.stage-editor.mode-inline', 'Inline code') }]} /></div>
    {mode === 'library' ? <>
      <Field label={t('gofi.python.stage-editor.transformer', 'Transformer')} noMargin>
        <Combobox disabled={readOnly} value={value?.function ? `${library}/${value.function}` : ''} placeholder={t('gofi.python.stage-editor.transformer-placeholder', 'Choose a transformer...')}
          options={[...transformers.map(fn => ({ value: `${fn.library}/${fn.name}`, label: functionLabel(fn.library, fn.name, defaultLibrary), description: fn.description || undefined })),
            ...(value?.function && !selected ? [{ value: `${library}/${value.function}`, label: t('gofi.python.stage-editor.transformer-unavailable', '{{label}} (unavailable)', { label: functionLabel(library, value.function, defaultLibrary) }) }] : [])]}
          onChange={option => { if (!option) return; const [library, ...rest] = option.value.split('/'); const fn = transformers.find(item => item.library === library && item.name === rest.join('/')); onChange(storedRef({ library, function: rest.join('/'), config: fn ? configWithDefaults(fn.params) : undefined }, defaultLibrary)) }} />
      </Field>
      {selected && <Field label={t('gofi.python.stage-editor.configuration', 'Configuration')} description={t('gofi.python.stage-editor.configuration-description', "Passed to this node's call as keyword arguments.")} noMargin>
        <ParamsForm params={selected.params} value={value?.config ?? configWithDefaults(selected.params)} disabled={readOnly} onChange={config => onChange({ ...value!, config })} />
      </Field>}
      {missing.length > 0 && <Text color="warning" variant="bodySmall">{t('gofi.python.common.required-params', 'Required: {{params}}', { params: missing.join(', ') })}</Text>}
      {registryError && <Text color="warning" variant="bodySmall">{t('gofi.python.stage-editor.registry-unavailable', 'Python function registry unavailable.')}</Text>}
      {!transformers.length && !registryError && <Text color="secondary" variant="bodySmall">{t('gofi.python.stage-editor.no-transformers', 'No transformers registered yet. Write one inline, or launch a library under Functions.')}</Text>}
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
  const sampleFieldsLabel = t('gofi.python.stage-editor.sample-fields', 'Sample fields')
  // Inline snippets define `transform`; run them by wrapping as a decorated function.
  const runnable = `import gofi\n\n${promoteInlineSource(source, 'transform')}`
  const offset = 3 // lines added above the user's snippet in `runnable`
  const snippetLines = source.split('\n').length
  const entryLine = Math.max(1, source.split('\n').findIndex(line => /^\s*(?:async\s+)?def\s+transform\s*\(/.test(line)) + 1)
  const highlights: SourceHighlights = { entry: { line: result?.entryLine ? Math.max(1, result.entryLine - offset) : entryLine, label: t('gofi.python.stage-editor.entry-label', 'transform · stage {{id}}', { id: stageId }), preview: inputPreview({ fields: safeFields(fields, sampleFieldsLabel) }) },
    executed: result ? unwrapLines(result.executedLines, offset, snippetLines) : undefined, error: result?.errorLocation?.line }
  async function validate() {
    setBusy('validate'); setError('')
    try { const validation = await validatePythonSource(source); setDiagnostics(validation.diagnostics); setValidity(validation.valid ? 'valid' : 'invalid'); if (validation.valid) setError('') }
    catch (reason) { setError(String(reason)) } finally { setBusy(null) }
  }
  async function run() {
    setBusy('run'); setError(''); setResult(null)
    try {
      const sample = objectInput(fields, sampleFieldsLabel)
      const id = newRunId()
      setRunId(id)
      await readiness.ready(false)
      const { result: outcome } = await runPython({ source: runnable }, { kind: 'transformer', name: 'transform', raw: '', fields: sample, meta: { listener: 'inline-test', stage: stageId }, runId: id, traceLines: trace })
      setResult(outcome.errorLocation ? { ...outcome, errorLocation: { ...outcome.errorLocation, line: Math.max(1, outcome.errorLocation.line - offset) } } : outcome)
    } catch (reason) { setError(reason instanceof Error ? reason.message : String(reason)) } finally { setBusy(null) }
  }
  return <Stack direction="column" gap={1.5}>
    <Text color="secondary" variant="bodySmall">
      <Trans i18nKey="gofi.python.stage-editor.inline-help">Define <Text variant="code">transform(fields, meta)</Text>. Return the fields, or <Text variant="code">None</Text> to drop. GoFi runs it in this listener's isolated worker.</Trans>
    </Text>
    {readOnly
      ? <CodeView source={source} language="python" maxHeight={220} ariaLabel={t('gofi.python.stage-editor.inline-source-aria', 'Inline Python source')} />
      : <Suspense fallback={<PageLoading text={t('gofi.common.loading-editor', 'Loading editor...')} />}>
        <PythonSourceEditor source={source} onChange={inlineSource => { onChange({ ...value, inlineSource }); setDiagnostics([]); setValidity(undefined); setResult(null) }} diagnostics={diagnostics} height="220px" reveal={reveal} ariaLabel={t('gofi.python.stage-editor.inline-source-aria', 'Inline Python source')} highlights={highlights} validity={validity} />
      </Suspense>}
    {validity === 'valid' && <Text color="success" variant="bodySmall"><Icon name="check-circle" /> {t('gofi.python.stage-editor.validation-passed', 'Validation passed')}</Text>}
    {diagnostics.map((diagnostic, index) => <Text key={index} color="error" variant="bodySmall">{diagnostic.line}:{diagnostic.column} {diagnostic.code} {diagnostic.message}</Text>)}
    <Field label={t('gofi.python.stage-editor.sample-fields-json', 'Sample fields (JSON)')} noMargin><TextArea rows={2} value={fields} aria-label={t('gofi.python.stage-editor.sample-fields-aria', 'Inline sample fields')} onChange={event => setFields(event.currentTarget.value)} /></Field>
    <Stack gap={1} wrap="wrap">
      <Button size="sm" variant="secondary" icon="check" disabled={!!busy || !source.trim()} onClick={() => void validate()}>{busy === 'validate' ? t('gofi.python.stage-editor.validating', 'Validating...') : t('gofi.python.stage-editor.validate', 'Validate')}</Button>
      <Button size="sm" icon={busy === 'run' ? 'spinner' : 'play'} disabled={!!busy || !source.trim()} onClick={() => void run()}>{t('gofi.python.stage-editor.run-with-sample', 'Run with sample')}</Button>
      {!readOnly && <Button size="sm" variant="secondary" fill="outline" icon="book" onClick={() => setPromoteOpen(true)}>{t('gofi.python.stage-editor.promote-to-library', 'Promote to library')}</Button>}
      <Checkbox label={t('gofi.python.common.trace-lines', 'Trace lines')} value={trace} onChange={event => setTrace(event.currentTarget.checked)} />
    </Stack>
    {runId && <WorkerLogsPanel compact height={110} title={t('gofi.python.stage-editor.live-output', 'Live output')} target={{ runId }} highlightInvocation={runId} onState={readiness.onState} emptyText={busy === 'run' ? t('gofi.python.common.waiting-for-output', 'Waiting for output...') : t('gofi.python.common.no-output', 'No output.')} />}
    {!busy && diagnostics.length === 0 && error === '' && result === null && <Text color="secondary" variant="bodySmall">{t('gofi.python.stage-editor.scratch-note', 'Runs in a temporary isolated worker; nothing is saved or sent.')}</Text>}
    {error && <Alert severity="error" title={t('gofi.python.stage-editor.inline-error-title', 'Inline Python')} bottomSpacing={0}>{error}</Alert>}
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
      if (existing?.managedBy) throw new Error(t('gofi.python.stage-editor.promote-managed-library', '{{library}} is managed by a flow; choose another library', { library }))
      const conflict = existing ? promotionConflict(existing.sourceCode, name) : ''
      if (conflict) throw new Error(t('gofi.python.stage-editor.promote-conflict', '{{library}} {{conflict}}; choose another function name', { library, conflict }))
      const fn = promoteInlineSource(source, name)
      const sourceCode = existing ? `${existing.sourceCode.trimEnd()}\n\n\n${fn}` : `import gofi\n\n\n${fn}`
      await applySandbox(existing ? { ...existing, sourceCode } : { name: library, sourceCode, timeoutSeconds: 5, memoryMib: 128 })
      onPromoted({ library, function: name.replace(/[^A-Za-z0-9_]/g, '_') })
    } catch (reason) { setError(reason instanceof Error ? reason.message : String(reason)) } finally { setBusy(false) }
  }
  return <Modal title={t('gofi.python.stage-editor.promote-to-library', 'Promote to library')} isOpen onDismiss={onClose}>
    <Stack direction="column" gap={2}>
      <Text color="secondary">
        <Trans i18nKey="gofi.python.stage-editor.promote-description">Adds this snippet as a <Text variant="code">@gofi.transformer</Text> to a library and switches the stage to reference it. Existing libraries are appended to and relaunched.</Trans>
      </Text>
      <Field label={t('gofi.python.stage-editor.promote-library', 'Library')} description={t('gofi.python.stage-editor.promote-library-description', 'New or existing library name')} noMargin><Input value={library} onChange={event => setLibrary(event.currentTarget.value)} /></Field>
      <Field label={t('gofi.python.stage-editor.promote-function-name', 'Function name')} noMargin><Input value={name} onChange={event => setName(event.currentTarget.value)} /></Field>
      {error && <Alert severity="error" title={t('gofi.python.stage-editor.promote-failed', 'Promotion failed')} bottomSpacing={0}>{error}</Alert>}
    </Stack>
    <Modal.ButtonRow>
      <Button variant="secondary" fill="outline" onClick={onClose}>{t('gofi.common.cancel', 'Cancel')}</Button>
      <Button icon={busy ? 'spinner' : 'book'} disabled={busy || !library || !name} onClick={() => void promote()}>{t('gofi.python.stage-editor.promote', 'Promote')}</Button>
    </Modal.ButtonRow>
  </Modal>
}

function safeFields(text: string, label: string): Record<string, unknown> | undefined {
  try { return objectInput(text, label) } catch { return undefined }
}

const getStyles = (theme: GrafanaTheme2) => ({
  modes: css({ label: { whiteSpace: 'normal', height: 'auto', minHeight: theme.spacing(3), lineHeight: 1.25, padding: theme.spacing(0.5, 1), textAlign: 'center', overflow: 'visible', textOverflow: 'clip' } }),
})
