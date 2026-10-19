import { useEffect, useMemo, useState, type CSSProperties } from 'react'
import { css, cx } from '@emotion/css'
import type { GrafanaTheme2 } from '@grafana/data'
import { useStyles2 } from '@grafana/ui'
import { getEditorTheme, subscribeEditorTheme } from '../theme/editorTheme'
import { t } from '../i18n'

type Colorize = (source: string, language: string) => Promise<string>
let loader: Promise<Colorize> | undefined

/** Loads Monaco once, on first use, so the chunk stays lazy like the editors.
 * `lib/monaco` defines and applies the app's editor theme before anything is
 * colorized; colorize() emits `mtk*` spans styled by the active theme. */
function colorizer(): Promise<Colorize> {
  if (!loader) {
    loader = Promise.all([import('../lib/monaco'), import('monaco-editor')])
      .then(([, monaco]) => (source: string, language: string) => monaco.editor.colorize(source, language, { tabSize: 4 }))
  }
  return loader
}

export const splitLines = (source: string): string[] => source.split('\n')

/** Splits colorize() markup (one `<br/>` after every line) into lines. The
 * renderer emits no-break spaces; plain spaces let long lines wrap at words
 * under `white-space: pre-wrap`, as the editor's word wrap does. */
export function splitMarkup(markup: string, lineCount: number): string[] {
  const lines = markup.replace(/\u00a0/g, ' ').split('<br/>')
  if (lines.length === lineCount + 1 && lines[lines.length - 1] === '') lines.pop()
  return lines
}

interface Props {
  source: string
  language?: string
  ariaLabel?: string
  /** Scrolls vertically past this height; never horizontally (long lines wrap). */
  maxHeight?: number | string
  className?: string
}

/** A static, read-only code block: syntax colors and line numbers in the
 * editor's metrics (13px / 19px), but no cursor, focus, or selection. */
export function CodeView({ source, language = 'python', ariaLabel, maxHeight, className }: Props) {
  const styles = useStyles2(getStyles)
  const plain = useMemo(() => splitLines(source), [source])
  const [highlighted, setHighlighted] = useState<{ source: string; language: string; lines: string[] } | null>(null)
  const [themeVersion, setThemeVersion] = useState(0)
  useEffect(() => subscribeEditorTheme(() => setThemeVersion(version => version + 1)), [])
  useEffect(() => {
    let cancelled = false
    colorizer().then(colorize => colorize(source, language))
      .then(markup => { if (!cancelled) setHighlighted({ source, language, lines: splitMarkup(markup, plain.length) }) })
      .catch(() => { /* the plain text already shown stays */ })
    return () => { cancelled = true }
  }, [source, language, plain.length, themeVersion])
  const colored = highlighted && highlighted.source === source && highlighted.language === language ? highlighted.lines : null
  const lines = colored ?? plain
  const style = { maxHeight, '--gofi-code-gutter': `${Math.max(2, String(lines.length).length)}ch` } as CSSProperties
  return <pre className={cx(styles.view, className)} style={style} aria-label={ariaLabel ?? t('gofi.components.code-view.aria-label', 'Source code')}>
    {lines.map((line, index) => <div key={index} className={styles.line}>
      <span className={styles.number} aria-hidden>{index + 1}</span>
      {colored ? <span className={styles.code} dangerouslySetInnerHTML={{ __html: line }} /> : <span className={styles.code}>{line}</span>}
    </div>)}
  </pre>
}

const getStyles = (theme: GrafanaTheme2) => {
  const editor = getEditorTheme()?.colors ?? {}
  return {
    view: css({
      margin: 0, padding: theme.spacing(0.5, 0), minHeight: theme.spacing(4.5),
      background: editor['editor.background'] ?? theme.components.input.background,
      color: editor['editor.foreground'] ?? theme.colors.text.primary,
      border: `1px solid ${theme.colors.border.medium}`, borderRadius: theme.shape.radius.default,
      overflowX: 'hidden', overflowY: 'auto',
      fontFamily: theme.typography.fontFamilyMonospace, fontSize: 13, lineHeight: '19px', tabSize: 4,
      whiteSpace: 'pre-wrap', userSelect: 'none', cursor: 'default',
    }),
    line: css({ display: 'flex', alignItems: 'flex-start' }),
    number: css({
      flex: '0 0 auto', width: `calc(var(--gofi-code-gutter) + ${theme.spacing(5)})`, paddingRight: theme.spacing(2), textAlign: 'right',
      color: editor['editorLineNumber.foreground'] ?? theme.colors.text.disabled, fontVariantNumeric: 'tabular-nums',
    }),
    code: css({ flex: '1 1 auto', minWidth: 0, paddingLeft: theme.spacing(2), paddingRight: theme.spacing(1), whiteSpace: 'pre-wrap', overflowWrap: 'anywhere' }),
  }
}
