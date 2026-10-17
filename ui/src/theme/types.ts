export type ColorMode = 'light' | 'dark'

/** Opaque hex colors for a non-Grafana theme. Grafana's stock themes omit the
 * palette and use createTheme's built-in colors. */
export interface ThemePalette {
  canvas: string
  primary: string
  secondary: string
  elevated: string
  text: string
  textSecondary: string
  textDisabled: string
  borderWeak: string
  borderMedium: string
  borderStrong: string
  /** The theme's own accent, used when the accent preference is "theme". */
  accent: string
  accentText?: string
  success: string
  warning: string
  error: string
  info?: string
}

/** Monaco token colors (hex). Rules match Monaco token prefixes, so `string`
 * also covers `string.yaml`, `string.key.json`, and so on. */
export interface SyntaxColors {
  comment: string
  keyword: string
  string: string
  number: string
  type: string
  function: string
  variable: string
  constant: string
  operator: string
  delimiter: string
  tag: string
  attribute: string
  regexp: string
  /** Optional editor surface override; defaults to the input background. */
  background?: string
}

export interface ThemeDefinition {
  id: string
  name: string
  mode: ColorMode
  /** Grouping label in the picker, e.g. "Grafana", "GitHub", "VS Code". */
  family: string
  palette?: ThemePalette
  /** Omitted for Grafana's stock themes: Monaco's vs / vs-dark rules apply. */
  syntax?: SyntaxColors
}

export interface AccentShade { main: string; text: string }

/** A selectable accent. The "theme" accent has no shades: it uses the
 * theme's own accent (Grafana blue for the stock themes). */
export interface AccentDefinition {
  id: string
  name: string
  dark?: AccentShade
  light?: AccentShade
}
