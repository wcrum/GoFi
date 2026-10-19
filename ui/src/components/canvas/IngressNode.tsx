import { createContext, useContext } from 'react'
import { Handle, Position, type NodeProps } from '@xyflow/react'
import type { IngressNode as IngressNodeType } from './types'
import { listenOf, sourceSummary, sourceTypeOf } from '../../types'
import { NodeCard, NodeDetail, NodeSection } from './NodeCard'
import { formatNumber, t } from '../../i18n'

/** The flow's run state when it is down (failed, crashed, invalid): the
 * phase label and the controller's reason. FlowCanvas provides it from the
 * shared run-state and runtime caches; undefined while the flow is healthy,
 * unsaved, or its state is unknown. */
export interface FlowDownState { text: string; reason?: string }
export const FlowDownStateContext = createContext<FlowDownState | undefined>(undefined)

const titles = () => ({
  listen: t('gofi.canvas.ingress-node.title-listen', 'Source · Listen'),
  fetch: t('gofi.canvas.ingress-node.title-fetch', 'Source · Fetch'),
  subscribe: t('gofi.canvas.ingress-node.title-subscribe', 'Source · Subscribe'),
  python: t('gofi.canvas.ingress-node.title-python', 'Source · Python'),
})
const ICONS = { listen: 'signal', fetch: 'sync', subscribe: 'rss', python: 'brackets-curly' } as const

/** Source: how bytes arrive. Framing, decoding, and parsing are the next nodes. */
export function IngressNode({ data, selected }: NodeProps<IngressNodeType>) {
  const { stats, mockState } = data
  const down = useContext(FlowDownStateContext)
  const type = sourceTypeOf(data.source)
  const listen = listenOf(data)
  const service = type === 'listen' && listen.service ? `${listen.service.type}${listen.service.nodePort ? ` :${listen.service.nodePort}` : ''}` : ''
  const detail = type === 'listen' ? `${listen.network || 'tcp'}:${listen.port || '—'}` : sourceSummary(data.source)
  // A down flow has no listener to connect to: the run state (with the reason
  // as its tooltip) replaces the connection badge, and the card turns red.
  const badge = down ? { text: down.text, color: 'red' as const, fixed: true, tooltip: down.reason }
    : stats ? { text: stats.pending ? t('gofi.canvas.ingress-node.connecting', 'connecting') : stats.connected ? t('gofi.canvas.ingress-node.connected', 'connected') : t('gofi.canvas.ingress-node.disconnected', 'disconnected'), color: stats.pending ? 'darkgrey' as const : stats.connected ? 'green' as const : 'red' as const, fixed: true }
    : undefined
  return (
    <NodeCard icon={ICONS[type]} title={titles()[type]} selected={selected} mockState={mockState} badge={badge} tone={down ? 'error' : undefined}>
      <NodeDetail>{detail}{service && ` · ${service}`}</NodeDetail>
      {down?.reason && <NodeDetail title={down.reason}>{down.reason}</NodeDetail>}
      {stats && !down && <NodeSection>{t('gofi.canvas.ingress-node.total', 'total: {{n}}', { n: stats.pending ? '—' : formatNumber(stats.total) })}</NodeSection>}
      <Handle type="source" position={Position.Right} />
    </NodeCard>
  )
}
