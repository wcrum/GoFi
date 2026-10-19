import type { ReactNode } from 'react'
import { css } from '@emotion/css'
import type { GrafanaTheme2 } from '@grafana/data'
import { useStyles2 } from '@grafana/ui'

/** Side-by-side form fields in equal columns; narrow inspectors wrap to
 * fewer columns. Each Field is a subgrid of two rows (label, control) shared
 * across the row, so the inputs line up whatever the neighbours carry: a
 * description, a wrapped label, or a validation message under the control.
 * Children must be `Field`s with a label around an input, textarea, or
 * select (a Field around a RadioButtonGroup renders a fieldset whose legend
 * leaves the grid). */
export function FieldRow({ children, min = 140 }: { children: ReactNode; min?: number }) {
  const styles = useStyles2(getStyles)
  return <div className={styles.row} style={{ gridTemplateColumns: `repeat(auto-fit, minmax(${min}px, 1fr))` }}>{children}</div>
}

const getStyles = (theme: GrafanaTheme2) => ({
  row: css({
    label: 'FieldRow',
    display: 'grid', columnGap: theme.spacing(2), rowGap: 0,
    // Wrapped columns keep the form's rhythm: each field carries the gap below
    // itself (a row gap would also open between a label and its control), and
    // the row gives the last one back.
    marginBottom: theme.spacing(-2),
    '& > div, & > fieldset': { minWidth: 0, display: 'grid', gridTemplateRows: 'subgrid', gridRow: 'span 2', paddingBottom: theme.spacing(2) },
    // The label sits right above its control; the control row aligns at the top.
    '& > div > :first-child': { alignSelf: 'end' },
    // Inputs fill their column instead of keeping fixed widths.
    '& input': { width: '100%' },
  }),
})
