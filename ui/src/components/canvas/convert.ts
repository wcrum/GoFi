// Pure, React-free conversion between the backend's FlowDefinition JSON
// contract and a React Flow node/edge graph. Kept free of any React Flow
// runtime dependency (only its `Edge` type is imported) so these functions
// are easy to reason about and manually verify without mounting a canvas.
//
import type { Edge } from '@xyflow/react'
import type { FlowDefinition } from '../../types'
import type { DecodeNode, FlowNode, FrameNode, IngressNode, ParseNode, StageNode, SinkNode } from './types'
import { genId } from '../../lib/id.ts'

const X_STEP = 340

/** Canonical order of the pipeline steps ahead of the stages. Only the Source
 * is required: without Frame the whole connection (or datagram) is one frame,
 * without Decode nothing is decoded, and without Parse frames pass through
 * unparsed (`fields.raw`). */
export const PIPELINE_RANK: Record<string, number> = { ingress: 0, frame: 1, decode: 2, parse: 3 }
export const DEFAULT_FRAMING = { mode: 'raw' }
export const DEFAULT_PARSER = { format: 'none' }
const SINK_Y_STEP = 140

// Fixed id for the single, optional, edge-free dead-letter node. Reserved —
// never used for a "real" fan-out sink node.
const DEADLETTER_ID = 'deadletter'

/** Build a React Flow graph from a FlowDefinition.
 *
 * Layout: the fixed Source, Frame, Decode, and Parse nodes at x=0..840, each
 * stage after them, then the terminal chain node (last stage, or Parse if
 * there are no stages) fans out via
 * one edge per entry in `def.sinks`, all sink nodes stacked vertically at
 * the terminal x-coordinate. If `def.deadLetterSink` is set, one extra
 * `'deadletter'` sink node is added with NO edges at all — it's a floating
 * config node, not part of the traffic-flow chain — positioned off to the
 * side below ingress.
 *
 * Edges are built by walking `def.stages` in array order — this is the one
 * place array order is trusted, since it's the authoritative order coming
 * straight from the backend. */
