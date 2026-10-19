import { useCallback, useEffect, useMemo, useRef, useState, type ReactNode } from 'react'
import {
  ReactFlow,
  ReactFlowProvider,
  Background,
  Controls,
  useNodesState,
  type NodeChange,
  useEdgesState,
  useReactFlow,
  addEdge,
  MarkerType,
  type Connection,
  type Edge,
  type NodeMouseHandler,
} from '@xyflow/react'
import { css } from '@emotion/css'
import type { GrafanaTheme2 } from '@grafana/data'
import { Button as GrafanaButton, InlineField, Input, ToolbarButton, useStyles2, useTheme2 } from '@grafana/ui'
import { useToast } from '../Toast'
import type { Encoding, FlowDefinition, Framing, StageSpec, TapEvent, TestCase } from '../../types'
import { saveFlow, streamFlow } from '../../api'
import { genId } from '../../lib/id'
import { DEADLETTER_ID, DEFAULT_FRAMING, DEFAULT_PARSER, PIPELINE_RANK, definitionToGraph, encoderOfSink, graphToDefinition, type FlowSettings } from './convert'
import { FlowSettingsButton } from './FlowSettings'
import { PythonDefaultContext } from './python-functions'
import { countDefaultRefs, fillDefaultLibrary } from '../../lib/python-ref'
import type { DecodeNodeData, EncodeNodeData, FlowNode, FrameNodeData, IngressNodeData, ParseNodeData, StageNodeData, MockState } from './types'
import { AddStageMenu, PALETTE_MIME, StagePalette, type PaletteDrop, type PipelineStep, type StagePaletteEntry } from './StagePalette'
import { NodeInspector } from './NodeInspector'
import { ResizableSplit } from './ResizableSplit'
import { MockPanel } from './MockPanel'
import { FlowDownStateContext, IngressNode, type FlowDownState } from './IngressNode'
import { useFlowStatus } from '../../operations-api'
import { useRuntimeInfo } from '../../runtime-api'
import { runStateView } from '../../lib/runstate'
import { DecodeNode, EncodeNode, FrameNode, ParseNode, encodingChain } from './PipelineNodes'
import { StageNode } from './StageNode'
import { SinkNode } from './SinkNode'
import { EDGE_TYPES } from './TrafficEdge'
import { RateTracker, edgesTraveled } from './traffic'
import { formatNumber, t } from '../../i18n'
import { edgeAriaLabel, flowAriaLabels } from './aria'
import './flow-theme.css'

export type PipelineStepType = 'frame' | 'decode' | 'parse'
const PIPELINE_DEFAULTS: Record<PipelineStepType, FrameNodeData | DecodeNodeData | ParseNodeData> = {
  frame: { framing: { mode: 'line' } },
  decode: { decoders: [{ type: 'base64' }] },
  parse: { parser: { format: 'json' } },
}

const NODE_TYPES = { ingress: IngressNode, frame: FrameNode, decode: DecodeNode, parse: ParseNode, stage: StageNode, encode: EncodeNode, sink: SinkNode }

// Layout constants — must match convert.ts's definitionToGraph so nodes added
// interactively land at the same spacing as a freshly-loaded graph.
const X_STEP = 340
const SINK_Y_STEP = 140
// A new Encode node starts as the spec's default: the wrapped JSON envelope.
const DEFAULT_ENCODING: Encoding = { format: 'wrapped' }

// A new flow is just Source -> Sink; Frame and Parse are added when needed.
const DEFAULT_FLOW: FlowDefinition = {
  name: '',
  source: { listen: { port: 0, network: 'tcp' } },
  framing: DEFAULT_FRAMING,
  parser: DEFAULT_PARSER,
  stages: [],
  sinks: [{ type: 'stdout' }],
}

/** Walk forward from the Source through the optional Frame/Decode/Parse steps
 * and then `type: 'stage'` nodes, stopping at the last node reached. This is the current "terminal" chain node that fans out
 * to the sink(s) — used to know where to splice in newly-added stage/sink
 * nodes. Deliberately lenient (no branch/cycle validation) since this is an
 * interactive-editing helper, not the authoritative graph validator — that
 * validation lives in convert.ts's graphToDefinition, run at save time. */
function findTerminalId(nodes: FlowNode[], edges: Edge[]): string {
  const stageIds = new Set(nodes.filter((n) => n.type === 'stage' || n.type === 'frame' || n.type === 'decode' || n.type === 'parse').map((n) => n.id))
  let current = 'ingress'
  const visited = new Set([current])
  for (;;) {
    const next = edges.find((e) => e.source === current && stageIds.has(e.target))
    if (!next || visited.has(next.target)) break
    current = next.target
    visited.add(current)
  }
  return current
}

interface FlowCanvasProps {
  initial?: FlowDefinition
  mode: 'create' | 'edit' | 'view'
  onSaved?: (def: FlowDefinition) => void
  onClose: () => void
  closeLabel?: string
  /** Part of another editor (the YAML split): no name, Save, or Cancel here;
   * edits are reported through onChange instead. */
  embedded?: boolean
  /** Stream the running flow's traffic onto the graph while editing too. */
  live?: boolean
  /** Each structurally different, valid definition the graph forms. */
  onChange?: (def: FlowDefinition) => void
}

function withPendingStats(nodes: FlowNode[]): FlowNode[] {
  return nodes.map((n) => {
    if (n.type === 'ingress') return { ...n, data: { ...n.data, stats: { total: 0, connected: false, pending: true } } as IngressNodeData } as FlowNode
    if (n.type === 'stage') return { ...n, data: { ...n.data, stats: { droppedCount: 0, passedCount: 0, pending: true } } as StageNodeData } as FlowNode
    return n
  })
}

