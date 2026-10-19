import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState, type ReactNode } from 'react'
import { css, cx } from '@emotion/css'
import type { GrafanaTheme2 } from '@grafana/data'
import { Alert, Button, Combobox, FilterInput, Icon, PanelChrome, RadioButtonGroup, Spinner, Stack, Text, useStyles2, useTheme2 } from '@grafana/ui'
import type { SimulateResult, TapEvent } from '../../types'
import { getFlow, simulateFlow, streamAllFlows, streamFlow } from '../../api'
import { MockTraceView } from '../MockTraceView'
import { useGoFi } from '../../state'
import { FrameDebugDrawer } from '../python/debugger/FrameDebugDrawer'
import { pythonSteps } from '../python/debugger/navigation'
import { PanelEmpty } from '../PanelEmpty'
import { formatNumber, t } from '../../i18n'
import { Batcher, VOLUME_LEVELS, appendBounded, capture, matches, volumeLevel, windowRows, type CapturedEvent, type VolumeLevel } from './log-buffer'

const MAX_EVENTS = 1000
const VOLUME_SECONDS = 60
/** Collapsed rows have one fixed height so the list can be windowed. */
const ROW_HEIGHT = 30
type Detail = 'fields' | 'metadata' | 'raw' | 'json'

function source(event: TapEvent): string { return event.meta.source || event.meta.remoteAddr || String(event.fields.source ?? '—') }
function summary(event: TapEvent): string {
  const message = event.fields.message ?? event.fields.msg ?? event.fields.event
  if (typeof message === 'string') return message
  return Object.entries(event.fields).slice(0, 6).map(([key, value]) => `${key}=${typeof value === 'object' ? JSON.stringify(value) : String(value)}`).join('  ') || event.raw || t('gofi.logs.explorer.empty-frame', '(empty frame)')
}

// Grafana log-level colors. Orange/green fail protan separation, so warnings
// use semi-dark yellow; every bar and row also carries a text level label.
function levelColors(theme: GrafanaTheme2): Record<VolumeLevel, string> {
  const named = theme.visualization.getColorByName
  return { error: named('red'), warn: named('semi-dark-yellow'), info: named('semi-dark-green'), debug: named('blue'), unknown: theme.colors.text.disabled }
}

/**
 * Grafana Explore-style live log view for one flow ('' = all). Events are
 * batched per animation frame (at most one re-render per frame under load),
 * searched against text lowercased once on arrival, and only the rows in
 * view are rendered.
 */
