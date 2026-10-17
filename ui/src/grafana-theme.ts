import { colorManipulator, createTheme, type GrafanaTheme2, type NewThemeOptions } from '@grafana/data'
import type { AccentDefinition, AccentShade, ColorMode, ThemeDefinition } from './theme/types'

export type { ColorMode } from './theme/types'

const { alpha, darken, emphasize, getContrastRatio, lighten, onBackground } = colorManipulator

// Only the shared rectangular radius is overridden from Grafana's shape.
const radius = Number.parseInt(getComputedStyle(document.documentElement).getPropertyValue('--gofi-radius')) || 4
const shape = { borderRadius: radius, borderRadiusSm: radius, borderRadiusLg: radius }

type ColorsInput = NonNullable<NewThemeOptions['colors']>

// Grafana's stock primary (blue), used as the "theme" accent of the stock themes.
const stockPrimary: Partial<Record<ColorMode, AccentShade>> = {}
function grafanaPrimary(mode: ColorMode): AccentShade {
  return stockPrimary[mode] ??= (() => {
    const { main, text } = createTheme({ colors: { mode } }).colors.primary
    return { main, text }
  })()
}

/** Steps `color` away from `background` (darker in light mode, lighter in
 * dark mode) until it reaches `ratio` contrast; a no-op for colors that
 * already pass. Keeps canonical palettes intact while guaranteeing text and
 * links stay readable on the panel background. */
export function readable(color: string, background: string, mode: ColorMode, ratio = 4.5): string {
  let result = color
  for (let i = 0; i < 12 && getContrastRatio(result, background) < ratio; i++) {
    result = mode === 'light' ? darken(result, 0.08) : lighten(result, 0.08)
  }
  return result
}

/** The accent shade a definition resolves to: the preset's shade for the
 * theme's mode, else the theme's own accent (or Grafana blue). */
export function accentShade(definition: ThemeDefinition, accent: AccentDefinition | undefined): AccentShade {
  const preset = accent?.[definition.mode]
  if (preset) return preset
  const palette = definition.palette
  if (!palette) return grafanaPrimary(definition.mode)
  return { main: palette.accent, text: palette.accentText ?? palette.accent }
}

function paletteColors(definition: ThemeDefinition): ColorsInput {
  const p = definition.palette!
  const mode = definition.mode
  const bg = p.primary
  const status = (main: string) => ({ main, text: readable(main, bg, mode) })
  const secondaryMain = emphasize(p.secondary, mode === 'dark' ? 0.08 : 0.04)
  return {
    mode,
    background: { canvas: p.canvas, page: p.primary, primary: p.primary, secondary: p.secondary, elevated: p.elevated },
    text: {
      primary: p.text,
      // 4:1 keeps secondary text distinct from primary on low-contrast palettes.
      secondary: readable(p.textSecondary, bg, mode, 4),
      disabled: p.textDisabled,
      maxContrast: mode === 'dark' ? '#ffffff' : '#000000',
    },
    border: { weak: p.borderWeak, medium: p.borderMedium, strong: p.borderStrong },
    secondary: {
      main: secondaryMain,
      shade: emphasize(secondaryMain, 0.08),
      text: p.text,
      contrastText: p.text,
      border: p.borderWeak,
      transparent: alpha(p.text, 0.08),
    },
    action: {
      hover: alpha(p.text, 0.12),
      selected: alpha(p.text, 0.08),
      focus: alpha(p.text, 0.12),
      disabledBackground: alpha(p.text, 0.04),
      disabledText: p.textDisabled,
    },
    success: status(p.success),
    warning: status(p.warning),
    error: status(p.error),
    info: status(p.info ?? p.accent),
    scrollbar: alpha(p.text, 0.3),
  }
}

const cache = new Map<string, GrafanaTheme2>()

/** Builds (and memoizes) the Grafana theme for a theme definition and accent.
 * The accent replaces Grafana's primary color, link color, and orange brand
 * gradient / selected-border bars (Tabs, Select options, FilterPill…). */
export function createGoFiTheme(definition: ThemeDefinition, accent: AccentDefinition | undefined): GrafanaTheme2 {
  const key = `${definition.id}:${accent?.id ?? 'theme'}`
  const cached = cache.get(key)
  if (cached) return cached
  const mode = definition.mode
  const colors: ColorsInput = definition.palette ? paletteColors(definition) : { mode }
  const background = definition.palette?.primary ?? (mode === 'dark' ? '#181b1f' : '#ffffff')
  const shade = accentShade(definition, accent)
  const text = readable(shade.text, background, mode)
  const solid = `linear-gradient(90deg, ${shade.main}, ${shade.main})`
  colors.primary = { main: shade.main, text, border: text }
  colors.accent = colors.primary
  colors.text = { ...colors.text, link: text }
  colors.gradients = { brandHorizontal: solid, brandVertical: solid }
  colors.action = { ...colors.action, selectedBorder: shade.main }
  const theme = createTheme({ name: definition.name, colors, shape })
  cache.set(key, theme)
  return theme
}

function triplet(color: string, background: string): string {
  const { r, g, b } = onBackground(color, background).toRgb()
  return `${Math.round(r)} ${Math.round(g)} ${Math.round(b)}`
}

/** The `--g-*` CSS variables (index.css, tailwind.config.js) for a theme, as
 * "R G B" triplets. Translucent colors are pre-composited over the panel
 * background so Tailwind opacity modifiers keep working. */
export function cssTokens(theme: GrafanaTheme2): Record<string, string> {
  const c = theme.colors
  const bg = c.background.primary
  const entries: Record<string, string> = {
    canvas: c.background.canvas,
    primary: bg,
    secondary: c.background.secondary,
    'text-primary': c.text.primary,
    'text-secondary': c.text.secondary,
    'text-disabled': c.text.disabled,
    'accent-main': c.primary.main,
    'accent-text': c.primary.text,
    hover: c.action.hover,
    'border-weak': c.border.weak,
    'border-medium': c.border.medium,
    'border-strong': c.border.strong,
    success: c.success.main,
    'success-text': c.success.text,
    warning: c.warning.main,
    'warning-text': c.warning.text,
    error: c.error.main,
    'error-text': c.error.text,
  }
  const vars: Record<string, string> = {}
  for (const [name, color] of Object.entries(entries)) vars[`--g-${name}`] = triplet(color, bg)
  return vars
}
