import { Handle, Position, type NodeProps } from '@xyflow/react'
import { Card, Badge } from '@cnak-us/design-system'
import { RiWifiLine } from 'react-icons/ri'
import { FRAMING_COLOR, PARSER_COLOR, MOCK_STATE_RING, MOCK_STATE_BADGE_COLOR, MOCK_STATE_LABEL } from '../stageColors'
import type { IngressNode as IngressNodeType } from './types'

export function IngressNode({ data, selected }: NodeProps<IngressNodeType>) {
  const { source, framing, parser, stats, mockState } = data
  const ringClass = (mockState && MOCK_STATE_RING[mockState]) || (selected ? 'ring-2 ring-g-accent-main' : '')

  return (
    <Card
      padding="sm"
      className={`w-56 !bg-g-secondary ${ringClass}`}
    >
      <div className="flex items-center gap-2 mb-2">
        <RiWifiLine className="text-g-accent-main shrink-0" />
        <span className="font-semibold text-g-text text-sm">Ingress</span>
        {stats && (
          <Badge
            text={stats.connected ? 'connected' : 'disconnected'}
            color={stats.connected ? 'green' : 'red'}
            className="ml-auto"
          />
        )}
      </div>

      <div className="text-xs text-g-text-secondary mb-2">
        {(source.network || 'tcp')}:{source.port || '—'}
      </div>

      <div className="flex flex-wrap gap-1.5 mb-1">
        <Badge text={framing.mode} color={FRAMING_COLOR[framing.mode] ?? 'gray'} />
        <Badge text={parser.format || 'none'} color={PARSER_COLOR[parser.format] ?? 'gray'} />
      </div>

      {stats && (
        <div className="text-xs text-g-text-secondary mt-2 pt-2 border-t border-g-border-weak">
          total: <span className="text-g-text font-mono">{stats.total}</span>
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
