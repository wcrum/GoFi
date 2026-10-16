import { Handle, Position, type NodeProps } from '@xyflow/react'
import type { IngressNode as IngressNodeType } from './types'
import { listenOf, sourceSummary, sourceTypeOf } from '../../types'
import { NodeCard, NodeDetail, NodeSection } from './NodeCard'

const TITLES = { listen: 'Source · Listen', fetch: 'Source · Fetch', subscribe: 'Source · Subscribe', python: 'Source · Python' } as const
const ICONS = { listen: 'signal', fetch: 'sync', subscribe: 'rss', python: 'brackets-curly' } as const

/** Source: how bytes arrive. Framing, decoding, and parsing are the next nodes. */
export function IngressNode({ data, selected }: NodeProps<IngressNodeType>) {
  const { stats, mockState } = data
  const type = sourceTypeOf(data.source)
  const listen = listenOf(data)
  const service = type === 'listen' && listen.service ? `${listen.service.type}${listen.service.nodePort ? ` :${listen.service.nodePort}` : ''}` : ''
  const detail = type === 'listen' ? `${listen.network || 'tcp'}:${listen.port || '—'}` : sourceSummary(data.source)
  return (
    <NodeCard icon={ICONS[type]} title={TITLES[type]} selected={selected} mockState={mockState}
      badge={stats ? { text: stats.connected ? 'connected' : 'disconnected', color: stats.connected ? 'green' : 'red' } : undefined}>
      <NodeDetail>{detail}{service && ` · ${service}`}</NodeDetail>
      {stats && <NodeSection>total: {stats.total}</NodeSection>}
      <Handle type="source" position={Position.Right} />
    </NodeCard>
  )
}
