import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import {
  ReactFlow,
  ReactFlowProvider,
  Background,
  Controls,
  useNodesState,
  useEdgesState,
  addEdge,
  type Connection,
  type Edge,
  type NodeMouseHandler,
} from '@xyflow/react'
import { Input, Alert } from '@cnak-us/design-system'
import type { ListenerDefinition, StageSpec, TapEvent, TestCase } from '../../types'
import { createListener, streamListener } from '../../api'
import { genId } from '../../lib/id'
import { listenerToFlow, flowToListener } from './convert'
import type { FlowNode, IngressNodeData, StageNodeData, MockState } from './types'
import { StagePalette, type StagePaletteEntry } from './StagePalette'
import { NodeInspector } from './NodeInspector'
import { MockPanel } from './MockPanel'
import { IngressNode } from './IngressNode'
import { StageNode } from './StageNode'
import { SinkNode } from './SinkNode'
import './flow-theme.css'

const NODE_TYPES = { ingress: IngressNode, stage: StageNode, sink: SinkNode }

// Layout constants — must match convert.ts's listenerToFlow so nodes added
// interactively land at the same spacing as a freshly-loaded graph.
const X_STEP = 280
const SINK_Y_STEP = 140
// Fixed id for the single, optional, edge-free dead-letter node (see convert.ts).
const DEADLETTER_ID = 'deadletter'

const DEFAULT_LISTENER: ListenerDefinition = {
  name: '',
  source: { port: 0, network: 'tcp' },
  framing: { mode: 'line' },
  parser: { format: 'json' },
  stages: [],
  sinks: [{ type: 'stdout' }],
}

/** Walk forward from `ingress` following edges into `type: 'stage'` nodes
 * only, stopping at the last stage node reached (or `ingress` itself if
 * there are none). This is the current "terminal" chain node that fans out
 * to the sink(s) — used to know where to splice in newly-added stage/sink
 * nodes. Deliberately lenient (no branch/cycle validation) since this is an
 * interactive-editing helper, not the authoritative graph validator — that
 * validation lives in convert.ts's flowToListener, run at save time. */
function findTerminalId(nodes: FlowNode[], edges: Edge[]): string {
  const stageIds = new Set(nodes.filter((n) => n.type === 'stage').map((n) => n.id))
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
  initial?: ListenerDefinition
  mode: 'create' | 'view'
  onSaved?: (def: ListenerDefinition) => void
  onClose: () => void
}

export default function FlowCanvas(props: FlowCanvasProps) {
  return (
    <ReactFlowProvider>
      <FlowCanvasInner {...props} />
    </ReactFlowProvider>
  )
}

