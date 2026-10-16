import type { IconName } from '@grafana/data'
import { css } from '@emotion/css'
import { Icon, Text, useTheme2 } from '@grafana/ui'

/** A panel's empty or waiting state, centered in the panel's full area. */
export function PanelEmpty({ text, icon, detail }: { text: string; icon?: IconName; detail?: string }) {
  const theme = useTheme2()
  return <div role="status" className={css({ flex: 1, height: '100%', minHeight: 80, display: 'flex', flexDirection: 'column', alignItems: 'center', justifyContent: 'center', gap: theme.spacing(1), padding: theme.spacing(2), textAlign: 'center' })}>
    {icon && <Icon name={icon} size="xl" className={css({ color: theme.colors.text.disabled })} />}
    <Text color="secondary">{text}</Text>
    {detail && <Text variant="bodySmall" color="secondary">{detail}</Text>}
  </div>
}
