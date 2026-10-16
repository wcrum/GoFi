import { useEffect, useState } from 'react'
import { css, cx } from '@emotion/css'
import type { GrafanaTheme2 } from '@grafana/data'
import { useStyles2 } from '@grafana/ui'
import { BaseEdge, EdgeLabelRenderer, getSmoothStepPath, type Edge, type EdgeProps } from '@xyflow/react'
import { formatRate, particlesFor } from './traffic'

export type TrafficEdgeData = {
  /** Smoothed messages per second on this edge; absent or 0 means idle. */
  rate?: number
  /** Rejected-traffic edge (dead letter): drawn dashed and in the error color. */
  rejected?: boolean
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

/** A directional edge: always arrowed, and while traffic flows it highlights
 * and carries dots from source to target at a pace that follows the rate. */
export function TrafficEdge({ id, sourceX, sourceY, targetX, targetY, sourcePosition, targetPosition, data, markerEnd, style, label, selected }: EdgeProps<TrafficEdgeType>) {
  const styles = useStyles2(getStyles)
  const reduced = useReducedMotion()
  const [path, labelX, labelY] = getSmoothStepPath({ sourceX, sourceY, targetX, targetY, sourcePosition, targetPosition })
  const rate = data?.rate ?? 0
  const active = rate > 0
  const rejected = !!data?.rejected
  const { count, seconds } = particlesFor(rate)
  return <>
    <BaseEdge id={id} path={path} markerEnd={markerEnd}
      className={cx(styles.edge, active && (rejected ? styles.activeRejected : styles.active), selected && styles.selected)}
      style={{ ...style, ...(rejected ? { strokeDasharray: '5 5' } : {}) }} />
    {active && !reduced && Array.from({ length: count }, (_, index) => (
      <circle key={index} r={3} className={rejected ? styles.dotRejected : styles.dot}>
        {/* A negative begin starts each dot mid-path, so none waits at the origin. */}
        <animateMotion dur={`${seconds}s`} begin={`-${(index * seconds) / count}s`} repeatCount="indefinite" path={path} />
      </circle>
    ))}
    {(active || label) && <EdgeLabelRenderer>
      <div className={cx('nodrag', 'nopan', styles.label, active && (rejected ? styles.labelRejected : styles.labelActive))}
        // Sits just above the line so short edges keep their arrow and dots visible.
        style={{ transform: `translate(-50%, calc(-100% - 6px)) translate(${labelX}px, ${labelY}px)` }}>
        {label && <span>{label}</span>}
        {active && <span className={styles.rate}>{formatRate(rate)}</span>}
      </div>
    </EdgeLabelRenderer>}
  </>
}

const getStyles = (theme: GrafanaTheme2) => ({
  edge: css({ stroke: theme.colors.border.strong, strokeWidth: 1.5, transition: 'stroke 300ms ease, stroke-width 300ms ease' }),
  active: css({ stroke: theme.colors.primary.main, strokeWidth: 2 }),
  activeRejected: css({ stroke: theme.colors.error.main, strokeWidth: 2 }),
  selected: css({ strokeWidth: 2.5 }),
  dot: css({ fill: theme.colors.primary.main, filter: `drop-shadow(0 0 3px ${theme.colors.primary.main})` }),
  dotRejected: css({ fill: theme.colors.error.main, filter: `drop-shadow(0 0 3px ${theme.colors.error.main})` }),
  label: css({
    position: 'absolute', pointerEvents: 'none', display: 'flex', gap: theme.spacing(0.5), alignItems: 'center',
    padding: theme.spacing(0, 0.5), borderRadius: theme.shape.radius.default, fontSize: 10, lineHeight: '16px', whiteSpace: 'nowrap',
    background: theme.colors.background.primary, border: `1px solid ${theme.colors.border.weak}`, color: theme.colors.text.secondary,
  }),
  labelActive: css({ borderColor: theme.colors.primary.border, color: theme.colors.primary.text }),
  labelRejected: css({ borderColor: theme.colors.error.border, color: theme.colors.error.text }),
  rate: css({ fontFamily: theme.typography.fontFamilyMonospace, fontVariantNumeric: 'tabular-nums', fontWeight: theme.typography.fontWeightMedium }),
})

export const EDGE_TYPES = { traffic: TrafficEdge }
