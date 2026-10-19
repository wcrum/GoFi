import { lazy, Suspense, useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { css, cx } from '@emotion/css'
import type { GrafanaTheme2, IconName } from '@grafana/data'
import { Alert, Button, IconButton, Input, PanelChrome, Stack, Tab, TabsBar, Text, ToolbarButton, ToolbarButtonRow, useStyles2 } from '@grafana/ui'
import { RunResultView } from '../RunResultView'
import { WorkerLogsPanel, type LogTarget } from '../WorkerLogsPanel'
import type { DebugRecording, DebugStep } from './debug-api'
import { continueToBreakpoint, frameStep, globalsAt, stdoutAt, stepBack, stepInto, stepOut, stepOver } from './navigation'
import { VariableTree } from './VariableTree'
import { StatusIndicator } from '../../StatusIndicator'
import { PageLoading } from '../../PageLoading'
import { t, Trans } from '../../../i18n'

const PythonSourceEditor = lazy(() => import('../../PythonSourceEditor'))

export interface DebugInputView { raw?: string; fields?: Record<string, unknown>; meta?: Record<string, unknown>; config?: Record<string, unknown> }
type Action = 'back' | 'over' | 'into' | 'out' | 'continue' | 'restart' | 'end'
// F-keys match common debuggers; letters work everywhere (F5/F11 may be
// reserved by the browser or OS in some environments).
const ACTIONS: { id: Action; icon: IconName; keys: string }[] = [
  { id: 'restart', icon: 'history-alt', keys: 'R' },
  { id: 'back', icon: 'step-backward', keys: 'Shift+F10 or B' },
  { id: 'over', icon: 'corner-up-right', keys: 'F10 or N' },
  { id: 'into', icon: 'arrow-down', keys: 'F11 or S' },
  { id: 'out', icon: 'arrow-up', keys: 'Shift+F11 or O' },
  { id: 'continue', icon: 'play', keys: 'F5 or C' },
  { id: 'end', icon: 'angle-double-right', keys: 'E' },
]
function actionLabel(action: Action): string {
  switch (action) {
    case 'restart': return t('gofi.python.debugger.action-restart', 'Restart')
    case 'back': return t('gofi.python.debugger.action-step-back', 'Step back')
    case 'over': return t('gofi.python.debugger.action-step-over', 'Step over')
    case 'into': return t('gofi.python.debugger.action-step-into', 'Step into')
    case 'out': return t('gofi.python.debugger.action-step-out', 'Step out')
    case 'continue': return t('gofi.python.debugger.action-continue', 'Continue to breakpoint')
    case 'end': return t('gofi.python.debugger.action-go-to-end', 'Go to end')
  }
}
function actionForKey(event: KeyboardEvent): Action | null {
  if (event.ctrlKey || event.metaKey || event.altKey) return null
  switch (event.key) {
    case 'F10': return event.shiftKey ? 'back' : 'over'
    case 'F11': return event.shiftKey ? 'out' : 'into'
    case 'F5': return 'continue'
  }
  const target = event.target as HTMLElement | null
  if (target && (target.closest('input, textarea, [contenteditable="true"]'))) return null
  switch (event.key.toLowerCase()) {
    case 'n': return 'over'
    case 's': return 'into'
    case 'o': return 'out'
    case 'b': return 'back'
    case 'c': return 'continue'
    case 'r': return 'restart'
    case 'e': return 'end'
  }
  return null
}

const storageKey = (key: string) => `gofi.debugger.breakpoints.${key}`
function loadBreakpoints(key: string): number[] {
  try { const value: unknown = JSON.parse(localStorage.getItem(storageKey(key)) ?? '[]'); return Array.isArray(value) ? value.filter((line): line is number => Number.isInteger(line) && line > 0) : [] } catch { return [] }
}
function saveBreakpoints(key: string, lines: number[]) {
  try { localStorage.setItem(storageKey(key), JSON.stringify(lines)) } catch { /* storage unavailable */ }
}

/** "Recording truncated" alert body for the worker's stated reason. */
function truncationText(reason: string, count: number): string {
  if (reason === 'deadline') return t('gofi.python.debugger.truncated-deadline', 'The worker stopped recording after {{count}} steps at the deadline. Steps past that point are not available; the final result below is still the real outcome.', { count })
  if (reason === 'size') return t('gofi.python.debugger.truncated-size', 'The worker stopped recording after {{count}} steps at the size limit. Steps past that point are not available; the final result below is still the real outcome.', { count })
  return t('gofi.python.debugger.truncated-other', 'The worker stopped recording after {{count}} steps. Steps past that point are not available; the final result below is still the real outcome.', { count })
}

/** Record-and-replay step debugger: every step was recorded by the isolated
 * worker; stepping only moves through the recording (forward and back). */
export function PythonDebugger({ recording, source, breakpointKey, input, watches, onWatchesChange, onRerecord, rerecording, logTarget, title }: {
  recording: DebugRecording
  /** Library-coordinate source the recording's line numbers refer to. */
  source: string
  /** Breakpoints persist per library (or "unsaved"). */
  breakpointKey: string
  input: DebugInputView
  watches: string[]
  onWatchesChange: (watches: string[]) => void
  /** Records again (e.g. with new watches). */
  onRerecord: () => void
  rerecording?: boolean
  logTarget?: LogTarget
  title?: string
}) {
  const styles = useStyles2(getStyles)
  const steps = recording.steps
  const [index, setIndex] = useState(0)
  const [breakpoints, setBreakpoints] = useState<number[]>(() => loadBreakpoints(breakpointKey))
  const [inputTab, setInputTab] = useState<'raw' | 'fields' | 'meta' | 'config'>(input.fields && Object.keys(input.fields).length ? 'fields' : 'raw')
  const root = useRef<HTMLDivElement>(null)
  useEffect(() => { setIndex(0) }, [recording])
  const step: DebugStep | undefined = steps[index]

  const move = useCallback((action: Action) => {
    setIndex(current => {
      switch (action) {
        case 'back': return stepBack(steps, current)
        case 'over': return stepOver(steps, current)
        case 'into': return stepInto(steps, current)
        case 'out': return stepOut(steps, current)
        case 'continue': return continueToBreakpoint(steps, current, breakpoints)
        case 'restart': return 0
        case 'end': return Math.max(0, steps.length - 1)
      }
    })
  }, [steps, breakpoints])
  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      if (!root.current?.isConnected) return
      const action = actionForKey(event)
      if (!action) return
      event.preventDefault(); event.stopPropagation()
      move(action)
    }
    window.addEventListener('keydown', onKey, true)
    return () => window.removeEventListener('keydown', onKey, true)
  }, [move])

  const toggleBreakpoint = useCallback((line: number) => {
    setBreakpoints(previous => {
      const next = previous.includes(line) ? previous.filter(item => item !== line) : [...previous, line].sort((a, b) => a - b)
      saveBreakpoints(breakpointKey, next)
      return next
    })
  }, [breakpointKey])
  const globals = useMemo(() => globalsAt(recording.initialGlobals, steps, index), [recording.initialGlobals, steps, index])
  const stdout = stdoutAt(recording.result?.stdout ?? '', step)
  const stack = step?.stack ?? []
  const atEnd = index >= steps.length - 1
  const debug = useMemo(() => ({ currentLine: step?.line, breakpoints, onToggleBreakpoint: toggleBreakpoint }), [step?.line, breakpoints, toggleBreakpoint])
  const highlights = useMemo(() => ({ entry: recording.entryLine ? { line: recording.entryLine, label: steps[0]?.function ?? t('gofi.common.function', 'function') } : undefined, error: recording.result?.errorLocation?.line }), [recording, steps])

  const toolbar = <div className={styles.toolbar}>
    <ToolbarButtonRow alignment="left">
      {ACTIONS.map(action => {
        const label = actionLabel(action.id)
        return <ToolbarButton key={action.id} icon={action.icon} tooltip={t('gofi.python.debugger.action-shortcut', '{{label}} ({{keys}})', { label, keys: action.keys })} aria-label={label} disabled={!steps.length || (action.id === 'back' || action.id === 'restart' ? index === 0 : atEnd)} onClick={() => move(action.id)} />
      })}
    </ToolbarButtonRow>
    <input type="range" className={styles.slider} min={0} max={Math.max(0, steps.length - 1)} value={index} onChange={event => setIndex(Number(event.currentTarget.value))} aria-label={t('gofi.python.debugger.step-slider-aria', 'Step')} disabled={!steps.length} />
    <Text variant="bodySmall" color="secondary" tabular>{t('gofi.python.debugger.step-position', 'step {{current}} / {{total}}', { current: steps.length ? index + 1 : 0, total: steps.length })}</Text>
    {step && <StatusIndicator tone={step.event === 'exception' ? 'error' : step.event === 'return' ? 'success' : step.event === 'call' ? 'info' : 'neutral'} text={`${step.event} · ${step.function}:${step.line}`} />}
    {breakpoints.length > 0 && <Button size="sm" variant="secondary" fill="text" icon="times" onClick={() => { setBreakpoints([]); saveBreakpoints(breakpointKey, []) }} tooltip={t('gofi.python.debugger.remove-breakpoints', 'Remove all breakpoints')}>{t('gofi.python.debugger.breakpoint-count', '{{count}} breakpoints', { count: breakpoints.length })}</Button>}
  </div>

  return <div ref={root} className={styles.root} tabIndex={-1} aria-label={title ?? t('gofi.python.debugger.title', 'Python debugger')}>
    {toolbar}
    {recording.truncated && <Alert severity="warning" title={t('gofi.python.debugger.truncated-title', 'Recording truncated ({{reason}})', { reason: recording.truncationReason || t('gofi.python.debugger.truncated-reason-limit', 'limit') })} bottomSpacing={0}>
      {truncationText(recording.truncationReason, steps.length)}
    </Alert>}
    {!steps.length && <Alert severity="info" title={t('gofi.python.debugger.no-steps-title', 'No steps recorded')} bottomSpacing={0}>{t('gofi.python.debugger.no-steps-body', 'The function did not execute any traced lines (for example, it was not found or failed to load).')}</Alert>}
    <div className={styles.layout}>
      <div className={styles.main}>
        <Suspense fallback={<PageLoading text={t('gofi.common.loading-editor', 'Loading editor...')} />}>
          <PythonSourceEditor source={source} onChange={() => {}} diagnostics={[]} disabled height="380px" ariaLabel={t('gofi.python.debugger.source-aria', 'Debugged source')} highlights={highlights} debug={debug} />
        </Suspense>
        <Text variant="bodySmall" color="secondary">
          <Trans i18nKey="gofi.python.debugger.breakpoints-help" values={{ key: breakpointKey }}>Click the gutter to toggle breakpoints. Breakpoints are kept for <Text variant="code">{'{{key}}'}</Text> in this browser.</Trans>
        </Text>
        <div className={styles.pair}>
          <PanelChrome title={t('gofi.python.debugger.input-title', 'Input')}>
            <TabsBar hideBorder>{(['raw', 'fields', 'meta', 'config'] as const).map(id => <Tab key={id} label={id} active={inputTab === id} onChangeTab={() => setInputTab(id)} />)}</TabsBar>
            <pre className={styles.pre}>{inputTab === 'raw' ? (input.raw || t('gofi.python.debugger.input-empty', '(empty)')) : JSON.stringify(input[inputTab] ?? {}, null, 2)}</pre>
          </PanelChrome>
          <PanelChrome title={t('gofi.python.debugger.output-title', 'Output')} description={t('gofi.python.debugger.output-description', 'stdout printed up to this step; the final result of the run.')}>
            <pre className={cx(styles.pre, styles.stdout)} aria-label={t('gofi.python.debugger.stdout-aria', 'stdout at step')}>{stdout || t('gofi.python.debugger.no-output-yet', '(no output yet)')}</pre>
            {step?.returnValue && <div className={styles.event}><Text variant="bodySmall" color="success">{t('gofi.python.debugger.returned', 'Returned')}</Text><VariableTree variables={[step.returnValue]} /></div>}
            {step?.exception && <Alert severity="error" title={t('gofi.python.debugger.exception-title', 'Exception at this step')} bottomSpacing={0}>{step.exception}</Alert>}
            {atEnd && recording.result && <RunResultView result={recording.result} compact />}
          </PanelChrome>
        </div>
        {logTarget && <WorkerLogsPanel compact height={120} title={t('gofi.python.debugger.worker-logs-title', 'Worker logs · debug run')} target={logTarget} highlightInvocation={recording.runId || logTarget.runId} />}
      </div>
      <div className={styles.side}>
        <PanelChrome title={t('gofi.python.debugger.variables-title', 'Variables')} description={t('gofi.python.debugger.variables-description', 'Locals of the current frame at this step.')}>
          <VariableTree variables={step?.locals ?? []} empty={t('gofi.python.debugger.no-locals', 'No locals at this step.')} />
        </PanelChrome>
        <PanelChrome title={t('gofi.python.debugger.watches-title', 'Watches')} description={t('gofi.python.debugger.watches-description', 'Python expressions evaluated in the worker at every step. Changes apply on the next recording.')}>
          <WatchList watches={watches} values={step?.watches ?? []} onChange={onWatchesChange} onRerecord={onRerecord} rerecording={rerecording} />
        </PanelChrome>
        <PanelChrome title={t('gofi.python.debugger.call-stack-title', 'Call stack')}>
          <ul className={styles.stack}>{[...stack].reverse().map((frame, position) => {
            const depth = (step?.depth ?? 0) - position
            return <li key={position}><button type="button" className={cx(styles.frame, position === 0 && styles.frameCurrent)} onClick={() => setIndex(frameStep(steps, index, depth))}>
              <span>{frame.function}</span><span className={styles.muted}>{t('gofi.python.common.line-number', 'line {{line}}', { line: frame.line })}</span>
            </button></li>
          })}{!stack.length && <li><Text color="secondary" italic variant="bodySmall">{t('gofi.python.debugger.no-frames', 'No frames.')}</Text></li>}</ul>
        </PanelChrome>
        <PanelChrome title={t('gofi.python.debugger.globals-title', 'Library globals')} description={t('gofi.python.debugger.globals-description', 'Module globals after loading, with changes applied up to this step.')}>
          <VariableTree variables={globals} empty={t('gofi.python.debugger.no-globals', 'No module globals.')} />
        </PanelChrome>
      </div>
    </div>
  </div>
}

