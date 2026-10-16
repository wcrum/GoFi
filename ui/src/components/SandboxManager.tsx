import { lazy, Suspense, useCallback, useEffect, useState } from 'react'
import { TextArea } from '@grafana/ui'
import { Alert, Badge, Button, FormField, Input, Select, Spinner } from './primitives'
import { applySandbox, deleteSandbox, listSandboxes, listLibraryFunctions, formatPythonSource, validatePythonSource, runPythonFunction, type SandboxDefinition, type SandboxStatus, type LibraryFunction, type SourceDiagnostic, type PythonRunResult } from '../sandbox-api'
const PythonSourceEditor=lazy(()=>import('./PythonSourceEditor'))
const EXAMPLE=`import json
import gofi

@gofi.parser("telemetry")
def parse_telemetry(data):
    return json.loads(data)

@gofi.transformer("enrich")
def enrich(data):
    data["processed_by"] = "python"
    return data

@gofi.sink("inspect")
def inspect(data):
    print(data)

@gofi.port(7000)
def ingress(data):
    return data
`
const newLibrary=():SandboxDefinition=>({name:'',sourceCode:EXAMPLE,timeoutSeconds:5,memoryMib:128})
function objectInput(value:string,label:string):Record<string,unknown> {
  const parsed:unknown=JSON.parse(value)
  if (!parsed || typeof parsed!=='object' || Array.isArray(parsed)) throw new Error(`${label} must be a JSON object`)
  return parsed as Record<string,unknown>
}
const functionKey=(fn:LibraryFunction)=>JSON.stringify([fn.kind,fn.name])
export default function SandboxManager() {
  const [libraries,setLibraries]=useState<SandboxDefinition[]>([])
  const [statuses,setStatuses]=useState<SandboxStatus[]>([])
  const [functions,setFunctions]=useState<LibraryFunction[]>([])
  const [draft,setDraft]=useState<SandboxDefinition>({...newLibrary(),name:'my-functions'})
  const [loadError,setLoadError]=useState('')
  const [error,setError]=useState('')
  const [message,setMessage]=useState('')
  const [messageTone,setMessageTone]=useState<'success'|'warning'>('success')
  const [busy,setBusy]=useState<string|null>(null)
  const [diagnostics,setDiagnostics]=useState<SourceDiagnostic[]>([])
  const [selectedFunction,setSelectedFunction]=useState('')
  const [raw,setRaw]=useState('{"sensor":"alpha","temperature":28}')
  const [fields,setFields]=useState('{"sensor":"alpha","temperature":28}')
  const [meta,setMeta]=useState('{}')
  const [result,setResult]=useState<PythonRunResult|null>(null)
  const refresh=useCallback(async()=>{
    try { const [inventory,registry]=await Promise.all([listSandboxes(),listLibraryFunctions()]);setLibraries(inventory.sandboxes);setStatuses(inventory.statuses);setFunctions(registry);setLoadError('') }
    catch(reason){setLoadError(String(reason))}
  },[])
  useEffect(()=>{void refresh();const timer=setInterval(refresh,5000);return()=>clearInterval(timer)},[refresh])
  const selectDraft=(value:SandboxDefinition)=>{setDraft(structuredClone(value));setDiagnostics([]);setError('');setMessage('');setResult(null);setSelectedFunction('')}
  const updateSource=(sourceCode:string)=>{setDraft(previous=>({...previous,sourceCode}));setDiagnostics([]);setMessage('');setResult(null)}
  async function action(kind:string,work:()=>Promise<void>){setBusy(kind);setError('');setMessage('');setMessageTone('success');try{await work()}catch(reason){setError(String(reason))}finally{setBusy(null)}}
  const available=functions.filter(fn=>fn.library===draft.name)
  const selected=available.find(fn=>functionKey(fn)===selectedFunction)??available[0]
  const saved=libraries.find(library=>library.name===draft.name)
  const dirty=!saved||saved.sourceCode!==draft.sourceCode
  return <div className="p-6 w-full space-y-5">
    <div><h3 className="text-xl font-semibold">Python function libraries</h3><p className="mt-2 text-sm text-g-text-secondary">Write reusable listeners, parsers, transformers, and sinks. GoFi runs each library in a separate worker; registered functions appear in the flow builder.</p></div>
    {loadError&&<Alert severity="error" title="Library inventory unavailable">{loadError}</Alert>}{error&&<Alert severity="error">{error}</Alert>}{message&&<Alert severity={messageTone} title="Python source">{message}</Alert>}
    <div className="grid grid-cols-1 xl:grid-cols-[260px_minmax(0,1fr)] gap-4">
      <section className="rounded border border-g-border-weak bg-g-primary p-4 space-y-3"><div className="flex items-center justify-between"><h4 className="text-sm font-semibold">Libraries</h4><Button disabled={!!busy} fill="text" onClick={()=>selectDraft(newLibrary())}>New library</Button></div>{libraries.map(library=>{const status=statuses.find(item=>item.name===library.name);return <div key={library.name} className="border-b border-g-border-weak py-3"><Button disabled={!!busy} fill="text" className="w-full" onClick={()=>selectDraft(library)}>{library.name}</Button><div className="mt-2"><Badge text={status?.phase??'Pending'} color={status?.error?'red':'gray'} /></div>{status?.error&&<p className="text-xs mt-2 text-red-300 break-words">{status.error}</p>}<Button variant="destructive" fill="text" disabled={!!busy} onClick={()=>void action('delete',async()=>{await deleteSandbox(library.name);await refresh()})} className="mt-2">Delete library</Button></div>})}{!libraries.length&&<p className="text-xs text-g-text-secondary">No saved libraries.</p>}</section>
      <section className="min-w-0 rounded border border-g-border-weak bg-g-primary p-4 space-y-4">
        <div className="grid grid-cols-1 md:grid-cols-3 gap-3"><FormField label="Library name"><Input aria-label="Library name" disabled={!!busy} value={draft.name} onChange={event=>setDraft({...draft,name:event.target.value})} placeholder="my-functions" /></FormField><FormField label="Deadline (seconds)"><Input aria-label="Worker deadline" disabled={!!busy} type="number" min={1} max={30} value={draft.timeoutSeconds??5} onChange={event=>setDraft({...draft,timeoutSeconds:Number(event.target.value)})} /></FormField><FormField label="Memory (MiB)"><Input aria-label="Worker memory" disabled={!!busy} type="number" min={64} max={1024} value={draft.memoryMib??128} onChange={event=>setDraft({...draft,memoryMib:Number(event.target.value)})} /></FormField></div>
        <div className="flex items-center gap-2"><h4 className="text-xs font-semibold mr-auto">Python source</h4><Button disabled={!!busy||!draft.sourceCode} onClick={()=>void action('format',async()=>{updateSource(await formatPythonSource(draft.sourceCode));setMessage('Source formatted.')})}>{busy==='format'?'Formatting…':'Format'}</Button><Button disabled={!!busy||!draft.sourceCode} onClick={()=>void action('validate',async()=>{const validation=await validatePythonSource(draft.sourceCode);setDiagnostics(validation.diagnostics);setMessageTone(validation.valid?'success':'warning');setMessage(validation.valid?'Validation passed.':`Validation found ${validation.diagnostics.length} issue(s).`)})}>{busy==='validate'?'Validating…':'Validate'}</Button></div>
        <Suspense fallback={<div className="h-[420px] flex items-center justify-center"><Spinner /></div>}><PythonSourceEditor source={draft.sourceCode} onChange={updateSource} diagnostics={diagnostics} disabled={!!busy} /></Suspense>
        {!!diagnostics.length&&<div role="status" className="rounded border border-red-500/40 p-3 text-xs space-y-2">{diagnostics.map((diagnostic,index)=><p key={index} className="text-red-300"><span className="font-mono">{diagnostic.line}:{diagnostic.column} · {diagnostic.code}</span> {diagnostic.message}</p>)}</div>}
        <div className="flex flex-wrap items-center gap-4"><Button variant="primary" disabled={!!busy||!draft.name.trim()} onClick={()=>void action('save',async()=>{await applySandbox(draft);await refresh();setMessage('Library saved and worker launch requested.')})}>{busy==='save'?'Launching worker…':'Save and launch'}</Button><p className="text-xs text-g-text-secondary">Formatting and validation run in an isolated tooling worker.</p></div>
        <section className="border-t border-g-border-weak pt-4 space-y-3"><h4 className="text-sm font-semibold">Run a saved function</h4><p className="text-xs text-g-text-secondary">Run invokes one function in the saved, running library. Save and launch after source changes.</p><FormField label="Registered function"><Select value={selected?functionKey(selected):''} options={available.length?available.map(fn=>({value:functionKey(fn),label:`${fn.name} · ${fn.kind}`})):[{value:'',label:'Save and launch a library first'}]} disabled={!!busy||!available.length} onChange={value=>{setSelectedFunction(value);setResult(null)}} /></FormField>
        <div className="grid grid-cols-1 lg:grid-cols-3 gap-3"><FormField label="Raw sample"><TextArea aria-label="Function raw input" value={raw} rows={4} onChange={event=>{setRaw(event.currentTarget.value);setResult(null)}} /></FormField><FormField label="Fields (JSON object)"><TextArea aria-label="Function fields input" value={fields} rows={4} onChange={event=>{setFields(event.currentTarget.value);setResult(null)}} /></FormField><FormField label="Metadata (JSON object)"><TextArea aria-label="Function metadata input" value={meta} rows={4} onChange={event=>{setMeta(event.currentTarget.value);setResult(null)}} /></FormField></div>
        <div className="flex items-center gap-3"><Button disabled={!!busy||!selected||dirty} onClick={()=>void action('run',async()=>{if(selected)setResult(await runPythonFunction({library:draft.name,name:selected.name,kind:selected.kind,raw,fields:objectInput(fields,'Fields'),meta:objectInput(meta,'Metadata')}))})}>{busy==='run'?'Running…':selected?.kind==='sink'?'Run sink function':'Run function'}</Button>{dirty&&<span className="text-xs text-g-text-secondary">Save and launch the current source before running.</span>}{selected?.kind==='sink'&&<span className="text-xs text-amber-300">This invocation performs the sink's configured side effects.</span>}</div>
        {result&&<div role="status" className="rounded border border-g-border-medium p-3"><h5 className="text-xs font-semibold mb-2">Function result</h5><pre className="text-xs font-mono whitespace-pre-wrap break-words">{JSON.stringify(result,null,2)}</pre>{result.rawBase64!==undefined&&<p className="text-[11px] text-g-text-secondary mt-2">Raw output is base64 encoded to preserve arbitrary bytes.</p>}</div>}
        </section>
      </section>
    </div>
  </div>
}