function FlowCanvasInner({ initial, mode, onSaved, onClose }: FlowCanvasProps) {
  const seed = useMemo(() => listenerToFlow(initial ?? DEFAULT_LISTENER), [initial])
  const [nodes, setNodes, onNodesChange] = useNodesState<FlowNode>(seed.nodes)
  const [edges, setEdges, onEdgesChange] = useEdgesState(seed.edges)
  const [selectedId, setSelectedId] = useState<string | null>(null)
  const [name, setName] = useState(initial?.name ?? '')
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [testCases, setTestCases] = useState<TestCase[]>(initial?.testCases ?? [])
  // Mock panel and NodeInspector share the right-hand rail, so they're
  // mutually exclusive: opening one closes the other.
  const [mockOpen, setMockOpen] = useState(false)

  const selectedNode = nodes.find((n) => n.id === selectedId) ?? null

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
  // flowToListener's structural validation at save time (branch/cycle/mixed
  // stage-sink-target checks) is the single source of truth for whether the
  // resulting graph is actually valid.
  const onConnect = useCallback(
    (connection: Connection) => {
      setEdges((eds) => addEdge({ ...connection, type: 'smoothstep' }, eds))
    },
    [setEdges],
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
      const sinkIds = new Set(nodes.filter((n) => n.type === 'sink' && n.id !== DEADLETTER_ID).map((n) => n.id))

      setNodes((prevNodes) => {
        const newNode: FlowNode = { id, type: 'stage', position: { x: insertX, y: 0 }, data: { spec } }
        const shifted = prevNodes.map((n) =>
          n.type === 'sink' && n.id !== DEADLETTER_ID
            ? { ...n, position: { ...n.position, x: n.position.x + X_STEP } }
            : n,
        )
        return [...shifted, newNode]
      })

      setEdges((prevEdges) => {
        // Every edge that currently runs terminal -> (a sink) gets rerouted
        // through the new stage node: terminal -> newStage -> sink.
        const rerouted = prevEdges.filter((e) => e.source === terminalId && sinkIds.has(e.target))
        const rest = prevEdges.filter((e) => !rerouted.includes(e))
        return [
          ...rest,
          { id: `e-${terminalId}-${id}`, source: terminalId, target: id, type: 'smoothstep' },
          ...rerouted.map((e) => ({ id: `e-${id}-${e.target}`, source: id, target: e.target, type: 'smoothstep' })),
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
    setEdges((prev) => [...prev, { id: `e-${terminalId}-${id}`, source: terminalId, target: id, type: 'smoothstep' }])
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

  async function handleCreate() {
    setError(null)
    setSaving(true)
    try {
      const def = { ...flowToListener(name.trim(), nodes, edges), testCases }
      if (!def.name) throw new Error('name is required')
      const created = await createListener(def)
      onSaved?.(created)
    } catch (e) {
      setError(String(e))
    } finally {
      setSaving(false)
    }
  }

  // --- Live stats (mode === 'view' only) ---
  // Aggregated in a ref (not per-event setState) to avoid a re-render per
  // SSE message under load; flushed to node data on a throttled interval.
  const statsRef = useRef<{
    connected: boolean
    total: number
    lastFields?: Record<string, unknown>
    perStage: Map<string, { passed: number; dropped: number }>
  }>({ connected: false, total: 0, perStage: new Map() })

  useEffect(() => {
    if (mode !== 'view' || !initial) return

    statsRef.current = { connected: true, total: 0, perStage: new Map() }

    const stop = streamListener(
      initial.name,
      (ev: TapEvent) => {
        const s = statsRef.current
        s.total += 1
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
    )

    const flush = window.setInterval(() => {
      const s = statsRef.current
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
    }, 300)

    return () => {
      stop()
      window.clearInterval(flush)
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [mode, initial?.name])

  const readOnly = mode === 'view'

  return (
    <div className="fixed inset-0 z-50 flex flex-col bg-g-canvas">
      <div className="flex items-center gap-3 px-4 py-3 border-b border-g-border-weak bg-g-primary">
        {mode === 'create' ? (
          <>
            <span className="text-sm text-g-text-secondary shrink-0">Name</span>
            <Input
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder="my-listener"
              className="max-w-xs"
            />
            <button
              onClick={handleCreate}
              disabled={saving}
              className="px-4 py-2 rounded-md text-sm font-medium bg-g-accent-main text-white hover:bg-g-accent-main/90 disabled:opacity-50 transition-colors"
            >
              {saving ? 'Creating…' : 'Create'}
            </button>
          </>
        ) : (
          <span className="text-sm font-semibold text-g-text">{initial?.name}</span>
        )}
        <button
          onClick={() => {
            setMockOpen((o) => !o)
            setSelectedId(null)
          }}
          className={`ml-auto px-3 py-1.5 rounded-md text-sm border transition-colors ${
            mockOpen
              ? 'border-g-accent-main/60 text-g-text bg-g-hover'
              : 'border-g-border-weak text-g-text-secondary hover:bg-g-hover'
          }`}
        >
          {mockOpen ? 'Hide Mock Mode' : 'Mock Mode'}
        </button>
        <button
          onClick={onClose}
          className="px-3 py-1.5 rounded-md text-sm text-g-text-secondary border border-g-border-weak hover:bg-g-hover transition-colors"
        >
          Close
        </button>
      </div>

      {error && (
        <div className="px-4 pt-3">
          <Alert severity="error">{error}</Alert>
        </div>
      )}

      <div className="flex-1 flex min-h-0">
        {!readOnly && (
          <StagePalette
            onAdd={handleAddStage}
            onAddSink={handleAddSink}
            onAddDeadLetter={handleAddDeadLetter}
            hasDeadLetter={hasDeadLetter}
          />
        )}

        <div className="flex-1 min-w-0">
          <ReactFlow
            nodes={nodes}
            edges={edges}
            onNodesChange={readOnly ? undefined : onNodesChange}
            onEdgesChange={readOnly ? undefined : onEdgesChange}
            nodeTypes={NODE_TYPES}
            onNodeClick={onNodeClick}
            onPaneClick={onPaneClick}
            onConnect={readOnly ? undefined : onConnect}
            nodesDraggable={!readOnly}
            nodesConnectable={!readOnly}
            elementsSelectable
            fitView
            proOptions={{ hideAttribution: true }}
          >
            <Background />
            <Controls />
          </ReactFlow>
        </div>

        {mockOpen ? (
          <MockPanel
            nodes={nodes}
            edges={edges}
            name={name}
            testCases={testCases}
            setTestCases={setTestCases}
            readOnly={readOnly}
            setMockStates={setMockStates}
            onClose={() => setMockOpen(false)}
          />
        ) : (
          <NodeInspector
            node={selectedNode}
            readOnly={readOnly}
            onChangeIngress={(data) => selectedNode && updateNodeData(selectedNode.id, data)}
            onChangeStage={(data) => selectedNode && updateNodeData(selectedNode.id, data)}
            onChangeSink={(data) => selectedNode && updateNodeData(selectedNode.id, data)}
            onClose={() => setSelectedId(null)}
          />
        )}
      </div>
    </div>
  )
}