function WatchList({ watches, values, onChange, onRerecord, rerecording }: { watches: string[]; values: { expression: string; repr: string; error: string }[]; onChange: (watches: string[]) => void; onRerecord: () => void; rerecording?: boolean }) {
  const styles = useStyles2(getStyles)
  const [draft, setDraft] = useState('')
  const recorded = new Map(values.map(value => [value.expression, value]))
  const pending = watches.some(watch => !recorded.has(watch))
  const add = () => { const value = draft.trim(); if (value && !watches.includes(value)) onChange([...watches, value]); setDraft('') }
  return <Stack direction="column" gap={1}>
    {watches.map(watch => {
      const value = recorded.get(watch)
      return <div key={watch} className={styles.watch}>
        <span className={styles.watchExpr}>{watch}</span>
        <span className={cx(styles.watchValue, value?.error && styles.watchError)}>{value ? (value.error ? value.error : value.repr) : t('gofi.python.debugger.watch-pending', '— record again')}</span>
        <IconButton name="times" size="sm" aria-label={t('gofi.python.debugger.remove-watch-aria', 'Remove watch {{expression}}', { expression: watch })} onClick={() => onChange(watches.filter(item => item !== watch))} />
      </div>
    })}
    <Stack gap={1}>
      <Input value={draft} placeholder={'fields["temperature"] * 2'} aria-label={t('gofi.python.debugger.new-watch-aria', 'New watch expression')} onChange={event => setDraft(event.currentTarget.value)} onKeyDown={event => { if (event.key === 'Enter') add() }} />
      <Button variant="secondary" icon="plus" onClick={add} disabled={!draft.trim()}>{t('gofi.common.add', 'Add')}</Button>
    </Stack>
    <div><Button size="sm" icon={rerecording ? 'spinner' : 'history-alt'} variant={pending ? 'primary' : 'secondary'} disabled={rerecording} onClick={onRerecord}>{rerecording ? t('gofi.python.debugger.recording', 'Recording...') : t('gofi.python.debugger.record-again', 'Record again')}</Button></div>
  </Stack>
}

