import { css, cx } from '@emotion/css'
import type { GrafanaTheme2, IconName } from '@grafana/data'
import { Icon, TextLink, useStyles2, useTheme2 } from '@grafana/ui'
import { useEffect, useMemo, useRef, useState } from 'react'
import { Background, Controls, Handle, MarkerType, Position, ReactFlow, type Edge, type NodeProps } from '@xyflow/react'
import type { FlowDefinition } from '../../types'
import { paths, to } from '../../router'
import { flowsToTopology, type TopologyNode } from './topology'
import { emptyObservation, useTrafficObservations, type TrafficObservations } from '../dashboard/useTrafficObservations'
import { EDGE_TYPES } from './TrafficEdge'
import { RateTracker } from './traffic'
import './flow-theme.css'

const ROLE: Record<TopologyNode['data']['role'], { label: string; icon: IconName }> = {
  source: { label: 'Source', icon: 'signal' },
  decode: { label: 'Decoding', icon: 'brackets-curly' },
  stage: { label: 'Processing', icon: 'filter' },
  destination: { label: 'Destination', icon: 'arrow-right' },
}

function OverviewNode({ data }: NodeProps<TopologyNode>) {
  const styles = useStyles2(getStyles)
  const role = ROLE[data.role]
  const observation = data.observation
  return (
    <div className={cx(styles.node, data.role === 'source' && styles.flow)}>
      {data.role !== 'source' && <Handle type="target" position={Position.Left} />}
      <div className={styles.body}>
        <div className={styles.role}><Icon name={role.icon} size="sm" className={data.role === 'decode' ? styles.decodeIcon : styles.roleIcon} /><span>{role.label}</span></div>
        {data.role === 'source'
          ? <span className={cx('nodrag', styles.titleLink)}><TextLink href={to(paths.flow(data.flowName!))} inline={false} weight="medium" icon="angle-right" onClick={data.onSelect ? (event) => { event.preventDefault(); data.onSelect!(data.flowName!) } : undefined}>{data.title}</TextLink></span>
          : <p className={cx(styles.title, styles.truncate)} title={data.title}>{data.title}</p>}
        <p className={styles.detail} title={data.detail}>{data.detail}</p>
      </div>
      {observation && <div className={styles.footer}>
        <div className={styles.footerRow}>
          <span className={observation.state === 'connected' ? styles.ok : styles.warn}>{observation.state === 'connected' ? 'Stream connected' : observation.state === 'connecting' ? 'Connecting stream…' : 'Stream reconnecting…'}</span>
          <span className={styles.mono}>{observation.observed} observed</span>
        </div>
        <div className={styles.footerRow}><span>{observation.dropped} rejected</span><span>{observation.parseErrors} parse errors</span></div>
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
}

/** Read-only overview of every flow's ingress, processing, and routes.
 * Fills its container; host it in a PanelChrome or a sized parent. */
export default function FlowTopology({ flows, onSelect, observations }: Props) {
  const topologyKey = JSON.stringify([...flows].sort((a, b) => a.name.localeCompare(b.name)))
  const graph = useMemo(() => flowsToTopology(JSON.parse(topologyKey) as FlowDefinition[]), [topologyKey])
  const names = flows.map((flow) => flow.name)
  const own = useTrafficObservations(names, !observations)
  const traffic = observations ?? own
  const namesKey = JSON.stringify([...names].sort())
  const colorMode = useTheme2().isDark ? 'dark' : 'light'
  const nodes = graph.nodes.map((node) => ({ ...node, data: { ...node.data, onSelect, observation: node.data.role === 'source' ? traffic.flows[node.data.flowName!] ?? emptyObservation() : undefined } }))
  const rates = useFlowRates(traffic)
  const edges = useMemo(() => graph.edges.map((edge: Edge) => {
    const data = edge.data as { flowName: string; deadLetter?: boolean; observedIngress?: boolean }
    const rate = data.deadLetter ? rates[`${data.flowName}:rejected`] : data.observedIngress ? rates[`${data.flowName}:observed`] : rates[`${data.flowName}:delivered`]
    return { ...edge, type: 'traffic', style: undefined, markerEnd: { type: MarkerType.ArrowClosed, width: 16, height: 16 }, data: { ...data, rejected: data.deadLetter, rate: rate ?? 0 } }
  }), [graph.edges, rates])
  return <ReactFlow className="gofi-flow-readonly" style={{ width: '100%', height: '100%' }} defaultEdgeOptions={{ interactionWidth: 20 }} key={namesKey} nodes={nodes} edges={edges} nodeTypes={NODE_TYPES} edgeTypes={EDGE_TYPES} nodesDraggable={false} nodesConnectable={false} elementsSelectable={false} fitView fitViewOptions={{ padding: 0.08, minZoom: 0.55, maxZoom: 1 }} minZoom={0.15} colorMode={colorMode} proOptions={{ hideAttribution: true }}>
    <Background gap={20} size={1} />
    <Controls showInteractive={false} />
  </ReactFlow>
}

/** Per-flow message rates from the session counters: observed (into the
 * pipeline), delivered (observed minus rejected), and rejected. */
function useFlowRates(traffic: TrafficObservations): Record<string, number> {
  const tracker = useRef(new RateTracker())
  const previous = useRef(new Map<string, { observed: number; dropped: number }>())
  const latest = useRef(traffic)
  latest.current = traffic
  const [rates, setRates] = useState<Record<string, number>>({})
  useEffect(() => {
    const timer = window.setInterval(() => {
      for (const [name, observation] of Object.entries(latest.current.flows)) {
        const before = previous.current.get(name)
        previous.current.set(name, { observed: observation.observed, dropped: observation.dropped })
        if (!before) continue
        const observed = Math.max(0, observation.observed - before.observed)
        const dropped = Math.max(0, observation.dropped - before.dropped)
        tracker.current.add(`${name}:observed`, observed)
        tracker.current.add(`${name}:delivered`, Math.max(0, observed - dropped))
        tracker.current.add(`${name}:rejected`, dropped)
      }
      setRates(tracker.current.tick())
    }, 500)
    return () => window.clearInterval(timer)
  }, [])
  return rates
}

const getStyles = (theme: GrafanaTheme2) => ({
  node: css({
    width: 224, height: 176, display: 'flex', flexDirection: 'column', overflow: 'visible',
    background: theme.colors.background.primary, border: `1px solid ${theme.colors.border.weak}`, borderRadius: theme.shape.radius.default,
  }),
  flow: css({ borderLeft: `2px solid ${theme.colors.primary.main}` }),
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
