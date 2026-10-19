import { useEffect, useMemo, useRef, useState } from 'react'
import { css, cx } from '@emotion/css'
import type { GrafanaTheme2 } from '@grafana/data'
import type { IconName } from '@grafana/data'
import { Button, FilterInput, Icon, InlineSwitch, PanelChrome, Stack, Text, useStyles2 } from '@grafana/ui'
import { watchWorkerLogs, type LogStreamState, type WorkerLogEntry } from '../../sandbox-api'
import { appendBounded, logTime, shortId } from '../canvas/python-templates'
import { StatusIndicator } from '../StatusIndicator'
import { buildLogItems, filterLogItems, formatDuration, type LifecycleTone, type LineSeverity, type LogItem, type RunOutcome } from './log-model'
import { PanelEmpty } from '../PanelEmpty'
import { t } from '../../i18n'

const MAX_LINES = 1000
export interface LogTarget { library?: string; runId?: string }
const targetKey = (target: LogTarget | null) => target ? `${target.library ?? ''}|${target.runId ?? ''}` : ''

/** Live worker output in Grafana Explore style. Follows a saved library's
 * worker, or one unsaved run's short-lived worker until it exits. */
export function WorkerLogsPanel({ target, highlightInvocation, compact, height, title: titleProp, onState, emptyText, onJumpToLine }: {
  target: LogTarget | null
  /** Rows of this invocation are emphasized; "Only this run" filters to them. */
  highlightInvocation?: string
  compact?: boolean
  height?: number
  title?: string
  onState?: (state: LogStreamState) => void
  emptyText?: string
  /** Error lines link to the editor; `fn` identifies the function (or inline stage). */
  onJumpToLine?: (line: number, fn: string) => void
}) {
  const styles = useStyles2(getStyles)
  const title = titleProp ?? t('gofi.python.worker-logs.title', 'Worker logs')
  const [entries, setEntries] = useState<WorkerLogEntry[]>([])
  const [state, setState] = useState<LogStreamState>('connecting')
  const [detail, setDetail] = useState('')
  const [paused, setPaused] = useState(false)
  const [skipped, setSkipped] = useState(0)
  const [query, setQuery] = useState('')
  const [onlyRun, setOnlyRun] = useState(false)
  const [follow, setFollow] = useState(true)
  const pausedRef = useRef(false)
  const onStateRef = useRef(onState)
  const scroller = useRef<HTMLDivElement>(null)
  pausedRef.current = paused
  onStateRef.current = onState

  const key = targetKey(target)
  useEffect(() => {
    setEntries([]); setSkipped(0); setDetail('')
    if (!target) return
    return watchWorkerLogs(target, batch => {
      if (pausedRef.current) { setSkipped(count => count + batch.length); return }
      setEntries(previous => appendBounded(previous, batch, MAX_LINES))
    }, (next, message) => { setState(next); setDetail(message ?? ''); onStateRef.current?.(next) })
  }, [key]) // eslint-disable-line react-hooks/exhaustive-deps

  const [showRuntime, setShowRuntime] = useState(false)
  const [collapsed, setCollapsed] = useState<Set<string>>(() => new Set())
  const items = useMemo(() => buildLogItems(entries, showRuntime), [entries, showRuntime])
  const visible = useMemo(() => filterLogItems(items, query, onlyRun ? highlightInvocation : undefined), [items, query, onlyRun, highlightInvocation])
  const runCount = items.filter(item => item.type === 'run').length
  // Newest first: following means staying at the top.
  const newestFirst = useMemo(() => [...visible].reverse(), [visible])
  useEffect(() => { if (follow && scroller.current) scroller.current.scrollTop = 0 }, [visible, follow])
  const toggle = (key: string) => setCollapsed(previous => { const next = new Set(previous); if (next.has(key)) next.delete(key); else next.add(key); return next })

  const stateIndicator = state === 'live' ? <StatusIndicator tone="success" pulse text={target?.runId ? t('gofi.python.worker-logs.state-streaming-run', 'Streaming run') : t('gofi.python.worker-logs.state-live', 'Live')} />
    : state === 'ended' ? <StatusIndicator tone="neutral" text={t('gofi.python.worker-logs.state-exited', 'Worker exited')} />
    : state === 'unavailable' ? <StatusIndicator tone="warning" text={t('gofi.python.worker-logs.state-unavailable', 'Unavailable')} tooltip={detail || undefined} />
    : state === 'reconnecting' ? <StatusIndicator tone="warning" text={t('gofi.python.worker-logs.state-reconnecting', 'Reconnecting…')} tooltip={detail || undefined} />
    : <StatusIndicator tone="info" text={t('gofi.python.worker-logs.state-connecting', 'Connecting…')} />
  const actions = <Stack gap={1} alignItems="center">
    {stateIndicator}
    <Button size="sm" variant="secondary" fill="outline" icon={paused ? 'play' : 'pause'} onClick={() => { setPaused(value => !value); setSkipped(0) }}>{paused ? t('gofi.common.resume', 'Resume') : t('gofi.common.pause', 'Pause')}</Button>
    <Button size="sm" variant="secondary" fill="outline" icon="trash-alt" onClick={() => { setEntries([]); setSkipped(0) }} disabled={!entries.length}>{t('gofi.common.clear', 'Clear')}</Button>
  </Stack>
  const summary = [
    t('gofi.python.worker-logs.run-count', '{{count}} runs', { count: runCount }),
    t('gofi.python.worker-logs.line-count', '{{count}} lines', { count: entries.length }),
    ...(paused && skipped ? [t('gofi.python.worker-logs.arrived-while-paused', '{{count}} arrived while paused', { count: skipped })] : []),
  ].join(' · ')
  const emptyMessage = (state === 'unavailable' ? detail
    : !target ? (emptyText ?? t('gofi.python.worker-logs.empty-nothing-to-follow', 'Nothing to follow.'))
    : entries.length ? t('gofi.python.worker-logs.empty-no-match', 'No lines match the filter.')
    : (emptyText ?? (target.runId ? t('gofi.python.worker-logs.empty-waiting-run', 'Waiting for the run to start…') : t('gofi.python.worker-logs.empty-waiting-output', 'Waiting for worker output…'))))
    || t('gofi.python.worker-logs.empty-nothing-to-show', 'Nothing to show.')

  return <PanelChrome title={title} padding="none" actions={actions}>
    <div className={styles.toolbar}>
      <div className={styles.filter}><FilterInput value={query} onChange={setQuery} placeholder={t('gofi.python.worker-logs.filter-placeholder', 'Filter lines, functions, or run ids')} /></div>
      {highlightInvocation && <InlineSwitch transparent label={t('gofi.python.worker-logs.only-this-run', 'Only this run')} showLabel value={onlyRun} onChange={event => setOnlyRun(event.currentTarget.checked)} />}
      <InlineSwitch transparent label={t('gofi.python.worker-logs.show-runtime-details', 'Show runtime details')} showLabel value={showRuntime} onChange={event => setShowRuntime(event.currentTarget.checked)} />
      <Text color="secondary" variant="bodySmall">{summary}</Text>
    </div>
    <div ref={scroller} role="log" aria-live="off" aria-label={title} className={styles.rows} style={{ height: height ?? (compact ? 160 : 360) }}
      onScroll={event => setFollow(event.currentTarget.scrollTop < 24)}>
      {!visible.length && <PanelEmpty icon={state === 'unavailable' ? 'exclamation-triangle' : 'document-info'} text={emptyMessage} />}
      {newestFirst.map(item => <LogItemRow key={item.key} item={item} compact={compact} collapsed={collapsed.has(item.key)} onToggle={() => toggle(item.key)}
        highlighted={!!highlightInvocation && item.type === 'run' && item.id === highlightInvocation} onJumpToLine={onJumpToLine} />)}
    </div>
    {!follow && visible.length > 0 && <div className={styles.jump}><Button size="sm" icon="arrow-up" onClick={() => { setFollow(true); if (scroller.current) scroller.current.scrollTop = 0 }}>{t('gofi.python.worker-logs.jump-to-newest', 'Jump to newest')}</Button></div>}
  </PanelChrome>
}

