import { css, cx } from '@emotion/css'
import type { GrafanaTheme2, IconName } from '@grafana/data'
import { Icon, TextLink, useStyles2, useTheme2 } from '@grafana/ui'
import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { Background, Controls, Handle, MarkerType, Position, ReactFlow, useNodesInitialized, useReactFlow, type Edge, type Node, type NodeChange, type NodeProps } from '@xyflow/react'
import type { FlowDefinition } from '../../types'
import { paths, to } from '../../router'
import { flowsToTopology, type TopologyNode } from './topology'
import { emptyObservation, useTrafficObservations, type TrafficObservations } from '../dashboard/useTrafficObservations'
import { EDGE_TYPES } from './TrafficEdge'
import { useFlowRates, useMediaQuery } from './useFlowRates'
import { formatNumber, t } from '../../i18n'
import { flowAriaLabels } from './aria'
import './flow-theme.css'

const roles = (): Record<TopologyNode['data']['role'], { label: string; icon: IconName }> => ({
  source: { label: t('gofi.canvas.flow-topology.role-source', 'Source'), icon: 'signal' },
  decode: { label: t('gofi.canvas.flow-topology.role-decode', 'Decoding'), icon: 'brackets-curly' },
  stage: { label: t('gofi.canvas.flow-topology.role-stage', 'Processing'), icon: 'filter' },
  destination: { label: t('gofi.canvas.flow-topology.role-destination', 'Destination'), icon: 'arrow-right' },
})

function OverviewNode({ data }: NodeProps<TopologyNode>) {
  const styles = useStyles2(getStyles)
  const role = roles()[data.role]
  const observation = data.observation
  return (
    <div className={cx(styles.node, data.role === 'source' && styles.flow, data.focused && styles.focused)}>
      {data.role !== 'source' && <Handle type="target" position={Position.Left} />}
      <div className={styles.body}>
        <div className={styles.role}><Icon name={role.icon} size="sm" className={data.role === 'decode' ? styles.decodeIcon : styles.roleIcon} /><span>{role.label}</span>{data.stopped && <span className={styles.stopped}>{t('gofi.canvas.flow-topology.stopped', 'Stopped')}</span>}</div>
        {data.role === 'source'
          ? <span className={cx('nodrag', styles.titleLink)}><TextLink href={to(paths.flow(data.flowName!))} inline={false} weight="medium" icon="angle-right" onClick={data.onSelect ? (event) => { event.preventDefault(); data.onSelect!(data.flowName!) } : undefined}>{data.title}</TextLink></span>
          : <p className={cx(styles.title, styles.truncate)} title={data.title}>{data.title}</p>}
        <p className={styles.detail} title={data.detail}>{data.detail}</p>
      </div>
      {observation && <div className={styles.footer}>
        <div className={styles.footerRow}>
          <span className={observation.state === 'connected' ? styles.ok : styles.warn}>{observation.state === 'connected' ? t('gofi.canvas.flow-topology.stream-connected', 'Stream connected') : observation.state === 'connecting' ? t('gofi.canvas.flow-topology.stream-connecting', 'Connecting stream…') : t('gofi.canvas.flow-topology.stream-reconnecting', 'Stream reconnecting…')}</span>
          <span className={styles.mono}>{t('gofi.canvas.flow-topology.observed', '{{n}} observed', { n: formatNumber(observation.observed) })}</span>
        </div>
        <div className={styles.footerRow}><span>{t('gofi.canvas.flow-topology.rejected', '{{n}} rejected', { n: formatNumber(observation.dropped) })}</span><span>{t('gofi.canvas.flow-topology.parse-errors', '{{n}} parse errors', { n: formatNumber(observation.parseErrors) })}</span></div>
      </div>}
      {data.role !== 'destination' && <Handle type="source" position={Position.Right} />}
      {data.hasDeadLetter && <Handle id="rejected" type="source" position={Position.Bottom} />}
    </div>
  )
}
const NODE_TYPES = { topology: OverviewNode }

interface Props {
  flows: FlowDefinition[]
  /** Overrides the default link to the flow's flow page. */
  onSelect?: (name: string) => void
  /** Observations from a parent-owned stream; omit to open one here. */
  observations?: TrafficObservations
  /** The focused flow: it stays fully visible while the others fade. */
  selected?: string | null
  /** Called with a flow's name when one of its cards or edges is clicked,
   * and with null when the empty canvas is clicked. */
  onSelectFlow?: (name: string | null) => void
}

const FIT = { padding: 0.08, minZoom: 0.55, maxZoom: 1 }

/** Read-only overview of every flow's ingress, processing, and routes.
 * Fills its container; host it in a PanelChrome or a sized parent. */
