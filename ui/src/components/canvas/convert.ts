// Pure, React-free conversion between the backend's FlowDefinition JSON
// contract and a React Flow node/edge graph. Kept free of any React Flow
// runtime dependency (only its `Edge` type is imported) so these functions
// are easy to reason about and manually verify without mounting a canvas.
//
import type { Edge } from '@xyflow/react'
import type { Encoding, FlowDefinition, FlowPython, SinkCfg } from '../../types'
import { normalizeFlow } from '../../lib/flow-normalize.ts'
import type { DecodeNode, EncodeNode, FlowNode, FrameNode, IngressNode, ParseNode, StageNode, SinkNode } from './types'
import { genId } from '../../lib/id.ts'
import { t } from '../../i18n/index.ts'
import { edgeAriaLabel } from './aria.ts'

const X_STEP = 340

/** Canonical order of the pipeline steps ahead of the stages. Only the Source
 * is required: without Frame the whole connection (or datagram) is one frame,
 * without Decode nothing is decoded, and without Parse frames pass through
 * unparsed (`fields.raw`). */
export const PIPELINE_RANK: Record<string, number> = { ingress: 0, frame: 1, decode: 2, parse: 3 }
export const DEFAULT_FRAMING = { mode: 'raw' }
export const DEFAULT_PARSER = { format: 'none' }
const SINK_Y_STEP = 140

// Fixed id for the single, optional dead-letter node. Reserved — never used
// for a "real" fan-out sink node.
export const DEADLETTER_ID = 'deadletter'

/** Identity of an encoding for sharing one Encode node: sinks whose saved
 * `encoding` is the same value hang off the same node. Exact (not semantic):
 * `{format: wrapped}` and `{}` mean the same thing to the runtime but stay
 * separate nodes, so saving reproduces each sink's encoding verbatim. */
export const encodingKey = (encoding: Encoding): string => JSON.stringify([encoding.format ?? null, encoding.encoders ?? null])

/** The sink as the node carries it: its encoding lives on the Encode node. */
function withoutEncoding(sink: SinkCfg): SinkCfg {
  const { encoding: _encoding, ...rest } = sink
  void _encoding
  return rest
}

/** The encoding as saved: unfinished encoder steps (no codec chosen, or a
 * Python step without a function) are dropped, like unfinished decoders, and
 * an empty encoder list is omitted (as the backend writes it). */
function savedEncoding(encoding: Encoding): Encoding {
  if (!encoding.encoders) return encoding
  const encoders = encoding.encoders.filter((step) => step.type && (step.type !== 'python' || step.python?.function))
  const { encoders: _all, ...rest } = encoding
  void _all
  return encoders.length ? { ...rest, encoders } : rest
}

/** Build a React Flow graph from a FlowDefinition.
 *
 * Layout: the fixed Source, Frame, Decode, and Parse nodes at x=0..840, each
 * stage after them, then the terminal chain node (last stage, or Parse if
 * there are no stages) fans out via one edge per entry in `def.sinks`, all
 * sink nodes stacked vertically at the terminal x-coordinate.
 *
 * Sinks that carry an `encoding` are fed through an Encode node instead:
 * terminal -> Encode -> sink. Sinks with the same encoding share one Encode
 * node, so one node may fan out to several sinks. Encode nodes take the
 * column before the sinks, each centred on the sinks it feeds.
 *
 * If `def.deadLetterSink` is set, one extra `'deadletter'` sink node is
 * added. Nothing in the traffic chain connects to it (it receives rejected
 * messages), but its encoding is an Encode node like any other sink's: a
 * shared one when a normal sink has the same encoding (the dead-letter node
 * then sits below the sinks), else its own, floating beside it under the
 * source.
 *
 * Edges are built by walking `def.stages` in array order — this is the one
 * place array order is trusted, since it's the authoritative order coming
 * straight from the backend. Sink edges are pushed in `def.sinks` order,
 * which is what `graphToDefinition` reads the sink order back from. */