export function LogsExplorer({ flow }: { flow: string }) {
  const styles = useStyles2(getStyles)
  const all = flow === ''
  const [events, setEvents] = useState<CapturedEvent[]>([])
  const [connected, setConnected] = useState(false)
  const [paused, setPaused] = useState(false)
  const [autoScroll, setAutoScroll] = useState(true)
  const [query, setQuery] = useState('')
  const [severity, setSeverity] = useState('all')
  const [status, setStatus] = useState('all')
  // Events the controller skipped for this stream because this tab fell behind.
  const [dropped, setDropped] = useState(0)
  const [expanded, setExpanded] = useState<Set<number>>(() => new Set())
  const [extras, setExtras] = useState<Map<number, number>>(() => new Map())
  const [scrollTop, setScrollTop] = useState(0)
  const [viewport, setViewport] = useState(0)
  const scrollRef = useRef<HTMLDivElement | null>(null)
  const viewportObserver = useRef<ResizeObserver | null>(null)
  const pausedRef = useRef(false)
  const nextId = useRef(0)
  const pendingDrops = useRef(0)
  // Per-second observed counts, recorded even while paused so the volume
  // reflects the stream rather than the visible buffer. Bounded to 60 seconds.
  const volume = useRef(new Map<number, Record<VolumeLevel, number>>())
  const [now, setNow] = useState(() => Math.floor(Date.now() / 1000))
  pausedRef.current = paused

  useEffect(() => {
    setConnected(false); setEvents([]); setDropped(0); setExpanded(new Set()); setExtras(new Map()); volume.current.clear(); pendingDrops.current = 0
    const batcher = new Batcher<CapturedEvent>(batch => {
      setEvents(previous => appendBounded(previous, batch, MAX_EVENTS))
      if (pendingDrops.current) { const drops = pendingDrops.current; pendingDrops.current = 0; setDropped(value => value + drops) }
    })
    const receive = (name: string, event: TapEvent) => {
      const item = capture(nextId.current++, name, event)
      const second = Math.floor(Date.now() / 1000)
      const bucket = volume.current.get(second) ?? { error: 0, warn: 0, info: 0, debug: 0, unknown: 0 }
      bucket[volumeLevel(item.level)]++
      volume.current.set(second, bucket)
      if (!pausedRef.current) batcher.push(item)
    }
    const onError = () => setConnected(false)
    const onOpen = () => setConnected(true)
    const onDropped = (count: number) => { pendingDrops.current += count; if (pausedRef.current) { setDropped(value => value + pendingDrops.current); pendingDrops.current = 0 } }
    const stop = all ? streamAllFlows(receive, onError, onOpen, onDropped) : streamFlow(flow, event => receive(flow, event), onError, onOpen, onDropped)
    return () => { stop(); batcher.clear() }
  }, [flow, all])
  useEffect(() => {
    const timer = window.setInterval(() => {
      const second = Math.floor(Date.now() / 1000)
      for (const key of volume.current.keys()) if (key <= second - VOLUME_SECONDS) volume.current.delete(key)
      setNow(second)
    }, 1000)
    return () => window.clearInterval(timer)
  }, [])

  const filters = useMemo(() => ({ query, severity, status }), [query, severity, status])
  const filtered = useMemo(() => { const lower = query.toLowerCase(); return events.filter(item => matches(item, filters, lower)) }, [events, filters, query])
  // Expanded rows keep their measured extra height by id; windowing wants it by index.
  const extraByIndex = useMemo(() => {
    const map = new Map<number, number>()
    if (!extras.size) return map
    filtered.forEach((item, index) => { const extra = extras.get(item.id); if (extra) map.set(index, extra) })
    return map
  }, [filtered, extras])
  const window_ = useMemo(() => windowRows(filtered.length, scrollTop, viewport, ROW_HEIGHT, extraByIndex), [filtered.length, scrollTop, viewport, extraByIndex])
  useLayoutEffect(() => { if (autoScroll && !paused && scrollRef.current) scrollRef.current.scrollTop = scrollRef.current.scrollHeight }, [filtered, autoScroll, paused])
  // The scroller mounts only once the panel has measured itself, after this
  // component's own mount, so the viewport is read when the element appears
  // (a callback ref), not in a one-time effect that found no element and
  // left the window at its overscan of a few rows.
  const setScroller = useCallback((element: HTMLDivElement | null) => {
    viewportObserver.current?.disconnect()
    viewportObserver.current = null
    scrollRef.current = element
    if (!element) return
    setViewport(element.clientHeight)
    const observer = new ResizeObserver(([entry]) => setViewport(Math.floor(entry.contentRect.height)))
    observer.observe(element)
    viewportObserver.current = observer
  }, [])
  useEffect(() => () => viewportObserver.current?.disconnect(), [])

  const toggle = useCallback((id: number) => setExpanded(previous => { const next = new Set(previous); if (next.has(id)) { next.delete(id) } else next.add(id); return next }), [])
  const measure = useCallback((id: number, extra: number) => setExtras(previous => {
    if ((previous.get(id) ?? 0) === extra) return previous
    const next = new Map(previous)
    if (extra) next.set(id, extra); else next.delete(id)
    return next
  }), [])
  const scrollToNewest = () => { setAutoScroll(true); if (scrollRef.current) scrollRef.current.scrollTop = scrollRef.current.scrollHeight }
  const clear = () => { setEvents([]); setDropped(0); setExpanded(new Set()); setExtras(new Map()) }
  const columns = all ? styles.columnsAll : styles.columns

  return <div className={styles.root}>
    <div className={styles.toolbar} role="toolbar" aria-label={t('gofi.logs.explorer.controls-label', 'Log controls')}>
      <RadioButtonGroup aria-label={t('gofi.logs.explorer.stream-mode-label', 'Stream mode')} value={paused ? 'paused' : 'live'} onChange={value => setPaused(value === 'paused')}
        options={[{ value: 'live', label: t('gofi.logs.explorer.mode-live', 'Live'), icon: 'play' }, { value: 'paused', label: t('gofi.logs.explorer.mode-paused', 'Paused'), icon: 'pause' }]} />
      <div className={styles.search}><FilterInput aria-label={t('gofi.logs.explorer.search-label', 'Search traffic logs')} value={query} onChange={setQuery} placeholder={t('gofi.logs.explorer.search-placeholder', 'Search fields, metadata, or raw traffic')} /></div>
      <Combobox aria-label={t('gofi.logs.explorer.level-filter-label', 'Level filter')} width={18} value={severity} onChange={option => setSeverity(option.value)}
        options={['all', 'error', 'warn', 'info', 'debug', 'trace'].map(value => ({ value, label: value === 'all' ? t('gofi.logs.explorer.all-levels', 'All levels') : value.toUpperCase() }))} />
      <Combobox aria-label={t('gofi.logs.explorer.status-filter-label', 'Status filter')} width={18} value={status} onChange={option => setStatus(option.value)}
        options={[{ value: 'all', label: t('gofi.logs.explorer.status-all', 'All traffic') }, { value: 'accepted', label: t('gofi.logs.explorer.status-accepted', 'Accepted') }, { value: 'rejected', label: t('gofi.logs.explorer.status-rejected', 'Rejected') }, { value: 'parse-error', label: t('gofi.logs.explorer.status-parse-errors', 'Parse errors') }]} />
      <Button variant="secondary" fill="outline" icon="trash-alt" onClick={clear}>{t('gofi.logs.explorer.clear', 'Clear')}</Button>
    </div>

    <Sized className={styles.volumePanel}>{(width, height) =>
      <PanelChrome width={width} height={height} padding="md" title={t('gofi.logs.explorer.volume-title', 'Logs volume')} subtitle={t('gofi.logs.explorer.volume-subtitle', 'Observed events per second over the last {{seconds}} seconds of this session', { seconds: VOLUME_SECONDS })}>
        {(_innerWidth, innerHeight) => <div style={{ height: innerHeight }}><LogsVolume volume={volume.current} now={now} /></div>}
      </PanelChrome>}
    </Sized>

    <Sized className={styles.logsPanel}>{(width, height) =>
      <PanelChrome width={width} height={height} padding="none" title={t('gofi.logs.explorer.logs-title', 'Logs')}
        subtitle={t('gofi.logs.explorer.logs-subtitle', '{{shown}} shown / {{buffered}} buffered · latest {{max}} events', { shown: formatNumber(filtered.length), buffered: formatNumber(events.length), max: formatNumber(MAX_EVENTS) })}
        actions={<Stack gap={2} alignItems="center">
          {dropped > 0 && <span className={cx(styles.stream, styles.droppedBadge)} role="status"
            title={t('gofi.logs.explorer.dropped-title', 'The controller skipped these events for this view because the browser could not keep up. Counters and destinations are unaffected; narrow the view to one flow or pause to catch up.')}>
            <Icon name="exclamation-triangle" size="sm" /> {t('gofi.logs.explorer.dropped-count', '{{n}} events dropped', { count: dropped, n: formatNumber(dropped), defaultValue_one: '{{n}} event dropped' })}
          </span>}
          <span className={styles.stream} role="status" title={connected ? t('gofi.logs.explorer.stream-connected-title', 'The traffic stream is connected') : t('gofi.logs.explorer.stream-reconnecting-title', 'Reconnecting to the traffic stream')}>
            <span className={cx(styles.dot, !connected ? styles.dotWarn : paused ? styles.dotIdle : styles.dotLive)} />
            {!connected ? t('gofi.logs.explorer.state-reconnecting', 'Reconnecting…') : paused ? t('gofi.logs.explorer.state-paused', 'Paused') : t('gofi.logs.explorer.state-streaming', 'Streaming')}
          </span>
          {!autoScroll && <Button size="sm" variant="secondary" icon="arrow-down" onClick={scrollToNewest}>{t('gofi.logs.explorer.scroll-to-newest', 'Scroll to newest')}</Button>}
        </Stack>}>
        {(_innerWidth, innerHeight) => <div className={styles.logsBody} style={{ height: innerHeight }}>
          <div className={cx(styles.headerRow, columns)} aria-hidden="true">
            <span /><span>{t('gofi.logs.explorer.column-time', 'Time (UTC)')}</span><span>{t('gofi.logs.explorer.column-level', 'Level')}</span><span>{t('gofi.logs.explorer.column-status', 'Status')}</span>{all && <span>{t('gofi.logs.explorer.column-flow', 'Flow')}</span>}<span>{t('gofi.logs.explorer.column-source', 'Source')}</span><span>{t('gofi.logs.explorer.column-message', 'Message')}</span><span />
          </div>
          <div ref={setScroller} className={styles.rows}
            onScroll={event => { const target = event.currentTarget; setScrollTop(target.scrollTop); const away = target.scrollHeight - target.scrollTop - target.clientHeight > 40; if (away && autoScroll) setAutoScroll(false) }}>
            {!filtered.length && <PanelEmpty icon={!events.length ? 'signal' : 'filter'} text={!events.length ? t('gofi.logs.explorer.empty-waiting', 'Waiting for observed traffic…') : t('gofi.logs.explorer.empty-no-match', 'No buffered events match these filters.')} detail={!events.length ? t('gofi.logs.explorer.empty-waiting-detail', 'Frames appear here as soon as the flow receives them.') : undefined} />}
            {filtered.length > 0 && <div style={{ height: window_.top, flexShrink: 0 }} aria-hidden="true" />}
            {filtered.slice(window_.start, window_.end).map(item => <LogRow key={item.id} item={item} showFlow={all} columns={columns} expanded={expanded.has(item.id)} onToggle={toggle} onMeasure={measure} />)}
            {filtered.length > 0 && <div style={{ height: window_.bottom, flexShrink: 0 }} aria-hidden="true" />}
          </div>
          <div className={styles.footer}><Text variant="bodySmall" color="secondary">{paused ? t('gofi.logs.explorer.footer-paused', 'Paused · incoming events are not appended') : t('gofi.logs.explorer.footer-observed', 'Observed traffic; destination processing is not confirmed')}</Text></div>
        </div>}
      </PanelChrome>}
    </Sized>
  </div>
}

