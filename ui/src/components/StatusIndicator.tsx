import { css, cx } from '@emotion/css'
import type { GrafanaTheme2 } from '@grafana/data'
import { Tooltip, useStyles2 } from '@grafana/ui'

export type StatusTone = 'success' | 'warning' | 'error' | 'info' | 'neutral'

/** State shown beside controls (Unsaved, Running, Streaming…). A dot and a
 * label at the same height as a medium Button, so control rows line up;
 * use Badge only for tags attached to items, not for state next to actions. */
export function StatusIndicator({ tone, text, tooltip, pulse, hideTextNarrow }: { tone: StatusTone; text: string; tooltip?: string; pulse?: boolean; /** Below the md breakpoint show only the dot (the text stays the accessible name). */ hideTextNarrow?: boolean }) {
  const styles = useStyles2(getStyles)
  const indicator = <span className={styles.root} role="status" aria-label={hideTextNarrow ? text : undefined}>
    <span className={cx(styles.dot, styles[tone], pulse && styles.pulse, pulse && styles[`${tone}Color`])} aria-hidden="true" />
    <span className={cx(hideTextNarrow && styles.textNarrow)}>{text}</span>
  </span>
  return tooltip ? <Tooltip content={tooltip}>{indicator}</Tooltip> : indicator
}

const getStyles = (theme: GrafanaTheme2) => ({
  root: css({
    display: 'inline-flex', alignItems: 'center', gap: theme.spacing(0.75), height: theme.spacing(theme.components.height.md),
    fontSize: theme.typography.bodySmall.fontSize, color: theme.colors.text.secondary, whiteSpace: 'nowrap',
  }),
  dot: css({ width: 8, height: 8, borderRadius: theme.shape.radius.circle, flexShrink: 0 }),
  textNarrow: css({ [theme.breakpoints.down('md')]: { display: 'none' } }),
  success: css({ background: theme.colors.success.main }),
  warning: css({ background: theme.colors.warning.main }),
  error: css({ background: theme.colors.error.main }),
  info: css({ background: theme.colors.primary.main }),
  neutral: css({ background: theme.colors.text.disabled }),
  // A soft halo in the tone's color; it breathes unless reduced motion is set.
  pulse: css({
    boxShadow: '0 0 0 3px color-mix(in srgb, currentColor 22%, transparent)',
    '@media (prefers-reduced-motion: no-preference)': { animation: 'gofi-status-pulse 1.6s ease-in-out infinite' },
    '@keyframes gofi-status-pulse': { '50%': { boxShadow: '0 0 0 5px color-mix(in srgb, currentColor 8%, transparent)' } },
  }),
  successColor: css({ color: theme.colors.success.main }),
  warningColor: css({ color: theme.colors.warning.main }),
  errorColor: css({ color: theme.colors.error.main }),
  infoColor: css({ color: theme.colors.primary.main }),
  neutralColor: css({ color: theme.colors.text.disabled }),
})
