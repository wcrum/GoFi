import * as monaco from 'monaco-editor'
import EditorWorker from 'monaco-editor/esm/vs/editor/editor.worker?worker'
import JsonWorker from 'monaco-editor/esm/vs/language/json/json.worker?worker'
import { getEditorTheme, subscribeEditorTheme } from '../theme/editorTheme'

// Bundle Monaco's workers locally for disconnected use. JSON resource views
// need the JSON language worker; every other language uses the base worker.
Object.assign(globalThis, { MonacoEnvironment: { getWorker: (_id: string, label: string) => label === 'json' ? new JsonWorker() : new EditorWorker() } })

// Grafana's editors define 'grafana-dark' / 'grafana-light' on mount and on
// every theme change, with only a background color. Substitute the app
// theme's full editor theme whenever those names are defined. @grafana/ui
// imports the same 'monaco-editor' module, and monaco.editor is a plain
// (mutable) object, so this wrapper sees Grafana's calls too.
const GRAFANA_THEMES = { dark: 'grafana-dark', light: 'grafana-light' } as const
const PATCHED = Symbol.for('gofi.monaco.defineTheme')

type EditorApi = typeof monaco.editor & { [PATCHED]?: true }
const editorApi = monaco.editor as EditorApi

function themeFor(name: string): monaco.editor.IStandaloneThemeData | undefined {
  const data = getEditorTheme()
  if (!data) return undefined
  const mode = name === GRAFANA_THEMES.dark ? 'vs-dark' : name === GRAFANA_THEMES.light ? 'vs' : undefined
  // Only substitute the name that matches the theme's mode; the other keeps
  // Grafana's data so a stray mode switch never renders mismatched colors.
  return mode === data.base ? data : undefined
}

if (!editorApi[PATCHED]) {
  const defineTheme = editorApi.defineTheme.bind(editorApi)
  editorApi.defineTheme = (name, data) => defineTheme(name, themeFor(name) ?? data)
  editorApi[PATCHED] = true

  const apply = () => {
    const data = getEditorTheme()
    if (!data) return
    const name = data.base === 'vs' ? GRAFANA_THEMES.light : GRAFANA_THEMES.dark
    // Redefining the active theme restyles every mounted editor; setTheme
    // also switches them when the mode changed before Grafana re-renders.
    editorApi.defineTheme(name, data)
    editorApi.setTheme(name)
  }
  subscribeEditorTheme(apply)
  apply()
}