/** Measures its box so PanelChrome can fill the remaining height exactly. */
function Sized({ className, children }: { className: string; children: (width: number, height: number) => ReactNode }) {
  const ref = useRef<HTMLDivElement>(null)
  const [size, setSize] = useState<{ width: number; height: number } | null>(null)
  useLayoutEffect(() => {
    const element = ref.current
    if (!element) return
    const observer = new ResizeObserver(([entry]) => setSize({ width: Math.floor(entry.contentRect.width), height: Math.floor(entry.contentRect.height) }))
    observer.observe(element)
    return () => observer.disconnect()
  }, [])
  return <div ref={ref} className={className}>{size && size.width > 0 && size.height > 0 && children(size.width, size.height)}</div>
}

function LogsVolume({ volume, now }: { volume: Map<number, Record<VolumeLevel, number>>; now: number }) {
  const styles = useStyles2(getStyles)
  const theme = useTheme2()
  const colors = levelColors(theme)
  const [hover, setHover] = useState<number | null>(null)
  const seconds = Array.from({ length: VOLUME_SECONDS }, (_, index) => now - VOLUME_SECONDS + 1 + index)
  const buckets = seconds.map(second => volume.get(second))
  const totals = buckets.map(bucket => bucket ? VOLUME_LEVELS.reduce((sum, key) => sum + bucket[key], 0) : 0)
  const max = Math.max(1, ...totals)
  const present = VOLUME_LEVELS.filter(key => buckets.some(bucket => bucket && bucket[key] > 0))
  const W = 600, H = 64, slot = W / VOLUME_SECONDS, barWidth = Math.max(2, slot - 2)
  const hovered = hover === null ? null : { second: seconds[hover], bucket: buckets[hover], total: totals[hover] }
  return <div className={styles.volume}>
    <div className={styles.volumeChart} onMouseLeave={() => setHover(null)}>
      <svg viewBox={`0 0 ${W} ${H}`} preserveAspectRatio="none" width="100%" height="100%" role="img" aria-label={t('gofi.logs.explorer.volume-chart-label', 'Logs volume: {{total}} events in the last {{seconds}} seconds', { total: formatNumber(totals.reduce((a, b) => a + b, 0)), seconds: VOLUME_SECONDS })}>
        <line x1={0} x2={W} y1={H - 0.5} y2={H - 0.5} stroke={theme.colors.border.weak} vectorEffect="non-scaling-stroke" />
        {buckets.map((bucket, index) => {
          if (!bucket) return null
          let y = H
          return <g key={seconds[index]}>{VOLUME_LEVELS.map(key => {
            if (!bucket[key]) return null
            const h = Math.max(1, (bucket[key] / max) * (H - 4))
            y -= h
            // A 1px surface gap separates stacked segments.
            return <rect key={key} x={index * slot + 1} y={y} width={barWidth} height={Math.max(0.5, h - 1)} fill={colors[key]} opacity={hover === null || hover === index ? 1 : 0.5} />
          })}</g>
        })}
        {seconds.map((second, index) => <rect key={`hit-${second}`} x={index * slot} y={0} width={slot} height={H} fill="transparent" onMouseEnter={() => setHover(index)} />)}
      </svg>
      {hovered && <div className={styles.tooltip} style={{ left: `${Math.min(80, Math.max(0, (hover! / VOLUME_SECONDS) * 100))}%` }}>
        <Text variant="bodySmall" weight="medium">{t('gofi.logs.explorer.volume-tooltip', '{{time}} UTC · {{n}} events', { time: new Date(hovered.second * 1000).toISOString().slice(11, 19), count: hovered.total, n: formatNumber(hovered.total), defaultValue_one: '{{time}} UTC · {{n}} event' })}</Text>
        {VOLUME_LEVELS.filter(key => hovered.bucket?.[key]).map(key => <div key={key} className={styles.tooltipRow}><span className={styles.swatch} style={{ background: colors[key] }} /><Text variant="bodySmall" color="secondary">{key}</Text><Text variant="bodySmall" tabular>{formatNumber(hovered.bucket![key])}</Text></div>)}
      </div>}
    </div>
    <div className={styles.volumeAxis}><Text variant="bodySmall" color="secondary">{t('gofi.logs.explorer.axis-start', '-{{seconds}}s', { seconds: VOLUME_SECONDS })}</Text><Text variant="bodySmall" color="secondary">{t('gofi.logs.explorer.axis-peak', 'peak {{rate}}/s', { rate: formatNumber(totals.some(Boolean) ? max : 0) })}</Text><Text variant="bodySmall" color="secondary">{t('gofi.logs.explorer.axis-now', 'now')}</Text></div>
    <Stack gap={2} wrap="wrap">{(present.length ? present : VOLUME_LEVELS.slice(0, 4)).map(key => <span key={key} className={styles.legendItem}><span className={styles.swatch} style={{ background: colors[key] }} /><Text variant="bodySmall" color="secondary">{key}</Text></span>)}</Stack>
  </div>
}