const LIFECYCLE_ICON: Record<LifecycleTone, IconName> = { starting: 'sync', ready: 'check-circle', stopped: 'pause', error: 'exclamation-triangle', info: 'info-circle' }
const OUTCOME_ICON: Record<RunOutcome, IconName> = { running: 'sync', ok: 'check-circle', dropped: 'minus-circle', error: 'exclamation-triangle' }
function outcomeLabel(outcome: RunOutcome): string {
  switch (outcome) {
    case 'running': return t('gofi.python.worker-logs.outcome-running', 'running')
    case 'ok': return t('gofi.python.worker-logs.outcome-ok', 'ok')
    case 'dropped': return t('gofi.python.worker-logs.outcome-dropped', 'dropped')
    case 'error': return t('gofi.python.worker-logs.outcome-error', 'error')
  }
}

function LogItemRow({ item, highlighted, compact, collapsed, onToggle, onJumpToLine }: { item: LogItem; highlighted: boolean; compact?: boolean; collapsed: boolean; onToggle: () => void; onJumpToLine?: (line: number, fn: string) => void }) {
  const styles = useStyles2(getStyles)
  // Every row shares one grid, Explore style: time | level bar + icon | message.
  if (item.type === 'lifecycle') return <div className={cx(styles.row, styles.level[item.tone === 'ready' ? 'ok' : item.tone === 'error' ? 'error' : item.tone === 'stopped' ? 'warn' : 'info'])}>
    <span className={styles.time}>{logTime(item.time)}</span>
    <Icon name={LIFECYCLE_ICON[item.tone]} size="sm" className={cx(styles.levelIcon, item.tone === 'starting' && !item.settled && styles.spin)} />
    <span className={styles.message}>
      <span className={styles.lifecycleText}>{item.text}</span>
      {item.functions && item.functions.length > 0 && <span className={styles.muted}> · {item.functions.join(', ')}</span>}
    </span>
  </div>
  if (item.type === 'runtime') return <div className={cx(styles.row, styles.level.debug, styles.runtime)}>
    <span className={styles.time}>{logTime(item.time)}</span><span className={styles.levelIcon} /><span className={cx(styles.message, styles.mono)}>{item.text}</span>
  </div>
  if (item.type === 'line') return <div className={cx(styles.row, item.severity === 'stderr' ? styles.level.warn : styles.level.output)}>
    <span className={styles.time}>{logTime(item.time)}</span><span className={styles.levelIcon} /><span className={cx(styles.message, styles.mono)}>{item.text}</span>
  </div>
  const outcome = outcomeLabel(item.outcome)
  const outcomeText = item.durationUs !== undefined ? t('gofi.python.worker-logs.outcome-with-duration', '{{outcome}} in {{duration}}', { outcome, duration: formatDuration(item.durationUs) }) : outcome
  const runLevel = item.outcome === 'error' ? styles.level.error : item.outcome === 'running' ? styles.level.info : item.outcome === 'dropped' ? styles.level.debug : styles.level.ok
  return <div className={cx(highlighted && styles.highlighted)}>
    <button type="button" className={cx(styles.row, styles.runHeader, runLevel)} onClick={onToggle} aria-expanded={!collapsed}>
      <span className={styles.time}>{logTime(item.start)}</span>
      <Icon name={OUTCOME_ICON[item.outcome]} size="sm" className={cx(styles.levelIcon, item.outcome === 'running' && styles.spin)} />
      <span className={styles.message}>
        <Icon name={collapsed ? 'angle-right' : 'angle-down'} size="sm" className={styles.caret} />
        <span className={styles.fn}>{item.function || t('gofi.common.function', 'function')}</span>
        {item.kind && <span className={styles.muted}> {item.kind}</span>}
        <span className={styles.outcomeLabel}> · {outcomeText}</span>
        {item.debugSteps !== undefined && <span className={styles.muted}> · {t('gofi.python.worker-logs.debug-steps', 'debug, {{count}} steps', { count: item.debugSteps })}</span>}
        {!compact && <span className={styles.runId} title={item.id}> {shortId(item.id)}</span>}
      </span>
    </button>
    {!collapsed && <div className={styles.runBody}>
      {item.lines.map((line, index) => <div key={index} className={cx(styles.row, styles.nested, styles.level[line.severity === 'output' ? 'output' : line.severity === 'stderr' || line.severity === 'warn' ? 'warn' : line.severity === 'error' ? 'error' : 'debug'])}>
        <span className={styles.time}>{logTime(line.time)}</span><span className={styles.levelIcon} /><span className={cx(styles.message, styles.mono)}>{line.text}</span>
      </div>)}
      {!item.lines.length && item.outcome !== 'error' && <div className={cx(styles.row, styles.nested, styles.level.debug)}><span /><span className={styles.levelIcon} /><span className={cx(styles.message, styles.muted)}>{item.outcome === 'running' ? t('gofi.python.worker-logs.run-running', 'Running…') : t('gofi.python.common.no-output', 'No output.')}</span></div>}
      {item.error && <div className={cx(styles.row, styles.nested, styles.level.error)}>
        <span /><span className={styles.levelIcon} />
        <span className={cx(styles.message, styles.mono, styles.errorText)}>{item.error}
          {item.errorLine && (onJumpToLine
            ? <Button size="sm" fill="text" variant="destructive" icon="arrow-right" onClick={() => onJumpToLine(item.errorLine!, item.function)}>{t('gofi.python.common.line-number', 'line {{line}}', { line: item.errorLine })}</Button>
            : <span className={styles.muted}> · {t('gofi.python.common.line-number', 'line {{line}}', { line: item.errorLine })}</span>)}
        </span>
      </div>}
    </div>}
  </div>
}

