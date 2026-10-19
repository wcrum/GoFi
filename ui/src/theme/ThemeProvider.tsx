import { createContext, useCallback, useContext, useEffect, useLayoutEffect, useMemo, useState, type ReactNode } from 'react'
import { ThemeContext } from '@grafana/data'
import { GlobalStyles } from '@grafana/ui'
import { createGoFiTheme, cssTokens } from '../grafana-theme'
import { gofiFaviconDataUrl } from '../components/gofi-mark-svg'
import { setEditorTheme } from './editorTheme'
import { DEFAULT_ACCENT, findAccent, findTheme } from './themes'
import type { ColorMode, ThemeDefinition } from './types'

/** 'system' or a theme id from THEMES. */
export type ThemeChoice = 'system' | string
export interface ThemePreference { theme: ThemeChoice; accent: string }

// Must match the pre-paint script in index.html.
export const THEME_STORAGE_KEY = 'gofi.theme'
export const ACCENT_STORAGE_KEY = 'gofi.theme.accent'
export const THEME_CSS_CACHE_KEY = 'gofi.theme.css'
const DARK_QUERY = '(prefers-color-scheme: dark)'
const LEGACY: Record<string, string> = { light: 'grafana-light', dark: 'grafana-dark' }

interface ThemeState {
  preference: ThemePreference
  definition: ThemeDefinition
  mode: ColorMode
  setTheme: (theme: ThemeChoice) => void
  setAccent: (accent: string) => void
}
const ThemeStateContext = createContext<ThemeState | null>(null)

function storageGet(key: string): string | null {
  try { return localStorage.getItem(key) } catch { return null }
}
function storageSet(key: string, value: string | null) {
  try {
    if (value === null) localStorage.removeItem(key)
    else localStorage.setItem(key, value)
  } catch { /* storage unavailable: the choice lasts for this page only */ }
}

function readTheme(): ThemeChoice {
  const stored = storageGet(THEME_STORAGE_KEY)
  if (!stored) return 'system'
  const mapped = LEGACY[stored] ?? stored
  if (!findTheme(mapped)) return 'system'
  if (mapped !== stored) storageSet(THEME_STORAGE_KEY, mapped)
  return mapped
}

function readAccent(): string {
  const stored = storageGet(ACCENT_STORAGE_KEY)
  return findAccent(stored) ? stored! : DEFAULT_ACCENT
}

function systemMode(): ColorMode {
  return typeof window.matchMedia === 'function' && !window.matchMedia(DARK_QUERY).matches ? 'light' : 'dark'
}

/** Points <link rel="icon"> at an accent-colored mark; returns the data URL
 * (or null when the color can't be rendered and the static icon stays). */
function applyFavicon(color: string): string | null {
  let href: string
  try { href = gofiFaviconDataUrl(color) } catch { return null }
  let link = document.querySelector<HTMLLinkElement>('link[rel~="icon"]')
  if (!link) {
    link = document.createElement('link')
    link.rel = 'icon'
    document.head.appendChild(link)
  }
  link.type = 'image/svg+xml'
  if (link.getAttribute('href') !== href) link.href = href
  return href
}

/** Resolves the theme preference (system or a named theme) and accent,
 * provides the matching Grafana theme to every @grafana/ui component, keeps
 * Monaco's theme in step, colors the favicon with the accent, and mirrors the palette into the `--g-*` CSS
 * variables on <html> (cached for the pre-paint script in index.html). */
export function ThemeProvider({ children }: { children: ReactNode }) {
  const [theme, setThemeState] = useState<ThemeChoice>(readTheme)
  const [accent, setAccentState] = useState<string>(readAccent)
  const [system, setSystem] = useState<ColorMode>(systemMode)
  useEffect(() => {
    if (typeof window.matchMedia !== 'function') return
    const query = window.matchMedia(DARK_QUERY)
    const update = () => setSystem(query.matches ? 'dark' : 'light')
    query.addEventListener('change', update)
    return () => query.removeEventListener('change', update)
  }, [])

  const definition = (theme === 'system' ? undefined : findTheme(theme)) ?? findTheme(`grafana-${system}`)!
  const mode = definition.mode
  const accentDefinition = findAccent(accent)

  // Built during render (not in an effect) so Monaco's theme is defined and
  // current before any CodeEditor mounts or re-renders with it.
  const grafanaTheme = useMemo(() => {
    const built = createGoFiTheme(definition, accentDefinition)
    setEditorTheme(definition, built)
    return built
  }, [definition, accentDefinition])

  useLayoutEffect(() => {
    const root = document.documentElement
    const vars = cssTokens(grafanaTheme)
    root.dataset.theme = mode
    root.style.colorScheme = mode
    for (const [name, value] of Object.entries(vars)) root.style.setProperty(name, value)
    const accentColor = grafanaTheme.colors.primary.main
    const icon = applyFavicon(accentColor)
    const key = theme === 'system' ? `system:${mode}` : definition.id
    storageSet(THEME_CSS_CACHE_KEY, JSON.stringify({ key, mode, vars, accent: accentColor, icon }))
  }, [grafanaTheme, mode, theme, definition])

  const setTheme = useCallback((next: ThemeChoice) => {
    const valid = next === 'system' || findTheme(next) ? next : 'system'
    setThemeState(valid)
    storageSet(THEME_STORAGE_KEY, valid === 'system' ? null : valid)
  }, [])
  const setAccent = useCallback((next: string) => {
    const valid = findAccent(next) ? next : DEFAULT_ACCENT
    setAccentState(valid)
    storageSet(ACCENT_STORAGE_KEY, valid)
  }, [])

  const value = useMemo<ThemeState>(() => ({
    preference: { theme, accent: accentDefinition ? accent : DEFAULT_ACCENT },
    definition, mode, setTheme, setAccent,
  }), [theme, accent, accentDefinition, definition, mode, setTheme, setAccent])
  return <ThemeStateContext.Provider value={value}>
    <ThemeContext.Provider value={grafanaTheme}>
      {/* Grafana's base element and form styles, which @grafana/ui components
          assume (e.g. Switch needs block-level labels). */}
      <GlobalStyles />
      {children}
    </ThemeContext.Provider>
  </ThemeStateContext.Provider>
}

function useThemeState(): ThemeState {
  const context = useContext(ThemeStateContext)
  if (!context) throw new Error('Theme hooks must be used inside ThemeProvider')
  return context
}

/** The resolved color mode, e.g. for React Flow's colorMode prop. */
export function useColorMode(): ColorMode { return useThemeState().mode }

/** The active theme definition (system already resolved). */
export function useThemeDefinition(): ThemeDefinition { return useThemeState().definition }

/** The stored preference and its setters, for the theme picker. */
export function useThemePreference(): [ThemePreference, (theme: ThemeChoice) => void, (accent: string) => void] {
  const { preference, setTheme, setAccent } = useThemeState()
  return [preference, setTheme, setAccent]
}
