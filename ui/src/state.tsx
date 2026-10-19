import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState, type ReactNode } from 'react'
import type { FlowDefinition } from './types'
import { getConfiguration, getMode, listFlowsSince, type ConfigurationInfo, type DeployModeInfo } from './api'

// Shared control-plane inventory for the shell and every page. Flow
// definitions poll every five seconds with the last revision, so an
// unchanged inventory costs one small round trip and no re-render; mode and
// configuration are static.
interface GoFiState {
  flows: FlowDefinition[]
  loading: boolean
  error: string | null
  mode: DeployModeInfo | null
  configuration: ConfigurationInfo | null
  reload: () => Promise<void>
  /** Applies a saved definition locally before the next poll confirms it. */
  upsertLocal: (definition: FlowDefinition) => void
}
const GoFiContext = createContext<GoFiState | null>(null)

export function GoFiProvider({ children }: { children: ReactNode }) {
  const [flows, setFlows] = useState<FlowDefinition[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [mode, setMode] = useState<DeployModeInfo | null>(null)
  const [configuration, setConfiguration] = useState<ConfigurationInfo | null>(null)
  const revision = useRef(0)
  const reload = useCallback(async () => {
    try {
      const result = await listFlowsSince(revision.current)
      revision.current = result.revision
      if (result.flows) setFlows(result.flows.sort((a, b) => a.name.localeCompare(b.name)))
      setError(null)
    }
    catch (e) { setError(String(e)) }
    finally { setLoading(false) }
  }, [])
  useEffect(() => { void reload(); const id = setInterval(reload, 5000); return () => clearInterval(id) }, [reload])
  useEffect(() => { getMode().then(setMode).catch(() => {}); getConfiguration().then(setConfiguration).catch(() => {}) }, [])
  const upsertLocal = useCallback((definition: FlowDefinition) => {
    revision.current = 0 // the next poll fetches the saved inventory in full
    setFlows(previous => [...previous.filter(item => item.name !== definition.name), definition].sort((a, b) => a.name.localeCompare(b.name)))
  }, [])
  const value = useMemo(() => ({ flows, loading, error, mode, configuration, reload, upsertLocal }), [flows, loading, error, mode, configuration, reload, upsertLocal])
  return <GoFiContext.Provider value={value}>{children}</GoFiContext.Provider>
}

export function useGoFi(): GoFiState {
  const context = useContext(GoFiContext)
  if (!context) throw new Error('useGoFi must be used inside GoFiProvider')
  return context
}