export function definitionToGraph(input: FlowDefinition): { nodes: FlowNode[]; edges: Edge[] } {
  // Older definitions keep decoders under parser; the Decode node edits `decode`.
  const def = normalizeFlow(input)
  // def.stages/def.sinks are declared non-optional, but the backend's
  // `,omitempty` json tags mean the keys are absent entirely on the wire for
  // an empty slice — guard against the resulting `undefined` at runtime.
  const stages = def.stages ?? []
  const sinks = def.sinks ?? []

  // Source (required) -> Frame? -> Decode? -> Parse?, each optional step
  // present only when it does something.
  const parser = def.parser ?? DEFAULT_PARSER
  const { decoders, maxDecodedBytes } = def.decode ?? { decoders: [] }
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

  // One Encode node per distinct encoding, shared by every sink that has it.
  const encodeNodes: EncodeNode[] = []
  const encoderByKey = new Map<string, EncodeNode>()
  const encoderFor = (encoding: Encoding | undefined): EncodeNode | undefined => {
    if (!encoding) return undefined
    const key = encodingKey(encoding)
    let node = encoderByKey.get(key)
    if (!node) {
      node = { id: genId(), type: 'encode', position: { x: 0, y: 0 }, data: { encoding } }
      encoderByKey.set(key, node)
      encodeNodes.push(node)
    }
    return node
  }
  const sinkEncoders = sinks.map((sink) => encoderFor(sink.encoding))
  const chainEncoders = new Set(sinkEncoders.filter((node): node is EncodeNode => !!node))
  const deadLetterEncoder = def.deadLetterSink ? encoderFor(def.deadLetterSink.encoding) : undefined

  const encodeX = X_STEP * (stages.length + PREFIX_LENGTH)
  const sinkX = chainEncoders.size ? encodeX + X_STEP : encodeX

  const sinkNodes: SinkNode[] = sinks.map((sink, i) => ({
    id: genId(),
    type: 'sink',
    position: { x: sinkX, y: i * SINK_Y_STEP },
    data: { sink: withoutEncoding(sink) },
  }))
  for (const encoder of chainEncoders) {
    const ys = sinkNodes.filter((_, i) => sinkEncoders[i] === encoder).map((node) => node.position.y)
    encoder.position = { x: encodeX, y: ys.reduce((sum, y) => sum + y, 0) / ys.length }
  }

  const nodes: FlowNode[] = [...prefix, ...stageNodes, ...encodeNodes, ...sinkNodes]

  // Chain: source -> [frame] -> [decode] -> [parse] -> stages[0] -> ... -> terminal.
  const chainIds = [...prefix.map((n) => n.id), ...stageNodes.map((n) => n.id)]
  const edges: Edge[] = []
  const connect = (source: string, target: string) => edges.push({ id: `e-${source}-${target}`, source, target, type: 'default', ariaLabel: edgeAriaLabel(source, target) })
  for (let i = 0; i < chainIds.length - 1; i++) connect(chainIds[i], chainIds[i + 1])
  // Fan-out: the terminal node feeds each Encode node, and each sink is fed
  // by its Encode node or by the terminal node directly, in `def.sinks` order.
  for (const encoder of chainEncoders) connect(terminalId, encoder.id)
  sinkNodes.forEach((sinkNode, i) => connect(sinkEncoders[i]?.id ?? terminalId, sinkNode.id))

  if (def.deadLetterSink) {
    const shared = deadLetterEncoder && chainEncoders.has(deadLetterEncoder)
    const position = shared ? { x: sinkX, y: sinks.length * SINK_Y_STEP + SINK_Y_STEP / 2 } : { x: deadLetterEncoder ? X_STEP : 0, y: 220 }
    if (deadLetterEncoder && !shared) deadLetterEncoder.position = { x: 0, y: 220 }
    nodes.push({ id: DEADLETTER_ID, type: 'sink', position, data: { sink: withoutEncoding(def.deadLetterSink), isDeadLetter: true } } satisfies SinkNode)
    if (deadLetterEncoder) connect(deadLetterEncoder.id, DEADLETTER_ID)
  }

  return { nodes, edges }
}

