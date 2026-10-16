// Pure, React-free conversion between the backend's ListenerDefinition JSON
// contract and a React Flow node/edge graph. Kept free of any React Flow
// runtime dependency (only its `Edge` type is imported) so these functions
// are easy to reason about and manually verify without mounting a canvas.
//
import type { Edge } from '@xyflow/react'
import type { ListenerDefinition } from '../../types'
import type { FlowNode, IngressNode, StageNode, SinkNode } from './types'
import { genId } from '../../lib/id.ts'

const X_STEP = 280
const SINK_Y_STEP = 140

// Fixed id for the single, optional, edge-free dead-letter node. Reserved —
// never used for a "real" fan-out sink node.
const DEADLETTER_ID = 'deadletter'

/** Build a React Flow graph from a ListenerDefinition.
 *
 * Layout: ingress at x=0, each stage at x=280,560,..., then the terminal
 * chain node (last stage, or ingress if there are no stages) fans out via
 * one edge per entry in `def.sinks`, all sink nodes stacked vertically at
 * the terminal x-coordinate. If `def.deadLetterSink` is set, one extra
 * `'deadletter'` sink node is added with NO edges at all — it's a floating
 * config node, not part of the traffic-flow chain — positioned off to the
 * side below ingress.
 *
 * Edges are built by walking `def.stages` in array order — this is the one
 * place array order is trusted, since it's the authoritative order coming
 * straight from the backend. */
export function listenerToFlow(def: ListenerDefinition): { nodes: FlowNode[]; edges: Edge[] } {
  // def.stages/def.sinks are declared non-optional, but the backend's
  // `,omitempty` json tags mean the keys are absent entirely on the wire for
  // an empty slice — guard against the resulting `undefined` at runtime.
  const stages = def.stages ?? []
  const sinks = def.sinks ?? []

  const ingress: IngressNode = {
    id: 'ingress',
    type: 'ingress',
    position: { x: 0, y: 0 },
    data: { source: def.source, framing: def.framing, parser: def.parser },
  }

  const stageNodes: StageNode[] = stages.map((spec, i) => ({
    id: spec.id,
    type: 'stage',
    position: { x: X_STEP * (i + 1), y: 0 },
    data: { spec },
  }))

  const terminalId = stageNodes.length > 0 ? stageNodes[stageNodes.length - 1].id : ingress.id
  const sinkX = X_STEP * (stages.length + 1)

  const sinkNodes: SinkNode[] = sinks.map((sink, i) => ({
    id: genId(),
    type: 'sink',
    position: { x: sinkX, y: i * SINK_Y_STEP },
    data: { sink },
  }))

  const nodes: FlowNode[] = [ingress, ...stageNodes, ...sinkNodes]

  // Chain: ingress -> stages[0] -> stages[1] -> ... -> terminal node.
  const chainIds = [ingress.id, ...stageNodes.map((n) => n.id)]
  const edges: Edge[] = []
  for (let i = 0; i < chainIds.length - 1; i++) {
    const source = chainIds[i]
    const target = chainIds[i + 1]
    edges.push({ id: `e-${source}-${target}`, source, target, type: 'smoothstep' })
  }
  // Fan-out: one edge from the terminal node to each sink node.
  for (const sinkNode of sinkNodes) {
    edges.push({ id: `e-${terminalId}-${sinkNode.id}`, source: terminalId, target: sinkNode.id, type: 'smoothstep' })
  }

  if (def.deadLetterSink) {
    const deadLetterNode: SinkNode = {
      id: DEADLETTER_ID,
      type: 'sink',
      position: { x: 0, y: 220 },
      data: { sink: def.deadLetterSink, isDeadLetter: true },
    }
    nodes.push(deadLetterNode)
    // Intentionally no edges for the dead-letter node.
  }

  return { nodes, edges }
}

/** Derive a ListenerDefinition from the current graph.
 *
 * Graph shape: ingress -> stage[0] -> stage[1] -> ... -> stage[n-1] fanning
 * out, at its end, to one or more sink nodes (one edge per sink). Stage
 * order is derived by walking edges from the ingress node (following
 * source->target hops), NOT from `nodes` array order or x-position —
 * React Flow's internal node array order does not reliably track
 * visual/logical order once a user has dragged nodes around. Sink order is
 * likewise derived from the fan-out point's outgoing edge array order (not
 * visual position), per the same established convention.
 *
 * The optional 'deadletter' node (if present) is excluded entirely from
 * this walk/validation — it carries `deadLetterSink` and has no edges.
 *
 * Throws a clear Error if:
 *  - ingress is missing
 *  - a stage node has zero or >1 outgoing edges (broken chain / branch mid-chain)
 *  - the fan-out point's outgoing edges are a mix of stage and sink targets
 *  - any non-deadletter sink node has outgoing edges (sinks must be terminal)
 *  - zero real sink nodes exist in the graph
 */
