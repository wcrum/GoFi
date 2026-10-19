import { useEffect, useMemo, useState } from 'react'
import { css, cx } from '@emotion/css'
import type { GrafanaTheme2, IconName } from '@grafana/data'
import { Icon, LinkButton, Text, useStyles2 } from '@grafana/ui'
import { getFlow } from '../../api'
import { paths } from '../../router'
import { sourceTypeOf, type FlowDefinition } from '../../types'
import { KIND_ICON } from '../canvas/StageNode'
import { FRAMING_COLOR, PARSER_COLOR, SINK_COLOR, STAGE_KIND_COLOR, type StageColor } from '../stageColors'
import type { Highlight } from './conversation'
import { t } from '../../i18n'

interface Chip { key: string; icon: IconName; title: string; detail?: string; color?: StageColor }

/** Source icons as the canvas draws them (IngressNode). */
const SOURCE_ICON: Record<string, IconName> = { listen: 'signal', fetch: 'sync', subscribe: 'rss', python: 'brackets-curly' }

/** The flow's pipeline, left to right, keyed like AssistantHighlight.block:
 * source, frame, decode, parse, stage:<id>, encode, sink:<index>. */
function pipelineChips(flow: FlowDefinition): Chip[] {
  const sourceType = sourceTypeOf(flow.source)
  const decoders = flow.decode?.decoders ?? flow.parser.decoders ?? []
  const chips: Chip[] = [
    { key: 'source', icon: SOURCE_ICON[sourceType] ?? 'signal', title: t('gofi.assistant.highlight.source', 'Source'), detail: sourceType },
    { key: 'frame', icon: 'gf-grid', title: t('gofi.assistant.highlight.frame', 'Frame'), detail: flow.framing?.mode, color: FRAMING_COLOR[flow.framing?.mode] },
    { key: 'decode', icon: 'unlock', title: t('gofi.assistant.highlight.decode', 'Decode'), detail: decoders.length ? decoders.map(step => step.type).join(' → ') : t('gofi.assistant.highlight.none', 'none') },
    { key: 'parse', icon: 'brackets-curly', title: t('gofi.assistant.highlight.parse', 'Parse'), detail: flow.parser.format || 'none', color: PARSER_COLOR[flow.parser.format] },
    ...flow.stages.map(stage => ({ key: `stage:${stage.id}`, icon: KIND_ICON[stage.kind] ?? 'cog', title: stage.id, detail: stage.kind, color: STAGE_KIND_COLOR[stage.kind] })),
    { key: 'encode', icon: 'lock', title: t('gofi.assistant.highlight.encode', 'Encode') },
    ...flow.sinks.map((sink, index) => ({ key: `sink:${index}`, icon: 'arrow-right' as IconName, title: t('gofi.assistant.highlight.sink', 'Sink {{n}}', { n: index + 1 }), detail: sink.type, color: SINK_COLOR[sink.type] })),
  ]
  return chips
}

/** Whether `block` names this chip. Sinks match by index, and by type or
 * url when the assistant used one of those as the "id". */
function matchesBlock(block: string, chip: Chip, flow: FlowDefinition | null): boolean {
  if (block === chip.key) return true
  if (!block.startsWith('sink:') || !chip.key.startsWith('sink:') || !flow) return false
  const sink = flow.sinks[Number(chip.key.slice(5))]
  const id = block.slice(5)
  return !!sink && (sink.type === id || sink.url === id || sink.subject === id)
}

const blockLabel = (block: string): string => block.replace(/^stage:/, `${t('gofi.assistant.highlight.stage', 'stage')} `).replace(/^sink:/, `${t('gofi.assistant.highlight.sink-prefix', 'sink')} `)

/** One flow block the assistant pointed at: the flow's pipeline as a strip
 * of chips with that block highlighted, the note under it, and links to the
 * flow. Lazy-loaded by the chat panel (it shares the canvas's icons). */
