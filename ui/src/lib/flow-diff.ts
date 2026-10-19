// Pure, React-free structural diff of two flow definitions, laid out like the
// canvas (convert.ts's definitionToGraph) so the Resource tab can draw both
// versions as one graph with each node and edge marked added, removed,
// changed, or unchanged.
//
// Node matching (the limits of what "the same node" means across versions):
//  - Source, Frame, Decode, Parse: by their fixed ids (ingress/frame/decode/parse).
//  - Stages (parse stages included): by StageSpec.id, which is generated once when a stage is added and
//    saved with the flow. A stage deleted and re-added reads as removed + added;
//    a stage whose kind changed under the same id reads as changed.
//  - Sinks have no id. They are matched first by identity (type plus where they
//    send: url, subject, Kubernetes selector/port, Python function), in order;
//    leftovers are then paired in order with a leftover of the same type (so an
//    edited URL reads as one changed sink, not removed + added). Two sinks of
//    one type that both changed address and swapped places can pair the wrong
//    way round; the fields listed are still a correct before/after of the pair.
//  - The dead-letter sink: by its fixed id.
// Reordering stages shows as changed edges (old hops removed, new ones added)
// while the stage nodes themselves stay unchanged.
import type { FlowDefinition, SinkCfg } from '../types'
import type { FlowNode } from '../components/canvas/types'
import { definitionToGraph } from '../components/canvas/convert.ts'
import { t } from '../i18n/index.ts'

export type DiffStatus = 'added' | 'removed' | 'changed' | 'unchanged'

/** One leaf field that differs; undefined means absent on that side. */
export interface FieldChange { path: string; before?: unknown; after?: unknown }

export interface DiffNode {
  /** Stable across both versions (see the matching notes above). */
  id: string
  type: FlowNode['type']
  position: { x: number; y: number }
  /** The node's data from the "after" version, or "before" when removed. */
  data: FlowNode['data']
  status: DiffStatus
  changes: FieldChange[]
}
export interface DiffEdge { id: string; source: string; target: string; status: Exclude<DiffStatus, 'changed'> }
export interface FlowDiff {
  nodes: DiffNode[]
  edges: DiffEdge[]
  counts: Record<DiffStatus, number>
}

const X_STEP = 340
const SINK_Y_STEP = 140
const DEADLETTER_ID = 'deadletter'

/** Identity of a sink for matching: its type and where it sends. */
export function sinkIdentity(sink: SinkCfg): string {
  const k8s = sink.kubernetes ? `${Object.entries(sink.kubernetes.selector ?? {}).sort().map(([k, v]) => `${k}=${v}`).join(',')}:${sink.kubernetes.port}` : ''
  const python = sink.python ? `${sink.python.library ?? ''}.${sink.python.function}` : ''
  return [sink.type, sink.url ?? '', sink.subject ?? '', k8s, python].join('|')
}

/** Pairs before/after sinks (see the matching notes above). Returns, for each
 * after sink, the index of its before sink or -1 when added. */
export function matchSinks(before: SinkCfg[], after: SinkCfg[]): number[] {
  const match = after.map(() => -1)
  const used = new Set<number>()
  after.forEach((sink, j) => {
    const id = sinkIdentity(sink)
    const i = before.findIndex((candidate, index) => !used.has(index) && sinkIdentity(candidate) === id)
    if (i >= 0) { match[j] = i; used.add(i) }
  })
  after.forEach((sink, j) => {
    if (match[j] >= 0) return
    const i = before.findIndex((candidate, index) => !used.has(index) && candidate.type === sink.type)
    if (i >= 0) { match[j] = i; used.add(i) }
  })
  return match
}

/** Leaf-level differences between two values, as dotted paths
 * ("expression", "decoders[1].type", "headers.Authorization"). */
