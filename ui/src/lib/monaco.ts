import EditorWorker from 'monaco-editor/esm/vs/editor/editor.worker?worker'
import JsonWorker from 'monaco-editor/esm/vs/language/json/json.worker?worker'

// Bundle Monaco's workers locally for disconnected use. JSON resource views
// need the JSON language worker; every other language uses the base worker.
Object.assign(globalThis, { MonacoEnvironment: { getWorker: (_id: string, label: string) => label === 'json' ? new JsonWorker() : new EditorWorker() } })