const getStyles = (theme: GrafanaTheme2) => ({
  root: css({ display: 'flex', flexDirection: 'column', gap: theme.spacing(1.5), minWidth: 0, outline: 'none' }),
  toolbar: css({ display: 'flex', alignItems: 'center', flexWrap: 'wrap', gap: theme.spacing(1, 2), padding: theme.spacing(1), background: theme.colors.background.secondary, border: `1px solid ${theme.colors.border.weak}`, borderRadius: theme.shape.radius.default, position: 'sticky', top: 0, zIndex: 2 }),
  slider: css({ flex: '1 1 160px', minWidth: 120, accentColor: theme.colors.primary.main }),
  layout: css({ display: 'grid', gap: theme.spacing(2), gridTemplateColumns: 'minmax(0, 1fr)', [theme.breakpoints.up('lg')]: { gridTemplateColumns: 'minmax(0, 3fr) minmax(300px, 2fr)' } }),
  main: css({ display: 'flex', flexDirection: 'column', gap: theme.spacing(1.5), minWidth: 0 }),
  side: css({ display: 'flex', flexDirection: 'column', gap: theme.spacing(1.5), minWidth: 0 }),
  pair: css({ display: 'grid', gap: theme.spacing(1.5), gridTemplateColumns: 'repeat(auto-fit, minmax(240px, 1fr))' }),
  pre: css({ margin: 0, padding: theme.spacing(1), maxHeight: 200, overflow: 'auto', fontFamily: theme.typography.fontFamilyMonospace, fontSize: theme.typography.bodySmall.fontSize, whiteSpace: 'pre-wrap', wordBreak: 'break-word', background: theme.colors.background.canvas, borderRadius: theme.shape.radius.default }),
  stdout: css({ marginBottom: theme.spacing(1) }),
  event: css({ marginBottom: theme.spacing(1) }),
  stack: css({ listStyle: 'none', margin: 0, padding: 0 }),
  frame: css({ display: 'flex', justifyContent: 'space-between', width: '100%', gap: theme.spacing(1), padding: theme.spacing(0.5, 1), border: 'none', background: 'transparent', color: theme.colors.text.primary, fontFamily: theme.typography.fontFamilyMonospace, fontSize: theme.typography.bodySmall.fontSize, borderBottom: `1px solid ${theme.colors.border.weak}`, textAlign: 'left', '&:hover': { background: theme.colors.action.hover } }),
  frameCurrent: css({ background: theme.colors.warning.transparent }),
  muted: css({ color: theme.colors.text.secondary }),
  watch: css({ display: 'grid', gridTemplateColumns: 'minmax(0, 1fr) minmax(0, 1fr) auto', gap: theme.spacing(1), alignItems: 'center', fontFamily: theme.typography.fontFamilyMonospace, fontSize: theme.typography.bodySmall.fontSize, padding: theme.spacing(0.25, 0), borderBottom: `1px solid ${theme.colors.border.weak}` }),
  watchExpr: css({ color: theme.colors.text.primary, overflow: 'hidden', textOverflow: 'ellipsis' }),
  watchValue: css({ color: theme.colors.primary.text, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }),
  watchError: css({ color: theme.colors.error.text }),
})
