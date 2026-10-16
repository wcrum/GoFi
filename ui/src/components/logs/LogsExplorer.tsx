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

const MAX_EVENTS = 200
const VOLUME_SECONDS = 60
type Level = 'error' | 'warn' | 'info' | 'debug' | 'trace' | 'unknown'
type VolumeLevel = 'error' | 'warn' | 'info' | 'debug' | 'unknown'
const VOLUME_LEVELS: VolumeLevel[] = ['error', 'warn', 'info', 'debug', 'unknown']
interface CapturedEvent { id: number; flow: string; event: TapEvent }
type Detail = 'fields' | 'metadata' | 'raw' | 'json'

function level(event: TapEvent): Level {
  const value = String(event.fields.level ?? event.fields.severity ?? '').toLowerCase()
  if (['fatal', 'critical', 'error', 'err'].includes(value)) return 'error'
  if (['warning', 'warn'].includes(value)) return 'warn'
  if (value === 'debug' || value === 'trace' || value === 'info') return value
  return event.meta.parseError ? 'error' : event.dropped ? 'warn' : 'info'
}
const volumeLevel = (value: Level): VolumeLevel => value === 'trace' ? 'debug' : value
function source(event: TapEvent): string { return event.meta.source || event.meta.remoteAddr || String(event.fields.source ?? '—') }
function summary(event: TapEvent): string {
  const message = event.fields.message ?? event.fields.msg ?? event.fields.event
  if (typeof message === 'string') return message
  return Object.entries(event.fields).slice(0, 6).map(([key, value]) => `${key}=${typeof value === 'object' ? JSON.stringify(value) : String(value)}`).join('  ') || event.raw || '(empty frame)'
}

// Grafana log-level colors. Orange/green fail protan separation, so warnings
// use semi-dark yellow; every bar and row also carries a text level label.
function levelColors(theme: GrafanaTheme2): Record<VolumeLevel, string> {
  const named = theme.visualization.getColorByName
  return { error: named('red'), warn: named('semi-dark-yellow'), info: named('semi-dark-green'), debug: named('blue'), unknown: theme.colors.text.disabled }
}

