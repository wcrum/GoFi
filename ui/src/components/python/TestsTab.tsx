import { useState } from 'react'
import { css } from '@emotion/css'
import type { GrafanaTheme2 } from '@grafana/data'
import { Badge, Button, Card, Checkbox, Combobox, EmptyState, Field, IconButton, Input, RadioButtonGroup, Stack, Text, TextArea, useStyles2 } from '@grafana/ui'
import type { FunctionTest, PythonFunctionKind, PythonRunResult } from '../../sandbox-api'
import { discoverFunctions, evaluateTest, inputPreview, type SourceHighlights } from '../canvas/python-templates'
import { RunResultView } from './RunResultView'
import { objectInput, runPython, type RunTarget } from './run'
import { functionKey } from './RunPanel'
import { DebugDrawer } from './debugger/DebugDrawer'
import { StatusIndicator } from '../StatusIndicator'

interface TestRun { status: 'pass' | 'fail' | 'recorded' | 'error'; reasons: string[]; result?: PythonRunResult }
const RAW_KINDS: PythonFunctionKind[] = ['framer', 'decoder', 'parser', 'encoder']

/** Saved sample inputs with optional expectations. Tests are part of the
 * library definition and persist with Save and launch. */
export function TestsTab({ tests, onChange, target, readOnly, onJumpToLine, onHighlight }: { tests: FunctionTest[]; onChange: (tests: FunctionTest[]) => void; target: RunTarget; readOnly?: boolean; onJumpToLine: (line: number, column?: number) => void; onHighlight?: (highlights: SourceHighlights) => void }) {
  const [runs, setRuns] = useState<Record<number, TestRun>>({})
  const [running, setRunning] = useState<number | 'all' | null>(null)
  const [trace, setTrace] = useState(false)
  const [debugging, setDebugging] = useState<FunctionTest | null>(null)
  const discovered = discoverFunctions(target.source)
  /** Editor highlights for one test: where its input enters, executed lines, error. */
  const highlightsFor = (test: FunctionTest, result?: PythonRunResult): SourceHighlights => {
    const fn = discovered.find(item => item.kind === test.kind && item.name === test.function)
    const line = result?.entryLine || fn?.defLine
    return { entry: line ? { line, label: `${test.function} · ${test.name}`, preview: inputPreview({ raw: test.raw, fields: test.fields }) } : undefined, executed: result?.executedLines, error: result?.errorLocation?.line }
  }
  const showInSource = (index: number) => {
    const highlights = highlightsFor(tests[index], runs[index]?.result)
    onHighlight?.(highlights)
    if (highlights.entry) onJumpToLine(highlights.entry.line)
  }
  const update = (index: number, test: FunctionTest) => { onChange(tests.map((item, position) => position === index ? test : item)); setRuns(previous => { const next = { ...previous }; delete next[index]; return next }) }
  const add = () => {
    const fn = discovered.find(item => item.kind === 'transformer') ?? discovered[0]
    onChange([...tests, { name: `Test ${tests.length + 1}`, function: fn?.name ?? '', kind: fn?.kind ?? 'transformer', raw: '', fields: { sensor: 'alpha', temperature: 28 }, meta: {} }])
  }
  async function runOne(index: number): Promise<TestRun> {
    const test = tests[index]
    try {
      const { result } = await runPython(target, { kind: test.kind, name: test.function, raw: test.raw ?? '', fields: test.fields ?? {}, meta: test.meta ?? {}, config: test.config, traceLines: trace })
      const verdict = evaluateTest(test, result)
      return { ...verdict, result }
    } catch (error) { return { status: 'error', reasons: [error instanceof Error ? error.message : String(error)] } }
  }
  async function run(index: number) { setRunning(index); const outcome = await runOne(index); setRuns(previous => ({ ...previous, [index]: outcome })); setRunning(null); onHighlight?.(highlightsFor(tests[index], outcome.result)) }
  async function runAll() {
    setRunning('all')
    const outcomes: Record<number, TestRun> = {}
    for (let index = 0; index < tests.length; index++) outcomes[index] = await runOne(index)
    setRuns(outcomes); setRunning(null)
  }
  const summary = Object.values(runs)
  const passed = summary.filter(run => run.status === 'pass').length
  const failed = summary.filter(run => run.status === 'fail' || run.status === 'error').length
  if (!tests.length) return <EmptyState variant="call-to-action" message="No saved tests" button={!readOnly ? <Button icon="plus" onClick={add}>Add test</Button> : undefined}>
    Save sample inputs for this library's functions, optionally with the expected output, then run them all after each change. Use <b>Save as test</b> in the Run panel to capture a result.
  </EmptyState>
  return <Stack direction="column" gap={2}>
    <Stack gap={1} alignItems="center" wrap="wrap">
      <Button icon={running === 'all' ? 'spinner' : 'play'} disabled={running !== null} onClick={() => void runAll()}>Run all ({tests.length})</Button>
      {!readOnly && <Button variant="secondary" icon="plus" onClick={add}>Add test</Button>}
      <Checkbox label="Trace lines" description="Highlight executed lines in Source" value={trace} onChange={event => setTrace(event.currentTarget.checked)} />
      {summary.length > 0 && <><StatusIndicator tone="success" text={`${passed} passed`} />{failed > 0 && <StatusIndicator tone="error" text={`${failed} failed`} />}</>}
      <Text color="secondary" variant="bodySmall">{target.savedLibrary ? 'Runs against the saved worker.' : 'Runs the unsaved source in a temporary isolated worker.'} Save and launch to keep test changes.</Text>
    </Stack>
    {tests.map((test, index) => <TestCard key={index} test={test} run={runs[index]} running={running === index || running === 'all'} readOnly={readOnly}
      functions={discovered.map(fn => ({ value: functionKey(fn.kind, fn.name), label: fn.name, description: fn.kind }))}
      onChange={next => update(index, next)} onRun={() => void run(index)} onDelete={() => { onChange(tests.filter((_, position) => position !== index)); setRuns({}) }} onJumpToLine={onJumpToLine} onShowInSource={() => showInSource(index)} onDebug={() => setDebugging(test)} />)}
    {debugging && <DebugDrawer target={target} input={{ kind: debugging.kind, name: debugging.function, raw: debugging.raw ?? '', fields: debugging.fields ?? {}, meta: debugging.meta ?? {}, config: debugging.config }} onClose={() => setDebugging(null)} />}
  </Stack>
}

