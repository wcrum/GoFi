import type { ReactNode } from 'react'
import { css } from '@emotion/css'
import type { GrafanaTheme2, IconName } from '@grafana/data'
import { Icon, Stack, Text, useStyles2 } from '@grafana/ui'
import { GoFiMark } from './GoFiMark'

export type EmptyStateVariant = 'call-to-action' | 'not-found' | 'completed'

export interface EmptyStateProps {
  variant: EmptyStateVariant
  /** Headline, rendered as an h4. */
  message: string
  /** Secondary text under the message. */
  children?: ReactNode
  /** Action rendered under the text. */
  button?: ReactNode
  /** Hide the GoFi mark (for tight spots). */
  hideImage?: boolean
  role?: string
}

const BADGE: Record<EmptyStateVariant, IconName> = { 'call-to-action': 'plus', 'not-found': 'search', completed: 'check' }

/** GoFi's empty state: the GoFi mark with a small variant badge, a centered
 * headline, secondary text, and an optional action. A compact stand-in for
 * Grafana's EmptyState (same props) without the Grot illustration. */
export function EmptyState({ variant, message, children, button, hideImage, role }: EmptyStateProps) {
  const styles = useStyles2(getEmptyStyles)
  return <div className={styles.root} role={role}>
    <Stack direction="column" alignItems="center" gap={2}>
      {!hideImage && <EmptyMark badge={BADGE[variant]} />}
      <Stack direction="column" alignItems="center" gap={1}>
        <Text element="h4" textAlignment="center">{message}</Text>
        {children && <Text color="secondary" textAlignment="center">{children}</Text>}
      </Stack>
      {button}
    </Stack>
  </div>
}

/** The GoFi mark with an optional variant glyph tucked into its corner. */
export function EmptyMark({ badge, size = 52 }: { badge?: IconName; size?: number }) {
  const styles = useStyles2(getEmptyStyles)
  return <div className={styles.mark} aria-hidden="true">
    <GoFiMark size={size} />
    {badge && <span className={styles.badge}><Icon name={badge} size="xs" /></span>}
  </div>
}

export const getEmptyStyles = (theme: GrafanaTheme2) => ({
  root: css({ width: '100%', maxWidth: 600, margin: '0 auto', padding: theme.spacing(4, 2) }),
  mark: css({ position: 'relative', display: 'inline-flex' }),
  badge: css({
    position: 'absolute', right: -6, bottom: -6, width: 20, height: 20, display: 'flex', alignItems: 'center', justifyContent: 'center',
    borderRadius: theme.shape.radius.circle, background: theme.colors.background.primary, color: theme.colors.text.secondary,
    border: `1px solid ${theme.colors.border.medium}`,
  }),
})
