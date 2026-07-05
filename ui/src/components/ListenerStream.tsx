import { useState, useEffect, useRef } from 'react'
import { PanelHeader, Badge, Spinner, Alert } from '@cnak-us/design-system'
import { RiCloseLine } from 'react-icons/ri'
import type { TapEvent, SimulateResult } from '../types'
import { streamListener, getListener, simulateListener } from '../api'
import { MockTraceView } from './MockTraceView'

interface Props {
  name: string
  onClose: () => void
}

const MAX_EVENTS = 200

export function ListenerStream({ name, onClose }: Props) {
  const [events, setEvents] = useState<TapEvent[]>([])
  const [connected, setConnected] = useState(false)
  const [paused, setPaused] = useState(false)
  const bottomRef = useRef<HTMLDivElement>(null)
  const pausedRef = useRef(false)

  pausedRef.current = paused

  useEffect(() => {
    setConnected(true)
    const stop = streamListener(
      name,
      (ev) => {
        if (pausedRef.current) return
        setEvents((prev) => [...prev.slice(-(MAX_EVENTS - 1)), ev])
      },
      () => setConnected(false),
    )
    return stop
  }, [name])

  useEffect(() => {
    if (!paused) {
      bottomRef.current?.scrollIntoView({ behavior: 'smooth' })
    }
  }, [events, paused])

  return (
    <div className="fixed inset-0 z-50 flex items-stretch justify-end bg-black/40">
      <div className="w-full max-w-3xl bg-g-primary border-l border-g-border-weak flex flex-col shadow-2xl">
        <PanelHeader
          title={`Live traffic — ${name}`}
          onClose={onClose}
          actions={
            <div className="flex items-center gap-2">
              <Badge
                text={connected ? 'connected' : 'disconnected'}
                color={connected ? 'green' : 'red'}
              />
              <button
                onClick={() => setPaused((p) => !p)}
                className="text-xs px-2 py-1 rounded border border-g-border-weak text-g-text-secondary hover:text-g-text transition-colors"
              >
                {paused ? 'Resume' : 'Pause'}
              </button>
              <button
                onClick={() => setEvents([])}
                className="text-xs px-2 py-1 rounded border border-g-border-weak text-g-text-secondary hover:text-g-text transition-colors"
              >
                Clear
              </button>
            </div>
          }
        />

        <div className="flex-1 overflow-y-auto font-mono text-xs p-3 space-y-1 bg-g-canvas">
          {events.length === 0 && (
            <div className="text-g-text-secondary text-center mt-10">
              Waiting for traffic on port…
            </div>
          )}
          {events.map((ev, i) => (
            <EventRow key={i} ev={ev} name={name} />
          ))}
          <div ref={bottomRef} />
        </div>
      </div>
    </div>
  )
}

function EventRow({ ev, name }: { ev: TapEvent; name: string }) {
  const [expanded, setExpanded] = useState(false)
  // Mock-replay state, local to this row so a failed simulate (e.g. a
  // draft-only listener deleted since capture) never crashes the whole stream.
  const [simResult, setSimResult] = useState<SimulateResult | null>(null)
  const [simError, setSimError] = useState<string | null>(null)
  const [simLoading, setSimLoading] = useState(false)

  // Replay this captured frame through the dry-run endpoint: fetch the
  // listener's current definition and simulate `ev.raw` against it, so we can
  // see *why* it landed where it did without it touching a real sink.
  async function handleReplay(e: React.MouseEvent) {
    e.stopPropagation()
    setSimError(null)
    setSimLoading(true)
    try {
      const def = await getListener(name)
      const result = await simulateListener(def, ev.raw)
      setSimResult(result)
    } catch (err) {
      setSimError(String(err))
      setSimResult(null)
    } finally {
      setSimLoading(false)
    }
  }

  return (
    <div
      className={`rounded px-2 py-1 cursor-pointer transition-colors ${
        ev.dropped
          ? 'bg-red-950/30 border border-red-900/30 text-red-400'
          : 'bg-g-primary border border-g-border-weak hover:border-g-accent-main/40'
      }`}
      onClick={() => setExpanded((e) => !e)}
    >
      <div className="flex items-center gap-2">
        <span className="text-g-text-secondary shrink-0">{ev.ts?.slice(11, 23)}</span>
        {ev.dropped && <Badge text="filtered" color="red" />}
        <span className="truncate text-g-text-secondary">
          {JSON.stringify(ev.fields).slice(0, 120)}
        </span>
        <button
          onClick={handleReplay}
          disabled={simLoading}
          className="ml-auto shrink-0 text-xs px-2 py-0.5 rounded border border-g-border-weak text-g-text-secondary hover:text-g-text hover:border-g-accent-main/40 transition-colors disabled:opacity-50"
        >
          {simLoading ? 'Testing…' : 'Test in Mock'}
        </button>
      </div>
      {expanded && (
        <pre className="mt-1 text-g-text whitespace-pre-wrap break-all">
          {JSON.stringify({ fields: ev.fields, meta: ev.meta }, null, 2)}
        </pre>
      )}
      {simError && (
        <div className="mt-2" onClick={(e) => e.stopPropagation()}>
          <Alert severity="error">{simError}</Alert>
        </div>
      )}
      {simLoading && (
        <div className="mt-2 flex justify-center">
          <Spinner size="16px" />
        </div>
      )}
      {simResult && (
        <div className="mt-2" onClick={(e) => e.stopPropagation()}>
          <MockTraceView result={simResult} />
        </div>
      )}
    </div>
  )
}