export default function FlowHighlightCard({ highlight }: { highlight: Highlight }) {
  const styles = useStyles2(getStyles)
  const [flow, setFlow] = useState<FlowDefinition | null>(null)
  const [error, setError] = useState('')
  useEffect(() => {
    let cancelled = false
    setFlow(null); setError('')
    getFlow(highlight.flow).then(result => { if (!cancelled) setFlow(result) }).catch(reason => { if (!cancelled) setError(reason instanceof Error ? reason.message : String(reason)) })
    return () => { cancelled = true }
  }, [highlight.flow])
  // Without the flow (not found, controller down) the named block still shows alone.
  const chips = useMemo(() => flow ? pipelineChips(flow) : [{ key: highlight.block, icon: 'question-circle' as IconName, title: blockLabel(highlight.block) }], [flow, highlight.block])
  const known = chips.some(chip => matchesBlock(highlight.block, chip, flow))
  return <div className={styles.card} role="group" aria-label={t('gofi.assistant.highlight.aria', 'Flow {{flow}}, {{block}}', { flow: highlight.flow, block: blockLabel(highlight.block) })}>
    <div className={styles.header}>
      <Icon name="exchange-alt" className={styles.headerIcon} />
      <Text weight="medium">{highlight.flow}</Text>
      <Text color="secondary" variant="bodySmall">{blockLabel(highlight.block)}</Text>
      {!flow && !error && <Icon name="spinner" className={styles.spinner} />}
    </div>
    <div className={styles.strip}>
      {chips.map((chip, index) => {
        const active = matchesBlock(highlight.block, chip, flow)
        return <div key={chip.key} className={styles.step}>
          {index > 0 && <Icon name="arrow-right" className={styles.arrow} size="sm" />}
          <div className={cx(styles.chip, active && styles.chipActive)} aria-current={active ? 'true' : undefined} title={chip.key}>
            <Icon name={chip.icon} size="sm" className={styles.chipIcon} style={chip.color ? { color: colorOf(chip.color) } : undefined} />
            <span className={styles.chipTitle}>{chip.title}</span>
            {chip.detail && <span className={styles.chipDetail}>{chip.detail}</span>}
          </div>
        </div>
      })}
    </div>
    {(highlight.note || !known || error) && <div className={styles.note}>
      <Icon name="exclamation-triangle" className={styles.noteIcon} />
      <div>
        {highlight.note && <Text>{highlight.note}</Text>}
        {error && <Text color="secondary" variant="bodySmall">{t('gofi.assistant.highlight.flow-unavailable', 'The flow could not be loaded: {{message}}', { message: error })}</Text>}
        {!error && flow && !known && <Text color="secondary" variant="bodySmall">{t('gofi.assistant.highlight.unknown-block', 'No block named {{block}} in this flow.', { block: highlight.block })}</Text>}
      </div>
    </div>}
    <div className={styles.links}>
      <LinkButton size="sm" variant="secondary" icon="eye" href={paths.flow(highlight.flow)}>{t('gofi.assistant.highlight.open-flow', 'Open flow')}</LinkButton>
      <LinkButton size="sm" variant="secondary" icon="pen" href={paths.flow(highlight.flow, 'edit')}>{t('gofi.assistant.highlight.open-editor', 'Open in editor')}</LinkButton>
    </div>
  </div>
}

// Stage colours are Grafana badge colours; the icon takes the matching theme colour.
let colorTheme: GrafanaTheme2 | null = null
const colorOf = (color: StageColor): string | undefined => colorTheme?.visualization.getColorByName(color === 'darkgrey' ? 'gray' : color)

const getStyles = (theme: GrafanaTheme2) => {
  colorTheme = theme
  return {
    card: css({
      display: 'flex', flexDirection: 'column', gap: theme.spacing(1), padding: theme.spacing(1, 1.5, 1.5), minWidth: 0,
      border: `1px solid ${theme.colors.border.medium}`, borderRadius: theme.shape.radius.default, background: theme.colors.background.secondary,
    }),
    header: css({ display: 'flex', alignItems: 'center', gap: theme.spacing(1), minWidth: 0, overflowWrap: 'anywhere' }),
    headerIcon: css({ color: theme.colors.text.secondary }),
    spinner: css({ color: theme.colors.text.secondary }),
    strip: css({ display: 'flex', alignItems: 'stretch', gap: theme.spacing(0.5), overflowX: 'auto', paddingBottom: theme.spacing(0.5), scrollbarWidth: 'thin' }),
    step: css({ display: 'flex', alignItems: 'center', gap: theme.spacing(0.5), flexShrink: 0 }),
    arrow: css({ color: theme.colors.text.disabled }),
    chip: css({
      display: 'grid', gridTemplateColumns: 'auto 1fr', columnGap: theme.spacing(0.75), alignItems: 'center', padding: theme.spacing(0.5, 1), minWidth: 72,
      border: `1px solid ${theme.colors.border.weak}`, borderRadius: theme.shape.radius.default, background: theme.colors.background.primary,
      fontSize: theme.typography.bodySmall.fontSize, lineHeight: 1.3,
    }),
    chipActive: css({
      borderColor: theme.colors.warning.border, background: theme.colors.warning.transparent, boxShadow: `0 0 0 1px ${theme.colors.warning.border}`,
      '& > span': { color: theme.colors.warning.text },
    }),
    chipIcon: css({ gridRow: '1 / span 2', color: theme.colors.text.secondary }),
    chipTitle: css({ fontWeight: theme.typography.fontWeightMedium, whiteSpace: 'nowrap', maxWidth: 160, overflow: 'hidden', textOverflow: 'ellipsis' }),
    chipDetail: css({ color: theme.colors.text.secondary, whiteSpace: 'nowrap', maxWidth: 160, overflow: 'hidden', textOverflow: 'ellipsis', fontFamily: theme.typography.fontFamilyMonospace }),
    note: css({ display: 'flex', alignItems: 'flex-start', gap: theme.spacing(1), padding: theme.spacing(1), borderLeft: `3px solid ${theme.colors.warning.border}`, background: theme.colors.warning.transparent, borderRadius: theme.shape.radius.default, overflowWrap: 'anywhere' }),
    noteIcon: css({ color: theme.colors.warning.text, marginTop: 2, flexShrink: 0 }),
    links: css({ display: 'flex', gap: theme.spacing(1), flexWrap: 'wrap' }),
  }
}
