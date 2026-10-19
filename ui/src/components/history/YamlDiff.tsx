import { useEffect, useRef } from 'react'
import { css } from '@emotion/css'
import type { GrafanaTheme2 } from '@grafana/data'
import { useStyles2 } from '@grafana/ui'
import * as monaco from 'monaco-editor'
import '../../lib/monaco' // applies the app's editor theme to every Monaco editor

/** Read-only Monaco diff of two YAML documents (original on the left). */
export function YamlDiff({ original, modified, sideBySide = true }: { original: string; modified: string; sideBySide?: boolean }) {
  const styles = useStyles2(getStyles)
  const host = useRef<HTMLDivElement>(null)
  const editor = useRef<monaco.editor.IStandaloneDiffEditor | null>(null)

  useEffect(() => {
    if (!host.current) return
    editor.current = monaco.editor.createDiffEditor(host.current, {
      readOnly: true, originalEditable: false, automaticLayout: true, renderSideBySide: sideBySide,
      minimap: { enabled: false }, scrollBeyondLastLine: false, fontSize: 13, renderOverviewRuler: false,
      ignoreTrimWhitespace: false,
    })
    return () => { editor.current?.dispose(); editor.current = null }
    // Created once; options and models update below.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  useEffect(() => { editor.current?.updateOptions({ renderSideBySide: sideBySide }) }, [sideBySide])

  useEffect(() => {
    const target = editor.current
    if (!target) return
    const models = { original: monaco.editor.createModel(original, 'yaml'), modified: monaco.editor.createModel(modified, 'yaml') }
    target.setModel(models)
    return () => {
      if (editor.current === target) target.setModel(null)
      models.original.dispose()
      models.modified.dispose()
    }
  }, [original, modified])

  return <div ref={host} className={styles.editor} data-testid="yaml-diff" />
}

const getStyles = (theme: GrafanaTheme2) => ({
  editor: css({ flex: 1, minHeight: 240, border: `1px solid ${theme.colors.border.weak}`, borderRadius: theme.shape.radius.default, overflow: 'hidden' }),
})
