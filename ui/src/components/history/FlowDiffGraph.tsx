import { useMemo, useState, type ComponentType } from 'react'
import { css, cx } from '@emotion/css'
import type { GrafanaTheme2, IconName } from '@grafana/data'
import { Badge, IconButton, Stack, Text, Tooltip, useStyles2, useTheme2, type BadgeColor } from '@grafana/ui'
import { Background, BaseEdge, Controls, getBezierPath, MarkerType, ReactFlow, type Edge, type EdgeProps, type Node, type NodeProps } from '@xyflow/react'
import { diffFlows, formatValue, type DiffStatus, type FieldChange } from '../../lib/flow-diff'
import type { FlowDefinition } from '../../types'
import { IngressNode } from '../canvas/IngressNode'
import { DecodeNode, FrameNode, ParseNode } from '../canvas/PipelineNodes'
import { StageNode } from '../canvas/StageNode'
import { SinkNode } from '../canvas/SinkNode'
import '../canvas/flow-theme.css'
import { t } from '../../i18n'
import { edgeAriaLabel, flowAriaLabels } from '../canvas/aria'

type DiffMark = { status: DiffStatus; changes: FieldChange[]; plain?: boolean }
type DiffNodeData = Record<string, unknown> & { diff: DiffMark }
type DiffEdgeData = { status: Exclude<DiffStatus, 'changed'> }

const CHIP: Record<Exclude<DiffStatus, 'unchanged'>, { color: BadgeColor; icon: IconName }> = {
  added: { color: 'green', icon: 'plus' },
  removed: { color: 'red', icon: 'minus' },
  changed: { color: 'orange', icon: 'pen' },
}
const chipText = (status: Exclude<DiffStatus, 'unchanged'>) => status === 'added' ? t('gofi.history.diff-graph.chip-added', 'Added') : status === 'removed' ? t('gofi.history.diff-graph.chip-removed', 'Removed') : t('gofi.history.diff-graph.chip-changed', 'Changed')

/** Wraps a canvas node component with its diff decoration, so the cards look
 * exactly like the editor's. */
function withDiff(Inner: ComponentType<NodeProps>) {
  return function DiffDecorated(props: NodeProps) {
    const styles = useStyles2(getStyles)
    const { status, changes, plain } = (props.data as DiffNodeData).diff
    return <div className={cx(styles.decor, !plain && styles[status])}>
      {/* Status is the outline color; the legend names it. Changed cards
          list their fields on hover. */}
      {status === 'changed' && !plain
        ? <Tooltip content={<div>{changes.slice(0, 8).map(change => <div key={change.path}>{change.path}</div>)}{changes.length > 8 && <div>{t('gofi.history.diff-graph.and-more', 'and {{count}} more', { count: changes.length - 8 })}</div>}</div>}><div><Inner {...props} /></div></Tooltip>
        : <Inner {...props} />}
    </div>
  }
}

const NODE_TYPES = {
  ingress: withDiff(IngressNode as ComponentType<NodeProps>),
  frame: withDiff(FrameNode as ComponentType<NodeProps>),
  decode: withDiff(DecodeNode as ComponentType<NodeProps>),
  parse: withDiff(ParseNode as ComponentType<NodeProps>),
  stage: withDiff(StageNode as ComponentType<NodeProps>),
  sink: withDiff(SinkNode as ComponentType<NodeProps>),
}

function DiffEdge({ id, sourceX, sourceY, targetX, targetY, sourcePosition, targetPosition, markerEnd, data }: EdgeProps<Edge<DiffEdgeData>>) {
  const styles = useStyles2(getStyles)
  const [path] = getBezierPath({ sourceX, sourceY, targetX: targetX - 4, targetY, sourcePosition, targetPosition })
  const status = data?.status ?? 'unchanged'
  return <BaseEdge id={id} path={path} markerEnd={markerEnd} className={styles.edge[status]} style={status === 'removed' ? { strokeDasharray: '6 5' } : undefined} />
}
const EDGE_TYPES = { diff: DiffEdge }

/** Legend badge: "{{count}} added" and so on. */
const countText = (status: Exclude<DiffStatus, 'unchanged'>, count: number) => status === 'added'
  ? t('gofi.history.diff-graph.count-added', '{{count}} added', { count })
  : status === 'removed' ? t('gofi.history.diff-graph.count-removed', '{{count}} removed', { count })
  : t('gofi.history.diff-graph.count-changed', '{{count}} changed', { count })

/** Both versions of a flow as one read-only graph, nodes and edges marked
 * added, removed, or changed going from `before` to `after`
 * (see lib/flow-diff.ts for how nodes are matched). */