export function definitionToGraph(def: FlowDefinition): { nodes: FlowNode[]; edges: Edge[] } {
  // def.stages/def.sinks are declared non-optional, but the backend's
  // `,omitempty` json tags mean the keys are absent entirely on the wire for
  // an empty slice — guard against the resulting `undefined` at runtime.
  const stages = def.stages ?? []
  const sinks = def.sinks ?? []

  // Source (required) -> Frame? -> Decode? -> Parse?, each optional step
  // present only when it does something.
  const { decoders, maxDecodedBytes, ...parser } = def.parser ?? DEFAULT_PARSER
  const ingress: IngressNode = { id: 'ingress', type: 'ingress', position: { x: 0, y: 0 }, deletable: false, data: { source: def.source } }
  const prefix: FlowNode[] = [ingress]
  if (def.framing?.mode && def.framing.mode !== 'raw') {
    prefix.push({ id: 'frame', type: 'frame', position: { x: 0, y: 0 }, data: { framing: def.framing } } satisfies FrameNode)
  }
  if ((decoders ?? []).length || maxDecodedBytes) {
    prefix.push({ id: 'decode', type: 'decode', position: { x: 0, y: 0 }, data: { decoders: decoders ?? [], maxDecodedBytes } } satisfies DecodeNode)
  }
  if (parser.format && parser.format !== 'none') {
    prefix.push({ id: 'parse', type: 'parse', position: { x: 0, y: 0 }, data: { parser } } satisfies ParseNode)
  }
  prefix.forEach((node, i) => { node.position = { x: X_STEP * i, y: 0 } })
  const PREFIX_LENGTH = prefix.length
  const last = prefix[prefix.length - 1]

  const stageNodes: StageNode[] = stages.map((spec, i) => ({
    id: spec.id,
    type: 'stage',
    position: { x: X_STEP * (i + PREFIX_LENGTH), y: 0 },
    data: { spec },
  }))

  const terminalId = stageNodes.length > 0 ? stageNodes[stageNodes.length - 1].id : last.id
  const sinkX = X_STEP * (stages.length + PREFIX_LENGTH)

  const sinkNodes: SinkNode[] = sinks.map((sink, i) => ({
    id: genId(),
    type: 'sink',
    position: { x: sinkX, y: i * SINK_Y_STEP },
    data: { sink },
  }))

  const nodes: FlowNode[] = [...prefix, ...stageNodes, ...sinkNodes]

  // Chain: source -> [frame] -> [decode] -> [parse] -> stages[0] -> ... -> terminal.
  const chainIds = [...prefix.map((n) => n.id), ...stageNodes.map((n) => n.id)]
  const edges: Edge[] = []
  for (let i = 0; i < chainIds.length - 1; i++) {
    const source = chainIds[i]
    const target = chainIds[i + 1]
    edges.push({ id: `e-${source}-${target}`, source, target, type: 'default' })
  }
  // Fan-out: one edge from the terminal node to each sink node.
  for (const sinkNode of sinkNodes) {
    edges.push({ id: `e-${terminalId}-${sinkNode.id}`, source: terminalId, target: sinkNode.id, type: 'default' })
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

/** Derive a FlowDefinition from the current graph.
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
export function graphToDefinition(name: string, nodes: FlowNode[], edges: Edge[]): FlowDefinition {
  const { ingressNode, frameNode, decodeNode, parseNode, deadletterNode, orderedStages, orderedSinks } = walkFlow(nodes, edges)
  const decoders = (decodeNode?.data.decoders ?? []).filter((step) => step.type && (step.type !== 'python' || step.python?.function))

  return {
    name,
    source: ingressNode.data.source,
    framing: frameNode?.data.framing ?? DEFAULT_FRAMING,
    parser: {
      ...(parseNode?.data.parser ?? DEFAULT_PARSER),
      ...(decoders.length ? { decoders } : {}),
      ...(decodeNode?.data.maxDecodedBytes ? { maxDecodedBytes: decodeNode.data.maxDecodedBytes } : {}),
    },
    stages: orderedStages.map((n) => n.data.spec),
    sinks: orderedSinks.map((n) => n.data.sink),
    deadLetterSink: deadletterNode?.data.sink,
  }
}

/** Shared walk used by both `graphToDefinition` and `orderedSinkNodeIds` so
 * there is exactly one validation path for the graph shape. See
 * `graphToDefinition`'s doc comment above for the exact error conditions this
 * throws on. Returns the ingress node, the optional dead-letter node, the
 * ordered stage-chain nodes, and the ordered (non-deadletter) sink nodes
 * reached at the fan-out point. */
function walkFlow(
  nodes: FlowNode[],
  edges: Edge[],
): {
  ingressNode: IngressNode
  frameNode: FrameNode | undefined
  decodeNode: DecodeNode | undefined
  parseNode: ParseNode | undefined
  deadletterNode: SinkNode | undefined
  orderedStages: StageNode[]
  orderedSinks: SinkNode[]
} {
  const ingressNode = nodes.find((n): n is IngressNode => n.type === 'ingress')
  if (!ingressNode) throw new Error('flow is missing its source')

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
  // Walk the optional Frame/Decode/Parse steps after the Source, each at most
  // once and in canonical order; the stage chain starts after the last one.
  const nodeById = new Map(nodes.map((n) => [n.id, n]))
  const steps: Partial<Record<'frame' | 'decode' | 'parse', FlowNode>> = {}
  const visited = new Set<string>([ingressNode.id])
  let currentId = ingressNode.id
  let rank = 0
  for (;;) {
    const out = outgoing.get(currentId) ?? []
    const next = out.length === 1 ? nodeById.get(out[0].target) : undefined
    if (!next || !(next.type === 'frame' || next.type === 'decode' || next.type === 'parse')) break
    if (PIPELINE_RANK[next.type] <= rank) throw new Error(`${next.type} must come after ${nodeById.get(currentId)?.type}: the order is source, frame, decode, parse`)
    rank = PIPELINE_RANK[next.type]
    steps[next.type] = next
    visited.add(next.id)
    currentId = next.id
  }
  for (const n of nodes) {
    if ((n.type === 'frame' || n.type === 'decode' || n.type === 'parse') && steps[n.type] !== n) {
      throw new Error(`the ${n.type} step must be connected directly after the source (source, frame, decode, parse)`)
    }
  }
  const frameNode = steps.frame as FrameNode | undefined
  const decodeNode = steps.decode as DecodeNode | undefined
  const parseNode = steps.parse as ParseNode | undefined

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
      return { ingressNode, frameNode, decodeNode, parseNode, deadletterNode, orderedStages, orderedSinks: collected }
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
 * shares `graphToDefinition`'s validation walk (see `walkFlow`), so it throws
 * the exact same errors for an invalid graph. A `SimulateResult.sinks[i]`
 * (from `GoFiService.Simulate`) maps directly to a canvas node id via
 * `orderedSinkNodeIds(nodes, edges)[i]`, since both are derived from the same
 * fan-out edge order. */
export function orderedSinkNodeIds(nodes: FlowNode[], edges: Edge[]): string[] {
  const { orderedSinks } = walkFlow(nodes, edges)
  return orderedSinks.map((n) => n.id)
}