export default function FlowTopology({ flows, onSelect, observations, selected, onSelectFlow }: Props) {
  const styles = useStyles2(getStyles)
  const topologyKey = JSON.stringify([...flows].sort((a, b) => a.name.localeCompare(b.name)))
  const graph = useMemo(() => flowsToTopology(JSON.parse(topologyKey) as FlowDefinition[]), [topologyKey])
  const names = flows.map((flow) => flow.name)
  const own = useTrafficObservations(names, !observations)
  const traffic = observations ?? own
  const namesKey = JSON.stringify([...names].sort())
  const colorMode = useTheme2().isDark ? 'dark' : 'light'
  const focus = selected && names.includes(selected) ? selected : null
  // Stopped flows fade like unfocused ones; selecting one brings it back.
  const stopped = useMemo(() => new Set(flows.filter((flow) => flow.state === 'stopped').map((flow) => flow.name)), [flows])
  // The focused flow's own cards plus the (possibly shared) destinations it routes to.
  const focusIds = useMemo(() => {
    if (!focus) return null
    const ids = new Set(graph.nodes.filter((node) => node.data.flowName === focus).map((node) => node.id))
    for (const edge of graph.edges) if ((edge.data as { flowName?: string }).flowName === focus) ids.add(edge.target)
    return [...ids]
  }, [graph, focus])
  // A shared destination is idle only when every flow routing to it is stopped.
  const idleIds = useMemo(() => {
    const ids = new Set(graph.nodes.filter((node) => node.data.flowName && stopped.has(node.data.flowName)).map((node) => node.id))
    const live = new Set(graph.edges.filter((edge) => !stopped.has((edge.data as { flowName: string }).flowName)).map((edge) => edge.target))
    for (const node of graph.nodes) if (!node.data.flowName && !live.has(node.id)) ids.add(node.id)
    return ids
  }, [graph, stopped])
  // React Flow keeps a node's measured size only on the object it adopted:
  // a replacement object without `measured` reads as unmeasured, and every
  // edge touching it renders nothing for a frame and remounts, so its dots
  // restarted on each stats message (the flashing). The sizes come back
  // through onNodesChange and are carried on each node; an object is reused
  // while nothing about it changed, so most nodes never get replaced.
  const measured = useRef(new Map<string, { width: number; height: number }>())
  const [, bumpMeasured] = useState(0)
  const onNodesChange = useCallback((changes: NodeChange[]) => {
    let changed = false
    for (const change of changes) {
      if (change.type === 'dimensions' && change.dimensions) { measured.current.set(change.id, change.dimensions); changed = true }
    }
    if (changed) bumpMeasured(value => value + 1)
  }, [])
  const previousNodes = useRef(new Map<string, { signature: string; node: Node }>())
  const nodes = graph.nodes.map((node) => {
    const next = {
    ...node,
    className: cx((focusIds ? !focusIds.includes(node.id) : idleIds.has(node.id)) && DIMMED, onSelectFlow && !node.data.flowName && INERT) || undefined,
    data: { ...node.data, onSelect, focused: focus !== null && node.data.flowName === focus, stopped: node.data.role === 'source' && stopped.has(node.data.flowName!), observation: node.data.role === 'source' ? traffic.flows[node.data.flowName!] ?? emptyObservation() : undefined },
      measured: measured.current.get(node.id),
    } as Node
    const signature = JSON.stringify({ c: next.className, d: { ...next.data, onSelect: undefined }, m: next.measured, p: next.position })
    const before = previousNodes.current.get(node.id)
    if (before && before.signature === signature && (before.node.data as { onSelect?: unknown }).onSelect === onSelect) return before.node
    previousNodes.current.set(node.id, { signature, node: next })
    return next
  })
  const rates = useFlowRates(traffic)
  const edges = useMemo(() => graph.edges.map((edge: Edge) => {
    const data = edge.data as { flowName: string; deadLetter?: boolean; observedIngress?: boolean }
    const rate = data.deadLetter ? rates[`${data.flowName}:rejected`] : data.observedIngress ? rates[`${data.flowName}:observed`] : rates[`${data.flowName}:delivered`]
    const dimmed = focus !== null ? data.flowName !== focus : stopped.has(data.flowName)
    return { ...edge, type: 'traffic', style: undefined, className: dimmed ? DIMMED : undefined, markerEnd: { type: MarkerType.ArrowClosed, width: 16, height: 16 }, data: { ...data, rejected: data.deadLetter, rate: rate ?? 0, dimmed } }
  }), [graph.edges, rates, focus, stopped])
  const ariaLabels = useMemo(flowAriaLabels, [])
  const flowOf = (data: unknown) => (data as { flowName?: string } | undefined)?.flowName
  return <ReactFlow className={cx('gofi-flow-readonly', styles.canvas, onSelectFlow && styles.selectable)} ariaLabelConfig={ariaLabels} style={{ width: '100%', height: '100%' }} defaultEdgeOptions={{ interactionWidth: 20 }} key={namesKey} nodes={nodes} edges={edges} onNodesChange={onNodesChange} nodeTypes={NODE_TYPES} edgeTypes={EDGE_TYPES} nodesDraggable={false} nodesConnectable={false} elementsSelectable={false} fitView fitViewOptions={FIT} minZoom={0.15} colorMode={colorMode} proOptions={{ hideAttribution: true }}
    onNodeClick={onSelectFlow ? (_, node) => { const name = flowOf(node.data); if (name) onSelectFlow(name) } : undefined}
    onEdgeClick={onSelectFlow ? (_, edge) => { const name = flowOf(edge.data); if (name) onSelectFlow(name) } : undefined}
    onPaneClick={onSelectFlow ? () => onSelectFlow(null) : undefined}>
    <Background gap={20} size={1} />
    <Controls showInteractive={false} />
    {onSelectFlow && <FocusView ids={focusIds} />}
  </ReactFlow>
}