/** Derive a FlowDefinition from the current graph.
 *
 * Graph shape: ingress -> stage[0] -> stage[1] -> ... -> stage[n-1] fanning
 * out, at its end, to one or more sink nodes, directly or through Encode
 * nodes (terminal -> Encode -> sinks). Stage order is derived by walking
 * edges from the ingress node (following source->target hops), NOT from
 * `nodes` array order or x-position — React Flow's internal node array order
 * does not reliably track visual/logical order once a user has dragged nodes
 * around. Sink order is likewise derived from the edge array order of the
 * edges that end at each sink (not visual position), per the same
 * established convention; a new connection appends, so a new sink is last.
 *
 * Each sink's `encoding` is the encoding of the Encode node it is connected
 * from, or absent when it connects from the chain directly. The 'deadletter'
 * node (if present) is not part of the chain walk: it carries
 * `deadLetterSink`, and its only allowed connection is from an Encode node,
 * which may be one the chain feeds or one that floats beside it.
 *
 * Throws a clear Error if:
 *  - ingress is missing
 *  - a stage node has zero or >1 outgoing edges (broken chain / branch mid-chain)
 *  - the fan-out point's outgoing edges are a mix of stage and sink/Encode targets
 *  - any non-deadletter sink node has outgoing edges (sinks must be terminal)
 *  - an Encode node connects to anything but a sink, feeds a normal sink
 *    without being fed by the chain, or a sink is connected more than once
 *  - anything but an Encode node connects to the dead-letter sink
 *  - zero real sink nodes exist in the graph
 */
export function graphToDefinition(name: string, nodes: FlowNode[], edges: Edge[], settings: FlowSettings = {}): FlowDefinition {
  const { ingressNode, frameNode, decodeNode, parseNode, deadletterNode, orderedStages, orderedSinks, encoderOf } = walkFlow(nodes, edges)
  const decoders = (decodeNode?.data.decoders ?? []).filter((step) => step.type && (step.type !== 'python' || step.python?.function))
  const maxDecodedBytes = decodeNode?.data.maxDecodedBytes
  const withEncoding = (node: SinkNode): SinkCfg => {
    const sink = withoutEncoding(node.data.sink)
    const encoder = encoderOf.get(node.id)
    return encoder ? { ...sink, encoding: savedEncoding(encoder.data.encoding) } : sink
  }

  return {
    name,
    source: ingressNode.data.source,
    framing: frameNode?.data.framing ?? DEFAULT_FRAMING,
    ...(decoders.length || maxDecodedBytes ? { decode: { decoders, ...(maxDecodedBytes ? { maxDecodedBytes } : {}) } } : {}),
    parser: parseNode?.data.parser ?? DEFAULT_PARSER,
    stages: orderedStages.map((n) => n.data.spec),
    sinks: orderedSinks.map(withEncoding),
    deadLetterSink: deadletterNode ? withEncoding(deadletterNode) : undefined,
    ...(settings.python?.library ? { python: settings.python } : {}),
  }
}

/** Flow-wide settings that are not nodes on the graph. */
export interface FlowSettings { python?: FlowPython }

/** The Encode node a sink (the dead-letter node included) is connected from,
 * if any. A plain lookup for the canvas and inspector; validation is
 * `walkFlow`'s job. */
export function encoderOfSink(nodes: FlowNode[], edges: Edge[], sinkId: string): EncodeNode | undefined {
  for (const edge of edges) {
    if (edge.target !== sinkId) continue
    const source = nodes.find((n) => n.id === edge.source)
    if (source?.type === 'encode') return source
  }
  return undefined
}