/** Grafana Explore-style live log view for one flow ('' = all). */
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
  const scrollRef = useRef<HTMLDivElement>(null)
  const pausedRef = useRef(false)
  const nextId = useRef(0)
  // Per-second observed counts, recorded even while paused so the volume
  // reflects the stream rather than the visible buffer. Bounded to 60 seconds.
  const volume = useRef(new Map<number, Record<VolumeLevel, number>>())
  const [now, setNow] = useState(() => Math.floor(Date.now() / 1000))
  pausedRef.current = paused

  useEffect(() => {
    setConnected(false); setEvents([]); volume.current.clear()
    const receive = (name: string, event: TapEvent) => {
      const second = Math.floor(Date.now() / 1000)
      const bucket = volume.current.get(second) ?? { error: 0, warn: 0, info: 0, debug: 0, unknown: 0 }
      bucket[volumeLevel(level(event))]++
      volume.current.set(second, bucket)
      if (!pausedRef.current) setEvents(previous => [...previous.slice(-(MAX_EVENTS - 1)), { id: nextId.current++, flow: name, event }])
    }
    const onError = () => setConnected(false)
    const onOpen = () => setConnected(true)
    return all ? streamAllFlows(receive, onError, onOpen) : streamFlow(flow, event => receive(flow, event), onError, onOpen)
  }, [flow, all])
  useEffect(() => {
    const timer = window.setInterval(() => {
      const second = Math.floor(Date.now() / 1000)
      for (const key of volume.current.keys()) if (key <= second - VOLUME_SECONDS) volume.current.delete(key)
      setNow(second)
    }, 1000)
    return () => window.clearInterval(timer)
  }, [])

  const filtered = useMemo(() => events.filter(({ event, flow: name }) => {
    if (severity !== 'all' && level(event) !== severity) return false
    if (status === 'accepted' && event.dropped || status === 'rejected' && !event.dropped || status === 'parse-error' && !event.meta.parseError) return false
    return !query || `${name} ${event.ts} ${event.droppedBy || ''} ${event.raw} ${JSON.stringify(event.fields)} ${JSON.stringify(event.meta)}`.toLowerCase().includes(query.toLowerCase())
  }), [events, query, severity, status])
  useLayoutEffect(() => { if (autoScroll && !paused && scrollRef.current) scrollRef.current.scrollTop = scrollRef.current.scrollHeight }, [filtered, autoScroll, paused])

  const scrollToNewest = () => { setAutoScroll(true); if (scrollRef.current) scrollRef.current.scrollTop = scrollRef.current.scrollHeight }
  const columns = all ? styles.columnsAll : styles.columns

  return <div className={styles.root}>
    <div className={styles.toolbar} role="toolbar" aria-label="Log controls">
      <RadioButtonGroup aria-label="Stream mode" value={paused ? 'paused' : 'live'} onChange={value => setPaused(value === 'paused')}
        options={[{ value: 'live', label: 'Live', icon: 'play' }, { value: 'paused', label: 'Paused', icon: 'pause' }]} />
      <div className={styles.search}><FilterInput aria-label="Search traffic logs" value={query} onChange={setQuery} placeholder="Search fields, metadata, or raw traffic" /></div>
      <Combobox aria-label="Level filter" width={18} value={severity} onChange={option => setSeverity(option.value)}
        options={['all', 'error', 'warn', 'info', 'debug', 'trace'].map(value => ({ value, label: value === 'all' ? 'All levels' : value.toUpperCase() }))} />
      <Combobox aria-label="Status filter" width={18} value={status} onChange={option => setStatus(option.value)}
        options={[{ value: 'all', label: 'All traffic' }, { value: 'accepted', label: 'Accepted' }, { value: 'rejected', label: 'Rejected' }, { value: 'parse-error', label: 'Parse errors' }]} />
      <Button variant="secondary" fill="outline" icon="trash-alt" onClick={() => setEvents([])}>Clear</Button>
    </div>

    <Sized className={styles.volumePanel}>{(width, height) =>
      <PanelChrome width={width} height={height} padding="md" title="Logs volume" subtitle="Observed events per second over the last 60 seconds of this session">
        {(_innerWidth, innerHeight) => <div style={{ height: innerHeight }}><LogsVolume volume={volume.current} now={now} /></div>}
      </PanelChrome>}
    </Sized>

    <Sized className={styles.logsPanel}>{(width, height) =>
      <PanelChrome width={width} height={height} padding="none" title="Logs"
        subtitle={`${filtered.length} shown / ${events.length} buffered · latest ${MAX_EVENTS} events`}
        actions={<Stack gap={2} alignItems="center">
          <span className={styles.stream} role="status" title={connected ? 'The traffic stream is connected' : 'Reconnecting to the traffic stream'}>
            <span className={cx(styles.dot, !connected ? styles.dotWarn : paused ? styles.dotIdle : styles.dotLive)} />
            {!connected ? 'Reconnecting…' : paused ? 'Paused' : 'Streaming'}
          </span>
          {!autoScroll && <Button size="sm" variant="secondary" icon="arrow-down" onClick={scrollToNewest}>Scroll to newest</Button>}
        </Stack>}>
        {(_innerWidth, innerHeight) => <div className={styles.logsBody} style={{ height: innerHeight }}>
          <div className={cx(styles.headerRow, columns)} aria-hidden="true">
            <span /><span>Time (UTC)</span><span>Level</span><span>Status</span>{all && <span>Flow</span>}<span>Source</span><span>Message</span><span />
          </div>
          <div ref={scrollRef} className={styles.rows}
            onScroll={event => { const target = event.currentTarget; const away = target.scrollHeight - target.scrollTop - target.clientHeight > 40; if (away && autoScroll) setAutoScroll(false) }}>
            {!filtered.length && <PanelEmpty icon={!events.length ? 'signal' : 'filter'} text={!events.length ? 'Waiting for observed traffic…' : 'No buffered events match these filters.'} detail={!events.length ? 'Frames appear here as soon as the flow receives them.' : undefined} />}
            {filtered.map(({ id, event, flow: name }) => <LogRow key={id} ev={event} flow={name} showFlow={all} columns={columns} />)}
          </div>
          <div className={styles.footer}><Text variant="bodySmall" color="secondary">{paused ? 'Paused · incoming events are not appended' : 'Observed traffic; destination processing is not confirmed'}</Text></div>
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
      <svg viewBox={`0 0 ${W} ${H}`} preserveAspectRatio="none" width="100%" height="100%" role="img" aria-label={`Logs volume: ${totals.reduce((a, b) => a + b, 0)} events in the last ${VOLUME_SECONDS} seconds`}>
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
        <Text variant="bodySmall" weight="medium">{new Date(hovered.second * 1000).toISOString().slice(11, 19)} UTC · {hovered.total} event{hovered.total === 1 ? '' : 's'}</Text>
        {VOLUME_LEVELS.filter(key => hovered.bucket?.[key]).map(key => <div key={key} className={styles.tooltipRow}><span className={styles.swatch} style={{ background: colors[key] }} /><Text variant="bodySmall" color="secondary">{key}</Text><Text variant="bodySmall" tabular>{hovered.bucket![key]}</Text></div>)}
      </div>}
    </div>
    <div className={styles.volumeAxis}><Text variant="bodySmall" color="secondary">-60s</Text><Text variant="bodySmall" color="secondary">peak {totals.some(Boolean) ? max : 0}/s</Text><Text variant="bodySmall" color="secondary">now</Text></div>
    <Stack gap={2} wrap="wrap">{(present.length ? present : VOLUME_LEVELS.slice(0, 4)).map(key => <span key={key} className={styles.legendItem}><span className={styles.swatch} style={{ background: colors[key] }} /><Text variant="bodySmall" color="secondary">{key}</Text></span>)}</Stack>
  </div>
}

