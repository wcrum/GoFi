import { useState, useEffect, useCallback, lazy, Suspense } from 'react'
import { Spinner, Badge, Button } from './components/primitives'
import { RiAddLine, RiNodeTree, RiCodeSSlashLine, RiServerLine, RiPulseLine, RiArrowRightLine, RiRefreshLine, RiListCheck2, RiCodeBoxLine } from 'react-icons/ri'
import { MdOutlineStream } from 'react-icons/md'
import type { ListenerDefinition } from './types'
import { listListeners, deleteListener, getMode, getConfiguration, getEndpoints, type ConfigurationInfo, type DiscoveredEndpoint, type DeployModeInfo } from './api'
import { ListenerCard } from './components/ListenerCard'
import { ListenerStream } from './components/ListenerStream'
import { useToast } from './components/Toast'
import { ResourceView } from './components/ResourceView'

const FlowCanvas = lazy(() => import('./components/canvas/FlowCanvas'))
const ListenerTopology = lazy(() => import('./components/canvas/ListenerTopology'))
const SandboxManager = lazy(() => import('./components/SandboxManager'))
type View = 'flow' | 'listeners' | 'resources' | 'endpoints' | 'libraries'

export default function App() {
  const addToast = useToast()
  const [listeners, setListeners] = useState<ListenerDefinition[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [streaming, setStreaming] = useState<string | null>(null)
  const [mode, setMode] = useState<DeployModeInfo | null>(null)
  const [configuration, setConfiguration] = useState<ConfigurationInfo | null>(null)
  const [endpoints, setEndpoints] = useState<DiscoveredEndpoint[] | null>(null)
  const [inventoryError, setInventoryError] = useState<string | null>(null)
  const [createOpen, setCreateOpen] = useState(false)
  const [editing, setEditing] = useState<ListenerDefinition | null>(null)
  const [selected, setSelected] = useState<string | null>(null)
  const [view, setView] = useState<View>('flow')

  const reload = useCallback(async () => {
    try { setListeners((await listListeners()) ?? []); setError(null) }
    catch (e) { setError(String(e)) }
    finally { setLoading(false) }
  }, [])
  useEffect(() => { void reload(); const id = setInterval(reload, 5000); return () => clearInterval(id) }, [reload])
  useEffect(() => { getMode().then(setMode).catch(() => {}); getConfiguration().then(setConfiguration).catch(() => {}) }, [])
  useEffect(() => {
    if (view !== 'endpoints') return
    setInventoryError(null)
    getEndpoints().then(setEndpoints).catch((e) => setInventoryError(String(e)))
  }, [view])
  async function handleDelete(name: string) {
    try { await deleteListener(name); await reload() } catch (e) { addToast(String(e)) }
  }
  const active = listeners.find((listener) => listener.name === selected)
  const navigate = (next: View) => { setEditing(null); setCreateOpen(false); setView(next) }
  const beginEdit = (definition: ListenerDefinition) => { setEditing(structuredClone(definition)); setCreateOpen(false); setSelected(definition.name); setView('flow') }
  const saved = (definition: ListenerDefinition) => { setListeners(previous => [...previous.filter(item => item.name !== definition.name), definition].sort((a,b)=>a.name.localeCompare(b.name))); setCreateOpen(false); setEditing(null); setSelected(definition.name); setView('flow'); void reload() }
  const nav = [
    { id: 'flow' as const, label: 'Live flow', icon: RiNodeTree },
    { id: 'listeners' as const, label: 'Listeners', icon: RiListCheck2 },
    { id: 'endpoints' as const, label: 'Endpoints', icon: RiServerLine },
    { id: 'libraries' as const, label: 'Python libraries', icon: RiCodeBoxLine },
    { id: 'resources' as const, label: 'Configuration', icon: RiCodeSSlashLine },
  ]

  return (
    <div className="h-screen min-h-[560px] flex bg-g-canvas overflow-hidden">
      <aside className="w-56 shrink-0 flex flex-col border-r border-g-border-weak bg-g-primary">
        <div className="px-5 py-5 flex items-center gap-3"><span className="p-2 rounded bg-g-accent-main/10"><MdOutlineStream className="text-g-accent-main text-2xl" /></span><div><h1 className="text-xl font-semibold tracking-tight">GoFi</h1><p className="text-[10px] uppercase tracking-[.2em] text-g-text-secondary">Traffic workspace</p></div></div>
        <nav aria-label="Primary tools" className="px-3 space-y-1">{nav.map(({ id, label, icon: Icon }) => <button key={id} onClick={() => { navigate(id); if (id === 'flow') setSelected(null) }} aria-current={!createOpen && view === id ? 'page' : undefined} className={`w-full flex items-center gap-3 rounded px-3 py-2 text-sm transition-colors ${!createOpen && view === id ? 'text-g-accent-main bg-g-accent-main/10' : 'text-g-text-secondary hover:bg-g-hover hover:text-g-text'}`}><Icon size={18} />{label}</button>)}</nav>
        <div className="mx-4 mt-6 border-t border-g-border-weak pt-5 flex items-center justify-between"><h2 className="text-[10px] font-semibold uppercase tracking-widest text-g-text-secondary">Your listeners</h2><span className="text-xs text-g-text-secondary">{listeners.length}</span></div>
        <div className="px-3 mt-2 overflow-y-auto flex-1">{listeners.map((listener) => <button key={listener.name} onClick={() => { setSelected(listener.name); navigate('flow') }} className={`w-full flex items-center gap-2 px-3 py-2 rounded text-left text-xs ${active?.name === listener.name && view === 'flow' && !createOpen ? 'bg-g-hover text-g-text' : 'text-g-text-secondary hover:bg-g-hover'}`}><span className="h-1.5 w-1.5 rounded-full bg-g-accent-main shrink-0" /><span className="truncate">{listener.name}</span><span className="ml-auto text-[10px] opacity-60">{listener.source.port}</span></button>)}{!loading && !listeners.length && <p className="text-xs text-g-text-disabled px-3 py-2">No listeners yet</p>}</div>
        <div className="p-4 border-t border-g-border-weak"><Button variant="primary" onClick={() => { setEditing(null); setCreateOpen(true) }} className="w-full inline-flex items-center justify-center gap-2 rounded bg-g-accent-main px-3 py-2 text-sm font-semibold text-slate-950 hover:opacity-90"><RiAddLine />New listener</Button><p className="mt-4 text-[10px] text-g-text-secondary flex items-center justify-between"><span>{mode?.mode ?? 'Connecting…'}</span><span>GoFi.wcrum.dev</span></p></div>
      </aside>
      <div className="flex-1 min-w-0 flex flex-col">
        <header className="h-14 shrink-0 flex items-center gap-3 px-6 border-b border-g-border-weak"><span className="text-xs uppercase tracking-widest text-g-text-secondary">Workspace</span><RiArrowRightLine className="text-g-text-disabled" /><h2 className="text-sm font-medium">{editing ? 'Edit listener' : createOpen ? 'Create listener' : nav.find((item) => item.id === view)?.label}</h2><span className="ml-auto text-xs text-g-text-secondary">{configuration?.source === 'kubernetes' ? 'Kubernetes resources' : 'Traffic control plane'}</span><Button fill="text" aria-label="Refresh listeners" onClick={reload} className="p-2 rounded hover:bg-g-hover text-g-text-secondary"><RiRefreshLine /></Button></header>
        {error && <div role="alert" className="px-6 py-2 text-xs bg-red-500/10 text-red-300">Unable to load listeners: {error}</div>}
        <main className="flex-1 relative min-h-0 overflow-auto">
          {editing ? <Suspense fallback={<Loading />}><FlowCanvas key={`edit:${editing.name}`} mode="edit" initial={editing} onSaved={saved} onClose={() => setEditing(null)} closeLabel="Cancel" /></Suspense> : createOpen ? <Suspense fallback={<Loading />}><FlowCanvas mode="create" onSaved={saved} onClose={() => setCreateOpen(false)} /></Suspense>
            : loading ? <Loading />
            : view === 'flow' ? active ? <Suspense fallback={<Loading />}><FlowCanvas key={JSON.stringify(active)} mode="view" initial={active} onClose={() => setSelected(null)} closeLabel="All listeners" onInspect={() => setStreaming(active.name)} onEdit={() => beginEdit(active)} /></Suspense> : listeners.length ? <Suspense fallback={<Loading />}><ListenerTopology listeners={listeners} onSelect={setSelected} /></Suspense> : <div className="h-full flex flex-col items-center justify-center text-center px-6"><RiNodeTree className="text-g-accent-main mb-5" size={48} /><p className="text-xs text-g-accent-main uppercase tracking-widest">Your traffic, connected</p><h3 className="text-2xl font-semibold mt-3">Build your first live flow</h3><p className="text-sm text-g-text-secondary mt-3 max-w-md leading-relaxed">Receive traffic from a NodePort or socket, decode incoming frames, filter with expressions, and send it to your applications.</p><Button variant="primary" onClick={() => { setEditing(null); setCreateOpen(true) }} className="mt-6 inline-flex items-center gap-2 rounded bg-g-accent-main px-4 py-2 text-sm font-semibold text-slate-950"><RiAddLine />Create listener</Button></div>
            : view === 'libraries' ? <Suspense fallback={<Loading />}><SandboxManager /></Suspense>
            : view === 'resources' ? <ResourceView listeners={listeners} configuration={configuration} />
            : view === 'endpoints' ? <div className="p-6 w-full min-w-0"><p className="text-xs uppercase tracking-widest text-g-accent-main">Connect applications</p><h3 className="text-2xl font-semibold mt-2">Available endpoints</h3><p className="mt-2 text-sm text-g-text-secondary">Use a destination URL to route traffic directly to a container, service, or host.</p>{inventoryError ? <p role="alert" className="mt-6 text-sm text-red-300">{inventoryError}</p> : endpoints ? <div className="mt-6 space-y-3">{endpoints.length ? endpoints.map((endpoint, index) => <div key={index} className="rounded border border-g-border-weak bg-g-primary p-4"><div className="flex items-center gap-3"><RiServerLine className="text-g-accent-main" /><strong className="text-sm">{endpoint.name}</strong><Badge text={endpoint.type ?? endpoint.kind} /><span className="text-xs text-g-text-secondary">{endpoint.namespace}</span></div><div className="mt-3 text-xs font-mono break-all text-g-text-secondary">{endpoint.ports.map((port) => <p key={`${port.protocol}-${port.port}`}>{port.url}{port.nodePort ? ` · NodePort ${port.nodePort}` : ''}</p>)}</div>{endpoint.externalAddresses?.length ? <p className="mt-2 text-xs text-g-text-secondary">External: {endpoint.externalAddresses.join(', ')}</p> : null}</div>) : <p className="text-sm text-g-text-secondary">No discoverable endpoints. Configure a destination URL in a listener sink.</p>}</div> : <div className="mt-8"><Spinner /></div>}</div>
            : <div className="p-6 w-full min-w-0 space-y-4"><div className="mb-6"><p className="text-xs uppercase tracking-widest text-g-accent-main">Traffic control</p><h3 className="text-2xl font-semibold mt-2">Listeners <span className="text-g-text-disabled">/ {listeners.length}</span></h3></div>{listeners.map((listener) => <ListenerCard key={listener.name} def={listener} onEdit={() => beginEdit(listener)} onDelete={() => handleDelete(listener.name)} onInspect={() => setStreaming(listener.name)} onOpenCanvas={() => { setSelected(listener.name); setView('flow') }} />)}{!listeners.length && <p className="text-sm text-g-text-secondary">Create a listener to start receiving traffic.</p>}</div>}
        </main>
        <footer className="h-8 shrink-0 flex items-center gap-2 px-5 border-t border-g-border-weak text-[10px] text-g-text-secondary"><RiPulseLine className="text-g-accent-main" /><span>Live traffic · {listeners.length} listener{listeners.length === 1 ? '' : 's'}</span><span className="ml-auto">Decode → Filter → Route</span></footer>
      </div>
      {streaming && <ListenerStream name={streaming} onClose={() => setStreaming(null)} />}
    </div>
  )
}
function Loading() { return <div className="absolute inset-0 flex items-center justify-center"><Spinner /></div> }