/** Shared walk used by both `graphToDefinition` and `orderedSinkNodeIds` so
 * there is exactly one validation path for the graph shape. See
 * `graphToDefinition`'s doc comment above for the exact error conditions this
 * throws on. Returns the ingress node, the optional dead-letter node, the
 * ordered stage-chain nodes, the ordered (non-deadletter) sink nodes reached
 * at the fan-out point, and the Encode node feeding each sink (by sink id,
 * the dead-letter node included). */
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
  encoderOf: Map<string, EncodeNode>
} {
  const ingressNode = nodes.find((n): n is IngressNode => n.type === 'ingress')
  if (!ingressNode) throw new Error(t('gofi.canvas.convert.missing-source', 'flow is missing its source'))

  const deadletterNode = nodes.find((n): n is SinkNode => n.type === 'sink' && n.id === DEADLETTER_ID)

  // All "real" (non-deadletter) sink nodes are fan-out candidates.
  const sinkNodeById = new Map<string, SinkNode>()
  for (const n of nodes) {
    if (n.type === 'sink' && n.id !== DEADLETTER_ID) sinkNodeById.set(n.id, n)
  }
  if (sinkNodeById.size === 0) throw new Error(t('gofi.canvas.convert.sink-required', 'at least one sink is required'))

  const stageById = new Map<string, StageNode>()
  const encodeById = new Map<string, EncodeNode>()
  for (const n of nodes) {
    if (n.type === 'stage') stageById.set(n.id, n)
    if (n.type === 'encode') encodeById.set(n.id, n)
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
      throw new Error(t('gofi.canvas.convert.sink-outgoing', 'sink nodes cannot have outgoing connections'))
    }
  }
  // Validate: Encode nodes only feed sinks (the dead-letter sink included).
  for (const encodeNode of encodeById.values()) {
    for (const e of outgoing.get(encodeNode.id) ?? []) {
      if (!sinkNodeById.has(e.target) && !(deadletterNode && e.target === DEADLETTER_ID)) {
        throw new Error(t('gofi.canvas.convert.encode-outgoing', 'an Encode step can only connect to sinks'))
      }
    }
  }
  // Validate: the dead-letter sink receives rejected messages, so nothing in
  // the chain connects to it — only an Encode step may, and only one.
  const deadLetterEdges = edges.filter((e) => e.target === DEADLETTER_ID)
  for (const e of deadLetterEdges) {
    if (!encodeById.has(e.source)) throw new Error(t('gofi.canvas.convert.deadletter-incoming', 'only an Encode step can connect to the dead-letter sink: it receives the messages the flow rejects'))
  }
  if (deadLetterEdges.length > 1) throw new Error(t('gofi.canvas.convert.deadletter-multiple', 'the dead-letter sink can be connected from one Encode step only'))

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
    if (PIPELINE_RANK[next.type] <= rank) throw new Error(t('gofi.canvas.convert.step-order', '{{step}} must come after {{previous}}: the order is source, frame, decode, parse', { step: next.type, previous: nodeById.get(currentId)?.type }))
    rank = PIPELINE_RANK[next.type]
    steps[next.type] = next
    visited.add(next.id)
    currentId = next.id
  }
  for (const n of nodes) {
    if ((n.type === 'frame' || n.type === 'decode' || n.type === 'parse') && steps[n.type] !== n) {
      throw new Error(t('gofi.canvas.convert.step-disconnected', 'the {{step}} step must be connected directly after the source (source, frame, decode, parse)', { step: n.type }))
    }
  }
  const frameNode = steps.frame as FrameNode | undefined
  const decodeNode = steps.decode as DecodeNode | undefined
  const parseNode = steps.parse as ParseNode | undefined

  // A destination is where the chain ends: a sink, or an Encode node that
  // feeds sinks.
  const isDestination = (id: string) => sinkNodeById.has(id) || encodeById.has(id)
  const edgeIndex = new Map(edges.map((e, i) => [e, i]))

  for (;;) {
    const edgesFromCurrent = outgoing.get(currentId) ?? []
    if (edgesFromCurrent.length === 0) {
      throw new Error(t('gofi.canvas.convert.no-outgoing', 'flow is broken: node "{{node}}" has no outgoing connection to a sink', { node: currentId }))
    }

    const targetsAreDestinations = edgesFromCurrent.map((e) => isDestination(e.target))
    const allDestinations = targetsAreDestinations.every(Boolean)
    const anyDestinations = targetsAreDestinations.some(Boolean)

    if (anyDestinations && !allDestinations) {
      throw new Error(
        t('gofi.canvas.convert.mixed-targets', 'node "{{node}}" has a mix of stage and sink targets — a stage\'s outputs must all be stages or all be sinks, not both', { node: currentId }),
      )
    }

    if (allDestinations) {
      // Fan-out point reached. Every sink fed from here, directly or through
      // an Encode node, in the array order of the edge that reaches it.
      const reached = new Map<string, { sink: SinkNode; encoder?: EncodeNode; order: number }>()
      const reach = (e: Edge, encoder?: EncodeNode) => {
        const sink = sinkNodeById.get(e.target)
        if (!sink) throw new Error(t('gofi.canvas.convert.unknown-sink', 'flow references unknown sink node "{{node}}"', { node: e.target }))
        if (reached.has(sink.id)) throw new Error(t('gofi.canvas.convert.sink-multiple-incoming', 'a sink is connected more than once: connect it from the last stage or from one Encode step'))
        reached.set(sink.id, { sink, encoder, order: edgeIndex.get(e) ?? 0 })
      }
      const chainEncoders = new Set<string>()
      for (const e of edgesFromCurrent) {
        const encoder = encodeById.get(e.target)
        if (!encoder) { reach(e); continue }
        if (chainEncoders.has(encoder.id)) continue
        chainEncoders.add(encoder.id)
        for (const out of outgoing.get(encoder.id) ?? []) if (out.target !== DEADLETTER_ID) reach(out, encoder)
      }
      // An Encode step the chain does not feed may only feed the dead-letter
      // sink (which has no chain input of its own); a normal sink behind it
      // would never receive anything.
      for (const encoder of encodeById.values()) {
        if (chainEncoders.has(encoder.id)) continue
        for (const out of outgoing.get(encoder.id) ?? []) {
          if (out.target !== DEADLETTER_ID) throw new Error(t('gofi.canvas.convert.encode-detached', 'the Encode step feeding a sink must itself be connected from the last stage'))
        }
      }
      const encoderOf = new Map<string, EncodeNode>()
      const orderedSinks = [...reached.values()].sort((a, b) => a.order - b.order).map(({ sink, encoder }) => {
        if (encoder) encoderOf.set(sink.id, encoder)
        return sink
      })
      const deadLetterEncoder = deadLetterEdges[0] ? encodeById.get(deadLetterEdges[0].source) : undefined
      if (deadLetterEncoder) encoderOf.set(DEADLETTER_ID, deadLetterEncoder)
      return { ingressNode, frameNode, decodeNode, parseNode, deadletterNode, orderedStages, orderedSinks, encoderOf }
    }

    // Not the fan-out point yet: must be a single-edge hop into another stage node.
    if (edgesFromCurrent.length > 1) {
      throw new Error(t('gofi.canvas.convert.branch', 'flow has a branch at "{{node}}" — a stage\'s outputs must be a single linear hop', { node: currentId }))
    }
    const nextId = edgesFromCurrent[0].target
    if (visited.has(nextId)) {
      throw new Error(t('gofi.canvas.convert.cycle', 'flow has a cycle involving "{{node}}"', { node: nextId }))
    }
    visited.add(nextId)

    const stageNode = stageById.get(nextId)
    if (!stageNode) {
      throw new Error(t('gofi.canvas.convert.unknown-node', 'flow references unknown node "{{node}}" between ingress and the sinks', { node: nextId }))
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
