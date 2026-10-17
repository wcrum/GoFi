import type { Edge, Node } from '@xyflow/react'
import { codecLabel } from '../../lib/codec.ts'
import { listenOf, sourceSummary, type FlowDefinition, type SinkCfg } from '../../types.ts'

export interface FlowObservation {
  state: 'connecting' | 'connected' | 'reconnecting'
  observed: number
  dropped: number
  parseErrors: number
  lastSeen: number
}
export type TopologyData = Record<string, unknown> & {
  role: 'source' | 'decode' | 'stage' | 'destination'
  title: string
  detail: string
  flowName?: string
  observation?: FlowObservation
  hasDeadLetter?: boolean
  onSelect?: (name: string) => void
}
export type TopologyNode = Node<TopologyData, 'topology'>

// Credentials and conditions do not identify a destination. Subject/topic/scope
// do: two routes to different Aster topics must remain distinguishable.
export function destinationKey(sink: SinkCfg): string {
  return JSON.stringify([sink.type, sink.url ?? '', sink.subject ?? '', sink.aster?.topic ?? '', sink.aster?.scope ?? '', sink.python?.library ?? '', sink.python?.function ?? '', sink.kubernetes ? [sink.kubernetes.namespace ?? '', Object.entries(sink.kubernetes.selector).sort(([a],[b]) => a.localeCompare(b)), sink.kubernetes.port] : null])
}

export function flowsToTopology(flows: FlowDefinition[]): { nodes: TopologyNode[]; edges: Edge[] } {
  const nodes: TopologyNode[] = []
  const edges: Edge[] = []
  const destinations = new Map<string, string>()
  const sinkX = (flows.some((flow) => flow.stages.length > 0) ? 3 : 2) * 280
  const normalDestinations = new Set(flows.flatMap(flow => flow.sinks.map(destinationKey))).size
  const rejectedY = Math.max(flows.length, normalDestinations) * 210
  let normalIndex = 0
  let rejectedIndex = 0
  const destination = (sink: SinkCfg, rejected = false) => {
    const key = `${rejected ? 'rejected:' : 'normal:'}${destinationKey(sink)}`
    const existing = destinations.get(key)
    if (existing) return existing
    const id = `destination:${destinations.size}`
    const y = rejected ? rejectedY + rejectedIndex++ * 210 : normalIndex++ * 210
    destinations.set(key, id)
    nodes.push({ id, type: 'topology', position: { x: sinkX, y }, data: {
      role: 'destination', title: `${rejected ? 'Dead letter · ' : ''}${sink.type.toUpperCase()}`,
      detail: [(sink.kubernetes ? `Pods ${sink.kubernetes.namespace || '(current namespace)'} :${sink.kubernetes.port}\n${Object.entries(sink.kubernetes.selector).map(([key,value]) => `${key}=${value}`).join(', ')}` : sink.url) || (sink.type === 'stdout' ? 'Process output' : 'Destination not configured'), sink.subject, sink.python ? `${sink.python.library}.${sink.python.function}` : '', sink.aster ? `${sink.aster.scope} / ${sink.aster.topic}` : ''].filter(Boolean).join('\n'),
    } })
    return id
  }
  [...flows].sort((a, b) => a.name.localeCompare(b.name)).forEach((flow, index) => {
    // Namespace every id: stage names only have to be unique within a flow.
    const prefix = `flow:${JSON.stringify(flow.name)}`
    const ingress = `${prefix}:ingress`
    const decode = `${prefix}:decode`
    const y = index * 210
    const listen = listenOf(flow)
    const service = listen.service
    const exposure = service?.type === 'NodePort' ? `NodePort ${service.nodePort ?? 'auto'} → ${service.port ?? listen.port}` : service ? `${service.type} :${service.port ?? listen.port}` : 'Listen · socket'
    nodes.push({ id: ingress, type: 'topology', position: { x: 0, y }, data: {
      role: 'source', flowName: flow.name, title: flow.name, hasDeadLetter: Boolean(flow.deadLetterSink),
      detail: flow.source.listen ? `${(listen.network || 'tcp').toUpperCase()} :${listen.port}\n${exposure}` : sourceSummary(flow.source),
    } })
    nodes.push({ id: decode, type: 'topology', position: { x: 280, y }, data: {
      role: 'decode', flowName: flow.name, title: 'Decode & parse',
      detail: [...(flow.parser.decoders ?? []).map(codecLabel), flow.parser.python ? `${flow.parser.python.library}.${flow.parser.python.function}` : flow.parser.format || 'raw'].join(' → ') + `\n${flow.framing.mode} framing`,
    } })
    const connect = (source: string, target: string, label?: string, deadLetter = false) => edges.push({
      id: `${prefix}:edge:${edges.length}`, source, sourceHandle: deadLetter ? 'rejected' : undefined, target, type: 'default', label,
      data: { flowName: flow.name, deadLetter, observedIngress: source === ingress && target === decode },
      style: deadLetter ? { strokeDasharray: '5 5' } : undefined,
      labelStyle: { fill: 'rgb(var(--g-text-secondary))', fontSize: 10 }, labelBgStyle: { fill: 'rgb(var(--g-primary))', fillOpacity: 1 }, labelBgPadding: [6, 4],
    })
    connect(ingress, decode)
    let terminal = decode
    if (flow.stages.length) {
      const id = `${prefix}:processing`
      nodes.push({ id, type: 'topology', position: { x: 560, y }, data: {
        role: 'stage', flowName: flow.name,
        title: `${flow.stages.length} processing stage${flow.stages.length === 1 ? '' : 's'}`,
        detail: flow.stages.map((stage) => `${stage.id} · ${stage.python?.inlineSource ? 'inline Python' : stage.python ? `${stage.python.library}.${stage.python.function}` : stage.type || stage.kind}`).join('\n'),
      } })
      connect(terminal, id)
      terminal = id
    }
    flow.sinks.forEach((sink) => connect(terminal, destination(sink), sink.when ? `When: ${sink.when}` : undefined))
    if (flow.deadLetterSink) connect(ingress, destination(flow.deadLetterSink, true), 'Rejected events', true)
  })
  return { nodes, edges }
}
