import { useState, useEffect, useCallback } from 'react'
import { PanelHeader, Alert, Spinner } from '@cnak-us/design-system'
import { RiAddLine, RiRadarLine } from 'react-icons/ri'
import type { ListenerDefinition } from './types'
import { listListeners, deleteListener } from './api'
import { ListenerCard } from './components/ListenerCard'
import { CreateListenerModal } from './components/CreateListenerModal'
import { ListenerStream } from './components/ListenerStream'

export default function App() {
  const [listeners, setListeners] = useState<ListenerDefinition[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [creating, setCreating] = useState(false)
  const [streaming, setStreaming] = useState<string | null>(null) // listener name

  const reload = useCallback(async () => {
    try {
      const data = await listListeners()
      setListeners(data ?? [])
      setError(null)
    } catch (e) {
      setError(String(e))
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    reload()
    const id = setInterval(reload, 5000)
    return () => clearInterval(id)
  }, [reload])

  async function handleDelete(name: string) {
    await deleteListener(name)
    reload()
  }

  return (
    <div className="min-h-screen bg-g-canvas flex flex-col">
      <header className="border-b border-g-border-weak px-6 py-4 flex items-center gap-3">
        <RiRadarLine className="text-g-accent-main text-2xl" />
        <span className="text-g-text text-xl font-semibold tracking-tight">Gogo</span>
        <span className="text-g-text-secondary text-sm ml-1">dynamic ETL listener</span>
        <div className="ml-auto flex items-center gap-2">
          <button
            onClick={() => setCreating(true)}
            className="flex items-center gap-1.5 px-3 py-1.5 rounded-md bg-g-accent-main text-white text-sm font-medium hover:bg-g-accent-main/90 transition-colors"
          >
            <RiAddLine />
            New Listener
          </button>
        </div>
      </header>

      <main className="flex-1 p-6 max-w-6xl mx-auto w-full">
        {error && (
          <Alert severity="error" title="API Error" className="mb-4">
            {error}
          </Alert>
        )}

        {loading ? (
          <div className="flex justify-center mt-20">
            <Spinner />
          </div>
        ) : listeners.length === 0 ? (
          <div className="text-center mt-20 text-g-text-secondary">
            <RiRadarLine className="text-5xl mx-auto mb-3 opacity-30" />
            <p className="text-lg">No listeners running</p>
            <p className="text-sm mt-1">Create one to start ingesting traffic.</p>
          </div>
        ) : (
          <div className="grid grid-cols-1 gap-4">
            {listeners.map((l) => (
              <ListenerCard
                key={l.name}
                def={l}
                onDelete={() => handleDelete(l.name)}
                onInspect={() => setStreaming(l.name)}
              />
            ))}
          </div>
        )}
      </main>

      {creating && (
        <CreateListenerModal
          onClose={() => setCreating(false)}
          onCreated={() => { setCreating(false); reload() }}
        />
      )}

      {streaming && (
        <ListenerStream
          name={streaming}
          onClose={() => setStreaming(null)}
        />
      )}
    </div>
  )
}
