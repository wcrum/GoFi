import { Handle, Position, type NodeProps } from '@xyflow/react'
import type { IconName } from '@grafana/data'
import { Badge } from '@grafana/ui'
import { STAGE_KIND_COLOR } from '../stageColors'
import type { StageSpec } from '../../types'
import type { StageNode as StageNodeType } from './types'
import { NodeCard, NodeDetail, NodeSection } from './NodeCard'

export const KIND_ICON: Record<StageSpec['kind'], IconName> = {
  filter: 'filter',
  transform: 'pen',
  'rate-limit': 'tachometer-fast',
  dedup: 'copy',
  schema: 'shield',
  python: 'brackets-curly',
}

const KIND_LABEL: Record<StageSpec['kind'], string> = {
  filter: 'Filter',
  transform: 'Transform',
  'rate-limit': 'Rate Limit',
  dedup: 'Deduplicate',
  schema: 'Schema',
  python: 'Python Transformer',
}

/** A stage linked to a CEL library entry (no literal expression). */
function linkedSummary(spec: StageSpec): string | undefined {
  if (!spec.expressionRef) return undefined
  return `library: ${spec.expressionRef.name}${spec.expressionRef.generation ? ` @${spec.expressionRef.generation}` : ''}`
}

/** One-line, kind-specific summary of a stage's config, shown in the node
 * body so the graph is scannable without opening the inspector. */
function summarize(spec: StageSpec): string | undefined {
  switch (spec.kind) {
    case 'python':
      if (spec.python?.inlineSource !== undefined) return 'Inline Python'
      return spec.python?.library ? `${spec.python.library}.${spec.python.function}` : 'Choose a Python function'
    case 'filter':
      return spec.expression || linkedSummary(spec)
    case 'transform':
      if (spec.transformOp === 'rename') return `${spec.field ?? '?'} -> ${spec.newField ?? '?'}`
      if (spec.transformOp === 'set') return `${spec.field ?? '?'} = ${spec.valueExpr || linkedSummary(spec) || ''}`
      if (spec.transformOp === 'remove') return `remove ${spec.field ?? '?'}`
      return undefined
    case 'rate-limit':
      return spec.maxPerSecond != null ? `${spec.maxPerSecond}/s` : undefined
    case 'dedup':
      return spec.keyExpr || spec.expressionRef ? `${spec.keyExpr || linkedSummary(spec)} (${spec.window || '30s'})` : undefined
    case 'schema':
      return spec.requiredFields && spec.requiredFields.length > 0 ? spec.requiredFields.join(', ') : undefined
    default:
      return undefined
  }
}

export function StageNode({ data, selected }: NodeProps<StageNodeType>) {
  const { spec, stats, mockState } = data
  const summary = summarize(spec)
  // The mock-playback outline takes precedence over the selection outline
  // while active, since it's the transient thing the user is watching move.
  return (
    <NodeCard icon={KIND_ICON[spec.kind] ?? 'filter'} title={KIND_LABEL[spec.kind] ?? spec.kind} badge={{ text: spec.kind, color: STAGE_KIND_COLOR[spec.kind] ?? 'darkgrey' }} selected={selected} mockState={mockState}>
      <Handle type="target" position={Position.Left} />
      {summary && <NodeDetail mono title={summary}>{summary}</NodeDetail>}
      {stats && <NodeSection>
        <Badge text={`passed ${stats.passedCount}`} color="green" />
        {stats.droppedCount > 0 && <Badge text={`dropped ${stats.droppedCount}`} color="red" />}
      </NodeSection>}
      <Handle type="source" position={Position.Right} />
    </NodeCard>
  )
}
