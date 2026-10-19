import type { ReactNode } from 'react'
import { css, cx, keyframes } from '@emotion/css'
import type { GrafanaTheme2, IconName } from '@grafana/data'
import { Badge, Icon, useStyles2, type BadgeColor } from '@grafana/ui'
import { MOCK_STATE_BADGE_COLOR, MOCK_STATE_OUTLINE, mockStateLabel } from '../stageColors'

interface NodeCardProps {
  icon: IconName
  title: string
  /** `fixed`: a state badge whose text changes (connecting / connected / disconnected) keeps one width, so the title beside it never reflows. */
  badge?: { text: string; color: BadgeColor; fixed?: boolean; tooltip?: string }
  selected?: boolean
  mockState?: string
  /** Dashed warning border for the edge-free dead-letter node. */
  variant?: 'default' | 'dead-letter'
  /** `error`: the node represents something that is down (a flow whose runtime failed): error border and icon. */
  tone?: 'error'
  /** Overrides the mock-state badge (e.g. dead letter "receives this drop"). */
  mockBadge?: { text: string; color: BadgeColor }
  children?: ReactNode
}

/** Fixed-width flow node surface shared by ingress, stage, and sink nodes.
 * Selection and mock playback draw an outline (box-shadow), so the node's
 * geometry never changes while traffic or playback state updates. */
export function NodeCard({ icon, title, badge, selected, mockState, variant = 'default', tone, mockBadge, children }: NodeCardProps) {
  const styles = useStyles2(getStyles)
  const outline = mockState ? MOCK_STATE_OUTLINE[mockState] : undefined
  const state = mockBadge ?? (mockState ? { text: mockStateLabel(mockState), color: MOCK_STATE_BADGE_COLOR[mockState] ?? 'darkgrey' } : undefined)
  return <div className={cx(styles.card, variant === 'dead-letter' && styles.deadLetter, tone === 'error' && styles.error, outline ? styles.outline[outline] : selected && styles.selected, mockState === 'active' && styles.pulse)}>
    <div className={styles.header}>
      <Icon name={icon} className={tone === 'error' ? styles.errorIcon : variant === 'dead-letter' ? styles.warningIcon : styles.icon} />
      <span className={styles.title}>{title}</span>
      {badge && <Badge text={badge.text} color={badge.color} tooltip={badge.tooltip} className={cx(styles.badge, badge.fixed && styles.badgeFixed)} />}
    </div>
    {children}
    {state && <NodeSection><Badge text={state.text} color={state.color} /></NodeSection>}
  </div>
}

/** A bordered section within a node (stats, mock state). */
export function NodeSection({ children }: { children: ReactNode }) {
  const styles = useStyles2(getStyles)
  return <div className={styles.section}>{children}</div>
}

/** Secondary single-line text; long values truncate with a title tooltip. */
export function NodeDetail({ children, title, mono }: { children: ReactNode; title?: string; mono?: boolean }) {
  const styles = useStyles2(getStyles)
  return <div className={cx(styles.detail, mono && styles.mono)} title={title}>{children}</div>
}

export function NodeBadges({ children }: { children: ReactNode }) {
  const styles = useStyles2(getStyles)
  return <div className={styles.badges}>{children}</div>
}

const pulse = keyframes({ '50%': { opacity: 0.6 } })
const getStyles = (theme: GrafanaTheme2) => {
  const ring = (color: string) => css({ boxShadow: `0 0 0 2px ${color}` })
  return {
    card: css({
      width: 224, padding: theme.spacing(1.5), background: theme.colors.background.primary,
      border: `1px solid ${theme.colors.border.weak}`, borderRadius: theme.shape.radius.default,
      color: theme.colors.text.primary, fontSize: theme.typography.bodySmall.fontSize,
    }),
    deadLetter: css({ borderStyle: 'dashed', borderColor: theme.colors.warning.border }),
    // A down flow: the same error border the Alert and the Problems tab use.
    error: css({ borderColor: theme.colors.error.border }),
    errorIcon: css({ color: theme.colors.error.text, flexShrink: 0 }),
    selected: ring(theme.colors.primary.main),
    outline: { warning: ring(theme.colors.warning.main), success: ring(theme.colors.success.main), error: ring(theme.colors.error.main) },
    pulse: css({ animation: `${pulse} 1.2s ease-in-out infinite` }),
    header: css({ display: 'flex', alignItems: 'center', gap: theme.spacing(1), marginBottom: theme.spacing(1) }),
    icon: css({ color: theme.colors.primary.text, flexShrink: 0 }),
    warningIcon: css({ color: theme.colors.warning.text, flexShrink: 0 }),
    // Titles like "Python Transformer" / «Джерело · Слухання» do not fit on one
    // line next to the icon and kind badge at the fixed card width, so they may
    // take two lines (clamped with an ellipsis beyond that). The card grows in
    // height only; widths and the layout steps in convert.ts are unchanged.
    title: css({
      flex: 1, minWidth: 0, fontWeight: theme.typography.fontWeightMedium, fontSize: theme.typography.body.fontSize, lineHeight: 1.25,
      display: '-webkit-box', WebkitLineClamp: 2, WebkitBoxOrient: 'vertical', overflow: 'hidden', overflowWrap: 'anywhere',
    }),
    badge: css({ marginLeft: 'auto', flexShrink: 0 }),
    badgeFixed: css({ minWidth: 84, justifyContent: 'center' }),
    section: css({ display: 'flex', flexWrap: 'wrap', alignItems: 'center', gap: theme.spacing(0.75), marginTop: theme.spacing(1), paddingTop: theme.spacing(1), borderTop: `1px solid ${theme.colors.border.weak}`, color: theme.colors.text.secondary }),
    detail: css({ color: theme.colors.text.secondary, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', marginBottom: theme.spacing(0.5) }),
    mono: css({ fontFamily: theme.typography.fontFamilyMonospace }),
    badges: css({ display: 'flex', flexWrap: 'wrap', gap: theme.spacing(0.75), marginBottom: theme.spacing(0.5) }),
  }
}
