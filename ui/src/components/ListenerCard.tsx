import { useEffect, useState } from 'react'
import { Card, Badge, Button } from './primitives'
import { RiDeleteBinLine, RiEyeLine, RiWifiLine, RiNodeTree, RiEditLine } from 'react-icons/ri'
import { getRuntimeStatus, type RuntimeInfo } from '../api'
import type { ListenerDefinition } from '../types'
import { FRAMING_COLOR, PARSER_COLOR, SINK_COLOR, STAGE_KIND_COLOR } from './stageColors'

interface Props {
  def: ListenerDefinition
  onDelete: () => void
  onInspect: () => void
  onOpenCanvas: () => void
  onEdit: () => void
}

const MAX_STAGE_BADGES = 3

export function ListenerCard({ def, onDelete, onInspect, onOpenCanvas, onEdit }: Props) {
  const stages = def.stages ?? []
  const sinks = def.sinks ?? []
  const [runtime, setRuntime] = useState<RuntimeInfo | null>(null)

  useEffect(() => {
    let cancelled = false
    getRuntimeStatus(def.name)
      .then((info) => {
        if (!cancelled) setRuntime(info)
      })
      .catch(() => {
        // Listener may not have started yet, or the endpoint may be
        // unreachable in this deployment mode — silently ignore.
      })
    return () => {
      cancelled = true
    }
  }, [def.name])

  return (
    <Card padding="md" className="flex flex-col gap-3">
      <div className="flex items-start justify-between gap-4">
        <div className="flex items-center gap-3 min-w-0">
          <RiWifiLine className="text-g-accent-main text-xl shrink-0" />
          <div className="min-w-0">
            <div className="font-semibold text-g-text truncate">{def.name}</div>
            <div className="text-sm text-g-text-secondary">
              {def.source.network ?? 'tcp'}:{def.source.port}
            </div>
          </div>
        </div>
        <div className="flex items-center gap-2 shrink-0">
          <Button fill="text" onClick={onEdit} title="Edit flow" aria-label={`Edit ${def.name}`} className="p-1.5 rounded text-g-text-secondary hover:text-g-accent-main hover:bg-g-hover"><RiEditLine /></Button>
          <Button fill="text"
            onClick={onOpenCanvas}
            title="View pipeline"
            className="p-1.5 rounded text-g-text-secondary hover:text-g-accent-main hover:bg-g-hover transition-colors"
          >
            <RiNodeTree />
          </Button>
          <Button fill="text"
            onClick={onInspect}
            title="Inspect live traffic"
            className="p-1.5 rounded text-g-text-secondary hover:text-g-accent-main hover:bg-g-hover transition-colors"
          >
            <RiEyeLine />
          </Button>
          <Button fill="text"
            onClick={onDelete}
            title="Delete listener"
            className="p-1.5 rounded text-g-text-secondary hover:text-red-400 hover:bg-g-hover transition-colors"
          >
            <RiDeleteBinLine />
          </Button>
        </div>
      </div>

      {/* Pipeline stages */}
      <div className="flex flex-wrap items-center gap-2 text-xs">
        <Stage label="frame" value={def.framing.mode} color={FRAMING_COLOR[def.framing.mode] ?? 'gray'} />
        {def.framing.delimiter && (
          <span className="text-g-text-secondary font-mono">"{def.framing.delimiter}"</span>
        )}
        <Arrow />
        <Stage label="parse" value={def.parser.format || 'none'} color={PARSER_COLOR[def.parser.format] ?? 'gray'} />
        {stages.length > 0 && (
          <>
            <Arrow />
            {stages.length > MAX_STAGE_BADGES ? (
              <Stage label="stages" value={`${stages.length} stages`} color="gray" />
            ) : (
              stages.map((s, i) => (
                <div key={s.id} className="flex items-center gap-2">
                  {i > 0 && <Arrow />}
                  <Stage label={s.kind} value={s.type ?? s.kind} color={STAGE_KIND_COLOR[s.kind] ?? 'gray'} />
                </div>
              ))
            )}
          </>
        )}
        {sinks.map((sink, i) => (
          <div key={i} className="flex items-center gap-2">
            <Arrow />
            <Stage label="sink" value={sink.type} color={SINK_COLOR[sink.type] ?? 'gray'} />
            {sink.subject && <span className="text-g-text-secondary font-mono">{sink.subject}</span>}
          </div>
        ))}
        {def.deadLetterSink && (
          <>
            <Arrow />
            <Stage label="dead-letter" value={def.deadLetterSink.type} color="orange" />
          </>
        )}
        {runtime && (
          <>
            <Arrow />
            <Stage label="runtime" value={`${runtime.kind}: ${runtime.status ?? 'unknown'}`} color="gray" />
          </>
        )}
      </div>
    </Card>
  )
}

function Stage({ label, value, color }: { label: string; value: string; color: string }) {
  return (
    <div className="flex items-center gap-1">
      <span className="text-g-text-secondary">{label}:</span>
      <Badge text={value} color={color as Parameters<typeof Badge>[0]['color']} />
    </div>
  )
}

function Arrow() {
  return <span className="text-g-text-secondary select-none">→</span>
}
