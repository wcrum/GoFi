import { useState } from 'react'
import { css } from '@emotion/css'
import type { GrafanaTheme2 } from '@grafana/data'
import { PanelChrome, useStyles2, useTheme2 } from '@grafana/ui'

interface StatPanelProps {
  title: string
  value: string | number
  description?: string
  /** Oldest-first per-second values; renders a 60s sparkline when present. */
  series?: number[]
  unit?: string
}

/** Grafana stat-panel look: title, big value, optional sparkline with a
 * per-second hover readout. */
export function StatPanel({ title, value, description, series, unit = '/s' }: StatPanelProps) {
  const styles = useStyles2(getStyles)
  const [hover, setHover] = useState<number | null>(null)
  const display = typeof value === 'number' ? value.toLocaleString() : value
  return <PanelChrome title={title} description={description}>
    <div className={styles.body}>
      <div className={styles.value} aria-live="polite">{display}</div>
      <div className={styles.readout}>{series && hover !== null ? `${series.length - 1 - hover}s ago · ${series[hover]}${unit}` : series ? `last ${series.length}s` : ' '}</div>
      {series && <Sparkline series={series} hover={hover} onHover={setHover} label={`${title} per second, last ${series.length} seconds`} />}
    </div>
  </PanelChrome>
}

const W = 240, H = 40
function Sparkline({ series, hover, onHover, label }: { series: number[]; hover: number | null; onHover: (index: number | null) => void; label: string }) {
  const theme = useTheme2()
  const max = Math.max(1, ...series)
  const step = W / Math.max(1, series.length - 1)
  const points = series.map((value, index) => [index * step, H - 2 - (value / max) * (H - 4)] as const)
  const line = points.map(([x, y], index) => `${index ? 'L' : 'M'}${x.toFixed(1)},${y.toFixed(1)}`).join('')
  const color = theme.colors.primary.main
  return <svg viewBox={`0 0 ${W} ${H}`} preserveAspectRatio="none" width="100%" height={H} role="img" aria-label={label} onMouseLeave={() => onHover(null)}>
    <path d={`${line}L${W},${H}L0,${H}Z`} fill={color} fillOpacity={0.15} />
    <path d={line} fill="none" stroke={color} strokeWidth={2} vectorEffect="non-scaling-stroke" strokeLinejoin="round" />
    {hover !== null && <line x1={points[hover][0]} x2={points[hover][0]} y1={0} y2={H} stroke={theme.colors.text.secondary} strokeWidth={1} vectorEffect="non-scaling-stroke" />}
    {series.map((_, index) => <rect key={index} x={index * step - step / 2} y={0} width={step} height={H} fill="transparent" onMouseEnter={() => onHover(index)} />)}
  </svg>
}

const getStyles = (theme: GrafanaTheme2) => ({
  body: css({ display: 'flex', flexDirection: 'column', height: '100%', justifyContent: 'flex-start' }),
  value: css({ fontSize: theme.typography.h1.fontSize, fontWeight: theme.typography.fontWeightMedium, lineHeight: 1.1, color: theme.colors.text.maxContrast, fontVariantNumeric: 'tabular-nums' }),
  readout: css({ fontSize: theme.typography.bodySmall.fontSize, color: theme.colors.text.secondary, margin: theme.spacing(0.5, 0) }),
})
