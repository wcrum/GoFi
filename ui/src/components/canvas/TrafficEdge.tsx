import { useEffect, useState } from 'react'
import { css, cx, keyframes } from '@emotion/css'
import type { GrafanaTheme2 } from '@grafana/data'
import { useStyles2 } from '@grafana/ui'
import { BaseEdge, EdgeLabelRenderer, getBezierPath, Position, type Edge, type EdgeProps } from '@xyflow/react'

export type TrafficEdgeData = {
  /** Smoothed messages per second on this edge; absent or 0 means idle. */
  rate?: number
  /** Rejected-traffic edge (dead letter): drawn dashed and in the error color. */
  rejected?: boolean
  /** Another flow is focused: the label fades with its edge. */
  dimmed?: boolean
  [key: string]: unknown
}
export type TrafficEdgeType = Edge<TrafficEdgeData, 'traffic'>

function useReducedMotion(): boolean {
  const query = '(prefers-reduced-motion: reduce)'
  const [reduced, setReduced] = useState(() => typeof window !== 'undefined' && window.matchMedia(query).matches)
  useEffect(() => {
    const media = window.matchMedia(query)
    const update = () => setReduced(media.matches)
    media.addEventListener('change', update)
    return () => media.removeEventListener('change', update)
  }, [])
  return reduced
}

/** Ports draw a 2px canvas-colored ring outside their measured bounds
 * (flow-theme.css); edges end this far outside the port so the arrowhead
 * stays clear of it. */
const PORT_CLEARANCE = 4
const PORT_OUTWARD: Record<Position, [number, number]> = {
  [Position.Left]: [-1, 0], [Position.Right]: [1, 0], [Position.Top]: [0, -1], [Position.Bottom]: [0, 1],
}

/** A directional edge: a bezier curve, always arrowed. While traffic flows
 * it takes the accent color and its stroke runs along the curve as a dashed
 * line at a constant pace. The pace is deliberately not tied to the rate:
 * anything derived from a changing number (dots, "/s" labels) restarted or
 * flickered with every sample; a steady animation only turns on and off. */
export function TrafficEdge({ id, sourceX, sourceY, targetX, targetY, sourcePosition, targetPosition, data, markerEnd, style, label, selected }: EdgeProps<TrafficEdgeType>) {
  const styles = useStyles2(getStyles)
  const reduced = useReducedMotion()
  // Stop short of the target port so its canvas ring doesn't cover the arrowhead.
  const [dx, dy] = PORT_OUTWARD[targetPosition]
  const [path, labelX, labelY] = getBezierPath({ sourceX, sourceY, targetX: targetX + dx * PORT_CLEARANCE, targetY: targetY + dy * PORT_CLEARANCE, sourcePosition, targetPosition })
  const active = (data?.rate ?? 0) > 0
  const rejected = !!data?.rejected
  return <>
    <BaseEdge id={id} path={path} markerEnd={markerEnd}
      className={cx(styles.edge, active && (rejected ? styles.activeRejected : styles.active), active && !reduced && styles.flowing, selected && styles.selected)}
      style={{ ...style, ...(rejected && !active ? { strokeDasharray: '5 5' } : {}) }} />
    {label && <EdgeLabelRenderer>
      <div className={cx('nodrag', 'nopan', styles.label, active && (rejected ? styles.labelRejected : styles.labelActive), data?.dimmed && styles.labelDimmed)}
        // Sits just above the line so short edges keep their arrow visible.
        style={{ transform: `translate(-50%, calc(-100% - 6px)) translate(${labelX}px, ${labelY}px)` }}>
        <span>{label}</span>
      </div>
    </EdgeLabelRenderer>}
  </>
}

// The dash pattern travels one period per cycle, so the loop is seamless.
const flow = keyframes({ from: { strokeDashoffset: 14 }, to: { strokeDashoffset: 0 } })

const getStyles = (theme: GrafanaTheme2) => ({
  edge: css({ stroke: theme.colors.border.strong, strokeWidth: 1.5, transition: 'stroke 300ms ease, stroke-width 300ms ease' }),
  active: css({ stroke: theme.colors.primary.main, strokeWidth: 2 }),
  activeRejected: css({ stroke: theme.colors.error.main, strokeWidth: 2 }),
  flowing: css({ strokeDasharray: '8 6', strokeLinecap: 'round', animation: `${flow} 0.7s linear infinite` }),
  selected: css({ strokeWidth: 2.5 }),
  label: css({
    position: 'absolute', pointerEvents: 'none', display: 'flex', gap: theme.spacing(0.5), alignItems: 'center',
    padding: theme.spacing(0, 0.5), borderRadius: theme.shape.radius.default, fontSize: 10, lineHeight: '16px', whiteSpace: 'nowrap',
    background: theme.colors.background.primary, border: `1px solid ${theme.colors.border.weak}`, color: theme.colors.text.secondary,
    transition: 'opacity 180ms ease', '@media (prefers-reduced-motion: reduce)': { transition: 'none' },
  }),
  labelActive: css({ borderColor: theme.colors.primary.border, color: theme.colors.primary.text }),
  labelRejected: css({ borderColor: theme.colors.error.border, color: theme.colors.error.text }),
  labelDimmed: css({ opacity: 0.25 }),
})

export const EDGE_TYPES = { traffic: TrafficEdge }
