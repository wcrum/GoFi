import { css, cx } from '@emotion/css'
import type { GrafanaTheme2 } from '@grafana/data'
import { Icon, Text, useStyles2 } from '@grafana/ui'
import { t } from '../../i18n'

export interface WizardStep { id: string; title: string }

/** The wizard's progress: numbered dots with their titles (the tutorial
 * panel's style), done steps clickable to go back. */
export function WizardSteps({ steps, current, onGoTo }: { steps: WizardStep[]; current: number; onGoTo: (step: number) => void }) {
  const styles = useStyles2(getStyles)
  const percent = Math.round(((current - 1) / Math.max(steps.length - 1, 1)) * 100)
  return <nav className={styles.progress} aria-label={t('gofi.function-wizard.steps.label', 'Wizard steps')}>
    <ol className={styles.list}>{steps.map((item, index) => {
      const number = index + 1
      const state = number === current ? 'current' : number < current ? 'done' : 'todo'
      return <li key={item.id} className={styles.item}>
        <button type="button" className={cx(styles.dot, state === 'current' && styles.dotCurrent, state === 'done' && styles.dotDone)} aria-current={state === 'current' ? 'step' : undefined}
          aria-label={t('gofi.function-wizard.steps.step-label', 'Step {{number}}: {{title}}', { number, title: item.title })} disabled={state === 'todo'} onClick={() => onGoTo(number)}>
          {state === 'done' ? <Icon name="check" size="sm" /> : number}
        </button>
        <Text variant="bodySmall" color={state === 'current' ? 'primary' : 'secondary'} weight={state === 'current' ? 'medium' : undefined}>{item.title}</Text>
      </li>
    })}</ol>
    <div className={styles.bar} role="progressbar" aria-valuemin={0} aria-valuemax={100} aria-valuenow={percent} aria-label={t('gofi.function-wizard.steps.progress', 'Wizard progress')}>
      <div className={styles.barFill} style={{ width: `${percent}%` }} />
    </div>
  </nav>
}

const getStyles = (theme: GrafanaTheme2) => ({
  progress: css({ display: 'flex', flexDirection: 'column', gap: theme.spacing(1) }),
  list: css({ display: 'flex', justifyContent: 'space-between', gap: theme.spacing(2), listStyle: 'none', margin: 0, padding: 0, flexWrap: 'wrap' }),
  item: css({ display: 'flex', alignItems: 'center', gap: theme.spacing(1), minWidth: 0 }),
  dot: css({
    width: 28, height: 28, flexShrink: 0, display: 'flex', alignItems: 'center', justifyContent: 'center', borderRadius: theme.shape.radius.circle,
    border: `1px solid ${theme.colors.border.medium}`, background: theme.colors.background.secondary, color: theme.colors.text.secondary,
    fontSize: theme.typography.bodySmall.fontSize, fontWeight: theme.typography.fontWeightMedium, cursor: 'pointer',
    '&:hover:not(:disabled)': { borderColor: theme.colors.primary.border, color: theme.colors.text.primary },
    '&:disabled': { cursor: 'default' },
    '&:focus-visible': { outline: `2px solid ${theme.colors.primary.main}`, outlineOffset: 1 },
  }),
  dotCurrent: css({ background: theme.colors.primary.main, borderColor: theme.colors.primary.main, color: theme.colors.primary.contrastText, '&:hover': { color: theme.colors.primary.contrastText } }),
  dotDone: css({ background: theme.colors.primary.transparent, borderColor: theme.colors.primary.border, color: theme.colors.primary.text }),
  bar: css({ height: 4, borderRadius: theme.shape.radius.pill, background: theme.colors.background.secondary, overflow: 'hidden' }),
  barFill: css({ height: '100%', background: theme.colors.primary.main, [theme.transitions.handleMotion('no-preference')]: { transition: 'width 200ms ease-out' } }),
})
