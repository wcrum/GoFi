import type { Edge, Node } from '@xyflow/react'
import type { ListenerDefinition, SinkCfg } from '../../types'

export interface ListenerObservation {
  state: 'connecting' | 'connected' | 'reconnecting'
  observed: number
  dropped: number
  parseErrors: number
  lastSeen: number
}
export type TopologyData = Record<string, unknown> & {
  role: 'listener' | 'decode' | 'stage' | 'destination'
  title: string
  detail: string
  listenerName?: string
  observation?: ListenerObservation
  hasDeadLetter?: boolean
  onSelect?: (name: string) => void
}
export type TopologyNode = Node<TopologyData, 'topology'>

// Credentials and conditions do not identify a destination. Subject/topic/scope
// do: two routes to different Aster topics must remain distinguishable.
export function destinationKey(sink: SinkCfg): string {
  return JSON.stringify([sink.type, sink.url ?? '', sink.subject ?? '', sink.aster?.topic ?? '', sink.aster?.scope ?? '', sink.python?.library ?? '', sink.python?.function ?? '', sink.kubernetes ? [sink.kubernetes.namespace ?? '', Object.entries(sink.kubernetes.selector).sort(([a],[b]) => a.localeCompare(b)), sink.kubernetes.port] : null])
}

export function listenersToTopology(listeners: ListenerDefinition[]): { nodes: TopologyNode[]; edges: Edge[] } {
  const nodes: TopologyNode[] = []
  const edges: Edge[] = []
  const destinations = new Map<string, string>()
  const sinkX = (listeners.some((listener) => listener.stages.length > 0) ? 3 : 2) * 280
  const normalDestinations = new Set(listeners.flatMap(listener => listener.sinks.map(destinationKey))).size
  const rejectedY = Math.max(listeners.length, normalDestinations) * 210
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
  [...listeners].sort((a, b) => a.name.localeCompare(b.name)).forEach((listener, index) => {
    // Namespace every id: stage names only have to be unique within a listener.
    const prefix = `listener:${JSON.stringify(listener.name)}`
    const ingress = `${prefix}:ingress`
    const decode = `${prefix}:decode`
    const y = index * 210
    const service = listener.source.service
    const exposure = service?.type === 'NodePort' ? `NodePort ${service.nodePort ?? 'auto'} → ${service.port ?? listener.source.port}` : service ? `${service.type} :${service.port ?? listener.source.port}` : 'Socket ingress'
    nodes.push({ id: ingress, type: 'topology', position: { x: 0, y }, data: {
      role: 'listener', listenerName: listener.name, title: listener.name, hasDeadLetter: Boolean(listener.deadLetterSink),
      detail: `${listener.source.network.toUpperCase()} :${listener.source.port}\n${exposure}${listener.source.python ? `\nHook: ${listener.source.python.library}.${listener.source.python.function}` : ''}`,
    } })
    nodes.push({ id: decode, type: 'topology', position: { x: 280, y }, data: {
      role: 'decode', listenerName: listener.name, title: 'Decode & parse',
      detail: [...(listener.parser.decoders ?? []), listener.parser.python ? `${listener.parser.python.library}.${listener.parser.python.function}` : listener.parser.format || 'raw'].join(' → ') + `\n${listener.framing.mode} framing`,
    } })
    const connect = (source: string, target: string, label?: string, deadLetter = false) => edges.push({
      id: `${prefix}:edge:${edges.length}`, source, sourceHandle: deadLetter ? 'rejected' : undefined, target, type: 'smoothstep', label,
      data: { listenerName: listener.name, deadLetter, observedIngress: source === ingress && target === decode },
      style: deadLetter ? { strokeDasharray: '5 5' } : undefined,
      labelStyle: { fill: '#96a8be', fontSize: 10 }, labelBgStyle: { fill: '#0f1a2a', fillOpacity: 1 }, labelBgPadding: [6, 4], labelBgBorderRadius: 4,
    })
    connect(ingress, decode)
    let terminal = decode
    if (listener.stages.length) {
      const id = `${prefix}:processing`
      nodes.push({ id, type: 'topology', position: { x: 560, y }, data: {
        role: 'stage', listenerName: listener.name,
        title: `${listener.stages.length} processing stage${listener.stages.length === 1 ? '' : 's'}`,
        detail: listener.stages.map((stage) => `${stage.id} · ${stage.python ? `${stage.python.library}.${stage.python.function}` : stage.type || stage.kind}`).join('\n'),
      } })
      connect(terminal, id)
      terminal = id
    }
    listener.sinks.forEach((sink) => connect(terminal, destination(sink), sink.when ? `When: ${sink.when}` : undefined))
    if (listener.deadLetterSink) connect(ingress, destination(listener.deadLetterSink, true), 'Rejected events', true)
  })
  return { nodes, edges }
}