/** `plain` draws `after` alone, undecorated: a revision with nothing earlier
 * to compare against. */
export function FlowDiffGraph({ before, after, beforeLabel, afterLabel, plain }: { before: FlowDefinition | null; after: FlowDefinition | null; beforeLabel: string; afterLabel: string; plain?: boolean }) {
  const styles = useStyles2(getStyles)
  const theme = useTheme2()
  const diff = useMemo(() => plain ? diffFlows(after, after) : diffFlows(before, after), [before, after, plain])
  const [selectedId, setSelectedId] = useState<string | null>(null)

  const nodes = useMemo<Node[]>(() => diff.nodes.map(node => ({
    id: node.id, type: node.type, position: node.position, draggable: false, connectable: false,
    selected: node.id === selectedId,
    data: { ...node.data, diff: { status: node.status, changes: node.changes, plain } },
  })), [diff, selectedId, plain])
  const edges = useMemo<Edge<DiffEdgeData>[]>(() => diff.edges.map(edge => {
    const color = edge.status === 'added' ? theme.colors.success.main : edge.status === 'removed' ? theme.colors.error.main : theme.colors.border.strong
    return { id: edge.id, source: edge.source, target: edge.target, type: 'diff', selectable: false, ariaLabel: edgeAriaLabel(edge.source, edge.target), data: { status: edge.status }, markerEnd: { type: MarkerType.ArrowClosed, width: 16, height: 16, color } }
  }), [diff, theme])
  const ariaLabels = useMemo(flowAriaLabels, [])
  const selected = diff.nodes.find(node => node.id === selectedId)
  const summary = (['added', 'removed', 'changed'] as const).filter(status => diff.counts[status])

  return <div className={styles.root}>
    {!plain && <div className={styles.legend}>
      {summary.length
        ? summary.map(status => <Badge key={status} text={countText(status, diff.counts[status])} color={CHIP[status].color} icon={CHIP[status].icon} />)
        : <Text color="secondary" variant="bodySmall">{t('gofi.history.diff-graph.no-differences', "No differences in the flow's steps")}</Text>}
    </div>}
    <ReactFlow
      className="gofi-flow-readonly"
      ariaLabelConfig={ariaLabels}
      nodes={nodes}
      edges={edges}
      nodeTypes={NODE_TYPES}
      edgeTypes={EDGE_TYPES}
      nodesDraggable={false}
      nodesConnectable={false}
      elementsSelectable
      onNodeClick={(_, node) => { if (!plain) setSelectedId(node.id) }}
      onPaneClick={() => setSelectedId(null)}
      fitView
      fitViewOptions={{ padding: 0.15, minZoom: 0.3, maxZoom: 1 }}
      minZoom={0.2}
      colorMode={theme.isDark ? 'dark' : 'light'}
      proOptions={{ hideAttribution: true }}
    >
      <Background />
      <Controls showInteractive={false} />
    </ReactFlow>
    {selected && <aside className={styles.panel} aria-label={t('gofi.history.diff-graph.field-changes', 'Field changes')}>
      <Stack justifyContent="space-between" alignItems="center">
        <Stack alignItems="center" gap={1}>
          <Text weight="medium">{nodeTitle(selected.type, selected.data)}</Text>
          {selected.status !== 'unchanged' && <Badge text={chipText(selected.status)} color={CHIP[selected.status].color} />}
        </Stack>
        <IconButton name="times" size="sm" aria-label={t('gofi.common.close', 'Close')} onClick={() => setSelectedId(null)} />
      </Stack>
      <Text color="secondary" variant="bodySmall">
        {selected.status === 'added' ? t('gofi.history.diff-graph.only-in-after', 'Only in {{label}}.', { label: afterLabel })
          : selected.status === 'removed' ? t('gofi.history.diff-graph.only-in-before', 'Only in {{label}}.', { label: beforeLabel })
          : selected.status === 'unchanged' ? t('gofi.history.diff-graph.same-in-both', 'The same in {{before}} and {{after}}.', { before: beforeLabel, after: afterLabel })
          : t('gofi.history.diff-graph.before-to-after', '{{before}} → {{after}}', { before: beforeLabel, after: afterLabel })}
      </Text>
      {selected.changes.length > 0 && <dl className={styles.changes}>
        {selected.changes.map(change => <div key={change.path} className={styles.change}>
          <dt className={styles.path}>{change.path}</dt>
          <dd className={styles.values}>
            {selected.status !== 'added' && <span className={styles.before} title={formatValue(change.before)}>{formatValue(change.before)}</span>}
            {selected.status === 'changed' && <span className={styles.arrow}>→</span>}
            {selected.status !== 'removed' && <span className={styles.after} title={formatValue(change.after)}>{formatValue(change.after)}</span>}
          </dd>
        </div>)}
      </dl>}
    </aside>}
  </div>
}

