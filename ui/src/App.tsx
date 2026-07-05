import { useState, useEffect, useCallback, lazy, Suspense } from 'react'
import { Spinner } from '@cnak-us/design-system'
import { RiAddLine, RiWifiLine, RiCloudLine, RiBox3Line, RiTerminalBoxLine } from 'react-icons/ri'
import { MdOutlineStream } from 'react-icons/md'
import type { ListenerDefinition } from './types'
import { listListeners, deleteListener, getMode, type DeployModeInfo } from './api'
import { ListenerCard } from './components/ListenerCard'
import { ListenerStream } from './components/ListenerStream'
import { useToast } from './components/Toast'

const FlowCanvas = lazy(() => import('./components/canvas/FlowCanvas'))

// "compose" and "docker" are both surfaced as a single generic "Container"
// badge: the underlying driver talks to any Docker-Engine-API-compatible
// socket (real Docker or Podman's compatibility layer, via DOCKER_HOST — see
// driver/docker.go), so distinguishing "Docker Compose" from a plain
// container as a brand name isn't meaningful to the user — what matters is
// that we're running inside *some* container runtime, not which one.
const MODE_DISPLAY: Record<string, { label: string; icon: typeof RiCloudLine }> = {
  kubernetes: { label: 'Kubernetes', icon: RiCloudLine },
  compose: { label: 'Container', icon: RiBox3Line },
  docker: { label: 'Container', icon: RiBox3Line },
  binary: { label: 'Binary', icon: RiTerminalBoxLine },
}

export default function App() {
  const addToast = useToast()
  const [listeners, setListeners] = useState<ListenerDefinition[]>([])
  const [loading, setLoading] = useState(true)
  const [streaming, setStreaming] = useState<string | null>(null)
  const [mode, setMode] = useState<DeployModeInfo | null>(null)
  const [createOpen, setCreateOpen] = useState(false)
  const [canvasListener, setCanvasListener] = useState<string | null>(null)

  const reload = useCallback(async () => {
    try {
      const data = await listListeners()
      setListeners(data ?? [])
    } catch (e) {
      addToast(String(e))
    } finally {
      setLoading(false)
    }
  }, [addToast])

  useEffect(() => {
    reload()
    const id = setInterval(reload, 5000)
    return () => clearInterval(id)
  }, [reload])

  useEffect(() => {
    getMode().then(setMode).catch(() => {})
  }, [])

  async function handleDelete(name: string) {
    await deleteListener(name)
    reload()
  }

  const canvasDef = canvasListener ? listeners.find((l) => l.name === canvasListener) : undefined

  return (
    <div className="min-h-screen bg-g-canvas flex flex-col">
      <header className="border-b border-g-border-weak px-6 py-4 flex items-center gap-3">
        <MdOutlineStream className="text-g-accent-main text-2xl" />
        <span className="text-g-text text-xl font-semibold tracking-tight">GoFi</span>
        <span className="text-g-text-secondary text-sm ml-1">dynamic ETL listener</span>
        {mode && (
          <span className="flex items-center gap-1 text-xs px-2 py-1 rounded-full border border-g-border-weak text-g-text-secondary">
            {(() => {
              const Icon = (MODE_DISPLAY[mode.mode] ?? MODE_DISPLAY.binary).icon
              return <Icon />
            })()}
            {(MODE_DISPLAY[mode.mode] ?? MODE_DISPLAY.binary).label}
          </span>
        )}
      </header>

      <div className="flex-1 relative min-h-0">
        {createOpen ? (
          <Suspense fallback={<CanvasLoadingFallback />}>
            <FlowCanvas
              mode="create"
              onSaved={() => { setCreateOpen(false); reload() }}
              onClose={() => setCreateOpen(false)}
            />
          </Suspense>
        ) : canvasDef ? (
          <Suspense fallback={<CanvasLoadingFallback />}>
            <FlowCanvas
              mode="view"
              initial={canvasDef}
              onClose={() => setCanvasListener(null)}
            />
          </Suspense>
        ) : (
          <main className="p-6 max-w-6xl mx-auto w-full">
            {loading ? (
              <div className="flex justify-center mt-20">
                <Spinner />
              </div>
            ) : listeners.length === 0 ? (
              <div className="text-center mt-20 text-g-text-secondary">
                <RiWifiLine className="text-5xl mx-auto mb-3 opacity-30" />
                <p className="text-lg">No listeners running</p>
                <p className="text-sm mt-1">Create one to start ingesting traffic.</p>
                <button
                  onClick={() => setCreateOpen(true)}
                  className="mt-4 inline-flex items-center gap-1.5 px-4 py-2 rounded-md bg-g-accent-main text-white text-sm font-medium hover:bg-g-accent-main/90 transition-colors"
                >
                  <RiAddLine />
                  New Listener
                </button>
              </div>
            ) : (
              <div className="grid grid-cols-1 gap-4">
                <button
                  onClick={() => setCreateOpen(true)}
                  className="flex items-center justify-center gap-2 w-full border border-dashed border-g-border-medium rounded-lg py-4 text-sm text-g-text-secondary hover:text-g-text hover:border-g-border-strong hover:bg-g-hover transition-colors"
                >
                  <RiAddLine />
                  New Listener
                </button>
                {listeners.map((l) => (
                  <ListenerCard
                    key={l.name}
                    def={l}
                    onDelete={() => handleDelete(l.name)}
                    onInspect={() => setStreaming(l.name)}
                    onOpenCanvas={() => setCanvasListener(l.name)}
                  />
                ))}
              </div>
            )}
          </main>
        )}
      </div>

      {streaming && (
        <ListenerStream
          name={streaming}
          onClose={() => setStreaming(null)}
        />
      )}
    </div>
  )
}

function CanvasLoadingFallback() {
  return (
    <div className="absolute inset-0 flex items-center justify-center bg-g-canvas">
      <Spinner />
    </div>
  )
}