function LogRow({ ev, flow, showFlow, columns }: { ev: TapEvent; flow: string; showFlow: boolean; columns: string }) {
  const styles = useStyles2(getStyles)
  const theme = useTheme2()
  const colors = levelColors(theme)
  const [expanded, setExpanded] = useState(false)
  const [detail, setDetail] = useState<Detail>('fields')
  const [simResult, setSimResult] = useState<SimulateResult | null>(null)
  const [simError, setSimError] = useState<string | null>(null)
  const [simLoading, setSimLoading] = useState(false)
  const [debugOpen, setDebugOpen] = useState(false)
  // "Debug this frame" only applies to flows with Python steps.
  const definition = useGoFi().flows.find(item => item.name === flow)
  const debuggable = !!definition && pythonSteps(definition).length > 0
  const severity = level(ev)
  const text = summary(ev)
  const handleReplay = useCallback(async () => {
    setExpanded(true); setSimError(null); setSimLoading(true)
    try { setSimResult(await simulateFlow(await getFlow(flow), ev.raw)) }
    catch (error) { setSimError(String(error)); setSimResult(null) }
    finally { setSimLoading(false) }
  }, [flow, ev.raw])
  const levelText = severity === 'error' ? styles.errorText : severity === 'warn' ? styles.warnText : undefined
  return <article className={cx(styles.row, expanded && styles.rowExpanded)}>
    <button type="button" onClick={() => setExpanded(value => !value)} aria-expanded={expanded} className={cx(styles.rowButton, columns)}>
      <span className={styles.levelBar} style={{ background: colors[volumeLevel(severity)] }} />
      <time className={styles.muted} title={ev.ts}>{ev.ts?.slice(11, 23) || '—'}</time>
      <span className={levelText}>{severity.toUpperCase()}</span>
      <span className={ev.dropped ? styles.errorText : styles.successText}>{ev.dropped ? 'REJECTED' : 'ACCEPTED'}</span>
      {showFlow && <span className={styles.truncate} title={flow}>{flow}</span>}
      <span className={cx(styles.truncate, styles.muted)} title={source(ev)}>{source(ev)}</span>
      <span className={styles.message}>
        <span className={styles.truncate} title={text}>{text}</span>
        {ev.droppedBy && <span className={cx(styles.subline, styles.errorText)}>Rejected by {ev.droppedBy}</span>}
        {ev.meta.parseError && <span className={cx(styles.subline, styles.warnText, styles.truncate)} title={ev.meta.parseError}>{ev.meta.parseError}</span>}
      </span>
      <Icon name={expanded ? 'angle-down' : 'angle-right'} className={styles.muted} />
    </button>
    {expanded && <div className={styles.details}>
      <div className={styles.detailBar}>
        <RadioButtonGroup size="sm" aria-label="Detail view" value={detail} onChange={setDetail}
          options={[{ value: 'fields', label: 'Fields' }, { value: 'metadata', label: 'Metadata' }, { value: 'raw', label: 'Raw' }, { value: 'json', label: 'JSON' }]} />
        <Button size="sm" variant="secondary" icon="play" disabled={simLoading} onClick={() => void handleReplay()}>{simLoading ? 'Testing…' : 'Test captured frame'}</Button>
        {debuggable && <Button size="sm" variant="secondary" icon="bug" onClick={() => setDebugOpen(true)} tooltip="Simulate this frame up to a Python step and step through it. Nothing is sent.">Debug this frame</Button>}
      </div>
      {debugOpen && definition && <FrameDebugDrawer flowName={flow} flow={definition} raw={ev.raw} onClose={() => setDebugOpen(false)} />}
      {detail === 'fields' || detail === 'metadata' ? <FieldTable values={detail === 'fields' ? ev.fields : ev.meta} />
        : <pre className={styles.pre}>{detail === 'raw' ? ev.raw : JSON.stringify({ fields: ev.fields, meta: ev.meta }, null, 2)}</pre>}
      {simError && <Alert severity="error" title="Simulation failed" bottomSpacing={0}>{simError}</Alert>}
      {simLoading && <Spinner />}
      {simResult && <div className={styles.simulation}><Text variant="bodySmall" color="secondary">Simulation of the captured frame. No traffic was sent.</Text><MockTraceView result={simResult} /></div>}
    </div>}
  </article>
}