export function fieldChanges(before: unknown, after: unknown, path = ''): FieldChange[] {
  if (isEmpty(before) && isEmpty(after)) return []
  if (isPlainObject(before) && isPlainObject(after)) {
    const keys = [...new Set([...Object.keys(before), ...Object.keys(after)])].sort()
    return keys.flatMap(key => fieldChanges(before[key], after[key], path ? `${path}.${key}` : key))
  }
  if (Array.isArray(before) && Array.isArray(after)) {
    const length = Math.max(before.length, after.length)
    return Array.from({ length }, (_, i) => fieldChanges(before[i], after[i], `${path}[${i}]`)).flat()
  }
  const structured = (value: unknown) => isPlainObject(value) || Array.isArray(value)
  if (structured(before) || structured(after)) {
    // A structure against nothing: list its leaves, so an added or removed
    // block reads field by field.
    if (isEmpty(before)) return leaves(after, path).map(([p, value]) => ({ path: p, after: value }))
    if (isEmpty(after)) return leaves(before, path).map(([p, value]) => ({ path: p, before: value }))
    return [{ path, before, after }]
  }
  if (JSON.stringify(before) === JSON.stringify(after)) return []
  return [{ path, ...(isEmpty(before) ? {} : { before }), ...(isEmpty(after) ? {} : { after }) }]
}

/** [path, value] for every non-empty leaf of value. */
function leaves(value: unknown, path: string): [string, unknown][] {
  if (isEmpty(value)) return []
  if (Array.isArray(value)) return value.flatMap((item, i) => leaves(item, `${path}[${i}]`))
  if (isPlainObject(value)) return Object.keys(value).sort().flatMap(key => leaves(value[key], path ? `${path}.${key}` : key))
  return [[path, value]]
}

const isPlainObject = (value: unknown): value is Record<string, unknown> => !!value && typeof value === 'object' && !Array.isArray(value)
/** Absent, null, and empty containers are all "not set" in a flow definition
 * (the backend omits them on the wire). */
const isEmpty = (value: unknown) => value === undefined || value === null
  || (Array.isArray(value) && value.length === 0) || (isPlainObject(value) && Object.keys(value).length === 0)

/** The diff of `before` -> `after` as one graph. Either side may be null (an
 * empty document), in which case everything reads as added or removed. */
export function diffFlows(before: FlowDefinition | null, after: FlowDefinition | null): FlowDiff {
  const a = keyed(before)
  const b = keyed(after)

  // Sinks: re-key the before sinks onto the after sinks they match.
  const match = matchSinks(a.sinks.map(n => sinkOf(n)), b.sinks.map(n => sinkOf(n)))
  const beforeSinkKey = new Map<number, string>()
  b.sinks.forEach((node, j) => { if (match[j] >= 0) beforeSinkKey.set(match[j], node.id) })
  let removedSink = 0
  a.sinks.forEach((node, i) => { node.id = beforeSinkKey.get(i) ?? `sink:removed-${removedSink++}` })
  const rekey = new Map(a.sinkOriginalIds.map((original, i) => [original, a.sinks[i].id]))
  for (const edge of a.edges) { edge.target = rekey.get(edge.target) ?? edge.target; edge.source = rekey.get(edge.source) ?? edge.source }

  // Chain order: the after chain, with removed nodes spliced in after their
  // nearest surviving predecessor in the before chain.
  const chain = [...b.chain]
  a.chain.forEach((id, i) => {
    if (chain.includes(id)) return
    const anchor = a.chain.slice(0, i).reverse().find(prev => chain.includes(prev))
    chain.splice(anchor ? chain.indexOf(anchor) + 1 : 0, 0, id)
  })
  const sinkOrder = [...b.sinks.map(n => n.id), ...a.sinks.map(n => n.id).filter(id => !b.sinks.some(n => n.id === id))]

  const beforeById = new Map(a.nodes.map(n => [n.id, n]))
  const afterById = new Map(b.nodes.map(n => [n.id, n]))
  const position = (id: string): { x: number; y: number } => {
    if (id === DEADLETTER_ID) return { x: 0, y: 220 }
    const c = chain.indexOf(id)
    if (c >= 0) return { x: X_STEP * c, y: 0 }
    return { x: X_STEP * chain.length, y: SINK_Y_STEP * sinkOrder.indexOf(id) }
  }

  const ids = [...chain, ...sinkOrder, ...(beforeById.has(DEADLETTER_ID) || afterById.has(DEADLETTER_ID) ? [DEADLETTER_ID] : [])]
  const nodes: DiffNode[] = ids.map(id => {
    const old = beforeById.get(id)
    const next = afterById.get(id)
    const changes = fieldChanges(old ? comparable(old) : undefined, next ? comparable(next) : undefined)
    const status: DiffStatus = !old ? 'added' : !next ? 'removed' : changes.length ? 'changed' : 'unchanged'
    const source = (next ?? old)!
    return { id, type: source.type, position: position(id), data: source.data, status, changes }
  })

  const edgeKey = (e: { source: string; target: string }) => `${e.source}->${e.target}`
  const beforeEdges = new Set(a.edges.map(edgeKey))
  const afterEdges = new Set(b.edges.map(edgeKey))
  const edges: DiffEdge[] = []
  for (const e of b.edges) edges.push({ id: `e:${edgeKey(e)}`, source: e.source, target: e.target, status: beforeEdges.has(edgeKey(e)) ? 'unchanged' : 'added' })
  for (const e of a.edges) if (!afterEdges.has(edgeKey(e))) edges.push({ id: `e:${edgeKey(e)}`, source: e.source, target: e.target, status: 'removed' })

  const counts: Record<DiffStatus, number> = { added: 0, removed: 0, changed: 0, unchanged: 0 }
  for (const node of nodes) counts[node.status]++
  return { nodes, edges, counts }
}

