import type { Edge, Node } from '@xyflow/react'
import { codecLabel } from '../../lib/codec.ts'
import { pythonRefLabel } from '../../lib/python-ref.ts'
import { parseStageSummary } from '../../lib/parse-stage.ts'
import { listenOf, sourceSummary, type FlowDefinition, type SinkCfg } from '../../types.ts'
import { formatNumber, t } from '../../i18n/index.ts'
import { edgeAriaLabel } from './aria.ts'

export interface FlowObservation {
  state: 'connecting' | 'connected' | 'reconnecting'
  observed: number
  dropped: number
  parseErrors: number
  lastSeen: number
  /** The runtime reported counters within the last few seconds. */
  up?: boolean
}
export type TopologyData = Record<string, unknown> & {
  role: 'source' | 'decode' | 'stage' | 'destination'
  title: string
  detail: string
  flowName?: string
  observation?: FlowObservation
  hasDeadLetter?: boolean
  onSelect?: (name: string) => void
  /** The overview's focused flow owns this card. */
  focused?: boolean
  /** The source card's flow is stopped (spec.state). */
  stopped?: boolean
}
export type TopologyNode = Node<TopologyData, 'topology'>

// Credentials and conditions do not identify a destination. Subject/topic/scope
// do: two routes to different Aster topics must remain distinguishable.
export function destinationKey(sink: SinkCfg): string {
  return JSON.stringify([sink.type, sink.url ?? '', sink.subject ?? '', sink.aster?.topic ?? '', sink.aster?.scope ?? '', sink.python?.library ?? '', sink.python?.function ?? '', sink.kubernetes ? [sink.kubernetes.namespace ?? '', Object.entries(sink.kubernetes.selector).sort(([a],[b]) => a.localeCompare(b)), sink.kubernetes.port] : null])
}

/** Where a sink delivers, for the destination card. */
function destinationAddress(sink: SinkCfg): string {
  if (sink.kubernetes) {
    const selector = Object.entries(sink.kubernetes.selector).map(([key, value]) => `${key}=${value}`).join(', ')
    return `${t('gofi.canvas.topology.pods-port', 'Pods {{namespace}} :{{port}}', { namespace: sink.kubernetes.namespace || t('gofi.canvas.topology.current-namespace', '(current namespace)'), port: sink.kubernetes.port })}\n${selector}`
  }
  if (sink.url) return sink.url
  return sink.type === 'stdout' ? t('gofi.canvas.topology.process-output', 'Process output') : t('gofi.canvas.topology.destination-not-configured', 'Destination not configured')
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
    const type = sink.type.toUpperCase()
    nodes.push({ id, type: 'topology', position: { x: sinkX, y }, data: {
      role: 'destination', title: rejected ? t('gofi.canvas.topology.dead-letter-destination', 'Dead letter · {{type}}', { type }) : type,
      detail: [destinationAddress(sink), sink.subject, sink.python ? pythonRefLabel(sink.python) : '', sink.aster ? `${sink.aster.scope} / ${sink.aster.topic}` : ''].filter(Boolean).join('\n'),
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
    const exposure = service?.type === 'NodePort'
      ? t('gofi.canvas.topology.node-port-exposure', 'NodePort {{nodePort}} → {{port}}', { nodePort: service.nodePort ?? t('gofi.canvas.topology.node-port-auto', 'auto'), port: service.port ?? listen.port })
      : service ? `${service.type} :${service.port ?? listen.port}` : t('gofi.canvas.topology.listen-socket', 'Listen · socket')
    nodes.push({ id: ingress, type: 'topology', position: { x: 0, y }, data: {
      role: 'source', flowName: flow.name, title: flow.name, hasDeadLetter: Boolean(flow.deadLetterSink),
      detail: flow.source.listen ? `${(listen.network || 'tcp').toUpperCase()} :${listen.port}\n${exposure}` : sourceSummary(flow.source),
    } })
    nodes.push({ id: decode, type: 'topology', position: { x: 280, y }, data: {
      role: 'decode', flowName: flow.name, title: t('gofi.canvas.topology.decode-and-parse', 'Decode & parse'),
      detail: [...(flow.decode?.decoders ?? flow.parser.decoders ?? []).map(codecLabel), flow.parser.python ? pythonRefLabel(flow.parser.python, flow.python?.library) : flow.parser.format || 'raw'].join(' → ') + `\n${t('gofi.canvas.topology.framing-mode', '{{mode}} framing', { mode: flow.framing.mode })}`,
    } })
    const connect = (source: string, target: string, label?: string, deadLetter = false) => edges.push({
      id: `${prefix}:edge:${edges.length}`, source, sourceHandle: deadLetter ? 'rejected' : undefined, target, type: 'default', label, ariaLabel: edgeAriaLabel(source, target),
      data: { flowName: flow.name, deadLetter, observedIngress: source === ingress && target === decode },
      style: deadLetter ? { strokeDasharray: '5 5' } : undefined,
      labelStyle: { fill: 'rgb(var(--g-text-secondary))', fontSize: 10 }, labelBgStyle: { fill: 'rgb(var(--g-primary))', fillOpacity: 1 }, labelBgPadding: [6, 4],
    })
    connect(ingress, decode)
    let terminal = decode
    if (flow.stages.length) {
      const id = `${prefix}:processing`
      const count = flow.stages.length
      nodes.push({ id, type: 'topology', position: { x: 560, y }, data: {
        role: 'stage', flowName: flow.name,
        title: t('gofi.canvas.topology.processing-stages', '{{n}} processing stages', { count, n: formatNumber(count), defaultValue_one: '{{n}} processing stage' }),
        detail: flow.stages.map((stage) => `${stage.id} · ${stage.python?.inlineSource ? t('gofi.canvas.topology.inline-python', 'inline Python') : stage.python ? pythonRefLabel(stage.python, flow.python?.library) : stage.kind === 'parse' ? parseStageSummary(stage, flow.python?.library) : stage.type || stage.kind}`).join('\n'),
      } })
      connect(terminal, id)
      terminal = id
    }
    flow.sinks.forEach((sink) => connect(terminal, destination(sink), sink.when ? t('gofi.canvas.topology.when-label', 'When: {{expression}}', { expression: sink.when }) : undefined))
    if (flow.deadLetterSink) connect(ingress, destination(flow.deadLetterSink, true), t('gofi.canvas.topology.rejected-events', 'Rejected events'), true)
  })
  return { nodes, edges }
}
