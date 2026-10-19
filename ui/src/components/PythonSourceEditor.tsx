import { useCallback, useEffect, useRef } from 'react'
import { css, cx } from '@emotion/css'
import type { GrafanaTheme2 } from '@grafana/data'
import { CodeEditor, useStyles2, type Monaco, type MonacoEditor } from '@grafana/ui'
import '../lib/monaco'
import type { SourceDiagnostic } from '../sandbox-api'
import { lineRanges, type SourceHighlights } from './canvas/python-templates'
import { t } from '../i18n'

export type { SourceHighlights }
export interface RevealRequest { line: number; column?: number; nonce: number }
interface Props {
  source: string
  onChange: (source: string) => void
  diagnostics: SourceDiagnostic[]
  disabled?: boolean
  height?: string
  /** Moves the cursor to a line; change `nonce` to repeat the same line. */
  reveal?: RevealRequest | null
  ariaLabel?: string
  /** Entry line (where the input enters), executed lines, and error line. */
  highlights?: SourceHighlights
  /** Debugger: the line about to execute, breakpoints, and glyph-margin toggling. */
  debug?: { currentLine?: number; breakpoints?: number[]; onToggleBreakpoint?: (line: number) => void }
  /** Result of the last validation of this exact source: the frame turns
   * green or red, like a validated form field. */
  validity?: 'valid' | 'invalid'
}

export default function PythonSourceEditor({ source, onChange, diagnostics, disabled, height = '420px', reveal, ariaLabel, highlights, debug, validity }: Props) {
  const styles = useStyles2(getStyles)
  const editorLabel = ariaLabel ?? t('gofi.components.python-source-editor.aria-label', 'Python source')
  const editor = useRef<MonacoEditor | null>(null)
  const monaco = useRef<Monaco | null>(null)
  const decorationIds = useRef<string[]>([])
  const toggleBreakpoint = useRef(debug?.onToggleBreakpoint)
  toggleBreakpoint.current = debug?.onToggleBreakpoint
  const decorate = useCallback(() => {
    const instance = editor.current
    const model = instance?.getModel()
    if (!instance || !model) return
    const lastLine = model.getLineCount()
    const valid = (line?: number) => !!line && line >= 1 && line <= lastLine
    const next: Parameters<MonacoEditor['deltaDecorations']>[1] = []
    for (const [start, end] of lineRanges(highlights?.executed ?? [])) {
      if (!valid(start)) continue
      next.push({ range: { startLineNumber: start, startColumn: 1, endLineNumber: Math.min(end, lastLine), endColumn: 1 }, options: { isWholeLine: true, className: styles.executedLine, linesDecorationsClassName: styles.executedGutter, hoverMessage: { value: t('gofi.components.python-source-editor.executed-for-input', 'Executed for this input') } } })
    }
    const entry = highlights?.entry
    if (entry && valid(entry.line)) {
      const message = { value: entry.preview
        ? t('gofi.components.python-source-editor.entry-hover-with-input', '**Input enters here:** `{{label}}`\n\nInput: `{{preview}}`', { label: entry.label, preview: entry.preview })
        : t('gofi.components.python-source-editor.entry-hover', '**Input enters here:** `{{label}}`', { label: entry.label }) }
      next.push({ range: { startLineNumber: entry.line, startColumn: 1, endLineNumber: entry.line, endColumn: 1 }, options: {
        isWholeLine: true, className: styles.entryLine, glyphMarginClassName: styles.entryGlyph, glyphMarginHoverMessage: message, hoverMessage: message,
        stickiness: 1, ...(entry.preview ? { after: { content: t('gofi.components.python-source-editor.entry-inline-preview', '  ← input: {{preview}}', { preview: entry.preview }), inlineClassName: styles.entryPreview } } : {}),
      } })
    }
    if (valid(highlights?.error)) {
      next.push({ range: { startLineNumber: highlights!.error!, startColumn: 1, endLineNumber: highlights!.error!, endColumn: 1 }, options: { isWholeLine: true, className: styles.errorLine, glyphMarginClassName: styles.errorGlyph, glyphMarginHoverMessage: { value: t('gofi.components.python-source-editor.raised-here', 'Raised here') } } })
    }
    for (const line of debug?.breakpoints ?? []) {
      if (valid(line)) next.push({ range: { startLineNumber: line, startColumn: 1, endLineNumber: line, endColumn: 1 }, options: { glyphMarginClassName: styles.breakpointGlyph, glyphMarginHoverMessage: { value: t('gofi.components.python-source-editor.breakpoint', 'Breakpoint (click to remove)') } } })
    }
    if (valid(debug?.currentLine)) {
      next.push({ range: { startLineNumber: debug!.currentLine!, startColumn: 1, endLineNumber: debug!.currentLine!, endColumn: 1 }, options: { isWholeLine: true, className: styles.currentLine, glyphMarginClassName: styles.currentGlyph, glyphMarginHoverMessage: { value: t('gofi.components.python-source-editor.current-step', 'Current step') }, zIndex: 10 } })
    }
    decorationIds.current = instance.deltaDecorations(decorationIds.current, next)
  }, [highlights, debug?.breakpoints, debug?.currentLine, styles]) // eslint-disable-line react-hooks/exhaustive-deps
  useEffect(() => {
    if (!debug?.currentLine || !editor.current) return
    editor.current.revealLineInCenterIfOutsideViewport(debug.currentLine)
  }, [debug?.currentLine])
  useEffect(decorate, [decorate, source])
  const markers = useCallback(() => {
    const model = editor.current?.getModel()
    if (!model || !monaco.current) return
    monaco.current.editor.setModelMarkers(model, 'gofi-python', diagnostics.map(diagnostic => ({
      startLineNumber: Math.max(1, diagnostic.line), endLineNumber: Math.max(1, diagnostic.line), startColumn: Math.max(1, diagnostic.column), endColumn: Math.max(2, diagnostic.column + 1),
      severity: monaco.current!.MarkerSeverity.Error, message: `${diagnostic.code}: ${diagnostic.message}`, code: diagnostic.code,
    })))
  }, [diagnostics])
  useEffect(markers, [markers])
  useEffect(() => {
    if (!reveal || !editor.current) return
    const position = { lineNumber: Math.max(1, reveal.line), column: Math.max(1, reveal.column ?? 1) }
    editor.current.revealLineInCenter(position.lineNumber)
    editor.current.setPosition(position)
    editor.current.focus()
  }, [reveal])
  return <div className={cx(styles.frame, validity === 'valid' && styles.frameValid, validity === 'invalid' && styles.frameInvalid)}>
    <CodeEditor language="python" value={source} onChange={onChange} height={height} width="100%" showLineNumbers showMiniMap={false} readOnly={disabled} wordWrap
      monacoOptions={{ tabSize: 4, insertSpaces: true, fontSize: 13, scrollBeyondLastLine: false, automaticLayout: true, ariaLabel: editorLabel, glyphMargin: true, renderValidationDecorations: 'on' }}
      onEditorDidMount={(instance, api) => {
        editor.current = instance; monaco.current = api; decorationIds.current = []; markers(); decorate()
        // Breakpoints toggle from the glyph margin or line numbers, even read-only.
        instance.onMouseDown(event => {
          const type = event.target.type
          const line = event.target.position?.lineNumber
          if (line && toggleBreakpoint.current && (type === api.editor.MouseTargetType.GUTTER_GLYPH_MARGIN || type === api.editor.MouseTargetType.GUTTER_LINE_NUMBERS)) toggleBreakpoint.current(line)
        })
      }}
      onEditorWillUnmount={() => { editor.current = null; monaco.current = null; decorationIds.current = [] }} />
  </div>
}

