import { Handle, Position, type NodeProps } from '@xyflow/react'
import { Card, Badge } from '../primitives'
import { RiSendPlaneLine, RiErrorWarningLine } from 'react-icons/ri'
import { SINK_COLOR, MOCK_STATE_RING, MOCK_STATE_BADGE_COLOR, MOCK_STATE_LABEL } from '../stageColors'
import type { SinkNode as SinkNodeType } from './types'

export function SinkNode({ data, selected, id }: NodeProps<SinkNodeType>) {
  const { sink, mockState } = data
  // `data.isDeadLetter` is the primary flag; `id === 'deadletter'` is a
  // belt-and-suspenders fallback since that id is fixed by convention (see convert.ts).
  const isDeadLetter = data.isDeadLetter ?? id === 'deadletter'
  const ringClass = (mockState && MOCK_STATE_RING[mockState]) || (selected ? 'ring-2 ring-g-accent-main' : '')

  return (
    <Card
      padding="sm"
      className={`w-56 !bg-g-secondary ${isDeadLetter ? 'border-dashed border-orange-400/60' : ''} ${ringClass}`}
    >
      {/* Dead-letter node is intentionally edge-free — no target handle. */}
      {!isDeadLetter && <Handle type="target" position={Position.Left} />}

      <div className="flex items-center gap-2 mb-2">
        {isDeadLetter ? (
          <RiErrorWarningLine className="text-orange-400 shrink-0" />
        ) : (
          <RiSendPlaneLine className="text-g-accent-main shrink-0" />
        )}
        <span className="font-semibold text-g-text text-sm">{isDeadLetter ? 'Dead Letter' : 'Sink'}</span>
        <Badge text={sink.type} color={SINK_COLOR[sink.type] ?? 'gray'} className="ml-auto" />
      </div>

      {isDeadLetter && <Badge text="dead letter" color="orange" className="mb-2" />}

      {sink.kubernetes && <p className="text-xs text-g-text-secondary font-mono truncate" title={JSON.stringify(sink.kubernetes)}>Pods :{sink.kubernetes.port} · {Object.entries(sink.kubernetes.selector).map(([key,value]) => `${key}=${value}`).join(", ")}</p>}
      {sink.python && <p className="text-xs text-g-text-secondary font-mono truncate" title={`${sink.python.library}.${sink.python.function}`}>{sink.python.library}.{sink.python.function}</p>}
      {sink.url && (
        <div className="text-xs text-g-text-secondary font-mono truncate" title={sink.url}>
          {sink.url}
        </div>
      )}
      {sink.subject && (
        <div className="text-xs text-g-text-secondary font-mono truncate" title={sink.subject}>
          {sink.subject}
        </div>
      )}

      {mockState &&
        (isDeadLetter && mockState === 'pass' ? (
          // A dead-letter node "passing" doesn't mean the same thing as a
          // regular sink passing — it means this is where a dropped message
          // landed, so it gets a distinct label instead of a plain "pass".
          <div className="mt-2 pt-2 border-t border-g-border-weak">
            <Badge text="receives this drop" color="orange" />
          </div>
        ) : (
          <div className="mt-2 pt-2 border-t border-g-border-weak">
            <Badge text={MOCK_STATE_LABEL[mockState] ?? mockState} color={MOCK_STATE_BADGE_COLOR[mockState] ?? 'gray'} />
          </div>
        ))}
    </Card>
  )
}
