import { Handle, Position, type NodeProps } from '@xyflow/react'
import type { IconName } from '@grafana/data'
import { Badge } from '@grafana/ui'
import { STAGE_KIND_COLOR } from '../stageColors'
import type { StageSpec } from '../../types'
import type { StageNode as StageNodeType } from './types'
import { NodeCard, NodeDetail, NodeSection } from './NodeCard'
import { parseStageSummary } from '../../lib/parse-stage'
import { pythonRefLabel } from '../../lib/python-ref'
import { usePythonDefault } from './python-functions'
import { formatNumber, t } from '../../i18n'

export const KIND_ICON: Record<StageSpec['kind'], IconName> = {
  filter: 'filter',
  transform: 'pen',
  'rate-limit': 'tachometer-fast',
  dedup: 'copy',
  schema: 'shield',
  python: 'brackets-curly',
  parse: 'brackets-curly',
}

const kindLabels = (): Record<StageSpec['kind'], string> => ({
  filter: t('gofi.canvas.stage-node.kind-filter', 'Filter'),
  transform: t('gofi.canvas.stage-node.kind-transform', 'Transform'),
  'rate-limit': t('gofi.canvas.stage-node.kind-rate-limit', 'Rate Limit'),
  dedup: t('gofi.canvas.stage-node.kind-dedup', 'Deduplicate'),
  schema: t('gofi.canvas.stage-node.kind-schema', 'Schema'),
  python: t('gofi.canvas.stage-node.kind-python', 'Python Transformer'),
  parse: t('gofi.canvas.stage-node.kind-parse', 'Parse field'),
})

/** A stage linked to a CEL library entry (no literal expression). */
function linkedSummary(spec: StageSpec): string | undefined {
  if (!spec.expressionRef) return undefined
  return spec.expressionRef.generation
    ? t('gofi.canvas.stage-node.library-ref-generation', 'library: {{name}} @{{generation}}', { name: spec.expressionRef.name, generation: spec.expressionRef.generation })
    : t('gofi.canvas.stage-node.library-ref', 'library: {{name}}', { name: spec.expressionRef.name })
}

/** One-line, kind-specific summary of a stage's config, shown in the node
 * body so the graph is scannable without opening the inspector. */
export function summarize(spec: StageSpec, defaultLibrary?: string): string | undefined {
  switch (spec.kind) {
    case 'parse':
      return parseStageSummary(spec, defaultLibrary)
    case 'python':
      if (spec.python?.inlineSource !== undefined) return t('gofi.canvas.stage-node.inline-python', 'Inline Python')
      return spec.python?.function ? pythonRefLabel(spec.python, defaultLibrary) : t('gofi.canvas.stage-node.choose-function', 'Choose a Python function')
    case 'filter':
      return spec.expression || linkedSummary(spec)
    case 'transform':
      if (spec.transformOp === 'rename') return `${spec.field ?? '?'} -> ${spec.newField ?? '?'}`
      if (spec.transformOp === 'set') return `${spec.field ?? '?'} = ${spec.valueExpr || linkedSummary(spec) || ''}`
      if (spec.transformOp === 'remove') return t('gofi.canvas.stage-node.remove-field', 'remove {{field}}', { field: spec.field ?? '?' })
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
  const summary = summarize(spec, usePythonDefault() || undefined)
  // The mock-playback outline takes precedence over the selection outline
  // while active, since it's the transient thing the user is watching move.
  return (
    <NodeCard icon={KIND_ICON[spec.kind] ?? 'filter'} title={kindLabels()[spec.kind] ?? spec.kind} badge={{ text: spec.kind, color: STAGE_KIND_COLOR[spec.kind] ?? 'darkgrey' }} selected={selected} mockState={mockState}>
      <Handle type="target" position={Position.Left} />
      {summary && <NodeDetail mono title={summary}>{summary}</NodeDetail>}
      {stats && <NodeSection>
        <Badge text={t('gofi.canvas.stage-node.passed', 'passed {{n}}', { n: stats.pending ? '—' : formatNumber(stats.passedCount) })} color={stats.pending ? 'darkgrey' : 'green'} />
        {stats.droppedCount > 0 && <Badge text={t('gofi.canvas.stage-node.dropped', 'dropped {{n}}', { n: formatNumber(stats.droppedCount) })} color="red" />}
      </NodeSection>}
      <Handle type="source" position={Position.Right} />
    </NodeCard>
  )
}
