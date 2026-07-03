import { useState, useEffect, useRef } from 'react'
import { PanelHeader, Badge } from '@cnak-us/design-system'
import { RiCloseLine } from 'react-icons/ri'
import type { TapEvent } from '../types'
import { streamListener } from '../api'

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
            <EventRow key={i} ev={ev} />
          ))}
          <div ref={bottomRef} />
        </div>
      </div>
    </div>
  )
}

function EventRow({ ev }: { ev: TapEvent }) {
  const [expanded, setExpanded] = useState(false)

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
      </div>
      {expanded && (
        <pre className="mt-1 text-g-text whitespace-pre-wrap break-all">
          {JSON.stringify({ fields: ev.fields, meta: ev.meta }, null, 2)}
        </pre>
      )}
    </div>
  )
}
