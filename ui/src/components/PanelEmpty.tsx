import type { IconName } from '@grafana/data'
import { css } from '@emotion/css'
import { Icon, Text, useTheme2 } from '@grafana/ui'
import { GoFiMark } from './GoFiMark'

/** A panel's empty or waiting state, centered in the panel's full area. It
 * shares EmptyState's look at panel scale: a tile (the GoFi mark, or the given
 * icon on a neutral tile for live-waiting states) over centered text. */
export function PanelEmpty({ text, icon, detail }: { text: string; icon?: IconName; detail?: string }) {
  const theme = useTheme2()
  const tile = 36
  return <div role="status" className={css({ flex: 1, height: '100%', minHeight: 80, display: 'flex', flexDirection: 'column', alignItems: 'center', justifyContent: 'center', gap: theme.spacing(1), padding: theme.spacing(2), textAlign: 'center' })}>
    {icon
      ? <span aria-hidden="true" className={css({
        width: tile, height: tile, display: 'flex', alignItems: 'center', justifyContent: 'center', marginBottom: theme.spacing(0.5),
        // Same corner ratio as the GoFi mark's tile (6/32).
        borderRadius: tile * 6 / 32, background: theme.colors.action.hover, color: theme.colors.text.secondary,
      })}><Icon name={icon} size="lg" /></span>
      : <GoFiMark size={tile} className={css({ marginBottom: theme.spacing(0.5) })} />}
    <Text color="secondary">{text}</Text>
    {detail && <Text variant="bodySmall" color="secondary">{detail}</Text>}
  </div>
}
