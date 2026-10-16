import { Handle, Position, type NodeProps } from '@xyflow/react'
import { SINK_COLOR } from '../stageColors'
import type { SinkNode as SinkNodeType } from './types'
import { NodeCard, NodeDetail } from './NodeCard'
import { codecLabel } from '../../lib/codec'

export function SinkNode({ data, selected, id }: NodeProps<SinkNodeType>) {
  const { sink, mockState } = data
  // `data.isDeadLetter` is the primary flag; `id === 'deadletter'` is a
  // belt-and-suspenders fallback since that id is fixed by convention (see convert.ts).
  const isDeadLetter = data.isDeadLetter ?? id === 'deadletter'
  const selector = sink.kubernetes ? Object.entries(sink.kubernetes.selector).map(([key, value]) => `${key}=${value}`).join(', ') : ''
  return (
    <NodeCard icon={isDeadLetter ? 'exclamation-triangle' : 'arrow-right'} title={isDeadLetter ? 'Dead letter' : 'Sink'}
      badge={{ text: sink.type, color: SINK_COLOR[sink.type] ?? 'darkgrey' }} selected={selected} mockState={mockState}
      variant={isDeadLetter ? 'dead-letter' : 'default'}
      // A dead-letter node "passing" means a dropped message landed here,
      // so it gets a distinct label instead of a plain "pass".
      mockBadge={isDeadLetter && mockState === 'pass' ? { text: 'receives this drop', color: 'orange' } : undefined}>
      {/* Dead-letter node is intentionally edge-free — no target handle. */}
      {!isDeadLetter && <Handle type="target" position={Position.Left} />}
      {sink.kubernetes && <NodeDetail mono title={JSON.stringify(sink.kubernetes)}>Pods :{sink.kubernetes.port} · {selector}</NodeDetail>}
      {sink.python && <NodeDetail mono title={`${sink.python.library}.${sink.python.function}`}>{sink.python.library}.{sink.python.function}</NodeDetail>}
      {sink.url && <NodeDetail mono title={sink.url}>{sink.url}</NodeDetail>}
      {sink.subject && <NodeDetail mono title={sink.subject}>{sink.subject}</NodeDetail>}
      {sink.encoding && <NodeDetail mono title="How this sink encodes each message">encode: {[sink.encoding.format || 'wrapped', ...(sink.encoding.encoders ?? []).map(codecLabel)].join(' → ')}</NodeDetail>}
    </NodeCard>
  )
}