interface LogRowProps { item: CapturedEvent; showFlow: boolean; columns: string; expanded: boolean; onToggle: (id: number) => void; onMeasure: (id: number, extra: number) => void }

function LogRow({ item, showFlow, columns, expanded, onToggle, onMeasure }: LogRowProps) {
  const { event: ev, flow, id, level: severity } = item
  const styles = useStyles2(getStyles)
  const theme = useTheme2()
  const colors = levelColors(theme)
  const [detail, setDetail] = useState<Detail>('fields')
  const [simResult, setSimResult] = useState<SimulateResult | null>(null)
  const [simError, setSimError] = useState<string | null>(null)
  const [simLoading, setSimLoading] = useState(false)
  const [debugOpen, setDebugOpen] = useState(false)
  const ref = useRef<HTMLElement>(null)
  // "Debug this frame" only applies to flows with Python steps.
  const definition = useGoFi().flows.find(item => item.name === flow)
  const debuggable = !!definition && pythonSteps(definition).length > 0
  const text = summary(ev)
  // The window needs each expanded row's height beyond the fixed row.
  useLayoutEffect(() => {
    const element = ref.current
    if (!expanded || !element) { onMeasure(id, 0); return }
    const report = () => onMeasure(id, Math.max(0, element.offsetHeight - ROW_HEIGHT))
    report()
    const observer = new ResizeObserver(report)
    observer.observe(element)
    return () => observer.disconnect()
  }, [expanded, id, onMeasure, detail, simResult, simError, simLoading])
  const handleReplay = useCallback(async () => {
    setSimError(null); setSimLoading(true)
    try { setSimResult(await simulateFlow(await getFlow(flow), ev.raw)) }
    catch (error) { setSimError(String(error)); setSimResult(null) }
    finally { setSimLoading(false) }
  }, [flow, ev.raw])
  const levelText = severity === 'error' ? styles.errorText : severity === 'warn' ? styles.warnText : undefined
  return <article ref={ref} className={cx(styles.row, expanded && styles.rowExpanded)}>
    <button type="button" onClick={() => onToggle(id)} aria-expanded={expanded} className={cx(styles.rowButton, columns)}>
      <span className={styles.levelBar} style={{ background: colors[volumeLevel(severity)] }} />
      <time className={styles.muted} title={ev.ts}>{ev.ts?.slice(11, 23) || '—'}</time>
      <span className={levelText}>{severity.toUpperCase()}</span>
      <span className={ev.dropped ? styles.errorText : styles.successText}>{ev.dropped ? t('gofi.logs.explorer.row-rejected', 'REJECTED') : t('gofi.logs.explorer.row-accepted', 'ACCEPTED')}</span>
      {showFlow && <span className={styles.truncate} title={flow}>{flow}</span>}
      <span className={cx(styles.truncate, styles.muted)} title={source(ev)}>{source(ev)}</span>
      <span className={cx(styles.truncate, styles.message)} title={text}>
        {text}
        {ev.droppedBy && <span className={cx(styles.inline, styles.errorText)}>{t('gofi.logs.explorer.row-rejected-by', '· Rejected by {{stage}}', { stage: ev.droppedBy })}</span>}
        {ev.meta.parseError && <span className={cx(styles.inline, styles.warnText)} title={ev.meta.parseError}>· {ev.meta.parseError}</span>}
        {ev.truncated && <span className={cx(styles.inline, styles.muted)} title={t('gofi.logs.explorer.row-truncated-title', 'The runtime cut this event to its per-event size limit (GOFI_TAP_MAX_BYTES) before streaming it.')}>{t('gofi.logs.explorer.row-truncated', '· truncated')}</span>}
      </span>
      <Icon name={expanded ? 'angle-down' : 'angle-right'} className={styles.muted} />
    </button>
    {expanded && <div className={styles.details}>
      <div className={styles.detailBar}>
        <RadioButtonGroup size="sm" aria-label={t('gofi.logs.explorer.detail-view-label', 'Detail view')} value={detail} onChange={setDetail}
          options={[{ value: 'fields', label: t('gofi.logs.explorer.detail-fields', 'Fields') }, { value: 'metadata', label: t('gofi.logs.explorer.detail-metadata', 'Metadata') }, { value: 'raw', label: t('gofi.logs.explorer.detail-raw', 'Raw') }, { value: 'json', label: t('gofi.logs.explorer.detail-json', 'JSON') }]} />
        <Button size="sm" variant="secondary" icon="play" disabled={simLoading} onClick={() => void handleReplay()}>{simLoading ? t('gofi.logs.explorer.testing', 'Testing…') : t('gofi.logs.explorer.test-frame', 'Test captured frame')}</Button>
        {debuggable && <Button size="sm" variant="secondary" icon="bug" onClick={() => setDebugOpen(true)} tooltip={t('gofi.logs.explorer.debug-frame-tooltip', 'Simulate this frame up to a Python step and step through it. Nothing is sent.')}>{t('gofi.logs.explorer.debug-frame', 'Debug this frame')}</Button>}
      </div>
      {ev.truncated && <Alert severity="info" title={t('gofi.logs.explorer.truncated-title', 'Truncated event')} bottomSpacing={0}>{t('gofi.logs.explorer.truncated-body', 'The runtime cut long values to its per-event limit before streaming. Raise GOFI_TAP_MAX_BYTES on the flow runtime to stream more of each event.')}</Alert>}
      {debugOpen && definition && <FrameDebugDrawer flowName={flow} flow={definition} raw={ev.raw} onClose={() => setDebugOpen(false)} />}
      {detail === 'fields' || detail === 'metadata' ? <FieldTable values={detail === 'fields' ? ev.fields : ev.meta} />
        : <pre className={styles.pre}>{detail === 'raw' ? ev.raw : JSON.stringify({ fields: ev.fields, meta: ev.meta }, null, 2)}</pre>}
      {simError && <Alert severity="error" title={t('gofi.logs.explorer.simulation-failed', 'Simulation failed')} bottomSpacing={0}>{simError}</Alert>}
      {simLoading && <Spinner />}
      {simResult && <div className={styles.simulation}><Text variant="bodySmall" color="secondary">{t('gofi.logs.explorer.simulation-note', 'Simulation of the captured frame. No traffic was sent.')}</Text><MockTraceView result={simResult} /></div>}
    </div>}
  </article>
}

