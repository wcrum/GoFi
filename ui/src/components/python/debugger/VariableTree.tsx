import { useState } from 'react'
import { css, cx } from '@emotion/css'
import type { GrafanaTheme2 } from '@grafana/data'
import { Icon, Text, Tooltip, useStyles2 } from '@grafana/ui'
import type { DebugVariable } from './debug-api'
import { t } from '../../../i18n'

/** Expandable name / type / repr tree; values changed at this step are marked. */
export function VariableTree({ variables, empty }: { variables: DebugVariable[]; empty?: string }) {
  const styles = useStyles2(getStyles)
  if (!variables.length) return <div className={styles.empty}><Text color="secondary" italic variant="bodySmall">{empty ?? t('gofi.python.debugger.no-variables', 'No variables.')}</Text></div>
  return <ul className={styles.tree} role="tree">{variables.map(variable => <VariableNode key={variable.name} variable={variable} depth={0} />)}</ul>
}

function VariableNode({ variable, depth }: { variable: DebugVariable; depth: number }) {
  const styles = useStyles2(getStyles)
  const [open, setOpen] = useState(false)
  const expandable = variable.children.length > 0
  return <li role="treeitem" aria-expanded={expandable ? open : undefined}>
    <div className={cx(styles.row, variable.changed && styles.changed)} style={{ paddingLeft: 4 + depth * 14 }} onClick={() => expandable && setOpen(value => !value)}>
      <span className={styles.toggle}>{expandable && <Icon name={open ? 'angle-down' : 'angle-right'} size="sm" />}</span>
      <span className={styles.name}>{variable.name}</span>
      <span className={styles.type}>{variable.type}</span>
      <Tooltip content={<pre className={styles.tooltip}>{variable.repr}</pre>} placement="left">
        <span className={styles.repr}>{variable.repr}{variable.truncated ? ' …' : ''}</span>
      </Tooltip>
      {variable.changed && <span className={styles.badge} aria-label={t('gofi.python.debugger.changed', 'changed')}>{t('gofi.python.debugger.changed', 'changed')}</span>}
    </div>
    {open && <ul className={styles.tree}>{variable.children.map((child, index) => <VariableNode key={`${child.name}-${index}`} variable={child} depth={depth + 1} />)}</ul>}
  </li>
}

const getStyles = (theme: GrafanaTheme2) => ({
  tree: css({ listStyle: 'none', margin: 0, padding: 0, fontFamily: theme.typography.fontFamilyMonospace, fontSize: theme.typography.bodySmall.fontSize }),
  row: css({ display: 'grid', gridTemplateColumns: '14px minmax(60px, max-content) minmax(40px, max-content) minmax(0, 1fr) auto', alignItems: 'baseline', gap: theme.spacing(1), padding: theme.spacing(0.25, 1, 0.25, 0.5), borderBottom: `1px solid ${theme.colors.border.weak}`, cursor: 'default' }),
  changed: css({ background: theme.colors.warning.transparent }),
  toggle: css({ color: theme.colors.text.secondary, cursor: 'pointer' }),
  name: css({ color: theme.colors.text.primary, fontWeight: theme.typography.fontWeightMedium }),
  type: css({ color: theme.colors.text.secondary }),
  repr: css({ color: theme.colors.primary.text, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', minWidth: 0 }),
  badge: css({ color: theme.colors.warning.text, fontSize: theme.typography.size.xs }),
  tooltip: css({ margin: 0, maxWidth: 480, maxHeight: 300, overflow: 'auto', whiteSpace: 'pre-wrap', wordBreak: 'break-word' }),
  empty: css({ padding: theme.spacing(1) }),
})
