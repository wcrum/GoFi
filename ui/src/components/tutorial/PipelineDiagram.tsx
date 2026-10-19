import { useId } from 'react'
import { css } from '@emotion/css'
import type { GrafanaTheme2 } from '@grafana/data'
import { useStyles2, useTheme2 } from '@grafana/ui'
import { t } from '../../i18n'

const BOX = { width: 96, height: 40, gap: 20, top: 8 }
const STEPS = 6
const VIEW = { width: BOX.width * STEPS + BOX.gap * (STEPS - 1), height: 84 }

/** The pipeline in one picture: source → frames → decode and parse → stages
 * → encode → sinks, in the same colours the editor's badges use for those parts. */
export function PipelineDiagram() {
  const theme = useTheme2()
  const styles = useStyles2(getStyles)
  const marker = useId()
  const palette = theme.visualization
  const stages = [
    { label: t('gofi.tutorial.diagram.source', 'Source'), detail: t('gofi.tutorial.diagram.source-detail', 'TCP / UDP listen'), color: theme.colors.primary.main },
    { label: t('gofi.tutorial.diagram.frames', 'Frames'), detail: t('gofi.tutorial.diagram.frames-detail', 'line, delimiter, raw'), color: palette.getColorByName('blue') },
    { label: t('gofi.tutorial.diagram.decode', 'Decode & parse'), detail: t('gofi.tutorial.diagram.decode-detail', 'base64 · JSON, XML'), color: palette.getColorByName('green') },
    { label: t('gofi.tutorial.diagram.stages', 'Stages'), detail: t('gofi.tutorial.diagram.stages-detail', 'CEL, regex, transforms'), color: palette.getColorByName('orange') },
    { label: t('gofi.tutorial.diagram.encode', 'Encode'), detail: t('gofi.tutorial.diagram.encode-detail', 'format · gzip, base64'), color: palette.getColorByName('red') },
    { label: t('gofi.tutorial.diagram.sinks', 'Sinks'), detail: t('gofi.tutorial.diagram.sinks-detail', 'HTTP, NATS, stdout…'), color: palette.getColorByName('purple') },
  ]
  return <svg className={styles.svg} viewBox={`0 0 ${VIEW.width} ${VIEW.height}`} role="img" aria-label={t('gofi.tutorial.diagram.label', 'A flow: source, frames, decode and parse, stages, encode, sinks')}>
    <defs>
      <marker id={marker} viewBox="0 0 8 8" refX="7" refY="4" markerWidth="6" markerHeight="6" orient="auto">
        <path d="M0 0 L8 4 L0 8 z" fill={theme.colors.border.strong} />
      </marker>
    </defs>
    {stages.map((stage, index) => {
      const x = index * (BOX.width + BOX.gap)
      return <g key={stage.label}>
        {index > 0 && <line x1={x - BOX.gap + 2} y1={BOX.top + BOX.height / 2} x2={x - 2} y2={BOX.top + BOX.height / 2} stroke={theme.colors.border.strong} strokeWidth={1.5} markerEnd={`url(#${marker})`} />}
        <rect x={x} y={BOX.top} width={BOX.width} height={BOX.height} rx={6} fill={theme.colors.background.secondary} stroke={theme.colors.border.medium} />
        <rect x={x} y={BOX.top} width={4} height={BOX.height} rx={2} fill={stage.color} />
        <text x={x + BOX.width / 2 + 2} y={BOX.top + BOX.height / 2} textAnchor="middle" dominantBaseline="central" fill={theme.colors.text.primary} fontSize={12} fontWeight={500} fontFamily={theme.typography.fontFamily}>{stage.label}</text>
        <text x={x + BOX.width / 2} y={BOX.top + BOX.height + 18} textAnchor="middle" fill={theme.colors.text.secondary} fontSize={10} fontFamily={theme.typography.fontFamily}>{stage.detail}</text>
      </g>
    })}
  </svg>
}

const getStyles = (theme: GrafanaTheme2) => ({
  svg: css({ display: 'block', width: '100%', height: 'auto', margin: theme.spacing(1, 0), overflow: 'visible' }),
})
