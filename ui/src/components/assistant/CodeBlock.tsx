import { useEffect, useState } from 'react'
import { css, cx } from '@emotion/css'
import type { GrafanaTheme2 } from '@grafana/data'
import { IconButton, useStyles2 } from '@grafana/ui'
import { CodeView } from '../CodeView'
import { registerCelLanguage } from '../cel/celLanguage'
import { t } from '../../i18n'

interface Props {
  source: string
  /** A Monaco language id (see helpers.codeLanguage); CEL is registered on first use. */
  language: string
  /** Shown top-right as a small caption (a field name, a fence language). */
  label?: string
  maxHeight?: number | string
  tone?: 'default' | 'error'
  className?: string
}

let celReady: Promise<void> | undefined
/** CodeView colorizes through Monaco, which knows CEL only after the CEL
 * editor's registration; run it before the first CEL block. */
function ensureCel(): Promise<void> {
  if (!celReady) celReady = import('monaco-editor').then(monaco => { registerCelLanguage(monaco) }).catch(() => undefined)
  return celReady
}

/** A read-only, syntax-coloured block (CodeView) with a copy button. */
export function CodeBlock({ source, language, label, maxHeight = 320, tone = 'default', className }: Props) {
  const styles = useStyles2(getStyles)
  const [copied, setCopied] = useState(false)
  const [celRegistered, setCelRegistered] = useState(language !== 'cel')
  useEffect(() => { if (language === 'cel') { let cancelled = false; void ensureCel().then(() => { if (!cancelled) setCelRegistered(true) }); return () => { cancelled = true } } }, [language])
  useEffect(() => { if (!copied) return; const timer = setTimeout(() => setCopied(false), 1500); return () => clearTimeout(timer) }, [copied])
  const copy = () => { navigator.clipboard?.writeText(source).then(() => setCopied(true)).catch(() => undefined) }
  return <div className={cx(styles.block, tone === 'error' && styles.error, className)}>
    <div className={styles.toolbar}>
      {label && <span className={styles.label}>{label}</span>}
      <IconButton name={copied ? 'check' : 'copy'} size="sm" tooltip={copied ? t('gofi.assistant.code-block.copied', 'Copied') : t('gofi.assistant.code-block.copy', 'Copy')} aria-label={t('gofi.assistant.code-block.copy', 'Copy')} onClick={copy} />
    </div>
    <CodeView source={source} language={celRegistered ? language : 'plaintext'} maxHeight={maxHeight} className={styles.view} />
  </div>
}

const getStyles = (theme: GrafanaTheme2) => ({
  block: css({ position: 'relative', minWidth: 0 }),
  error: css({ '& pre': { borderColor: theme.colors.error.border } }),
  toolbar: css({
    position: 'absolute', top: 2, right: 2, zIndex: 1, display: 'flex', alignItems: 'center', gap: theme.spacing(0.5), padding: theme.spacing(0, 0, 0, 0.5),
    background: theme.colors.background.secondary, borderRadius: theme.shape.radius.default, opacity: 0.85, '&:hover': { opacity: 1 },
  }),
  label: css({ fontSize: theme.typography.bodySmall.fontSize, color: theme.colors.text.secondary, fontFamily: theme.typography.fontFamilyMonospace }),
  // Room on the right for the toolbar; selectable so text can also be copied by hand.
  view: css({ paddingRight: theme.spacing(5), userSelect: 'text', cursor: 'text' }),
})
