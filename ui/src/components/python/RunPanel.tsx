import { useEffect, useMemo, useState } from 'react'
import { css } from '@emotion/css'
import type { GrafanaTheme2 } from '@grafana/data'
import { Alert, Badge, Button, Checkbox, Combobox, Field, Stack, Text, TextArea, useStyles2 } from '@grafana/ui'
import type { FunctionTest, LibraryFunction, PythonFunctionKind, PythonRunResult } from '../../sandbox-api'
import { configWithDefaults, discoverFunctions, inputPreview, missingParams, newRunId, type SourceHighlights } from '../canvas/python-templates'
import { ParamsForm } from './ParamsForm'
import { RunResultView } from './RunResultView'
import { objectInput, runPython, type RunTarget } from './run'
import { WorkerLogsPanel, type LogTarget } from './WorkerLogsPanel'
import { useLogReadiness } from './useLogReadiness'
import { DebugDrawer } from './debugger/DebugDrawer'
import type { DebugInput } from './debugger/debug-api'
import { STEP_OF_KIND } from './FunctionsTab'

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
  const discovered = useMemo(() => discoverFunctions(target.source), [target.source])
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
  const preview = inputPreview(takesRaw ? { raw } : { fields: safeObject(fields) })

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
    if (!current) throw new Error('Declare a function with a @gofi decorator first')
    const resolvedConfig = params.length ? config : objectInput(configText, 'Config')
    return { kind: current.kind, name: current.name, raw, fields: takesRaw ? {} : objectInput(fields, 'Fields'), meta: objectInput(meta, 'Metadata'), config: Object.keys(resolvedConfig).length ? resolvedConfig : undefined }
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
      onSaveTest?.({ name: `${input.name} sample`, function: input.name, kind: input.kind, raw: takesRaw ? raw : undefined, fields: takesRaw ? undefined : input.fields, meta: input.meta, config: input.config,
        ...(result && !result.error ? (result.drop ? { expectDrop: true } : { expectDrop: false, expectFields: result.fields }) : {}) })
    } catch (reason) { setError(reason instanceof Error ? reason.message : String(reason)) }
  }

  if (!discovered.length) return <Text color="secondary">No decorated functions found. Insert one from <b>New function</b> to run it.</Text>
  return <Stack direction="column" gap={2}>
    <div className={styles.row}>
      <Field label="Function" noMargin className={styles.picker}>
        <Combobox value={current ? functionKey(current.kind, current.name) : ''} options={options} disabled={disabled} onChange={option => option && onSelect(option.value)} />
      </Field>
      {current && <Badge color={STEP_OF_KIND[current.kind]?.color ?? 'blue'} text={`${current.kind} · ${STEP_OF_KIND[current.kind]?.step ?? ''}`} />}
      <Badge color={target.savedLibrary ? 'green' : 'darkgrey'} icon={target.savedLibrary ? 'check' : 'pen'} text={target.savedLibrary ? 'Saved worker' : 'Unsaved · scratch worker'} tooltip={target.savedLibrary ? 'Runs in the launched library worker.' : 'Runs this exact source in a short-lived isolated worker. Nothing is saved.'} />
    </div>
    <div className={styles.inputs}>
      {takesRaw && <Field label={current?.kind === 'framer' ? 'Buffered bytes (as text)' : current?.kind === 'encoder' ? 'Payload to encode (as text)' : 'Raw input (as text)'} noMargin><TextArea aria-label="Raw input" rows={4} value={raw} onChange={event => setRaw(event.currentTarget.value)} /></Field>}
      {(!takesRaw || current?.kind === 'encoder') && <Field label="Fields (JSON object)" noMargin><TextArea aria-label="Fields input" rows={4} value={fields} onChange={event => setFields(event.currentTarget.value)} /></Field>}
      <Field label="Metadata (JSON object)" noMargin><TextArea aria-label="Metadata input" rows={4} value={meta} onChange={event => setMeta(event.currentTarget.value)} /></Field>
      {!params.length && <Field label="Config (JSON object)" description="Keyword arguments; declared params appear as a form once saved." noMargin><TextArea aria-label="Config input" rows={4} value={configText} onChange={event => setConfigText(event.currentTarget.value)} /></Field>}
    </div>
    {params.length > 0 && <Field label="Configuration" noMargin><ParamsForm params={params} value={config} onChange={setConfig} disabled={disabled} /></Field>}
    <Stack gap={1} alignItems="center" wrap="wrap">
      <Button icon={running ? 'spinner' : 'play'} disabled={disabled || running || missing.length > 0} onClick={() => void run()}>{running ? 'Running...' : current?.kind === 'sink' ? 'Run sink' : 'Run'}</Button>
      <Button variant="secondary" icon="bug" disabled={disabled || running || missing.length > 0} tooltip="Record this run step by step and open the debugger"
        onClick={() => { try { setDebugging(inputs()) } catch (reason) { setError(reason instanceof Error ? reason.message : String(reason)) } }}>Debug</Button>
      {onSaveTest && <Button variant="secondary" fill="outline" icon="save" disabled={disabled || running} onClick={saveTest} tooltip="Save these inputs (and the last result as the expectation) to the Tests tab">Save as test</Button>}
      <Checkbox label="Trace lines" description="Highlight the lines this input executes (slower)" value={trace} onChange={event => setTrace(event.currentTarget.checked)} />
      {missing.length > 0 && <Text color="warning" variant="bodySmall">Required: {missing.join(', ')}</Text>}
      {current?.kind === 'sink' && <Text color="warning" variant="bodySmall">Sinks perform their side effects when run.</Text>}
    </Stack>
    {logs && <WorkerLogsPanel compact height={140} title={logs.target.runId ? 'Live output · this run' : `Live output · ${logs.target.library}`} target={logs.target}
      highlightInvocation={logs.invocation} onState={readiness.onState} emptyText={running ? 'Waiting for output...' : 'No output.'} />}
    {error && <Alert severity="error" title="Run failed" bottomSpacing={0}>{error}</Alert>}
    {debugging && <DebugDrawer target={target} input={debugging} onClose={() => setDebugging(null)} />}
    {result && <>
      <RunResultView result={result} onJumpToLine={onJumpToLine} />
      <Text color="secondary" variant="bodySmall">{mode === 'unsaved' ? 'Ran unsaved source in a temporary isolated worker; nothing was saved or sent.' : 'Ran in the saved library worker.'}</Text>
    </>}
  </Stack>
}

function safeObject(text: string): Record<string, unknown> | undefined {
  try { return objectInput(text, 'Fields') } catch { return undefined }
}

const getStyles = (theme: GrafanaTheme2) => ({
  row: css({ display: 'flex', alignItems: 'flex-end', flexWrap: 'wrap', gap: theme.spacing(1) }),
  picker: css({ minWidth: 240, flex: '0 1 320px' }),
  inputs: css({ display: 'grid', gap: theme.spacing(2), gridTemplateColumns: 'repeat(auto-fit, minmax(220px, 1fr))' }),
})