function nodeTitle(type: string, data: unknown): string {
  const record = data as { spec?: { kind?: string; id?: string }; sink?: { type?: string }; isDeadLetter?: boolean }
  switch (type) {
    case 'ingress': return t('gofi.history.diff-graph.node-source', 'Source')
    case 'frame': return t('gofi.history.diff-graph.node-frame', 'Frame')
    case 'decode': return t('gofi.history.diff-graph.node-decode', 'Decode')
    case 'parse': return t('gofi.history.diff-graph.node-parse', 'Parse')
    case 'stage': return t('gofi.history.diff-graph.node-stage', 'Stage {{id}} ({{kind}})', { id: record.spec?.id ?? '', kind: record.spec?.kind ?? '' })
    default: return record.isDeadLetter ? t('gofi.history.diff-graph.node-dead-letter', 'Dead letter') : t('gofi.history.diff-graph.node-sink', 'Sink ({{type}})', { type: record.sink?.type ?? '' })
  }
}

const getStyles = (theme: GrafanaTheme2) => ({
  root: css({ position: 'relative', flex: 1, minHeight: 360, border: `1px solid ${theme.colors.border.weak}`, borderRadius: theme.shape.radius.default, overflow: 'hidden', background: theme.colors.background.canvas }),
  legend: css({ position: 'absolute', top: theme.spacing(1), left: theme.spacing(1), zIndex: 5, display: 'flex', gap: theme.spacing(0.75), flexWrap: 'wrap', padding: theme.spacing(0.5, 1), background: theme.colors.background.primary, border: `1px solid ${theme.colors.border.weak}`, borderRadius: theme.shape.radius.default }),
  decor: css({ position: 'relative', borderRadius: theme.shape.radius.default }),
  chip: css({ position: 'absolute', top: -12, right: theme.spacing(1), zIndex: 1 }),
  added: css({ outline: `2px solid ${theme.colors.success.main}`, outlineOffset: 2 }),
  removed: css({ outline: `2px dashed ${theme.colors.error.main}`, outlineOffset: 2, opacity: 0.6 }),
  changed: css({ outline: `2px solid ${theme.colors.warning.main}`, outlineOffset: 2 }),
  unchanged: css({ opacity: 0.7 }),
  edge: {
    added: css({ stroke: `${theme.colors.success.main} !important`, strokeWidth: 2 }),
    removed: css({ stroke: `${theme.colors.error.main} !important`, strokeWidth: 2, opacity: 0.7 }),
    unchanged: css({ stroke: theme.colors.border.strong, strokeWidth: 1.5 }),
  },
  panel: css({
    position: 'absolute', top: theme.spacing(1), right: theme.spacing(1), bottom: theme.spacing(1), width: 320, maxWidth: `calc(100% - ${theme.spacing(2)})`, zIndex: 6,
    display: 'flex', flexDirection: 'column', gap: theme.spacing(1), padding: theme.spacing(1.5), overflowY: 'auto',
    background: theme.colors.background.primary, border: `1px solid ${theme.colors.border.weak}`, borderRadius: theme.shape.radius.default, boxShadow: theme.shadows.z2,
  }),
  changes: css({ margin: 0, display: 'flex', flexDirection: 'column', gap: theme.spacing(1) }),
  change: css({ display: 'flex', flexDirection: 'column', gap: theme.spacing(0.25), paddingTop: theme.spacing(1), borderTop: `1px solid ${theme.colors.border.weak}` }),
  path: css({ fontFamily: theme.typography.fontFamilyMonospace, fontSize: theme.typography.bodySmall.fontSize, color: theme.colors.text.secondary }),
  values: css({ margin: 0, display: 'flex', flexWrap: 'wrap', alignItems: 'baseline', gap: theme.spacing(0.5), fontFamily: theme.typography.fontFamilyMonospace, fontSize: theme.typography.bodySmall.fontSize, wordBreak: 'break-all' }),
  before: css({ color: theme.colors.error.text, background: theme.colors.error.transparent, padding: theme.spacing(0, 0.5), borderRadius: theme.shape.radius.default, textDecoration: 'line-through' }),
  after: css({ color: theme.colors.success.text, background: theme.colors.success.transparent, padding: theme.spacing(0, 0.5), borderRadius: theme.shape.radius.default }),
  arrow: css({ color: theme.colors.text.secondary }),
})