/** Fades unfocused and stopped flows (nodes, edges, and their particles). */
const DIMMED = 'gofi-topology-dimmed'
/** Shared destination cards: they belong to no single flow, so clicking them does nothing. */
const INERT = 'gofi-topology-inert'

/** Pans gently to the focused flow, and back to the whole topology when the
 * focus clears. Never zooms past 1:1 or below the overview's minimum. */
function FocusView({ ids }: { ids: string[] | null }) {
  const { fitView } = useReactFlow()
  const initialized = useNodesInitialized()
  const reduced = useMediaQuery('(prefers-reduced-motion: reduce)')
  const key = ids?.join('|') ?? ''
  const focused = useRef(false)
  useEffect(() => {
    if (!initialized) return
    const duration = reduced ? 0 : 320
    if (ids) { focused.current = true; void fitView({ nodes: ids.map((id) => ({ id })), padding: 0.15, minZoom: FIT.minZoom, maxZoom: FIT.maxZoom, duration }) }
    else if (focused.current) { focused.current = false; void fitView({ ...FIT, duration }) }
  }, [key, initialized])
  return null
}

const getStyles = (theme: GrafanaTheme2) => ({
  node: css({
    width: 224, height: 176, display: 'flex', flexDirection: 'column', overflow: 'visible',
    background: theme.colors.background.primary, border: `1px solid ${theme.colors.border.weak}`, borderRadius: theme.shape.radius.default,
  }),
  flow: css({ borderLeft: `2px solid ${theme.colors.primary.main}` }),
  focused: css({ borderColor: theme.colors.primary.border, boxShadow: `0 0 0 1px ${theme.colors.primary.border}` }),
  canvas: css({
    '.react-flow__node, .react-flow__edge': { transition: 'opacity 180ms ease' },
    [`.react-flow__node.${DIMMED}, .react-flow__edge.${DIMMED}`]: { opacity: 0.25 },
    '@media (prefers-reduced-motion: reduce)': { '.react-flow__node, .react-flow__edge': { transition: 'none' } },
  }),
  // Every card and route belongs to a flow and selects it.
  selectable: css({
    '&.gofi-flow-readonly .react-flow__node, &.gofi-flow-readonly .react-flow__edge, &.gofi-flow-readonly .react-flow__edge-path, &.gofi-flow-readonly .react-flow__edge-interaction': { cursor: 'pointer' },
    [`&.gofi-flow-readonly .react-flow__node.${INERT}`]: { cursor: 'default' },
    // One cursor per card: text, ports, and links inside it don't switch it.
    '&.gofi-flow-readonly .react-flow__node *': { cursor: 'inherit' },
  }),
  stopped: css({ marginLeft: 'auto', color: theme.colors.text.secondary, fontWeight: theme.typography.fontWeightMedium }),
  body: css({ padding: theme.spacing(1.5), flex: 1, minHeight: 0, overflow: 'hidden' }),
  role: css({ display: 'flex', alignItems: 'center', gap: theme.spacing(1), fontSize: theme.typography.bodySmall.fontSize, color: theme.colors.text.secondary }),
  roleIcon: css({ color: theme.colors.primary.text }),
  decodeIcon: css({ color: theme.visualization.getColorByName('purple') }),
  titleLink: css({ display: 'block', marginTop: theme.spacing(1), minWidth: 0, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }),
  title: css({ margin: theme.spacing(1, 0, 0), fontWeight: theme.typography.fontWeightMedium }),
  truncate: css({ overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', minWidth: 0, flex: 1 }),
  detail: css({
    margin: theme.spacing(0.75, 0, 0), whiteSpace: 'pre-line', wordBreak: 'break-all', fontSize: theme.typography.bodySmall.fontSize,
    color: theme.colors.text.secondary, display: '-webkit-box', WebkitLineClamp: 3, WebkitBoxOrient: 'vertical', overflow: 'hidden',
  }),
  footer: css({ padding: theme.spacing(1, 1.5), borderTop: `1px solid ${theme.colors.border.weak}`, fontSize: 11, color: theme.colors.text.secondary }),
  footerRow: css({ display: 'flex', justifyContent: 'space-between', '& + &': { marginTop: theme.spacing(0.5) } }),
  ok: css({ color: theme.colors.success.text }),
  warn: css({ color: theme.colors.warning.text }),
  mono: css({ fontFamily: theme.typography.fontFamilyMonospace, fontVariantNumeric: 'tabular-nums' }),
})
