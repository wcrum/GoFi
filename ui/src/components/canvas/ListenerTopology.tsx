import { useEffect, useMemo, useRef, useState } from 'react'
import { Background, Controls, Handle, Position, ReactFlow, type NodeProps } from '@xyflow/react'
import { RiArrowRightLine, RiCodeSSlashLine, RiFilterLine, RiServerLine, RiWifiLine } from 'react-icons/ri'
import type { ListenerDefinition } from '../../types'
import { streamAllListeners } from '../../api'
import { listenersToTopology, type ListenerObservation, type TopologyNode } from './topology'
import './flow-theme.css'

function OverviewNode({ data }: NodeProps<TopologyNode>) {
  const Icon = data.role === 'listener' ? RiWifiLine : data.role === 'decode' ? RiCodeSSlashLine : data.role === 'stage' ? RiFilterLine : RiServerLine
  return (
    <div className={`w-56 h-44 rounded border border-g-border-medium bg-g-primary shadow-sm flex flex-col ${data.role === 'listener' ? 'border-l-2 border-l-g-accent-main' : ''}`}>
      {data.role !== 'listener' && <Handle type="target" position={Position.Left} />}
      <div className="p-3 flex-1 min-h-0 overflow-hidden">
        <div className="flex items-center gap-2 text-xs text-g-text-secondary"><Icon className={data.role === 'decode' ? 'text-purple-300' : 'text-g-accent-main'} /><span>{data.role === 'listener' ? 'Listener' : data.role === 'destination' ? 'Destination' : data.role === 'decode' ? 'Decoding' : 'Processing'}</span></div>
        {data.role === 'listener' ? <button onClick={() => data.onSelect?.(data.listenerName!)} className="nodrag mt-2 text-sm font-semibold text-left hover:text-g-accent-main flex items-center gap-1 w-full"><span className="truncate">{data.title}</span><RiArrowRightLine className="ml-auto shrink-0" /></button> : <p className="mt-2 text-sm font-medium truncate" title={data.title}>{data.title}</p>}
        <p className="mt-1.5 whitespace-pre-line break-all text-[11px] text-g-text-secondary leading-relaxed line-clamp-3" title={data.detail}>{data.detail}</p>
      </div>
      {data.observation && <div className="px-3 py-2 border-t border-g-border-weak text-[10px] text-g-text-secondary"><div className="flex justify-between"><span className={data.observation.state === 'connected' ? 'text-emerald-300' : 'text-amber-300'}>{data.observation.state === 'connected' ? 'Stream connected' : data.observation.state === 'connecting' ? 'Connecting stream…' : 'Stream reconnecting…'}</span><span className="font-mono tabular-nums">{data.observation.observed} observed</span></div><div className="mt-1 flex justify-between"><span>{data.observation.dropped} rejected</span><span>{data.observation.parseErrors} parse errors</span></div></div>}
      {data.role !== 'destination' && <Handle type="source" position={Position.Right} />}
      {data.hasDeadLetter && <Handle id="rejected" type="source" position={Position.Bottom} />}
    </div>
  )
}
const NODE_TYPES = { topology: OverviewNode }
const emptyObservation = (): ListenerObservation => ({ state: 'connecting', observed: 0, dropped: 0, parseErrors: 0, lastSeen: 0 })

export default function ListenerTopology({ listeners, onSelect }: { listeners: ListenerDefinition[]; onSelect: (name: string) => void }) {
  const topologyKey = JSON.stringify([...listeners].sort((a, b) => a.name.localeCompare(b.name)))
  const graph = useMemo(() => listenersToTopology(JSON.parse(topologyKey) as ListenerDefinition[]), [topologyKey])
  const counters = useRef(new Map<string, ListenerObservation>())
  const connection = useRef<ListenerObservation['state']>('connecting')
  const [observations, setObservations] = useState<Record<string, ListenerObservation>>({})
  // Reconcile names without resetting observations when the inventory polls.
  // Only counters are retained: memory is bounded by the configured listeners.
  const namesKey = JSON.stringify(listeners.map((listener) => listener.name).sort())
  useEffect(() => {
    const names = new Set<string>(JSON.parse(namesKey))
    for (const name of counters.current.keys()) if (!names.has(name)) counters.current.delete(name)
    for (const name of names) if (!counters.current.has(name)) counters.current.set(name, emptyObservation())
  }, [namesKey])
  useEffect(() => {
    const stop = streamAllListeners((name, event) => {
      const observation = counters.current.get(name)
      if (!observation) return
      observation.observed++
      if (event.dropped) observation.dropped++
      if (event.meta?.parseError) observation.parseErrors++
      observation.lastSeen = Date.now()
    }, () => { connection.current = 'reconnecting' }, () => { connection.current = 'connected' })
    const flush = () => setObservations(Object.fromEntries([...counters.current].map(([name, observation]) => [name, { ...observation, state: connection.current }])))
    flush()
    const timer = window.setInterval(flush, 500)
    return () => { stop(); window.clearInterval(timer) }
  }, [])
  const nodes = graph.nodes.map((node) => ({ ...node, data: { ...node.data, onSelect, observation: node.data.role === 'listener' ? observations[node.data.listenerName!] ?? emptyObservation() : undefined } }))
  const edges = graph.edges
  const totals = Object.values(observations).reduce((sum, item) => ({ observed: sum.observed + item.observed, dropped: sum.dropped + item.dropped }), { observed: 0, dropped: 0 })
  return <div className="absolute inset-0 flex flex-col">
    <div className="flex flex-wrap items-center gap-x-5 gap-y-2 px-5 py-3 border-b border-g-border-weak bg-g-primary"><div><h3 className="text-sm font-medium">All listeners</h3><p className="mt-1 text-[11px] text-g-text-secondary">Select a listener to inspect its flow and live traffic.</p></div><div className="ml-auto flex gap-5 text-xs text-g-text-secondary"><span><strong className="text-g-text font-mono tabular-nums">{listeners.length}</strong> listener{listeners.length === 1 ? '' : 's'}</span><span><strong className="text-g-text font-mono tabular-nums">{totals.observed}</strong> observed</span><span><strong className="text-g-text font-mono tabular-nums">{totals.dropped}</strong> rejected</span></div></div>
    <div className="flex-1 min-h-0"><ReactFlow className="gofi-flow-readonly" defaultEdgeOptions={{ interactionWidth: 20 }} key={namesKey} nodes={nodes} edges={edges} nodeTypes={NODE_TYPES} nodesDraggable={false} nodesConnectable={false} elementsSelectable={false} fitView fitViewOptions={{ padding: 0.15, maxZoom: 1 }} minZoom={0.15} colorMode="dark" proOptions={{ hideAttribution: true }}><Background gap={20} size={1} /><Controls showInteractive={false} /></ReactFlow></div>
    <p className="px-5 py-2 border-t border-g-border-weak text-[10px] text-g-text-secondary bg-g-primary">Session counters start when this view opens. Destination connections show configured routes; delivery is not confirmed.</p>
  </div>
}