export default function FlowCanvas(props: FlowCanvasProps) {
  return (
    <ReactFlowProvider>
      <FlowCanvasInner {...props} />
    </ReactFlowProvider>
  )
}

function FlowCanvasInner({ initial, mode, onSaved, onClose, closeLabel, embedded, live, onChange }: FlowCanvasProps) {
  const styles = useStyles2(getStyles)
  const colorMode = useTheme2().isDark ? 'dark' : 'light'
  const addToast = useToast()
  // Viewing live, every node that will show traffic stats shows the stats
  // section from the first paint (with placeholders), so the cards keep
  // their size when the stream connects instead of growing badge by badge.
  const seed = useMemo(() => {
    const graph = definitionToGraph(initial ?? DEFAULT_FLOW)
    return mode === 'view' || live ? { ...graph, nodes: withPendingStats(graph.nodes) } : graph
  }, [initial, mode, live])
  const ariaLabels = useMemo(flowAriaLabels, [])
  const [nodes, setNodes, onNodesChange] = useNodesState<FlowNode>(seed.nodes)
  // Read-only canvases still take React Flow's dimension changes: a node
  // rewritten with live counters but without `measured` reads as unmeasured,
  // and every edge touching it remounted each tick (its dots restarted).
  const onDimensionChanges = useCallback((changes: NodeChange<FlowNode>[]) => {
    const dimensions = changes.filter(change => change.type === 'dimensions')
    if (dimensions.length) onNodesChange(dimensions)
  }, [onNodesChange])
  const [edges, setEdges, onEdgesChange] = useEdgesState(seed.edges)
  const edgesRef = useRef(edges)
  edgesRef.current = edges
  const [edgeRates, setEdgeRates] = useState<Record<string, number>>({})
  const [selectedId, setSelectedId] = useState<string | null>(null)
  const [name, setName] = useState(initial?.name ?? '')
  const [saving, setSaving] = useState(false)
  const [nameError, setNameError] = useState<string | null>(null)
  const nameInput = useRef<HTMLInputElement>(null)
  const [testCases, setTestCases] = useState<TestCase[]>(initial?.testCases ?? [])
  // Flow-wide settings that are not nodes (the default Python library).
  const [settings, setSettings] = useState<FlowSettings>(settingsOf(initial))
  const defaultLibrary = settings.python?.library ?? ''
  // Removing the default writes it into the refs that relied on it, so they
  // keep running in the same library.
  const changeSettings = useCallback((next: FlowSettings) => {
    const removed = settings.python?.library && !next.python?.library ? settings.python.library : ''
    if (removed) setNodes((prev) => prev.map((n) => ({ ...n, data: fillDefaultLibrary(n.data, removed) }) as FlowNode))
    setSettings(next)
  }, [settings, setNodes])
  // Mock panel and NodeInspector share the right-hand rail, so they're
  // mutually exclusive: opening one closes the other.
  const [mockOpen, setMockOpen] = useState(false)

  const selectedNode = nodes.find((n) => n.id === selectedId) ?? null
  // Every Encode node a sink can connect from, named by its chain (numbered
  // when two nodes read the same).
  const encoderOptions = useMemo(() => {
    const encoders = nodes.filter((n) => n.type === 'encode')
    const labels = encoders.map((n) => encodingChain(n.data.encoding, defaultLibrary))
    return encoders.map((n, i) => ({ id: n.id, label: labels.filter((label) => label === labels[i]).length > 1 ? `${labels[i]} #${labels.slice(0, i + 1).filter((label) => label === labels[i]).length}` : labels[i] }))
  }, [nodes, defaultLibrary])

  const onNodeClick: NodeMouseHandler = useCallback((_e, node) => {
    setSelectedId(node.id)
    setMockOpen(false)
  }, [])

  const onPaneClick = useCallback(() => setSelectedId(null), [])

  // Handles the drag-to-connect gesture (dragging from one node's Handle to
  // another). `nodesConnectable` alone only enables the drag interaction —
  // without this callback, releasing the drag does nothing since nothing
  // adds the resulting edge to state. Deliberately unvalidated here (mirrors
  // findTerminalId's comment above): interactive editing stays lenient, and
  // graphToDefinition's structural validation at save time (branch/cycle/mixed
  // stage-sink-target checks) is the single source of truth for whether the
  // resulting graph is actually valid.
  const onConnect = useCallback(
    (connection: Connection) => {
      // A sink has exactly one input (the chain, or one Encode node), so a
      // new connection into a sink replaces the old one instead of adding a
      // second that the save would reject.
      const target = nodes.find((n) => n.id === connection.target)
      setEdges((eds) => addEdge({ ...connection, type: 'default', ariaLabel: edgeAriaLabel(connection.source, connection.target) }, target?.type === 'sink' ? eds.filter((e) => e.target !== connection.target) : eds))
    },
    [nodes, setEdges],
  )
  // The dead-letter sink receives rejected messages: only an Encode node may
  // connect to it (convert.ts rejects anything else at save time too).
  const isValidConnection = useCallback(
    (connection: Connection | Edge) => connection.target !== DEADLETTER_ID || nodes.find((n) => n.id === connection.source)?.type === 'encode',
    [nodes],
  )

  // Adds a new stage node, splicing it in right before the fan-out point
  // (the current terminal chain node): terminal -> newStage -> {sinks it
  // used to point at directly}. All non-deadletter sink nodes shift one
  // X_STEP further right so the chain stays left-to-right readable.
  const handleAddStage = useCallback(
    (entry: StagePaletteEntry) => {
      const id = genId()
      const spec = { id, ...entry.defaults } as StageSpec
      const terminalId = findTerminalId(nodes, edges)
      const terminalNode = nodes.find((n) => n.id === terminalId)
      const insertX = (terminalNode?.position.x ?? 0) + X_STEP
      // Destinations: the sinks and the Encode nodes that feed them (not the
      // dead letter, nor an Encode node floating beside it).
      const destinationIds = new Set(nodes.filter((n) => (n.type === 'sink' && n.id !== DEADLETTER_ID) || (n.type === 'encode' && edges.some((e) => e.target === n.id))).map((n) => n.id))

      setNodes((prevNodes) => {
        const newNode: FlowNode = { id, type: 'stage', position: { x: insertX, y: 0 }, data: { spec } }
        const shifted = prevNodes.map((n) =>
          destinationIds.has(n.id)
            ? { ...n, position: { ...n.position, x: n.position.x + X_STEP } }
            : n,
        )
        return [...shifted, newNode]
      })

      setEdges((prevEdges) => {
        // Every edge that currently runs terminal -> (a sink or Encode node)
        // gets rerouted through the new stage node: terminal -> newStage -> …
        const rerouted = prevEdges.filter((e) => e.source === terminalId && destinationIds.has(e.target))
        const rest = prevEdges.filter((e) => !rerouted.includes(e))
        return [
          ...rest,
          { id: `e-${terminalId}-${id}`, source: terminalId, target: id, type: 'default' },
          ...rerouted.map((e) => ({ id: `e-${id}-${e.target}`, source: id, target: e.target, type: 'default' })),
        ]
      })

      setSelectedId(id)
    },
    [nodes, edges, setNodes, setEdges],
  )

  // Adds another sink node fanning out from the current terminal chain node,
  // in parallel with any existing sinks — doesn't disturb existing
  // sink nodes/edges.
  const handleAddSink = useCallback(() => {
    const id = genId()
    const terminalId = findTerminalId(nodes, edges)
    const terminalNode = nodes.find((n) => n.id === terminalId)
    const existingSinks = nodes.filter((n) => n.type === 'sink' && n.id !== DEADLETTER_ID)
    const sinkX = existingSinks.length > 0 ? existingSinks[0].position.x : (terminalNode?.position.x ?? 0) + X_STEP
    const maxY = existingSinks.length > 0 ? Math.max(...existingSinks.map((n) => n.position.y)) : -SINK_Y_STEP

    const newNode: FlowNode = {
      id,
      type: 'sink',
      position: { x: sinkX, y: maxY + SINK_Y_STEP },
      data: { sink: { type: 'stdout' } },
    }
    setNodes((prev) => [...prev, newNode])
    setEdges((prev) => [...prev, { id: `e-${terminalId}-${id}`, source: terminalId, target: id, type: 'default' }])
    setSelectedId(id)
  }, [nodes, edges, setNodes, setEdges])

  // Adds the single, fixed-id, edge-free dead-letter node if one doesn't
  // already exist. No-op (guarded by StagePalette hiding the control) once present.
  const handleAddDeadLetter = useCallback(() => {
    setNodes((prev) => {
      if (prev.some((n) => n.id === DEADLETTER_ID)) return prev
      const newNode: FlowNode = {
        id: DEADLETTER_ID,
        type: 'sink',
        position: { x: 0, y: 220 },
        data: { sink: { type: 'stdout' }, isDeadLetter: true },
      }
      return [...prev, newNode]
    })
    setSelectedId(DEADLETTER_ID)
  }, [setNodes])

  const hasDeadLetter = nodes.some((n) => n.id === DEADLETTER_ID)

  // Adds an Encode step fed by the last stage with no sinks yet (the palette
  // card): it takes the column before the sinks, below any Encode step
  // already there, and is wired to sinks afterwards (drag, or a sink's
  // Encoding field). Until then it is ignored when saving.
  const handleAddEncode = useCallback(() => {
    const id = genId()
    const terminalId = findTerminalId(nodes, edges)
    const terminalNode = nodes.find((n) => n.id === terminalId)
    const column = nodes.filter((n) => n.type === 'encode' && edges.some((e) => e.target === n.id))
    const sinks = nodes.filter((n) => n.type === 'sink' && n.id !== DEADLETTER_ID)
    const x = column.length ? column[0].position.x : sinks.length ? Math.min(...sinks.map((n) => n.position.x)) : (terminalNode?.position.x ?? 0) + X_STEP
    const y = column.length ? Math.max(...column.map((n) => n.position.y)) + SINK_Y_STEP : 0
    const node: FlowNode = { id, type: 'encode', position: { x, y }, data: { encoding: { ...DEFAULT_ENCODING } } }
    setNodes((prev) => [...prev.map((n) => (!column.length && n.type === 'sink' && n.id !== DEADLETTER_ID ? { ...n, position: { ...n.position, x: n.position.x + X_STEP } } : n)), node])
    setEdges((prev) => [...prev, { id: `e-${terminalId}-${id}`, source: terminalId, target: id, type: 'default' }])
    setSelectedId(id)
    setMockOpen(false)
  }, [nodes, edges, setNodes, setEdges])

  // Connects a sink (the dead letter included) from an Encode node: an
  // existing one, a new one (`'new'`), or none (`''`: the chain directly; the
  // dead letter then has no input). A sink has exactly one input, so its
  // current incoming connection is replaced in place to keep the sink order.
  const handleSelectEncoder = useCallback(
    (sinkId: string, choice: string) => {
      const sink = nodes.find((n) => n.id === sinkId)
      if (!sink) return
      const isDeadLetter = sinkId === DEADLETTER_ID
      const terminalId = findTerminalId(nodes, edges)
      let encoderId = choice
      if (choice === 'new') {
        encoderId = genId()
        // The first chain-fed Encode node opens a column before the sinks;
        // the dead letter's own node sits where the dead letter was.
        const column = nodes.find((n) => n.type === 'encode' && edges.some((e) => e.target === n.id))
        const x = isDeadLetter ? sink.position.x : column ? column.position.x : sink.position.x
        const encoder: FlowNode = { id: encoderId, type: 'encode', position: { x, y: sink.position.y }, data: { encoding: { ...DEFAULT_ENCODING } } }
        setNodes((prev) => [
          ...prev.map((n) => {
            if (isDeadLetter) return n.id === DEADLETTER_ID ? { ...n, position: { ...n.position, x: n.position.x + X_STEP } } : n
            return !column && n.type === 'sink' && n.id !== DEADLETTER_ID ? { ...n, position: { ...n.position, x: n.position.x + X_STEP } } : n
          }),
          encoder,
        ])
      }
      setEdges((prev) => {
        const connection = encoderId ? { id: `e-${encoderId}-${sinkId}`, source: encoderId, target: sinkId, type: 'default' } : !isDeadLetter ? { id: `e-${terminalId}-${sinkId}`, source: terminalId, target: sinkId, type: 'default' } : undefined
        let replaced = false
        const next = prev.flatMap((e): Edge[] => {
          if (e.target !== sinkId) return [e]
          if (replaced || !connection) return []
          replaced = true
          return [connection]
        })
        if (connection && !replaced) next.push(connection)
        // A new chain-fed Encode node is fed by the last stage.
        if (choice === 'new' && !isDeadLetter) next.push({ id: `e-${terminalId}-${encoderId}`, source: terminalId, target: encoderId, type: 'default' })
        return next
      })
      if (choice === 'new') setSelectedId(encoderId)
    },
    [nodes, edges, setNodes, setEdges],
  )

  // Each Encode node shows how many sinks it feeds, derived from the edges.
  useEffect(() => {
    setNodes((prev) => {
      let changed = false
      const next = prev.map((n) => {
        if (n.type !== 'encode') return n
        const sinkCount = edges.filter((e) => e.source === n.id).length
        if (n.data.sinkCount === sinkCount) return n
        changed = true
        return { ...n, data: { ...n.data, sinkCount } } as FlowNode
      })
      return changed ? next : prev
    })
  }, [edges, setNodes])

  // Adds an optional Frame/Decode/Parse step at its canonical position:
  // after the last present step that precedes it, taking over that step's
  // outgoing connections. Everything to its right shifts by one column.
  const handleAddPipelineStep = useCallback(
    (type: PipelineStepType, data?: FrameNodeData | DecodeNodeData | ParseNodeData) => {
      if (nodes.some((n) => n.type === type)) { setSelectedId(type); return }
      const rank = PIPELINE_RANK[type]
      const present = nodes.filter((n) => n.type in PIPELINE_RANK).sort((a, b) => PIPELINE_RANK[a.type!] - PIPELINE_RANK[b.type!])
      const before = [...present].reverse().find((n) => PIPELINE_RANK[n.type!] < rank) ?? present[0]
      const x = before.position.x + X_STEP
      const node = { id: type, type, position: { x, y: 0 }, data: data ?? PIPELINE_DEFAULTS[type] } as FlowNode
      setNodes((prev) => [...prev.map((n) => (n.id !== DEADLETTER_ID && n.position.x >= x ? { ...n, position: { ...n.position, x: n.position.x + X_STEP } } : n)), node])
      setEdges((prev) => {
        const moved = prev.filter((e) => e.source === before.id)
        return [
          ...prev.filter((e) => e.source !== before.id),
          { id: `e-${before.id}-${type}`, source: before.id, target: type, type: 'default' },
          ...moved.map((e) => ({ ...e, id: `e-${type}-${e.target}`, source: type })),
        ]
      })
      setSelectedId(type)
    },
    [nodes, setNodes, setEdges],
  )

  // Deleting a node in the chain reconnects its neighbours, so removing a
  // step or stage never leaves a gap: predecessor -> each former target.
  const onNodesDelete = useCallback(
    (deleted: FlowNode[]) => {
      setEdges((prev) => {
        let next = prev
        for (const node of deleted) {
          const incoming = next.filter((e) => e.target === node.id)
          const from = incoming.length === 1 ? incoming[0].source : undefined
          // Each outgoing connection is replaced where it was (predecessor ->
          // target), so the sinks keep their order. The dead letter takes no
          // chain input: removing its Encode node leaves it unconnected.
          next = next.flatMap((e): Edge[] => {
            if (e.target === node.id) return []
            if (e.source !== node.id) return [e]
            if (!from || e.target === DEADLETTER_ID || next.some((other) => other.source === from && other.target === e.target)) return []
            return [{ id: `e-${from}-${e.target}`, source: from, target: e.target, type: 'default' }]
          })
        }
        return next
      })
      if (deleted.some((n) => n.id === selectedId)) setSelectedId(null)
    },
    [setEdges, selectedId],
  )

  // Drag-and-drop from the palette. A stage dropped on a connection between two
  // chain nodes is inserted there; dropped on a sink connection or empty
  // canvas it is appended before the sinks. Steps go to their canonical place.
  const { screenToFlowPosition, fitView } = useReactFlow()
  // Embedded beside another editor, the pane's width settles after mount and
  // changes as the splitter moves: keep the whole flow in view.
  const canvasRef = useRef<HTMLDivElement>(null)
  useEffect(() => {
    const element = canvasRef.current
    if (!embedded || !element) return
    let frame = 0
    const observer = new ResizeObserver(() => { cancelAnimationFrame(frame); frame = requestAnimationFrame(() => void fitView({ padding: 0.1, minZoom: 0.3, maxZoom: 1 })) })
    observer.observe(element)
    return () => { observer.disconnect(); cancelAnimationFrame(frame) }
  }, [embedded, fitView])
  const insertStageOnEdge = useCallback(
    (entry: StagePaletteEntry, edge: Edge) => {
      const target = nodes.find((n) => n.id === edge.target)
      if (!target || target.type !== 'stage') { handleAddStage(entry); return }
      const id = genId()
      const spec = { id, ...entry.defaults } as StageSpec
      const x = target.position.x
      setNodes((prev) => [
        ...prev.map((n) => (n.id !== DEADLETTER_ID && n.position.x >= x ? { ...n, position: { ...n.position, x: n.position.x + X_STEP } } : n)),
        { id, type: 'stage', position: { x, y: 0 }, data: { spec } } as FlowNode,
      ])
      setEdges((prev) => [
        ...prev.filter((e) => e.id !== edge.id),
        { id: `e-${edge.source}-${id}`, source: edge.source, target: id, type: 'default' },
        { id: `e-${id}-${edge.target}`, source: id, target: edge.target, type: 'default' },
      ])
      setSelectedId(id)
    },
    [nodes, handleAddStage, setNodes, setEdges],
  )
  const nearestEdge = useCallback(
    (point: { x: number; y: number }): Edge | undefined => {
      const box = (id: string) => {
        const n = nodes.find((node) => node.id === id)
        return n && { x: n.position.x, y: n.position.y, w: n.measured?.width ?? 224, h: n.measured?.height ?? 80 }
      }
      let best: { edge: Edge; distance: number } | undefined
      for (const edge of edges) {
        const a = box(edge.source)
        const b = box(edge.target)
        if (!a || !b) continue
        const ax = a.x + a.w, ay = a.y + a.h / 2, bx = b.x, by = b.y + b.h / 2
        const dx = bx - ax, dy = by - ay
        const t = Math.max(0, Math.min(1, ((point.x - ax) * dx + (point.y - ay) * dy) / (dx * dx + dy * dy || 1)))
        const distance = Math.hypot(point.x - (ax + t * dx), point.y - (ay + t * dy))
        if (distance < 70 && (!best || distance < best.distance)) best = { edge, distance }
      }
      return best?.edge
    },
    [nodes, edges],
  )
  const onDragOver = useCallback((event: React.DragEvent) => {
    if (event.dataTransfer.types.includes(PALETTE_MIME)) { event.preventDefault(); event.dataTransfer.dropEffect = 'copy' }
  }, [])
  const onDrop = useCallback(
    (event: React.DragEvent) => {
      const raw = event.dataTransfer.getData(PALETTE_MIME)
      if (!raw) return
      event.preventDefault()
      const drop = JSON.parse(raw) as PaletteDrop
      if (drop.kind === 'step') return handleAddPipelineStep(drop.step)
      if (drop.kind === 'sink') return handleAddSink()
      if (drop.kind === 'dead-letter') return handleAddDeadLetter()
      const edge = nearestEdge(screenToFlowPosition({ x: event.clientX, y: event.clientY }))
      if (drop.kind === 'encode') {
        // Dropped on a sink's connection, the Encode step takes over that sink.
        const target = edge && nodes.find((n) => n.id === edge.target)
        return target?.type === 'sink' && target.id !== DEADLETTER_ID ? handleSelectEncoder(target.id, 'new') : handleAddEncode()
      }
      if (edge) insertStageOnEdge(drop.entry, edge)
      else handleAddStage(drop.entry)
    },
    [nodes, handleAddPipelineStep, handleAddSink, handleAddDeadLetter, handleAddEncode, handleSelectEncoder, nearestEdge, screenToFlowPosition, insertStageOnEdge, handleAddStage],
  )

  const updateNodeData = useCallback(
    <T,>(id: string, data: T) => {
      setNodes((prev) => prev.map((n) => (n.id === id ? ({ ...n, data } as FlowNode) : n)))
    },
    [setNodes],
  )

  // Apply transient mock-playback highlight state: every id in `map` gets its
  // mockState set; every other node is cleared. Only touches `data.mockState`,
  // mirroring the live-stats flush pattern below — no other field is disturbed.
  const setMockStates = useCallback(
    (map: Record<string, MockState | undefined>) => {
      setNodes((prev) =>
        prev.map((n) => {
          const mockState = map[n.id]
          if (mockState === n.data.mockState) return n
          return { ...n, data: { ...n.data, mockState } } as FlowNode
        }),
      )
    },
    [setNodes],
  )

  function loadExample() {
    const example: FlowDefinition = {
      name: 'application-events',
      source: { listen: { port: 9000, network: 'tcp' } },
      framing: { mode: 'line' },
      decode: { decoders: [{ type: 'base64' }] },
      parser: { format: 'json', errorPolicy: 'drop' },
      stages: [{ id: 'keep-info', kind: 'filter', type: 'cel', expression: 'fields["level"] == "info"' }],
      sinks: [{ type: 'stdout' }],
      testCases: [{ id: 'info-example', name: t('gofi.canvas.flow-canvas.example-keep-info', 'Keep an info event'), raw: btoa(JSON.stringify({ level: 'info', message: 'Hello from GoFi' })) }, { id: 'debug-example', name: t('gofi.canvas.flow-canvas.example-drop-debug', 'Drop a debug event'), raw: btoa(JSON.stringify({ level: 'debug', message: 'Diagnostic event' })) }],
    }
    const graph = definitionToGraph(example)
    setNodes(graph.nodes); setEdges(graph.edges); setName(example.name); setTestCases(example.testCases ?? []); setSettings({})
    setSelectedId(null); setMockOpen(true)
  }

  async function handleSave() {
    const problem = flowNameProblem(name.trim())
    if (problem) {
      setNameError(problem)
      nameInput.current?.focus()
      return
    }
    setSaving(true)
    try {
      // Editing never changes the declared run state (Start/Stop do).
      const def = { ...graphToDefinition(name.trim(), nodes, edges, settings), testCases, ...(initial?.state ? { state: initial.state } : {}) }
      const { flow: created, warnings } = await saveFlow(def)
      if (warnings.length) addToast(warnings.join('\n'), 'warning', t('gofi.canvas.flow-canvas.saved-with-warnings', 'Saved, but {{n}} paths are not allowed', { count: warnings.length, n: formatNumber(warnings.length), defaultValue_one: 'Saved, but a path is not allowed' }))
      onSaved?.(created)
    } catch (e) {
      addToast(String(e))
    } finally {
      setSaving(false)
    }
  }

  // --- Live stats (view mode, or editing with live) ---
  // Aggregated in a ref (not per-event setState) to avoid a re-render per
  // streamed observation under load; flushed to node data on a throttled interval.
  const statsRef = useRef<{
    connected: boolean
    total: number
    lastFields?: Record<string, unknown>
    perStage: Map<string, { passed: number; dropped: number }>
  }>({ connected: false, total: 0, perStage: new Map() })

  useEffect(() => {
    if ((mode !== 'view' && !live) || !initial) return

    statsRef.current = { connected: false, total: 0, perStage: new Map() }
    const rates = new RateTracker()

    const stop = streamFlow(
      initial.name,
      (ev: TapEvent) => {
        const s = statsRef.current
        s.connected = true
        s.total += 1
        for (const id of edgesTraveled(edgesRef.current, ev)) rates.add(id)
        s.lastFields = ev.fields
        if (ev.dropped && ev.droppedBy) {
          const entry = s.perStage.get(ev.droppedBy) ?? { passed: 0, dropped: 0 }
          entry.dropped += 1
          s.perStage.set(ev.droppedBy, entry)
        } else if (!ev.dropped) {
          // Every stage the message passed through counts as "passed" for
          // that stage. Since stages are strictly sequential (up to the
          // fan-out point) and a drop short-circuits the rest, a passing
          // message passed all of them — attribution by `droppedBy` id is
          // kind-agnostic, so this works for all 5 stage kinds unchanged.
          for (const n of nodes) {
            if (n.type !== 'stage') continue
            const entry = s.perStage.get(n.id) ?? { passed: 0, dropped: 0 }
            entry.passed += 1
            s.perStage.set(n.id, entry)
          }
        }
      },
      () => {
        statsRef.current.connected = false
      },
      () => { statsRef.current.connected = true },
    )

    // Once a second, like the stats stream: faster ticks saw no events on
    // most of them (a sender once a second, the server batching the tap),
    // so the rates sagged and the edge animation restarted each time.
    let lastStatsKey = ''
    const flush = window.setInterval(() => {
      setEdgeRates(rates.tick())
      const s = statsRef.current
      // Rewriting every node each tick re-rendered them all; skip when nothing changed.
      const statsKey = `${s.total}:${s.connected}:${[...s.perStage].map(([id, c]) => `${id}=${c.passed}/${c.dropped}`).join(',')}`
      if (statsKey === lastStatsKey) return
      lastStatsKey = statsKey
      setNodes((prev) =>
        prev.map((n) => {
          if (n.type === 'ingress') {
            const data: IngressNodeData = {
              ...n.data,
              stats: { total: s.total, lastFields: s.lastFields, connected: s.connected },
            }
            return { ...n, data }
          }
          if (n.type === 'stage') {
            const counts = s.perStage.get(n.id) ?? { passed: 0, dropped: 0 }
            const data: StageNodeData = {
              ...n.data,
              stats: { droppedCount: counts.dropped, passedCount: counts.passed },
            }
            return { ...n, data }
          }
          return n
        }),
      )
    }, 1000)

    return () => {
      stop()
      window.clearInterval(flush)
      setEdgeRates({})
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [mode, live, initial?.name])

  // Report graph edits (not the live stats refreshed above) as definitions.
  // A graph that is mid-edit and not yet a valid flow reports nothing.
  const onChangeRef = useRef(onChange)
  onChangeRef.current = onChange
  const lastReported = useRef<string | null>(null)
  useEffect(() => {
    if (!onChangeRef.current) return
    let def: FlowDefinition
    try { def = { ...graphToDefinition(name.trim() || initial?.name || '', nodes, edges, settings), ...(testCases.length ? { testCases } : {}) } } catch { return }
    const key = JSON.stringify(def)
    if (lastReported.current === null) { lastReported.current = key; return } // the initial graph is not an edit
    if (key === lastReported.current) return
    lastReported.current = key
    onChangeRef.current(def)
  }, [nodes, edges, testCases, settings, name, initial?.name])

  // Embedded, a new `initial` (the YAML changed the flow) updates the graph in
  // place: remounting would re-measure every node and refit, which flashes.
  // Unchanged node ids keep their DOM; the new seed is a baseline, not an edit.
  const seeded = useRef(seed)
  useEffect(() => {
    if (!embedded || seeded.current === seed) return
    seeded.current = seed
    lastReported.current = null
    setNodes(seed.nodes)
    setEdges(seed.edges)
    setTestCases(initial?.testCases ?? [])
    setSettings(settingsOf(initial))
    const frame = requestAnimationFrame(() => void fitView({ padding: 0.1, minZoom: 0.3, maxZoom: 1, duration: 200 }))
    return () => cancelAnimationFrame(frame)
  }, [embedded, seed, initial, setNodes, setEdges, fitView])

  const readOnly = mode === 'view'
  // Every edge is directional; in view mode it also carries live traffic.
  const displayEdges = useMemo(() => edges.map((edge) => ({
    ...edge, type: 'traffic', markerEnd: { type: MarkerType.ArrowClosed, width: 16, height: 16 },
    data: { ...edge.data, rate: edgeRates[edge.id] ?? 0 },
  })), [edges, edgeRates])

  return (
    <PythonDefaultContext.Provider value={defaultLibrary}>
    <FlowDownStateProvider name={mode === 'create' ? '' : initial?.name ?? ''}>
    <div className={styles.root}>
      <div className={styles.toolbar}>
        {mode !== 'view' && !embedded && (<>
          <InlineField label={t('gofi.common.name', 'Name')} labelWidth={8} invalid={!!nameError}>
            <Input
              ref={nameInput}
              value={name}
              disabled={mode === 'edit'}
              aria-label={t('gofi.canvas.flow-canvas.flow-name', 'Flow name')}
              invalid={!!nameError}
              onChange={(e) => { setName(e.target.value); setNameError(null) }}
              placeholder={t('gofi.canvas.flow-canvas.name-placeholder', 'e.g. telemetry')}
              width={32}
            />
          </InlineField>
          {nameError && <span className={styles.nameError} role="alert">{nameError}</span>}
        </>)}
        {mode === 'create' && <GrafanaButton variant="secondary" fill="text" icon="file-alt" onClick={loadExample}>{t('gofi.canvas.flow-canvas.load-example', 'Load example')}</GrafanaButton>}
        {/* The side palette adds stages on wide screens; this menu replaces it where the palette is hidden (narrow screens, embedded). */}
        {!readOnly && <span className={embedded ? undefined : styles.narrowOnly}><AddStageMenu onAdd={handleAddStage} onAddSink={handleAddSink} onAddEncode={handleAddEncode} onAddDeadLetter={handleAddDeadLetter} hasDeadLetter={hasDeadLetter} /></span>}
        <div className={styles.spacer} />
        <FlowSettingsButton value={settings} onChange={changeSettings} readOnly={readOnly} defaultRefs={defaultLibrary ? countDefaultRefs(nodes.map((n) => n.data)) : 0} />
        {/* ToolbarButton hides its text below the md breakpoint, so the label is also the accessible name. */}
        <ToolbarButton icon="vial" aria-label={t('gofi.canvas.flow-canvas.test-flow', 'Test flow')} aria-pressed={mockOpen} variant={mockOpen ? 'active' : 'default'} onClick={() => { setMockOpen((o) => !o); setSelectedId(null) }}>
          {t('gofi.canvas.flow-canvas.test-flow', 'Test flow')}
        </ToolbarButton>
        {mode !== 'view' && !embedded && <>
          <GrafanaButton variant="secondary" onClick={onClose}>{closeLabel ?? t('gofi.common.close', 'Close')}</GrafanaButton>
          <GrafanaButton variant="primary" icon={saving ? 'spinner' : 'save'} onClick={handleSave} disabled={saving}>
            {saving ? t('gofi.common.saving', 'Saving...') : mode === 'edit' ? t('gofi.canvas.flow-canvas.save-flow', 'Save flow') : t('gofi.common.create-flow', 'Create flow')}
          </GrafanaButton>
        </>}
      </div>

      <div className={styles.body}>
        <ResizableSplit side="left" storageKey="gofi.flow.paletteShare" minPanelWidth={200} defaultShare={0.22} panel={readOnly || embedded ? null : (
          <StagePalette
            onAdd={handleAddStage}
            onAddSink={handleAddSink}
            onAddEncode={handleAddEncode}
            onAddDeadLetter={handleAddDeadLetter}
            hasDeadLetter={hasDeadLetter}
            missingSteps={(['frame', 'decode', 'parse'] as PipelineStep[]).filter((step) => !nodes.some((n) => n.type === step))}
            onAddStep={(step) => handleAddPipelineStep(step)}
          />
        )} main={
        <ResizableSplit side="right" storageKey="gofi.flow.inspectorShare" minPanelWidth={240} defaultShare={0.3} panel={mockOpen ? (
          <MockPanel
            nodes={nodes}
            edges={edges}
            name={name}
            testCases={testCases}
            settings={settings}
            setTestCases={setTestCases}
            readOnly={readOnly}
            setMockStates={setMockStates}
            onClose={() => setMockOpen(false)}
          />
        ) : selectedNode ? (
          <NodeInspector
            node={selectedNode}
            readOnly={readOnly}
            onChangeIngress={(data) => selectedNode && updateNodeData(selectedNode.id, data)}
            onChangeFrame={(data) => (nodes.some((n) => n.type === 'frame') ? updateNodeData('frame', data) : handleAddPipelineStep('frame', data))}
            onChangeDecode={(data) => updateNodeData('decode', data)}
            onChangeParse={(data) => updateNodeData('parse', data)}
            onChangeStage={(data) => selectedNode && updateNodeData(selectedNode.id, data)}
            onChangeEncode={(data) => selectedNode && updateNodeData(selectedNode.id, { ...data, sinkCount: (selectedNode.data as EncodeNodeData).sinkCount })}
            onChangeSink={(data) => selectedNode && updateNodeData(selectedNode.id, data)}
            encoders={encoderOptions}
            encoderId={selectedNode.type === 'sink' ? encoderOfSink(nodes, edges, selectedNode.id)?.id : undefined}
            onSelectEncoder={(choice) => selectedNode && handleSelectEncoder(selectedNode.id, choice)}
            fedSinks={selectedNode.type === 'encode' ? edges.filter((e) => e.source === selectedNode.id).flatMap((e) => { const n = nodes.find((node) => node.id === e.target); return n?.type === 'sink' ? [{ id: n.id, sink: n.data.sink, isDeadLetter: n.data.isDeadLetter }] : [] }) : []}
            onDisconnectSink={(sinkId) => handleSelectEncoder(sinkId, '')}
            onClose={() => setSelectedId(null)}
          />
        ) : null} main={
        <div className={styles.canvas} ref={canvasRef}>
          <ReactFlow
            className={readOnly ? 'gofi-flow-readonly' : 'gofi-flow-editor'}
            ariaLabelConfig={ariaLabels}
            defaultEdgeOptions={{ interactionWidth: 20 }}
            nodes={nodes}
            edges={displayEdges}
            edgeTypes={EDGE_TYPES}
            onNodesChange={readOnly ? onDimensionChanges : onNodesChange}
            onNodesDelete={readOnly ? undefined : onNodesDelete}
            onDragOver={readOnly ? undefined : onDragOver}
            onDrop={readOnly ? undefined : onDrop}
            onEdgesChange={readOnly ? undefined : onEdgesChange}
            nodeTypes={NODE_TYPES}
            onNodeClick={onNodeClick}
            onPaneClick={onPaneClick}
            onConnect={readOnly ? undefined : onConnect}
            isValidConnection={isValidConnection}
            nodesDraggable={!readOnly}
            nodesConnectable={!readOnly}
            elementsSelectable
            fitView
            fitViewOptions={{ padding: 0.1, minZoom: 0.5, maxZoom: 1 }}
            minZoom={0.2}
            colorMode={colorMode}
            proOptions={{ hideAttribution: true }}
          >
            <Background />
            <Controls />
          </ReactFlow>
        </div>} />} />
      </div>
    </div>
    </FlowDownStateProvider>
    </PythonDefaultContext.Provider>
  )
}

/** Tells the Source node when the saved flow is down (failed, crashed,
 * invalid), from the run-state poller and runtime cache the flow page already
 * subscribes to; a flow that has no name yet (create) has no run state. */
function FlowDownStateProvider({ name, children }: { name: string; children: ReactNode }) {
  return name ? <SavedFlowDownState name={name}>{children}</SavedFlowDownState> : children
}
function SavedFlowDownState({ name, children }: { name: string; children: ReactNode }) {
  const status = useFlowStatus(name)
  const runtime = useRuntimeInfo(name)
  const view = runStateView(status)
  const down = useMemo<FlowDownState | undefined>(() => status && view.tone === 'error'
    ? { text: view.text, reason: runtime?.error || status.message || view.tooltip }
    : undefined, [status, view.tone, view.text, view.tooltip, runtime?.error])
  return <FlowDownStateContext.Provider value={down}>{children}</FlowDownStateContext.Provider>
}

const settingsOf = (flow?: FlowDefinition): FlowSettings => (flow?.python?.library ? { python: flow.python } : {})

const getStyles = (theme: GrafanaTheme2) => ({
  toolbar: css({ display: 'flex', alignItems: 'center', flexWrap: 'wrap', gap: theme.spacing(1), padding: theme.spacing(1, 2), background: theme.colors.background.primary, borderBottom: `1px solid ${theme.colors.border.weak}` }),
  spacer: css({ flex: 1 }),
  nameError: css({ color: theme.colors.error.text, fontSize: theme.typography.bodySmall.fontSize }),
  narrowOnly: css({ display: 'none', [theme.breakpoints.down('md')]: { display: 'inline-flex' } }),
  root: css({ position: 'absolute', inset: 0, display: 'flex', flexDirection: 'column', background: theme.colors.background.canvas }),
  body: css({ flex: 1, display: 'flex', minHeight: 0, position: 'relative' }),
  canvas: css({ flex: 1, minWidth: 0, minHeight: 0 }),
})

/** Flows are Kubernetes resources: the name must be a DNS-1123 subdomain. */
export function flowNameProblem(name: string): string | null {
  if (!name) return t('gofi.canvas.flow-canvas.name-required', 'Enter a name for this flow.')
  if (name.length > 253) return t('gofi.canvas.flow-canvas.name-too-long', 'Use at most 253 characters.')
  if (!/^[a-z0-9]([-a-z0-9]*[a-z0-9])?(\.[a-z0-9]([-a-z0-9]*[a-z0-9])?)*$/.test(name)) return t('gofi.canvas.flow-canvas.name-format', 'Use lowercase letters, numbers, "-" or ".", starting and ending with a letter or number.')
  return null
}
