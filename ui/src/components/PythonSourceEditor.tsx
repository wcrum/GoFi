import { useCallback, useEffect, useRef } from 'react'
import { CodeEditor, type Monaco, type MonacoEditor } from '@grafana/ui'
import EditorWorker from 'monaco-editor/esm/vs/editor/editor.worker?worker'
import type { SourceDiagnostic } from '../sandbox-api'

// Bundle the language-independent editor worker locally for disconnected use.
Object.assign(globalThis, { MonacoEnvironment: { getWorker: () => new EditorWorker() } })
export default function PythonSourceEditor({ source, onChange, diagnostics, disabled }: { source:string; onChange:(source:string)=>void; diagnostics:SourceDiagnostic[]; disabled?:boolean }) {
  const editor = useRef<MonacoEditor | null>(null)
  const monaco = useRef<Monaco | null>(null)
  const markers = useCallback(() => {
    const model=editor.current?.getModel()
    if (!model || !monaco.current) return
    monaco.current.editor.setModelMarkers(model,'gofi-python',diagnostics.map(diagnostic => ({
      startLineNumber:Math.max(1,diagnostic.line), endLineNumber:Math.max(1,diagnostic.line), startColumn:Math.max(1,diagnostic.column), endColumn:Math.max(2,diagnostic.column+1),
      severity:monaco.current!.MarkerSeverity.Error, message:`${diagnostic.code}: ${diagnostic.message}`, code:diagnostic.code,
    })))
  },[diagnostics])
  useEffect(markers,[markers])
  return <div className="rounded border border-g-border-medium overflow-hidden"><CodeEditor language="python" value={source} onChange={onChange} height="420px" width="100%" showLineNumbers showMiniMap={false} readOnly={disabled} wordWrap monacoOptions={{tabSize:4,insertSpaces:true,fontSize:13,scrollBeyondLastLine:false,automaticLayout:true,ariaLabel:'Python source',glyphMargin:true,renderValidationDecorations:'on'}} onEditorDidMount={(instance,api)=>{editor.current=instance;monaco.current=api;markers()}} onEditorWillUnmount={()=>{editor.current=null;monaco.current=null}} /></div>
}
