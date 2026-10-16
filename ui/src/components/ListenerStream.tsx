import { useState, useEffect, useRef, useMemo } from 'react'
import { PanelHeader, Badge, Spinner, Alert, Input, Select, Button } from './primitives'
import type { TapEvent, SimulateResult } from '../types'
import { streamListener, getListener, simulateListener } from '../api'
import { MockTraceView } from './MockTraceView'

interface Props { name: string; onClose: () => void }
interface CapturedEvent { id: number; event: TapEvent }
const MAX_EVENTS = 200
function level(event: TapEvent): string {
  const value = String(event.fields.level ?? event.fields.severity ?? '').toLowerCase()
  if (['fatal', 'critical', 'error', 'err'].includes(value)) return 'error'
  if (['warning', 'warn'].includes(value)) return 'warn'
  if (['debug', 'trace', 'info'].includes(value)) return value
  return event.meta.parseError ? 'error' : event.dropped ? 'warn' : 'info'
}
function source(event: TapEvent): string { return event.meta.source || event.meta.remoteAddr || String(event.fields.source ?? '—') }
function summary(event: TapEvent): string {
  const message = event.fields.message ?? event.fields.msg ?? event.fields.event
  if (typeof message === 'string') return message
  return Object.entries(event.fields).slice(0, 6).map(([key,value]) => `${key}=${typeof value === 'object' ? JSON.stringify(value) : String(value)}`).join('  ') || event.raw || '(empty frame)'
}
export function ListenerStream({ name, onClose }: Props) {
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
  pausedRef.current = paused
  useEffect(() => {
    setConnected(false); setEvents([])
    return streamListener(name, event => {
      if (!pausedRef.current) setEvents(previous => [...previous.slice(-(MAX_EVENTS - 1)), { id: nextId.current++, event }])
    }, () => setConnected(false), () => setConnected(true))
  }, [name])
  const filtered = useMemo(() => events.filter(({event}) => {
    if (severity !== 'all' && level(event) !== severity) return false
    if (status === 'accepted' && event.dropped || status === 'rejected' && !event.dropped || status === 'parse-error' && !event.meta.parseError) return false
    return !query || `${event.ts} ${event.droppedBy || ''} ${event.raw} ${JSON.stringify(event.fields)} ${JSON.stringify(event.meta)}`.toLowerCase().includes(query.toLowerCase())
  }), [events,query,severity,status])
  useEffect(() => { if (autoScroll && !paused && scrollRef.current) scrollRef.current.scrollTop = scrollRef.current.scrollHeight }, [filtered,autoScroll,paused])
  useEffect(() => {
    const key = (event: KeyboardEvent) => { if (event.key === 'Escape') onClose() }
    window.addEventListener('keydown',key)
    return () => window.removeEventListener('keydown',key)
  }, [onClose])
  return <div className="fixed inset-0 z-50 flex items-stretch justify-end bg-black/40">
    <section aria-label={`Live traffic for ${name}`} className="w-full max-w-[1200px] min-w-0 bg-g-primary border-l border-g-border-weak flex flex-col shadow-2xl">
      <PanelHeader title={`Live traffic · ${name}`} onClose={onClose} actions={<div className="flex items-center gap-2"><Badge text={connected ? 'Connected' : 'Reconnecting'} color={connected ? 'green' : 'yellow'} /><Button onClick={() => setPaused(value=>!value)} className="text-xs px-2 py-1 border border-g-border-medium hover:bg-g-hover">{paused ? 'Resume' : 'Pause'}</Button><Button onClick={() => setEvents([])} className="text-xs px-2 py-1 border border-g-border-medium hover:bg-g-hover">Clear</Button></div>} />
      <div className="flex flex-wrap gap-2 p-3 border-b border-g-border-weak"><Input aria-label="Search traffic logs" value={query} onChange={event=>setQuery(event.target.value)} placeholder="Search fields, metadata, or raw traffic…" className="flex-1 !w-auto min-w-52 !text-xs !py-1.5" /><div className="w-36"><Select value={severity} onChange={setSeverity} options={['all','error','warn','info','debug','trace'].map(value=>({value,label:value==='all'?'All levels':value.toUpperCase()}))} /></div><div className="w-40"><Select value={status} onChange={setStatus} options={[{value:'all',label:'All traffic'},{value:'accepted',label:'Accepted'},{value:'rejected',label:'Rejected'},{value:'parse-error',label:'Parse errors'}]} /></div><Button onClick={()=>setAutoScroll(value=>!value)} aria-pressed={autoScroll} className={`text-xs px-2 border border-g-border-medium ${autoScroll?'text-g-accent-main':'text-g-text-secondary'}`}>{autoScroll?'Live tail on':'Scroll to newest'}</Button></div>
      <div className="grid grid-cols-[92px_52px_72px_120px_minmax(0,1fr)] gap-3 px-5 py-2 text-[10px] uppercase tracking-wider text-g-text-secondary border-b border-g-border-weak"><span>Time (UTC)</span><span>Level</span><span>Status</span><span>Source</span><span>Message</span></div>
      <div ref={scrollRef} onScroll={event=>{const target=event.currentTarget;if(target.scrollHeight-target.scrollTop-target.clientHeight>40)setAutoScroll(false)}} className="flex-1 overflow-auto bg-g-canvas min-h-0">
        {!filtered.length && <p role="status" className="px-5 py-10 text-sm text-g-text-secondary">{!events.length?'Waiting for observed traffic…':'No buffered events match these filters.'}</p>}
        {filtered.map(({id,event})=><EventRow key={id} ev={event} name={name} />)}
      </div>
      <footer className="px-4 py-2 border-t border-g-border-weak text-[10px] text-g-text-secondary flex gap-4"><span>{filtered.length} shown / {events.length} buffered · latest {MAX_EVENTS} events</span><span className="ml-auto">{paused?'Paused · incoming events are not appended':'Observed traffic; destination processing is not confirmed'}</span></footer>
    </section>
  </div>
}
function EventRow({ ev, name }: { ev: TapEvent; name: string }) {
  const [expanded, setExpanded] = useState(false)
  const [tab, setTab] = useState<'fields'|'metadata'|'raw'|'json'>('fields')
  const [simResult, setSimResult] = useState<SimulateResult | null>(null)
  const [simError, setSimError] = useState<string | null>(null)
  const [simLoading, setSimLoading] = useState(false)
  const severity = level(ev)
  async function handleReplay() {
    setExpanded(true); setSimError(null); setSimLoading(true)
    try { setSimResult(await simulateListener(await getListener(name), ev.raw)) }
    catch(error) { setSimError(String(error)); setSimResult(null) }
    finally { setSimLoading(false) }
  }
  return <article className={`border-b border-g-border-weak border-l-2 ${ev.dropped ? 'border-l-red-400' : severity==='error'?'border-l-orange-400':'border-l-transparent'}`}>
    <button onClick={()=>setExpanded(value=>!value)} aria-expanded={expanded} className="w-full text-left grid grid-cols-[92px_52px_72px_120px_minmax(0,1fr)] gap-3 px-4 py-2 text-[11px] font-mono items-start hover:bg-g-hover">
      <time className="text-g-text-secondary tabular-nums" title={ev.ts}>{ev.ts?.slice(11,23)||'—'}</time><span className={severity==='error'?'text-red-300':severity==='warn'?'text-amber-300':'text-g-text-secondary'}>{severity.toUpperCase()}</span><span className={ev.dropped?'text-red-300':'text-emerald-300'}>{ev.dropped?'REJECTED':'ACCEPTED'}</span><span className="truncate text-g-text-secondary" title={source(ev)}>{source(ev)}</span><span className="min-w-0"><span className="block truncate text-g-text" title={summary(ev)}>{summary(ev)}</span>{ev.droppedBy&&<span className="block mt-1 text-red-300 text-[10px]">Rejected by {ev.droppedBy}</span>}{ev.meta.parseError&&<span className="block mt-1 truncate text-orange-300" title={ev.meta.parseError}>{ev.meta.parseError}</span>}</span>
    </button>
    {expanded&&<div className="px-4 pb-4 pt-2 bg-g-primary border-t border-g-border-weak"><div className="flex gap-1 border-b border-g-border-weak pb-2 mb-3">{(['fields','metadata','raw','json'] as const).map(value=><Button key={value} onClick={()=>setTab(value)} aria-pressed={tab===value} className={`px-2 py-1 text-xs ${tab===value?'bg-g-hover text-g-accent-main':'text-g-text-secondary hover:bg-g-hover'}`}>{value==='json'?'JSON':value[0].toUpperCase()+value.slice(1)}</Button>)}<Button onClick={handleReplay} disabled={simLoading} className="ml-auto text-xs border border-g-border-medium px-2 py-1 text-g-accent-main hover:bg-g-hover disabled:opacity-50">{simLoading?'Testing…':'Test captured frame'}</Button></div>
      {tab==='fields'||tab==='metadata'?<StructuredFields values={tab==='fields'?ev.fields:ev.meta} />:<pre className="text-xs font-mono text-g-text overflow-auto whitespace-pre-wrap break-words">{tab==='raw'?ev.raw:JSON.stringify({fields:ev.fields,meta:ev.meta},null,2)}</pre>}
      {simError&&<div className="mt-3"><Alert severity="error">{simError}</Alert></div>}{simLoading&&<div className="mt-3"><Spinner size="16px" /></div>}{simResult&&<div className="mt-3 border-t border-g-border-weak pt-3"><p className="text-[11px] text-g-text-secondary mb-3">Simulation of the captured frame. No traffic was sent.</p><MockTraceView result={simResult} /></div>}
    </div>}
  </article>
}
function StructuredFields({values}:{values:Record<string,unknown>}) {
  const entries=Object.entries(values)
  return entries.length?<dl className="text-xs divide-y divide-g-border-weak">{entries.map(([key,value])=><div key={key} className="grid grid-cols-[160px_minmax(0,1fr)] gap-4 py-2"><dt className="text-g-text-secondary font-mono break-all">{key}</dt><dd className="text-g-text font-mono whitespace-pre-wrap break-words">{typeof value==='object'?JSON.stringify(value,null,2):String(value)}</dd></div>)}</dl>:<p className="text-xs text-g-text-secondary">No fields in this section.</p>
}
