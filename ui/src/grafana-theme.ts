import { createTheme, type GrafanaTheme2 } from '@grafana/data'

export type ColorMode = 'light' | 'dark'

// Grafana's stock themes. index.css mirrors both palettes as RGB triplets for
// Tailwind and React Flow; only the shared rectangular radius is overridden.
const radius = Number.parseInt(getComputedStyle(document.documentElement).getPropertyValue('--gofi-radius')) || 4
const themes: Partial<Record<ColorMode, GrafanaTheme2>> = {}

export function createGoFiTheme(mode: ColorMode): GrafanaTheme2 {
  return themes[mode] ??= createTheme({
    name: mode === 'dark' ? 'GoFi dark' : 'GoFi light',
    colors: { mode },
    shape: { borderRadius: radius, borderRadiusSm: radius, borderRadiusLg: radius },
  })
}