function FieldTable({ values }: { values: Record<string, unknown> }) {
  const styles = useStyles2(getStyles)
  const entries = Object.entries(values)
  if (!entries.length) return <Text variant="bodySmall" color="secondary">No fields in this section.</Text>
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
    row: css({ borderBottom: `1px solid ${theme.colors.border.weak}` }),
    rowExpanded: css({ background: theme.colors.background.secondary }),
    rowButton: css({
      ...mono, display: 'grid', alignItems: 'start', gap: theme.spacing(1.5), width: '100%', textAlign: 'left',
      padding: theme.spacing(0.5, 1), background: 'transparent', border: 'none', borderRadius: 0, color: theme.colors.text.primary,
      '&:hover': { background: theme.colors.action.hover },
    }),
    levelBar: css({ alignSelf: 'stretch', width: 3, borderRadius: theme.shape.radius.default }),
    muted: css({ color: theme.colors.text.secondary }),
    errorText: css({ color: theme.colors.error.text }),
    warnText: css({ color: theme.colors.warning.text }),
    successText: css({ color: theme.colors.success.text }),
    truncate: css({ display: 'block', minWidth: 0, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }),
    message: css({ minWidth: 0 }),
    subline: css({ display: 'block', marginTop: theme.spacing(0.25) }),
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