function TestCard({ test, run, running, readOnly, functions, onChange, onRun, onDelete, onJumpToLine, onShowInSource, onDebug }: { test: FunctionTest; run?: TestRun; running: boolean; readOnly?: boolean; functions: { value: string; label: string; description: string }[]; onChange: (test: FunctionTest) => void; onRun: () => void; onDelete: () => void; onJumpToLine: (line: number, column?: number) => void; onShowInSource: () => void; onDebug: () => void }) {
  const styles = useStyles2(getStyles)
  const [open, setOpen] = useState(false)
  const takesRaw = RAW_KINDS.includes(test.kind)
  const expectation = test.expectDrop === true ? 'drop' : test.expectFields && Object.keys(test.expectFields).length ? 'fields' : test.expectDrop === false ? 'pass' : 'none'
  const statusBadge = !run ? <Badge color="darkgrey" text="Not run" /> : run.status === 'pass' ? <Badge color="green" icon="check" text="Pass" /> : run.status === 'recorded' ? <Badge color="blue" text="Recorded" tooltip="No expectation set; the result is shown for review." /> : <Badge color="red" icon="exclamation-triangle" text={run.status === 'error' ? 'Error' : 'Fail'} />
  return <Card noMargin isCompact>
    <Card.Heading>{test.name || 'Untitled test'}</Card.Heading>
    <Card.Meta>{[`${test.function || '—'} · ${test.kind}`, expectation === 'none' ? 'no expectation' : expectation === 'drop' ? 'expects drop' : expectation === 'pass' ? 'expects pass' : 'expects fields']}</Card.Meta>
    <Card.Tags>{statusBadge}</Card.Tags>
    <Card.Actions>
      <Button size="sm" icon={running ? 'spinner' : 'play'} disabled={running} onClick={onRun}>Run</Button>
      <Button size="sm" variant="secondary" icon="bug" disabled={running} onClick={onDebug} tooltip="Record this test step by step and open the debugger">Debug</Button>
      <Button size="sm" variant="secondary" fill="outline" icon={open ? 'angle-up' : 'angle-down'} onClick={() => setOpen(value => !value)}>{open ? 'Hide' : readOnly ? 'Details' : 'Edit'}</Button>
      <Button size="sm" variant="secondary" fill="text" icon="arrow-right" onClick={onShowInSource} tooltip="Highlight where this test's input enters the source">Show in source</Button>
    </Card.Actions>
    {!readOnly && <Card.SecondaryActions><IconButton name="trash-alt" tooltip="Delete test" onClick={onDelete} /></Card.SecondaryActions>}
    {(open || run) && <Card.Description>
      <div className={styles.body}>
        {open && <div className={styles.grid}>
          <Field label="Name" noMargin><Input value={test.name} disabled={readOnly} onChange={event => onChange({ ...test, name: event.currentTarget.value })} /></Field>
          <Field label="Function" noMargin><Combobox value={functionKey(test.kind, test.function)} options={functions.length ? functions : [{ value: functionKey(test.kind, test.function), label: test.function, description: test.kind }]} disabled={readOnly} onChange={option => { if (!option) return; const [kind, ...rest] = option.value.split(':'); onChange({ ...test, kind: kind as PythonFunctionKind, function: rest.join(':') }) }} /></Field>
          {takesRaw ? <JsonOrText label="Raw input" text value={test.raw ?? ''} readOnly={readOnly} onChange={raw => onChange({ ...test, raw: raw as string })} />
            : <JsonOrText label="Fields (JSON)" value={test.fields ?? {}} readOnly={readOnly} onChange={fields => onChange({ ...test, fields: fields as Record<string, unknown> })} />}
          <JsonOrText label="Metadata (JSON)" value={test.meta ?? {}} readOnly={readOnly} onChange={meta => onChange({ ...test, meta: meta as Record<string, unknown> })} />
          <JsonOrText label="Config (JSON)" value={test.config ?? {}} readOnly={readOnly} onChange={config => onChange({ ...test, config: Object.keys(config as object).length ? config as Record<string, unknown> : undefined })} />
          <Field label="Expectation" noMargin>
            <RadioButtonGroup size="sm" disabled={readOnly} value={expectation} onChange={value => onChange({ ...test, expectDrop: value === 'drop' ? true : value === 'pass' || value === 'fields' ? false : undefined, expectFields: value === 'fields' ? (test.expectFields ?? run?.result?.fields ?? {}) : undefined })}
              options={[{ value: 'none', label: 'Record' }, { value: 'pass', label: 'Passes' }, { value: 'fields', label: 'Fields' }, { value: 'drop', label: 'Drops' }]} />
          </Field>
          {expectation === 'fields' && <JsonOrText label="Expected fields (subset match)" value={test.expectFields ?? {}} readOnly={readOnly} onChange={expectFields => onChange({ ...test, expectFields: expectFields as Record<string, unknown> })} />}
        </div>}
        {run && run.reasons.length > 0 && <ul className={styles.reasons}>{run.reasons.map(reason => <li key={reason}><Text color="error" variant="bodySmall">{reason}</Text></li>)}</ul>}
        {run?.result && <RunResultView result={run.result} onJumpToLine={onJumpToLine} compact />}
      </div>
    </Card.Description>}
  </Card>
}

/** A JSON object (or plain text) editor that only commits valid input. */
function JsonOrText({ label, value, onChange, readOnly, text }: { label: string; value: unknown; onChange: (value: unknown) => void; readOnly?: boolean; text?: boolean }) {
  const [draft, setDraft] = useState(text ? String(value) : JSON.stringify(value, null, 2))
  const [error, setError] = useState('')
  return <Field label={label} invalid={!!error} error={error} noMargin>
    <TextArea rows={3} value={draft} disabled={readOnly} aria-label={label} onChange={event => {
      const next = event.currentTarget.value
      setDraft(next)
      if (text) { onChange(next); return }
      try { onChange(objectInput(next, label)); setError('') } catch (reason) { setError(reason instanceof Error ? reason.message : String(reason)) }
    }} />
  </Field>
}

const getStyles = (theme: GrafanaTheme2) => ({
  body: css({ display: 'flex', flexDirection: 'column', gap: theme.spacing(1.5), marginTop: theme.spacing(1) }),
  grid: css({ display: 'grid', gap: theme.spacing(1.5), gridTemplateColumns: 'repeat(auto-fit, minmax(220px, 1fr))' }),
  reasons: css({ margin: 0, paddingLeft: theme.spacing(2) }),
})
