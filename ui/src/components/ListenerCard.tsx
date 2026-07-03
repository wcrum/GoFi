import { Card, Badge } from '@cnak-us/design-system'
import { RiDeleteBinLine, RiEyeLine, RiRadarLine } from 'react-icons/ri'
import type { ListenerDefinition } from '../types'

interface Props {
  def: ListenerDefinition
  onDelete: () => void
  onInspect: () => void
}

const FRAMING_COLOR: Record<string, 'blue' | 'green' | 'purple' | 'orange' | 'gray'> = {
  line: 'blue',
  delimited: 'green',
  'length-prefix': 'purple',
  raw: 'orange',
}

const PARSER_COLOR: Record<string, 'blue' | 'green' | 'gray'> = {
  xml: 'blue',
  json: 'green',
  none: 'gray',
}

const SINK_COLOR: Record<string, 'blue' | 'green' | 'purple' | 'gray'> = {
  nats: 'green',
  http: 'blue',
  stdout: 'gray',
}

export function ListenerCard({ def, onDelete, onInspect }: Props) {
  return (
    <Card padding="md" className="flex flex-col gap-3">
      <div className="flex items-start justify-between gap-4">
        <div className="flex items-center gap-3 min-w-0">
          <RiRadarLine className="text-g-accent-main text-xl shrink-0" />
          <div className="min-w-0">
            <div className="font-semibold text-g-text truncate">{def.name}</div>
            <div className="text-sm text-g-text-secondary">
              {def.source.network ?? 'tcp'}:{def.source.port}
            </div>
          </div>
        </div>
        <div className="flex items-center gap-2 shrink-0">
          <button
            onClick={onInspect}
            title="Inspect live traffic"
            className="p-1.5 rounded text-g-text-secondary hover:text-g-accent-main hover:bg-g-hover transition-colors"
          >
            <RiEyeLine />
          </button>
          <button
            onClick={onDelete}
            title="Delete listener"
            className="p-1.5 rounded text-g-text-secondary hover:text-red-400 hover:bg-g-hover transition-colors"
          >
            <RiDeleteBinLine />
          </button>
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
        {def.filter?.expression && (
          <>
            <Arrow />
            <Stage label="filter" value="CEL" color="yellow" />
            <span className="text-g-text-secondary font-mono truncate max-w-xs" title={def.filter.expression}>
              {def.filter.expression}
            </span>
          </>
        )}
        <Arrow />
        <Stage label="sink" value={def.sink.type} color={SINK_COLOR[def.sink.type] ?? 'gray'} />
        {def.sink.subject && (
          <span className="text-g-text-secondary font-mono">{def.sink.subject}</span>
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
