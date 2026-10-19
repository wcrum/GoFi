import { useMemo } from 'react'
import { css, cx } from '@emotion/css'
import type { GrafanaTheme2 } from '@grafana/data'
import { useStyles2 } from '@grafana/ui'
import { useThemeDefinition } from '../../theme/ThemeProvider'
import type { SyntaxColors } from '../../theme/types'
import { tokenizeCel, type CelTokenKind } from './cel-tokens'

// Monaco's vs-dark / vs token colors, for Grafana's stock themes, which leave
// syntax to Monaco's built-in rules.
const VS_DARK: SyntaxColors = { comment: '#6a9955', keyword: '#569cd6', string: '#ce9178', number: '#b5cea8', type: '#4ec9b0', function: '#dcdcaa', variable: '#9cdcfe', constant: '#4fc1ff', operator: '#d4d4d4', delimiter: '#d4d4d4', tag: '#569cd6', attribute: '#9cdcfe', regexp: '#d16969' }
const VS_LIGHT: SyntaxColors = { comment: '#008000', keyword: '#0000ff', string: '#a31515', number: '#098658', type: '#267f99', function: '#795e26', variable: '#001080', constant: '#0070c1', operator: '#000000', delimiter: '#000000', tag: '#800000', attribute: '#e50000', regexp: '#811f3f' }

const COLOR: Record<CelTokenKind, keyof SyntaxColors | null> = {
  comment: 'comment', keyword: 'keyword', variable: 'variable', function: 'function', identifier: null,
  string: 'string', number: 'number', operator: 'operator', delimiter: 'delimiter', text: null,
}

/** A CEL expression rendered read-only with the current theme's editor
 * syntax colors, for tables and summaries outside a code editor. */
export function CelCode({ expression, className, title }: { expression: string; className?: string; title?: string }) {
  const styles = useStyles2(getStyles)
  const definition = useThemeDefinition()
  const syntax = definition.syntax ?? (definition.mode === 'dark' ? VS_DARK : VS_LIGHT)
  const tokens = useMemo(() => tokenizeCel(expression), [expression])
  return <code className={cx(styles.code, className)} title={title}>
    {tokens.map((token, index) => {
      const key = COLOR[token.kind]
      return key ? <span key={index} style={{ color: syntax[key], fontStyle: token.kind === 'comment' ? 'italic' : undefined }}>{token.text}</span> : token.text
    })}
  </code>
}

const getStyles = (theme: GrafanaTheme2) => ({
  code: css({ fontFamily: theme.typography.fontFamilyMonospace, color: theme.colors.text.primary }),
})