interface KeyedGraph {
  nodes: FlowNode[]
  edges: { source: string; target: string }[]
  /** Source, Frame, Decode, Parse, then stages, in execution order. */
  chain: string[]
  sinks: FlowNode[]
  sinkOriginalIds: string[]
}

/** definitionToGraph with stable ids: stages "stage:<id>", sinks "sink:<index>"
 * (the after side's index; the before side is re-keyed by matchSinks). */
function keyed(def: FlowDefinition | null): KeyedGraph {
  if (!def) return { nodes: [], edges: [], chain: [], sinks: [], sinkOriginalIds: [] }
  const graph = definitionToGraph(def)
  const rename = new Map<string, string>()
  let sinkIndex = 0
  const nodes = graph.nodes.map(node => {
    const id = node.type === 'stage' ? `stage:${node.id}` : node.type === 'sink' && node.id !== DEADLETTER_ID ? `sink:${sinkIndex++}` : node.id
    rename.set(node.id, id)
    return { ...node, id } as FlowNode
  })
  const edges = graph.edges.map(e => ({ source: rename.get(e.source) ?? e.source, target: rename.get(e.target) ?? e.target }))
  const sinks = nodes.filter(n => n.type === 'sink' && n.id !== DEADLETTER_ID)
  return {
    nodes, edges, sinks, sinkOriginalIds: sinks.map(n => n.id),
    chain: nodes.filter(n => n.type !== 'sink').map(n => n.id),
  }
}

const sinkOf = (node: FlowNode) => (node.data as { sink: SinkCfg }).sink

/** The definition slice a node edits, without view-only state. */
function comparable(node: FlowNode): unknown {
  switch (node.type) {
    case 'ingress': return node.data.source
    case 'frame': return node.data.framing
    case 'decode': return { decoders: node.data.decoders, maxDecodedBytes: node.data.maxDecodedBytes }
    case 'parse': return node.data.parser
    case 'stage': return node.data.spec
    case 'sink': return node.data.sink
  }
}

/** A short display form of a changed value. */
export function formatValue(value: unknown): string {
  return value === undefined ? t('gofi.lib.flow-diff.unset', '(unset)') : JSON.stringify(value)
}
