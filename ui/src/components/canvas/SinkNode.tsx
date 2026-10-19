import { Handle, Position, type NodeProps } from '@xyflow/react'
import { SINK_COLOR } from '../stageColors'
import type { SinkNode as SinkNodeType } from './types'
import { NodeCard, NodeDetail } from './NodeCard'
import { pythonRefLabel } from '../../lib/python-ref'
import { usePythonDefault } from './python-functions'
import { t } from '../../i18n'

export function SinkNode({ data, selected, id }: NodeProps<SinkNodeType>) {
  const { sink, mockState } = data
  const defaultLibrary = usePythonDefault()
  // `data.isDeadLetter` is the primary flag; `id === 'deadletter'` is a
  // belt-and-suspenders fallback since that id is fixed by convention (see convert.ts).
  const isDeadLetter = data.isDeadLetter ?? id === 'deadletter'
  const selector = sink.kubernetes ? Object.entries(sink.kubernetes.selector).map(([key, value]) => `${key}=${value}`).join(', ') : ''
  return (
    <NodeCard icon={isDeadLetter ? 'exclamation-triangle' : 'arrow-right'} title={isDeadLetter ? t('gofi.canvas.sink-node.title-dead-letter', 'Dead letter') : t('gofi.canvas.sink-node.title', 'Sink')}
      badge={{ text: sink.type, color: SINK_COLOR[sink.type] ?? 'darkgrey' }} selected={selected} mockState={mockState}
      variant={isDeadLetter ? 'dead-letter' : 'default'}
      // A dead-letter node "passing" means a dropped message landed here,
      // so it gets a distinct label instead of a plain "pass".
      mockBadge={isDeadLetter && mockState === 'pass' ? { text: t('gofi.canvas.sink-node.receives-drop', 'receives this drop'), color: 'orange' } : undefined}>
      {/* Nothing in the chain feeds the dead-letter node (it receives rejected
          messages), but an Encode node may: its port only accepts those. */}
      <Handle type="target" position={Position.Left} />
      {sink.kubernetes && <NodeDetail mono title={JSON.stringify(sink.kubernetes)}>{t('gofi.canvas.sink-node.pods', 'Pods :{{port}} · {{selector}}', { port: sink.kubernetes.port, selector })}</NodeDetail>}
      {sink.python && <NodeDetail mono title={pythonRefLabel(sink.python, defaultLibrary)}>{pythonRefLabel(sink.python, defaultLibrary)}</NodeDetail>}
      {sink.url && <NodeDetail mono title={sink.url}>{sink.url}</NodeDetail>}
      {sink.subject && <NodeDetail mono title={sink.subject}>{sink.subject}</NodeDetail>}
    </NodeCard>
  )
}
