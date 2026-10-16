import { createContext, useCallback, useContext, useEffect, useMemo, useState, type ReactNode } from 'react'
import { ThemeContext } from '@grafana/data'
import { createGoFiTheme, type ColorMode } from '../grafana-theme'

export type ThemePreference = 'system' | 'light' | 'dark'

// Must match the pre-paint script in index.html.
export const THEME_STORAGE_KEY = 'gofi.theme'
const DARK_QUERY = '(prefers-color-scheme: dark)'

interface ColorModeState {
  preference: ThemePreference
  mode: ColorMode
  setPreference: (preference: ThemePreference) => void
}
const ColorModeContext = createContext<ColorModeState | null>(null)

function readPreference(): ThemePreference {
  try {
    const stored = localStorage.getItem(THEME_STORAGE_KEY)
    return stored === 'light' || stored === 'dark' ? stored : 'system'
  } catch { return 'system' }
}

function systemMode(): ColorMode {
  return typeof window.matchMedia === 'function' && !window.matchMedia(DARK_QUERY).matches ? 'light' : 'dark'
}

/** Resolves the user's theme preference (system, light, or dark), applies it
 * to <html data-theme> for the CSS tokens, and provides the matching Grafana
 * theme to every @grafana/ui component. */
export function ThemeProvider({ children }: { children: ReactNode }) {
  const [preference, setPreferenceState] = useState<ThemePreference>(readPreference)
  const [system, setSystem] = useState<ColorMode>(systemMode)
  useEffect(() => {
    if (typeof window.matchMedia !== 'function') return
    const query = window.matchMedia(DARK_QUERY)
    const update = () => setSystem(query.matches ? 'dark' : 'light')
    query.addEventListener('change', update)
    return () => query.removeEventListener('change', update)
  }, [])
  const mode: ColorMode = preference === 'system' ? system : preference
  useEffect(() => { document.documentElement.dataset.theme = mode }, [mode])
  const setPreference = useCallback((next: ThemePreference) => {
    setPreferenceState(next)
    try {
      if (next === 'system') localStorage.removeItem(THEME_STORAGE_KEY)
      else localStorage.setItem(THEME_STORAGE_KEY, next)
    } catch { /* storage unavailable: the choice lasts for this page only */ }
  }, [])
  const value = useMemo(() => ({ preference, mode, setPreference }), [preference, mode, setPreference])
  return <ColorModeContext.Provider value={value}>
    <ThemeContext.Provider value={createGoFiTheme(mode)}>{children}</ThemeContext.Provider>
  </ColorModeContext.Provider>
}

function useColorModeState(): ColorModeState {
  const context = useContext(ColorModeContext)
  if (!context) throw new Error('useColorMode must be used inside ThemeProvider')
  return context
}

/** The resolved color mode, e.g. for React Flow's colorMode prop. */
export function useColorMode(): ColorMode { return useColorModeState().mode }

/** The stored preference and its setter, for the theme picker. */
export function useThemePreference(): [ThemePreference, (preference: ThemePreference) => void] {
  const { preference, setPreference } = useColorModeState()
  return [preference, setPreference]
}
