import '../../lib/monaco'
import { useCallback, useEffect, useRef, useState } from 'react'
import { css } from '@emotion/css'
import type { GrafanaTheme2 } from '@grafana/data'
import { CodeEditor, CodeEditorSuggestionItemKind, Text, useStyles2, type CodeEditorSuggestionItem, type Monaco, type MonacoEditor } from '@grafana/ui'
import { checkExpression, describeEnvironment, type CelEnvironment, type ExpressionDiagnostic, type ResultType } from '../../expression-api'
import { registerCelLanguage } from './celLanguage'

export interface CelEditorProps {
  value: string
  onChange: (value: string) => void
  resultType: ResultType
  disabled?: boolean
  placeholder?: string
  /** Parsed field names offered as fields["name"] completions. */
  fieldNames?: string[]
  /** Fixed height; defaults to fitting the text (1-6 lines). */
  height?: number
  'aria-label'?: string
}

const LINE = 19
const KEYWORDS = ['true', 'false', 'null', 'in']

/** Monaco CEL editor: completions from the server's CEL environment and
 * debounced CheckExpression diagnostics shown as markers. */
export function CelEditor({ value, onChange, resultType, disabled, placeholder, fieldNames, height, 'aria-label': ariaLabel }: CelEditorProps) {
  const styles = useStyles2(getStyles)
  const [env, setEnv] = useState<CelEnvironment | null>(null)
  const [diagnostics, setDiagnostics] = useState<ExpressionDiagnostic[]>([])
  const editor = useRef<MonacoEditor | null>(null)
  const monaco = useRef<Monaco | null>(null)
  const suggestionsRef = useRef<CodeEditorSuggestionItem[]>([])

  useEffect(() => { describeEnvironment().then(setEnv).catch(() => {}) }, [])
  suggestionsRef.current = [
    ...(env?.variables ?? []).map(v => ({ label: v.name, kind: CodeEditorSuggestionItemKind.Field, detail: v.type, documentation: v.description })),
    ...(fieldNames ?? []).map(name => ({ label: `fields["${name}"]`, insertText: `fields["${name}"]`, kind: CodeEditorSuggestionItemKind.Property, detail: 'parsed field' })),
    ...(env?.functions ?? []).map(f => ({ label: f.name, insertText: f.name, kind: CodeEditorSuggestionItemKind.Method, detail: f.signatures?.[0], documentation: [f.description, ...(f.signatures ?? [])].filter(Boolean).join('\n') })),
    ...KEYWORDS.map(k => ({ label: k, kind: CodeEditorSuggestionItemKind.Constant })),
  ]
  const getSuggestions = useCallback(() => suggestionsRef.current, [])

  // Debounced server-side check against the runtime CEL environment.
  useEffect(() => {
    if (!value.trim()) { setDiagnostics([]); return }
    let active = true
    const timer = window.setTimeout(() => {
      checkExpression(value, resultType).then(result => { if (active) setDiagnostics(result.diagnostics) }).catch(() => {})
    }, 400)
    return () => { active = false; window.clearTimeout(timer) }
  }, [value, resultType])

  const applyMarkers = useCallback(() => {
    const model = editor.current?.getModel()
    if (!model || !monaco.current) return
    monaco.current.editor.setModelMarkers(model, 'gofi-cel', diagnostics.map(d => ({
      startLineNumber: d.line, endLineNumber: d.line, startColumn: d.column, endColumn: d.column + 1,
      severity: monaco.current!.MarkerSeverity.Error, message: d.message,
    })))
  }, [diagnostics])
  useEffect(applyMarkers, [applyMarkers])

  const lines = Math.min(Math.max(value.split('\n').length, 1), 6)
  return <div className={styles.wrapper}>
    <div className={styles.editor} aria-invalid={diagnostics.length > 0 || undefined}>
      <CodeEditor
        language="cel"
        value={value}
        onChange={onChange}
        onBlur={onChange}
        readOnly={disabled}
        height={height ?? lines * LINE + 10}
        width="100%"
        showLineNumbers={false}
        showMiniMap={false}
        wordWrap
        getSuggestions={getSuggestions}
        onBeforeEditorMount={registerCelLanguage}
        onEditorDidMount={(instance, api) => { editor.current = instance; monaco.current = api; applyMarkers() }}
        onEditorWillUnmount={() => { editor.current = null; monaco.current = null }}
        monacoOptions={{
          ariaLabel: ariaLabel ?? 'CEL expression', fontSize: 13, scrollBeyondLastLine: false, automaticLayout: true,
          glyphMargin: false, folding: false, lineDecorationsWidth: 6, renderLineHighlight: 'none', overviewRulerLanes: 0,
          scrollbar: { vertical: 'auto', horizontal: 'hidden', alwaysConsumeMouseWheel: false },
          padding: { top: 4, bottom: 4 }, fixedOverflowWidgets: true,
        }}
      />
      {!value && placeholder && <span className={styles.placeholder} aria-hidden>{placeholder}</span>}
    </div>
    {diagnostics.length > 0 && <div role="status" className={styles.diagnostic}><Text color="error" variant="bodySmall">{diagnostics[0].line}:{diagnostics[0].column} {diagnostics[0].message}</Text></div>}
  </div>
}

const getStyles = (theme: GrafanaTheme2) => ({
  wrapper: css({ minWidth: 0 }),
  editor: css({
    position: 'relative', border: `1px solid ${theme.components.input.borderColor}`, borderRadius: theme.shape.radius.default,
    background: theme.components.input.background, overflow: 'hidden',
    '&:focus-within': { borderColor: theme.colors.primary.border },
    '&[aria-invalid="true"]': { borderColor: theme.colors.error.border },
    // The CodeEditor container draws its own border; the wrapper owns it.
    '& > div': { border: 'none' },
  }),
  placeholder: css({ position: 'absolute', top: 5, left: 9, pointerEvents: 'none', color: theme.colors.text.disabled, fontFamily: theme.typography.fontFamilyMonospace, fontSize: 13 }),
  diagnostic: css({ marginTop: theme.spacing(0.5) }),
})
