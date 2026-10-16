import { Handle, Position, type NodeProps } from '@xyflow/react'
import { Card, Badge } from '../primitives'
import { RiFilterLine, RiEditLine, RiSpeedLine, RiFingerprintLine, RiShieldCheckLine } from 'react-icons/ri'
import { STAGE_KIND_COLOR, MOCK_STATE_RING, MOCK_STATE_BADGE_COLOR, MOCK_STATE_LABEL } from '../stageColors'
import type { StageSpec } from '../../types'
import type { StageNode as StageNodeType } from './types'

const KIND_ICON: Record<StageSpec['kind'], typeof RiFilterLine> = {
  filter: RiFilterLine,
  transform: RiEditLine,
  'rate-limit': RiSpeedLine,
  dedup: RiFingerprintLine,
  schema: RiShieldCheckLine,
  python: RiEditLine,
}

const KIND_LABEL: Record<StageSpec['kind'], string> = {
  filter: 'Filter',
  transform: 'Transform',
  'rate-limit': 'Rate Limit',
  dedup: 'Deduplicate',
  schema: 'Schema',
  python: 'Python Transformer',
}

/** One-line, kind-specific summary of a stage's config, shown in the node
 * body so the graph is scannable without opening the inspector. */
function summarize(spec: StageSpec): string | undefined {
  switch (spec.kind) {
    case 'python':
      return spec.python ? `${spec.python.library}.${spec.python.function}` : 'Choose a Python function'
    case 'filter':
      return spec.expression
    case 'transform':
      if (spec.transformOp === 'rename') return `${spec.field ?? '?'} -> ${spec.newField ?? '?'}`
      if (spec.transformOp === 'set') return `${spec.field ?? '?'} = ${spec.valueExpr ?? ''}`
      if (spec.transformOp === 'remove') return `remove ${spec.field ?? '?'}`
      return undefined
    case 'rate-limit':
      return spec.maxPerSecond != null ? `${spec.maxPerSecond}/s` : undefined
    case 'dedup':
      return spec.keyExpr ? `${spec.keyExpr} (${spec.window || '30s'})` : undefined
    case 'schema':
      return spec.requiredFields && spec.requiredFields.length > 0 ? spec.requiredFields.join(', ') : undefined
    default:
      return undefined
  }
}

export function StageNode({ data, selected }: NodeProps<StageNodeType>) {
  const { spec, stats, mockState } = data
  const Icon = KIND_ICON[spec.kind] ?? RiFilterLine
  const summary = summarize(spec)
  // Mock-playback ring takes precedence over the selection ring while active,
  // since it's the transient thing the user is watching move.
  const ringClass = (mockState && MOCK_STATE_RING[mockState]) || (selected ? 'ring-2 ring-g-accent-main' : '')

  return (
    <Card
      padding="sm"
      className={`w-56 ${ringClass}`}
    >
      <Handle type="target" position={Position.Left} />

      <div className="flex items-center gap-2 mb-2">
        <Icon className="text-g-accent-main shrink-0" />
        <span className="font-semibold text-g-text text-sm">{KIND_LABEL[spec.kind] ?? spec.kind}</span>
        <Badge text={spec.kind} color={STAGE_KIND_COLOR[spec.kind] ?? 'gray'} className="ml-auto" />
      </div>

      {summary && (
        <div className="text-xs text-g-text-secondary font-mono truncate" title={summary}>
          {summary}
        </div>
      )}

      {stats && (
        <div className="flex items-center gap-1.5 mt-2 pt-2 border-t border-g-border-weak">
          <Badge text={`passed ${stats.passedCount}`} color="green" />
          {stats.droppedCount > 0 && <Badge text={`dropped ${stats.droppedCount}`} color="red" />}
        </div>
      )}

      {mockState && (
        <div className="flex items-center gap-1.5 mt-2 pt-2 border-t border-g-border-weak">
          <Badge text={MOCK_STATE_LABEL[mockState] ?? mockState} color={MOCK_STATE_BADGE_COLOR[mockState] ?? 'gray'} />
        </div>
      )}

      <Handle type="source" position={Position.Right} />
    </Card>
  )
}