export function flowToListener(name: string, nodes: FlowNode[], edges: Edge[]): ListenerDefinition {
  const { ingressNode, deadletterNode, orderedStages, orderedSinks } = walkFlow(nodes, edges)

  return {
    name,
    source: ingressNode.data.source,
    framing: ingressNode.data.framing,
    parser: { ...ingressNode.data.parser, decoders: ingressNode.data.parser.decoders?.map((value) => value.trim()).filter(Boolean) },
    stages: orderedStages.map((n) => n.data.spec),
    sinks: orderedSinks.map((n) => n.data.sink),
    deadLetterSink: deadletterNode?.data.sink,
  }
}

/** Shared walk used by both `flowToListener` and `orderedSinkNodeIds` so
 * there is exactly one validation path for the graph shape. See
 * `flowToListener`'s doc comment above for the exact error conditions this
 * throws on. Returns the ingress node, the optional dead-letter node, the
 * ordered stage-chain nodes, and the ordered (non-deadletter) sink nodes
 * reached at the fan-out point. */
function walkFlow(
  nodes: FlowNode[],
  edges: Edge[],
): {
  ingressNode: IngressNode
  deadletterNode: SinkNode | undefined
  orderedStages: StageNode[]
  orderedSinks: SinkNode[]
} {
  const ingressNode = nodes.find((n): n is IngressNode => n.type === 'ingress')
  if (!ingressNode) throw new Error('flow is missing an ingress node')

  const deadletterNode = nodes.find((n): n is SinkNode => n.type === 'sink' && n.id === DEADLETTER_ID)

  // All "real" (non-deadletter) sink nodes are fan-out candidates.
  const sinkNodeById = new Map<string, SinkNode>()
  for (const n of nodes) {
    if (n.type === 'sink' && n.id !== DEADLETTER_ID) sinkNodeById.set(n.id, n)
  }
  if (sinkNodeById.size === 0) throw new Error('at least one sink is required')

  const stageById = new Map<string, StageNode>()
  for (const n of nodes) {
    if (n.type === 'stage') stageById.set(n.id, n)
  }

  const outgoing = new Map<string, Edge[]>()
  for (const e of edges) {
    const list = outgoing.get(e.source) ?? []
    list.push(e)
    outgoing.set(e.source, list)
  }

  // Validate: no (non-deadletter) sink node has outgoing edges — sinks must
  // be terminal leaves.
  for (const sinkNode of sinkNodeById.values()) {
    if ((outgoing.get(sinkNode.id) ?? []).length > 0) {
      throw new Error('sink nodes cannot have outgoing connections')
    }
  }

  // Walk the linear stage chain from ingress until we reach the fan-out
  // point — the node whose outgoing edges all target sink nodes.
  const orderedStages: StageNode[] = []
  const visited = new Set<string>([ingressNode.id])
  let currentId = ingressNode.id

  for (;;) {
    const edgesFromCurrent = outgoing.get(currentId) ?? []
    if (edgesFromCurrent.length === 0) {
      throw new Error(`flow is broken: node "${currentId}" has no outgoing connection to a sink`)
    }

    const targetsAreSinks = edgesFromCurrent.map((e) => sinkNodeById.has(e.target))
    const allSinks = targetsAreSinks.every(Boolean)
    const anySinks = targetsAreSinks.some(Boolean)

    if (anySinks && !allSinks) {
      throw new Error(
        `node "${currentId}" has a mix of stage and sink targets — a stage's outputs must all be stages or all be sinks, not both`,
      )
    }

    if (allSinks) {
      // Fan-out point reached. Collect every outgoing edge's target, in
      // edge array order, as the ordered sinks list.
      const collected: SinkNode[] = []
      for (const e of edgesFromCurrent) {
        const sinkNode = sinkNodeById.get(e.target)
        if (!sinkNode) throw new Error(`flow references unknown sink node "${e.target}"`)
        collected.push(sinkNode)
      }
      return { ingressNode, deadletterNode, orderedStages, orderedSinks: collected }
    }

    // Not the fan-out point yet: must be a single-edge hop into another stage node.
    if (edgesFromCurrent.length > 1) {
      throw new Error(`flow has a branch at "${currentId}" — a stage's outputs must be a single linear hop`)
    }
    const nextId = edgesFromCurrent[0].target
    if (visited.has(nextId)) {
      throw new Error(`flow has a cycle involving "${nextId}"`)
    }
    visited.add(nextId)

    const stageNode = stageById.get(nextId)
    if (!stageNode) {
      throw new Error(`flow references unknown node "${nextId}" between ingress and the sinks`)
    }
    orderedStages.push(stageNode)
    currentId = nextId
  }
}

/** The ordered, non-deadletter sink NODE IDS reached at the fan-out point —
 * shares `flowToListener`'s validation walk (see `walkFlow`), so it throws
 * the exact same errors for an invalid graph. A `SimulateResult.sinks[i]`
 * (from `GoFiService.Simulate`) maps directly to a canvas node id via
 * `orderedSinkNodeIds(nodes, edges)[i]`, since both are derived from the same
 * fan-out edge order. */
export function orderedSinkNodeIds(nodes: FlowNode[], edges: Edge[]): string[] {
  const { orderedSinks } = walkFlow(nodes, edges)
  return orderedSinks.map((n) => n.id)
}