function FieldTable({ values }: { values: Record<string, unknown> }) {
  const styles = useStyles2(getStyles)
  const entries = Object.entries(values)
  if (!entries.length) return <Text variant="bodySmall" color="secondary">{t('gofi.logs.explorer.no-fields', 'No fields in this section.')}</Text>
  return <table className={styles.fieldTable}><tbody>{entries.map(([key, value]) => <tr key={key}>
    <th scope="row">{key}</th>
    <td>{typeof value === 'object' ? JSON.stringify(value, null, 2) : String(value)}</td>
  </tr>)}</tbody></table>
}

const getStyles = (theme: GrafanaTheme2) => {
  const mono = { fontFamily: theme.typography.fontFamilyMonospace, fontSize: theme.typography.bodySmall.fontSize }
  return {
    root: css({ display: 'flex', flexDirection: 'column', gap: theme.spacing(1), height: '100%', minHeight: 360, minWidth: 0 }),
    toolbar: css({ display: 'flex', flexWrap: 'wrap', alignItems: 'center', gap: theme.spacing(1), flexShrink: 0 }),
    search: css({ flex: '1 1 260px', minWidth: 200 }),
    stream: css({ display: 'inline-flex', alignItems: 'center', gap: theme.spacing(0.75), fontSize: theme.typography.bodySmall.fontSize, color: theme.colors.text.secondary, paddingRight: theme.spacing(1) }),
    droppedBadge: css({ color: theme.colors.warning.text }),
    dot: css({ width: 8, height: 8, borderRadius: theme.shape.radius.circle, flexShrink: 0 }),
    dotLive: css({ background: theme.colors.success.main, boxShadow: `0 0 0 3px ${theme.colors.success.transparent}` }),
    dotIdle: css({ background: theme.colors.text.disabled }),
    dotWarn: css({ background: theme.colors.warning.main }),
    volumePanel: css({ height: 176, flexShrink: 0, minWidth: 0 }),
    logsPanel: css({ flex: 1, minHeight: 200, minWidth: 0 }),
    logsBody: css({ display: 'flex', flexDirection: 'column', minHeight: 0 }),
    columns: css({ gridTemplateColumns: '3px 92px 52px 72px minmax(80px, 140px) minmax(0, 1fr) 16px' }),
    columnsAll: css({ gridTemplateColumns: '3px 92px 52px 72px minmax(80px, 140px) minmax(80px, 120px) minmax(0, 1fr) 16px' }),
    headerRow: css({
      display: 'grid', gap: theme.spacing(1.5), padding: theme.spacing(0.75, 1), flexShrink: 0,
      fontSize: theme.typography.bodySmall.fontSize, color: theme.colors.text.secondary, fontWeight: theme.typography.fontWeightMedium,
      borderBottom: `1px solid ${theme.colors.border.weak}`,
    }),
    rows: css({ flex: 1, minHeight: 0, overflow: 'auto', display: 'flex', flexDirection: 'column' }),
    empty: css({ padding: theme.spacing(4, 2) }),
    footer: css({ flexShrink: 0, padding: theme.spacing(0.5, 1), borderTop: `1px solid ${theme.colors.border.weak}` }),
    row: css({ borderBottom: `1px solid ${theme.colors.border.weak}`, flexShrink: 0, boxSizing: 'border-box' }),
    rowExpanded: css({ background: theme.colors.background.secondary }),
    rowButton: css({
      ...mono, display: 'grid', alignItems: 'center', gap: theme.spacing(1.5), width: '100%', textAlign: 'left', height: ROW_HEIGHT - 1, boxSizing: 'border-box',
      padding: theme.spacing(0, 1), background: 'transparent', border: 'none', borderRadius: 0, color: theme.colors.text.primary,
      '&:hover': { background: theme.colors.action.hover },
    }),
    levelBar: css({ alignSelf: 'stretch', width: 3, borderRadius: theme.shape.radius.default }),
    muted: css({ color: theme.colors.text.secondary }),
    errorText: css({ color: theme.colors.error.text }),
    warnText: css({ color: theme.colors.warning.text }),
    successText: css({ color: theme.colors.success.text }),
    truncate: css({ display: 'block', minWidth: 0, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }),
    message: css({ minWidth: 0 }),
    inline: css({ marginLeft: theme.spacing(1) }),
    details: css({ display: 'flex', flexDirection: 'column', gap: theme.spacing(1.5), padding: theme.spacing(1, 2, 2, 3) }),
    detailBar: css({ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: theme.spacing(1), flexWrap: 'wrap' }),
    pre: css({ ...mono, margin: 0, padding: theme.spacing(1), whiteSpace: 'pre-wrap', wordBreak: 'break-word', background: theme.colors.background.canvas, border: `1px solid ${theme.colors.border.weak}`, borderRadius: theme.shape.radius.default, maxHeight: 360, overflow: 'auto' }),
    fieldTable: css({
      ...mono, width: '100%', borderCollapse: 'collapse',
      'th, td': { padding: theme.spacing(0.5, 1), verticalAlign: 'top', borderBottom: `1px solid ${theme.colors.border.weak}`, textAlign: 'left' },
      th: { width: 200, color: theme.colors.text.secondary, fontWeight: theme.typography.fontWeightRegular, wordBreak: 'break-all' },
      td: { whiteSpace: 'pre-wrap', wordBreak: 'break-word' },
      'tr:hover': { background: theme.colors.action.hover },
    }),
    simulation: css({ display: 'flex', flexDirection: 'column', gap: theme.spacing(1), paddingTop: theme.spacing(1.5), borderTop: `1px solid ${theme.colors.border.weak}` }),
    volume: css({ display: 'flex', flexDirection: 'column', gap: theme.spacing(0.5), height: '100%' }),
    volumeChart: css({ position: 'relative', flex: 1, minHeight: 40 }),
    volumeAxis: css({ display: 'flex', justifyContent: 'space-between' }),
    tooltip: css({
      position: 'absolute', top: 0, zIndex: theme.zIndex.tooltip, pointerEvents: 'none', minWidth: 140,
      padding: theme.spacing(0.75, 1), background: theme.colors.background.elevated, border: `1px solid ${theme.colors.border.medium}`,
      borderRadius: theme.shape.radius.default, boxShadow: theme.shadows.z2,
    }),
    tooltipRow: css({ display: 'grid', gridTemplateColumns: '10px 1fr auto', alignItems: 'center', gap: theme.spacing(1) }),
    legendItem: css({ display: 'inline-flex', alignItems: 'center', gap: theme.spacing(0.75) }),
    swatch: css({ display: 'inline-block', width: 10, height: 10, borderRadius: theme.shape.radius.default }),
  }
}
