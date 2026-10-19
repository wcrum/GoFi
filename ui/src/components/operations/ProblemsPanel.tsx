import { useCallback, useEffect, useRef, useState } from 'react'
import { css, cx } from '@emotion/css'
import type { GrafanaTheme2 } from '@grafana/data'
import { Button, Icon, IconButton, InlineSwitch, Stack, Text, Tooltip, useStyles2 } from '@grafana/ui'
import { acknowledgeBulletins, clearBulletins, listBulletins, type Bulletin } from '../../operations-api'
import { componentLabel } from '../../lib/runstate'
import { useToast } from '../Toast'
import { PanelEmpty } from '../PanelEmpty'
import { formatDateTime, formatNumber, formatRelativeTime, t } from '../../i18n'

const POLL_MS = 5000
const SEVERITY_ICON = { error: 'exclamation-circle', warning: 'exclamation-triangle', info: 'info-circle' } as const

/** A flow's bulletin board: recent runtime problems aggregated by component
 * and message, with acknowledge and clear. `initial` is the list a page
 * loaded up front, shown at once and refreshed on the first poll. */
export function ProblemsPanel({ flow, initial }: { flow: string; initial?: Bulletin[] }) {
  const styles = useStyles2(getStyles)
  const addToast = useToast()
  const [items, setItems] = useState<Bulletin[] | null>(initial ?? null)
  const seeded = useRef(initial !== undefined)
  const [showAcknowledged, setShowAcknowledged] = useState(false)
  const [error, setError] = useState<string>()

  const load = useCallback(async () => {
    try { setItems(await listBulletins(flow, true)); setError(undefined) }
    catch (err) { setError(String(err)) }
  }, [flow])
  useEffect(() => {
    if (seeded.current) seeded.current = false
    else void load()
    const timer = window.setInterval(() => { if (document.visibilityState !== 'hidden') void load() }, POLL_MS)
    return () => clearInterval(timer)
  }, [load])

  async function act(kind: 'ack' | 'clear', ids: string[] = []) {
    try {
      await (kind === 'ack' ? acknowledgeBulletins : clearBulletins)(flow, ids)
      await load()
    } catch (err) { addToast(String(err)) }
  }

  if (!items) return error ? <PanelEmpty icon="exclamation-triangle" text={t('gofi.operations.problems-panel.unavailable', 'Problems are unavailable')} detail={error} /> : <PanelEmpty icon="sync" text={t('gofi.operations.problems-panel.loading', 'Loading problems…')} />
  const open = items.filter(item => !item.acknowledged)
  const shown = showAcknowledged ? items : open
  const openText = open.length ? t('gofi.operations.problems-panel.open-count', '{{count}} open problems', { count: open.length, defaultValue_one: '{{count}} open problem' }) : t('gofi.operations.problems-panel.none-open', 'No open problems')
  const summary = items.length > open.length
    ? t('gofi.operations.problems-panel.summary-with-acknowledged', '{{open}} · {{count}} acknowledged', { open: openText, count: items.length - open.length })
    : openText
  const severityLabel = (severity: Bulletin['severity']) => severity === 'error' ? t('gofi.operations.problems-panel.severity-error', 'error') : severity === 'warning' ? t('gofi.operations.problems-panel.severity-warning', 'warning') : t('gofi.operations.problems-panel.severity-info', 'info')
  return <Stack direction="column" gap={2}>
    <div className={styles.toolbar}>
      <Text color="secondary">{summary}</Text>
      <Stack gap={1} alignItems="center" wrap="wrap">
        {/* InlineField adds its own baseline margin, which misaligns it next to the buttons. */}
        <InlineSwitch transparent showLabel label={t('gofi.operations.problems-panel.show-acknowledged', 'Show acknowledged')} value={showAcknowledged} onChange={() => setShowAcknowledged(value => !value)} />
        <Button size="sm" variant="secondary" icon="check" disabled={!open.length} onClick={() => void act('ack')}>{t('gofi.operations.problems-panel.acknowledge-all', 'Acknowledge all')}</Button>
        <Button size="sm" variant="secondary" fill="outline" icon="trash-alt" disabled={!items.length} onClick={() => void act('clear')}>{t('gofi.operations.problems-panel.clear-all', 'Clear all')}</Button>
      </Stack>
    </div>
    {!shown.length
      ? <PanelEmpty icon="check-circle" text={items.length ? t('gofi.operations.problems-panel.all-acknowledged', 'Every problem is acknowledged') : t('gofi.operations.problems-panel.none-reported', 'No problems reported')} detail={t('gofi.operations.problems-panel.empty-detail', 'Source, parser, stage, and destination errors and runtime restarts appear here.')} />
      : <table className={styles.table}>
        <thead><tr><th aria-label={t('gofi.operations.problems-panel.column-severity', 'Severity')} /><th>{t('gofi.operations.problems-panel.column-component', 'Component')}</th><th>{t('gofi.operations.problems-panel.column-message', 'Message')}</th><th className={styles.num}>{t('gofi.operations.problems-panel.column-count', 'Count')}</th><th>{t('gofi.operations.problems-panel.column-last-seen', 'Last seen')}</th><th>{t('gofi.operations.problems-panel.column-first-seen', 'First seen')}</th><th aria-label={t('gofi.operations.problems-panel.column-actions', 'Actions')} /></tr></thead>
        <tbody>{shown.map(item => <tr key={item.id} className={cx(item.acknowledged && styles.acknowledged)}>
          <td><Tooltip content={severityLabel(item.severity)}><Icon name={SEVERITY_ICON[item.severity]} className={styles[item.severity]} /></Tooltip></td>
          <td className={styles.nowrap}>{componentLabel(item.component)}</td>
          <td className={styles.message}>{item.message}</td>
          <td className={styles.num}>{formatNumber(item.count)}</td>
          <td className={styles.nowrap} title={item.lastSeen ? formatDateTime(item.lastSeen) : undefined}>{item.lastSeen ? formatRelativeTime(item.lastSeen) : '—'}</td>
          <td className={styles.nowrap} title={item.firstSeen ? formatDateTime(item.firstSeen) : undefined}>{item.firstSeen ? formatRelativeTime(item.firstSeen) : '—'}</td>
          <td className={styles.nowrap}>
            {!item.acknowledged && <IconButton name="check" tooltip={t('gofi.operations.problems-panel.acknowledge-tooltip', 'Acknowledge (until it happens again)')} aria-label={t('gofi.operations.problems-panel.acknowledge', 'Acknowledge')} onClick={() => void act('ack', [item.id])} />}
            <IconButton name="times" tooltip={t('gofi.operations.problems-panel.clear-tooltip', 'Clear (until it happens again)')} aria-label={t('gofi.operations.problems-panel.clear', 'Clear')} onClick={() => void act('clear', [item.id])} />
          </td>
        </tr>)}</tbody>
      </table>}
  </Stack>
}

const getStyles = (theme: GrafanaTheme2) => ({
  toolbar: css({ display: 'flex', flexWrap: 'wrap', gap: theme.spacing(1), alignItems: 'center', justifyContent: 'space-between' }),
  table: css({
    width: '100%', borderCollapse: 'collapse', fontSize: theme.typography.bodySmall.fontSize,
    'th, td': { padding: theme.spacing(0.75, 1), borderBottom: `1px solid ${theme.colors.border.weak}`, textAlign: 'left', verticalAlign: 'top' },
    th: { color: theme.colors.text.secondary, fontWeight: theme.typography.fontWeightMedium },
  }),
  num: css({ textAlign: 'right !important' as 'right', fontVariantNumeric: 'tabular-nums' }),
  nowrap: css({ whiteSpace: 'nowrap' }),
  message: css({ fontFamily: theme.typography.fontFamilyMonospace, wordBreak: 'break-word' }),
  acknowledged: css({ opacity: 0.6 }),
  error: css({ color: theme.colors.error.text }),
  warning: css({ color: theme.colors.warning.text }),
  info: css({ color: theme.colors.info.text }),
})