const getStyles = (theme: GrafanaTheme2) => {
  const bar = (color: string) => css({ boxShadow: `inset 3px 0 0 ${color}` })
  const severity: Record<LineSeverity, string> = {
    output: bar('transparent'),
    stderr: css({ boxShadow: `inset 3px 0 0 ${theme.colors.warning.main}`, color: theme.colors.warning.text }),
    warn: css({ boxShadow: `inset 3px 0 0 ${theme.colors.warning.main}`, color: theme.colors.warning.text }),
    error: css({ boxShadow: `inset 3px 0 0 ${theme.colors.error.main}`, color: theme.colors.error.text }),
    system: css({ color: theme.colors.text.secondary }),
  }
  const tone: Record<LifecycleTone, string> = {
    starting: css({ color: theme.colors.primary.text, background: theme.colors.primary.transparent }),
    ready: css({ color: theme.colors.success.text, background: theme.colors.success.transparent }),
    stopped: css({ color: theme.colors.warning.text, background: theme.colors.warning.transparent }),
    error: css({ color: theme.colors.error.text, background: theme.colors.error.transparent }),
    info: css({ color: theme.colors.text.secondary }),
  }
  const outcome: Record<RunOutcome, string> = {
    running: css({ '--outcome': theme.colors.primary.text } as Record<string, string>),
    ok: css({ '--outcome': theme.colors.success.text } as Record<string, string>),
    dropped: css({ '--outcome': theme.colors.text.secondary } as Record<string, string>),
    error: css({ '--outcome': theme.colors.error.text } as Record<string, string>),
  }
  return {
    severity, tone, outcome,
    // Explore-style log rows: time | 3px level bar + icon | message.
    row: css({ display: 'grid', gridTemplateColumns: '96px 20px minmax(0, 1fr)', alignItems: 'center', columnGap: theme.spacing(1), padding: theme.spacing(0.25, 1), boxShadow: 'inset 3px 0 0 var(--level)', minHeight: 24 }),
    level: {
      ok: css({ '--level': theme.colors.success.main, '--level-text': theme.colors.success.text } as Record<string, string>),
      info: css({ '--level': theme.colors.primary.main, '--level-text': theme.colors.primary.text } as Record<string, string>),
      warn: css({ '--level': theme.colors.warning.main, '--level-text': theme.colors.warning.text } as Record<string, string>),
      error: css({ '--level': theme.colors.error.main, '--level-text': theme.colors.error.text } as Record<string, string>),
      output: css({ '--level': theme.colors.border.medium, '--level-text': theme.colors.text.primary } as Record<string, string>),
      debug: css({ '--level': theme.colors.border.weak, '--level-text': theme.colors.text.secondary } as Record<string, string>),
    },
    levelIcon: css({ color: 'var(--level-text)', justifySelf: 'center' }),
    message: css({ minWidth: 0, whiteSpace: 'pre-wrap', wordBreak: 'break-word' }),
    mono: css({ fontFamily: theme.typography.fontFamilyMonospace }),
    nested: css({ '& > span:last-child': { paddingLeft: theme.spacing(2.5) } }),
    caret: css({ color: theme.colors.text.secondary, marginRight: theme.spacing(0.5) }),
    errorText: css({ color: theme.colors.error.text }),
    toolbar: css({ display: 'flex', alignItems: 'center', flexWrap: 'wrap', gap: theme.spacing(1, 2), padding: theme.spacing(1), borderBottom: `1px solid ${theme.colors.border.weak}` }),
    filter: css({ flex: '1 1 220px', minWidth: 0, maxWidth: 420 }),
    rows: css({ overflow: 'auto', fontSize: theme.typography.bodySmall.fontSize, background: theme.colors.background.canvas, padding: theme.spacing(0.5, 0) }),
    lifecycle: css({ display: 'flex', alignItems: 'center', flexWrap: 'wrap', gap: theme.spacing(0.75), margin: theme.spacing(0.25, 1), padding: theme.spacing(0.5, 1), borderRadius: theme.shape.radius.default, fontWeight: theme.typography.fontWeightMedium }),
    lifecycleText: css({ marginRight: theme.spacing(0.5) }),
    chip: css({ fontFamily: theme.typography.fontFamilyMonospace, fontWeight: theme.typography.fontWeightRegular, color: theme.colors.text.primary, background: theme.colors.background.primary, border: `1px solid ${theme.colors.border.weak}`, borderRadius: theme.shape.radius.default, padding: theme.spacing(0, 0.75), lineHeight: '18px' }),
    line: css({ display: 'grid', gridTemplateColumns: '92px 56px minmax(0, 1fr)', gap: theme.spacing(1), padding: theme.spacing(0.25, 1, 0.25, 1.5), fontFamily: theme.typography.fontFamilyMonospace, alignItems: 'baseline' }),
    runtime: css({ color: theme.colors.text.disabled }),
    tag: css({ color: theme.colors.text.disabled, fontSize: theme.typography.size.xs }),
    run: css({ margin: theme.spacing(0.5, 1), border: `1px solid ${theme.colors.border.weak}`, borderRadius: theme.shape.radius.default, background: theme.colors.background.primary, overflow: 'hidden' }),
    runHeader: css({
      all: 'unset', boxSizing: 'border-box', width: '100%', cursor: 'pointer', display: 'grid', gridTemplateColumns: '96px 20px minmax(0, 1fr)', alignItems: 'center', columnGap: theme.spacing(1), padding: theme.spacing(0.25, 1), boxShadow: 'inset 3px 0 0 var(--level)', minHeight: 24, '&:hover': { background: theme.colors.action.hover },
      '&:focus-visible': { outline: `2px solid ${theme.colors.primary.main}`, outlineOffset: -2 },
    }),
    outcomeIcon: css({ color: 'var(--outcome)' }),
    outcomeLabel: css({ color: 'var(--level-text)' }),
    fn: css({ fontWeight: theme.typography.fontWeightMedium, color: theme.colors.text.primary }),
    kind: css({ color: theme.colors.text.secondary }),
    runId: css({ color: theme.colors.text.disabled, fontFamily: theme.typography.fontFamilyMonospace }),
    runBody: css({}),
    runLine: css({ display: 'flex', alignItems: 'baseline', gap: theme.spacing(1), padding: theme.spacing(0.125, 1, 0.125, 2.5), fontFamily: theme.typography.fontFamilyMonospace }),
    text: css({ whiteSpace: 'pre-wrap', wordBreak: 'break-word', minWidth: 0 }),
    muted: css({ color: theme.colors.text.secondary }),
    highlighted: css({ background: theme.colors.primary.transparent }),
    time: css({ color: theme.colors.text.secondary, whiteSpace: 'nowrap' }),
    timeRight: css({ marginLeft: 'auto', color: theme.colors.text.secondary, fontFamily: theme.typography.fontFamilyMonospace, fontWeight: theme.typography.fontWeightRegular, whiteSpace: 'nowrap' }),
    spin: css({ '@media (prefers-reduced-motion: no-preference)': { animation: 'gofi-spin 1.2s linear infinite' }, '@keyframes gofi-spin': { to: { transform: 'rotate(360deg)' } } }),
    empty: css({ padding: theme.spacing(2) }),
    jump: css({ display: 'flex', justifyContent: 'center', padding: theme.spacing(1), borderTop: `1px solid ${theme.colors.border.weak}` }),
  }
}