const glyph = (content: string, color: string) => css({ '&::before': { content: `"${content}"`, display: 'flex', alignItems: 'center', justifyContent: 'center', height: '100%', color, fontSize: 12, fontWeight: 700 } })
const getStyles = (theme: GrafanaTheme2) => ({
  frame: css({ border: `1px solid ${theme.colors.border.medium}`, borderRadius: theme.shape.radius.default, overflow: 'hidden', transition: 'border-color 150ms, box-shadow 150ms' }),
  frameValid: css({ borderColor: theme.colors.success.border, boxShadow: `0 0 0 1px ${theme.colors.success.border}` }),
  frameInvalid: css({ borderColor: theme.colors.error.border, boxShadow: `0 0 0 1px ${theme.colors.error.border}` }),
  entryLine: css({ background: theme.colors.primary.transparent }),
  entryGlyph: glyph('▶', theme.colors.primary.text),
  entryPreview: css({ color: theme.colors.primary.text, fontStyle: 'italic', opacity: 0.85 }),
  executedLine: css({ background: theme.colors.success.transparent }),
  executedGutter: css({ background: theme.colors.success.main, width: '3px !important', marginLeft: 3 }),
  errorLine: css({ background: theme.colors.error.transparent }),
  errorGlyph: glyph('✕', theme.colors.error.text),
  breakpointGlyph: glyph('●', theme.colors.error.main),
  currentLine: css({ background: theme.colors.warning.transparent, outline: `1px solid ${theme.colors.warning.border}`, outlineOffset: -1 }),
  currentGlyph: glyph('➜', theme.colors.warning.text),
})
