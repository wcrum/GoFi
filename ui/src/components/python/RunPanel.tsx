import { useEffect, useMemo, useState } from 'react'
import { css } from '@emotion/css'
import type { GrafanaTheme2 } from '@grafana/data'
import { Alert, Badge, Button, Checkbox, Combobox, Field, Icon, Label, Stack, Text, TextArea, Tooltip, useStyles2 } from '@grafana/ui'
import type { FunctionTest, LibraryFunction, PythonFunctionKind, PythonRunResult } from '../../sandbox-api'
import { configWithDefaults, discoverFunctions, inputPreview, missingParams, newRunId, type SourceHighlights } from '../canvas/python-templates'
import { ParamsForm } from './ParamsForm'
import { RunResultView } from './RunResultView'
import { objectInput, runPython, type RunTarget } from './run'
import { WorkerLogsPanel, type LogTarget } from './WorkerLogsPanel'
import { useLogReadiness } from './useLogReadiness'
import { DebugDrawer } from './debugger/DebugDrawer'
import type { DebugInput } from './debugger/debug-api'
import { STEP_OF_KIND, stepOfKind } from './FunctionsTab'
import { t, Trans } from '../../i18n'

export const functionKey = (kind: string, name: string) => `${kind}:${name}`
// Steps that take bytes as input; an encoder also receives fields.
const RAW_KINDS: PythonFunctionKind[] = ['framer', 'decoder', 'parser', 'encoder']

/** Run any function in the editor with sample inputs; saved and launched
 * libraries use their worker, unsaved source runs in a scratch worker. */
export function RunPanel({ target, registry, selected, onSelect, onJumpToLine, onSaveTest, onHighlight, disabled }: {
  target: RunTarget
  /** Registered functions of the saved library (for declared params). */
  registry: LibraryFunction[]
  selected: string
  onSelect: (key: string) => void
  onJumpToLine: (line: number, column?: number) => void
  onSaveTest?: (test: FunctionTest) => void
  /** Editor highlights: where the input enters, executed lines, error line. */
  /** `static` marks the selected function before any run; `result` follows a run. */
  onHighlight?: (highlights: SourceHighlights, reason: 'static' | 'result') => void
  disabled?: boolean
}) {
  const styles = useStyles2(getStyles)
  // Streamers run only as a flow source (WorkerService.Stream), never once.
  const discovered = useMemo(() => discoverFunctions(target.source).filter(fn => fn.kind !== 'streamer'), [target.source])
  const options = discovered.map(fn => ({ value: functionKey(fn.kind, fn.name), label: `${fn.name}`, description: fn.kind }))
  const current = discovered.find(fn => functionKey(fn.kind, fn.name) === selected) ?? discovered[0]
  const registered = current && registry.find(fn => fn.kind === current.kind && fn.name === current.name)
  const params = registered?.params ?? []
  const [raw, setRaw] = useState('{"sensor":"alpha","temperature":28}')
  const [fields, setFields] = useState('{"sensor":"alpha","temperature":28}')
  const [meta, setMeta] = useState('{"name":"sample"}')
  const [config, setConfig] = useState<Record<string, unknown>>({})
  const [configText, setConfigText] = useState('{}')
  const [result, setResult] = useState<PythonRunResult | null>(null)
  const [mode, setMode] = useState<'saved' | 'unsaved' | null>(null)
  const [error, setError] = useState('')
  const [running, setRunning] = useState(false)
  const [trace, setTrace] = useState(false)
  const [logs, setLogs] = useState<{ target: LogTarget; invocation: string } | null>(null)
  const readiness = useLogReadiness()
  const [debugging, setDebugging] = useState<DebugInput | null>(null)
  useEffect(() => { setResult(null); setError(''); setConfig(configWithDefaults(params)) }, [current?.kind, current?.name, registered?.params.length]) // eslint-disable-line react-hooks/exhaustive-deps
  const takesRaw = current ? RAW_KINDS.includes(current.kind) : true
  const missing = missingParams(params, config)
  const fieldsLabel = t('gofi.python.run-panel.fields', 'Fields')
  const preview = inputPreview(takesRaw ? { raw } : { fields: safeObject(fields, fieldsLabel) })

  // Before any run, mark the selected function's `def` (from the static scan);
  // after a run, use the line the worker reports plus any trace/error lines.
  useEffect(() => {
    if (!onHighlight) return
    if (!current) { onHighlight({}, 'static'); return }
    const label = `${current.name} (${current.kind})`
    if (!result) { onHighlight({ entry: { line: current.defLine, label, preview } }, 'static'); return }
    onHighlight({ entry: { line: result.entryLine || current.defLine, label, preview }, executed: result.executedLines, error: result.errorLocation?.line }, 'result')
  }, [current?.name, current?.kind, current?.defLine, result, preview]) // eslint-disable-line react-hooks/exhaustive-deps

  function inputs() {
    if (!current) throw new Error(t('gofi.python.run-panel.no-function-error', 'Declare a function with a @gofi decorator first'))
    const resolvedConfig = params.length ? config : objectInput(configText, t('gofi.python.run-panel.config', 'Config'))
    return { kind: current.kind, name: current.name, raw, fields: takesRaw ? {} : objectInput(fields, fieldsLabel), meta: objectInput(meta, t('gofi.python.run-panel.metadata', 'Metadata')), config: Object.keys(resolvedConfig).length ? resolvedConfig : undefined }
  }
  async function run() {
    setRunning(true); setError(''); setResult(null)
    try {
      const input = inputs()
      // Follow the logs first: the saved library's worker, or this run's own
      // short-lived worker (the stream waits for it to start).
      const runId = newRunId()
      const logTarget: LogTarget = target.savedLibrary ? { library: target.savedLibrary } : { runId }
      const reuse = !!target.savedLibrary && logs?.target.library === target.savedLibrary
      setLogs({ target: logTarget, invocation: runId })
      await readiness.ready(reuse)
      const outcome = await runPython(target, { ...input, runId, traceLines: trace })
      setResult(outcome.result); setMode(outcome.mode)
    }
    catch (reason) { setError(reason instanceof Error ? reason.message : String(reason)) }
    finally { setRunning(false) }
  }
  function saveTest() {
    try {
      const input = inputs()
      onSaveTest?.({ name: t('gofi.python.run-panel.sample-test-name', '{{name}} sample', { name: input.name }), function: input.name, kind: input.kind, raw: takesRaw ? raw : undefined, fields: takesRaw ? undefined : input.fields, meta: input.meta, config: input.config,
        ...(result && !result.error ? (result.drop ? { expectDrop: true } : { expectDrop: false, expectFields: result.fields }) : {}) })
    } catch (reason) { setError(reason instanceof Error ? reason.message : String(reason)) }
  }

  if (!discovered.length) return <Text color="secondary"><Trans i18nKey="gofi.python.run-panel.no-functions">No decorated functions found. Insert one from <b>New function</b> to run it.</Trans></Text>
  const rawLabel = current?.kind === 'framer' ? t('gofi.python.run-panel.raw-framer', 'Buffered bytes (as text)') : current?.kind === 'encoder' ? t('gofi.python.run-panel.raw-encoder', 'Payload to encode (as text)') : t('gofi.python.run-panel.raw-input', 'Raw input (as text)')
  return <Stack direction="column" gap={2}>
    <Field label={t('gofi.common.function-header', 'Function')} noMargin>
      <div className={styles.picker}>
        <div className={styles.combobox}><Combobox value={current ? functionKey(current.kind, current.name) : ''} options={options} disabled={disabled} onChange={option => option && onSelect(option.value)} /></div>
        {current && <Badge color={STEP_OF_KIND[current.kind]?.color ?? 'blue'} text={t('gofi.python.run-panel.kind-step', '{{kind}} · {{step}}', { kind: current.kind, step: stepOfKind(current.kind) ?? '' })} />}
        <Badge color={target.savedLibrary ? 'green' : 'darkgrey'} icon={target.savedLibrary ? 'check' : 'pen'} text={target.savedLibrary ? t('gofi.python.run-panel.saved-worker', 'Saved worker') : t('gofi.python.common.unsaved-scratch-worker', 'Unsaved · scratch worker')} tooltip={target.savedLibrary ? t('gofi.python.run-panel.saved-worker-tooltip', 'Runs in the launched library worker.') : t('gofi.python.run-panel.scratch-worker-tooltip', 'Runs this exact source in a short-lived isolated worker. Nothing is saved.')} />
      </div>
    </Field>
    <div className={styles.inputs}>
      {takesRaw && <Field label={rawLabel} noMargin><TextArea aria-label={t('gofi.python.run-panel.raw-input-aria', 'Raw input')} rows={4} value={raw} onChange={event => setRaw(event.currentTarget.value)} /></Field>}
      {(!takesRaw || current?.kind === 'encoder') && <Field label={current?.kind === 'fetcher' ? t('gofi.python.run-panel.state-json', 'State (ctx.state, JSON object)') : t('gofi.python.run-panel.fields-json', 'Fields (JSON object)')} noMargin><TextArea aria-label={t('gofi.python.run-panel.fields-input-aria', 'Fields input')} rows={4} value={fields} onChange={event => setFields(event.currentTarget.value)} /></Field>}
      <Field label={t('gofi.python.run-panel.metadata-json', 'Metadata (JSON object)')} noMargin><TextArea aria-label={t('gofi.python.run-panel.metadata-input-aria', 'Metadata input')} rows={4} value={meta} onChange={event => setMeta(event.currentTarget.value)} /></Field>
      {!params.length && <Field label={<Label htmlFor="gofi-run-config">{t('gofi.python.run-panel.config-json', 'Config (JSON object)')}<Tooltip content={t('gofi.python.run-panel.config-description', 'Keyword arguments; declared params appear as a form once saved.')}><Icon name="info-circle" size="sm" className={styles.hint} /></Tooltip></Label>} noMargin><TextArea id="gofi-run-config" aria-label={t('gofi.python.run-panel.config-input-aria', 'Config input')} rows={4} value={configText} onChange={event => setConfigText(event.currentTarget.value)} /></Field>}
    </div>
    {params.length > 0 && <Field label={t('gofi.python.stage-editor.configuration', 'Configuration')} noMargin><ParamsForm params={params} value={config} onChange={setConfig} disabled={disabled} /></Field>}
    <Stack gap={1} alignItems="center" wrap="wrap">
      <Button icon={running ? 'spinner' : 'play'} disabled={disabled || running || missing.length > 0} onClick={() => void run()}>{running ? t('gofi.python.common.running', 'Running...') : current?.kind === 'sink' ? t('gofi.python.run-panel.run-sink', 'Run sink') : t('gofi.common.run', 'Run')}</Button>
      <Button variant="secondary" icon="bug" disabled={disabled || running || missing.length > 0} tooltip={t('gofi.python.run-panel.debug-tooltip', 'Record this run step by step and open the debugger')}
        onClick={() => { try { setDebugging(inputs()) } catch (reason) { setError(reason instanceof Error ? reason.message : String(reason)) } }}>{t('gofi.common.debug', 'Debug')}</Button>
      {onSaveTest && <Button variant="secondary" fill="outline" icon="save" disabled={disabled || running} onClick={saveTest} tooltip={t('gofi.python.run-panel.save-test-tooltip', 'Save these inputs (and the last result as the expectation) to the Tests tab')}>{t('gofi.python.run-panel.save-as-test', 'Save as test')}</Button>}
      <Stack gap={0.5} alignItems="center">
        <Checkbox label={t('gofi.python.common.trace-lines', 'Trace lines')} value={trace} onChange={event => setTrace(event.currentTarget.checked)} />
        <Tooltip content={t('gofi.python.run-panel.trace-lines-description', 'Highlight the lines this input executes (slower)')}><Icon name="info-circle" size="sm" className={styles.hint} /></Tooltip>
      </Stack>
      {missing.length > 0 && <Text color="warning" variant="bodySmall">{t('gofi.python.common.required-params', 'Required: {{params}}', { params: missing.join(', ') })}</Text>}
      {current?.kind === 'sink' && <Text color="warning" variant="bodySmall">{t('gofi.python.run-panel.sink-side-effects', 'Sinks perform their side effects when run.')}</Text>}
    </Stack>
    {logs && <WorkerLogsPanel compact height={140} title={logs.target.runId ? t('gofi.python.run-panel.live-output-this-run', 'Live output · this run') : t('gofi.python.run-panel.live-output-library', 'Live output · {{library}}', { library: logs.target.library })} target={logs.target}
      highlightInvocation={logs.invocation} onState={readiness.onState} emptyText={running ? t('gofi.python.common.waiting-for-output', 'Waiting for output...') : t('gofi.python.common.no-output', 'No output.')} />}
    {error && <Alert severity="error" title={t('gofi.python.run-panel.run-failed', 'Run failed')} bottomSpacing={0}>{error}</Alert>}
    {debugging && <DebugDrawer target={target} input={debugging} onClose={() => setDebugging(null)} />}
    {result && <>
      <RunResultView result={result} onJumpToLine={onJumpToLine} />
      <Text color="secondary" variant="bodySmall">{mode === 'unsaved' ? t('gofi.python.run-panel.ran-unsaved', 'Ran unsaved source in a temporary isolated worker; nothing was saved or sent.') : t('gofi.python.run-panel.ran-saved', 'Ran in the saved library worker.')}</Text>
    </>}
  </Stack>
}

function safeObject(text: string, label: string): Record<string, unknown> | undefined {
  try { return objectInput(text, label) } catch { return undefined }
}

const getStyles = (theme: GrafanaTheme2) => ({
  // Combobox and badges share one row, vertically centred on the combobox.
  picker: css({ display: 'flex', alignItems: 'center', flexWrap: 'wrap', gap: theme.spacing(1) }),
  combobox: css({ minWidth: 240, flex: '0 1 320px' }),
  // Equal columns; labels are one line each so the textareas top-align, and
  // rows={4} keeps them the same height.
  inputs: css({ display: 'grid', gap: theme.spacing(2), gridTemplateColumns: 'repeat(auto-fit, minmax(220px, 1fr))', alignItems: 'start', '& textarea': { resize: 'vertical' } }),
  hint: css({ marginLeft: theme.spacing(0.5), color: theme.colors.text.secondary, cursor: 'help' }),
})
