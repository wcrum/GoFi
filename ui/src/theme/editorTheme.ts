import type { GrafanaTheme2 } from '@grafana/data'
import type { editor } from 'monaco-editor'
import type { SyntaxColors, ThemeDefinition } from './types'

// Monaco theme data for the selected app theme and accent. This module must
// stay free of runtime Monaco (and @grafana/data) imports: ThemeProvider calls
// setEditorTheme from the main bundle, and src/lib/monaco.ts (loaded with the
// editors) applies the data whenever Grafana defines its editor themes.

type ThemeData = editor.IStandaloneThemeData
type TokenRule = editor.ITokenThemeRule

/** The subset of GrafanaTheme2 the builder reads; keeps tests light. */
export type EditorThemeSource = {
  isDark: boolean
  colors: {
    primary: { main: string; text?: string; border?: string }
    text: { primary: string; secondary: string; disabled: string }
    background: { canvas: string; primary: string; secondary: string; elevated?: string }
    border: { weak: string; medium: string; strong: string }
    action: { hover: string; selected: string }
  }
  components: { input: { background: string } }
  visualization: { getColorByName: (name: string) => string }
}

interface Rgba { r: number; g: number; b: number; a: number }

const hex2 = (n: number) => Math.round(Math.min(255, Math.max(0, n))).toString(16).padStart(2, '0')

/** Parses #rgb, #rgba, #rrggbb, #rrggbbaa, rgb() and rgba() (comma or space
 * syntax, with optional "/ alpha"). Returns undefined for anything else. */
export function parseColor(color: string | undefined): Rgba | undefined {
  if (!color) return undefined
  const value = color.trim().toLowerCase()
  if (value.startsWith('#')) {
    let digits = value.slice(1)
    if (!/^[0-9a-f]+$/.test(digits)) return undefined
    if (digits.length === 3 || digits.length === 4) digits = [...digits].map(d => d + d).join('')
    if (digits.length !== 6 && digits.length !== 8) return undefined
    const channel = (i: number) => parseInt(digits.slice(i, i + 2), 16)
    return { r: channel(0), g: channel(2), b: channel(4), a: digits.length === 8 ? channel(6) / 255 : 1 }
  }
  const match = /^rgba?\(([^)]*)\)$/.exec(value)
  if (!match) return undefined
  const parts = match[1].split(/[\s,/]+/).filter(Boolean)
  if (parts.length < 3 || parts.length > 4) return undefined
  const number = (part: string, scale: number) => part.endsWith('%') ? parseFloat(part) / 100 * scale : parseFloat(part)
  const [r, g, b] = parts.slice(0, 3).map(part => number(part, 255))
  const a = parts[3] === undefined ? 1 : number(parts[3], 1)
  if ([r, g, b, a].some(Number.isNaN)) return undefined
  return { r, g, b, a: Math.min(1, Math.max(0, a)) }
}

function format({ r, g, b, a }: Rgba): string {
  const base = `#${hex2(r)}${hex2(g)}${hex2(b)}`
  return a >= 1 ? base : base + hex2(a * 255)
}

/** Normalizes a CSS color to #rrggbb, or #rrggbbaa when translucent. */
export function toMonacoColor(color: string | undefined): string | undefined {
  const parsed = parseColor(color)
  return parsed && format(parsed)
}

/** Composites `color` over `background` into an opaque #rrggbb. */
export function opaqueColor(color: string | undefined, background: string | undefined): string | undefined {
  const fg = parseColor(color)
  if (!fg) return undefined
  const bg = parseColor(background) ?? { r: 0, g: 0, b: 0, a: 1 }
  const mix = (f: number, b: number) => f * fg.a + b * (1 - fg.a)
  return format({ r: mix(fg.r, bg.r), g: mix(fg.g, bg.g), b: mix(fg.b, bg.b), a: 1 })
}

/** Sets the alpha of `color` (any parsed format) and returns #rrggbbaa. */
export function withAlpha(color: string | undefined, alpha: number): string | undefined {
  const parsed = parseColor(color)
  return parsed && format({ ...parsed, a: parsed.a * alpha })
}

/** Token foreground for a Monaco rule: hex without '#', alpha dropped. */
function tokenColor(color: string | undefined, background: string): string | undefined {
  return opaqueColor(color, background)?.slice(1)
}

function syntaxRules(syntax: SyntaxColors, background: string): TokenRule[] {
  const rules: TokenRule[] = []
  const add = (color: string, fontStyle: string | undefined, ...tokens: string[]) => {
    const foreground = tokenColor(color, background)
    if (!foreground) return
    for (const token of tokens) rules.push(fontStyle ? { token, foreground, fontStyle } : { token, foreground })
  }
  // Monaco matches rules by token prefix with the longest match winning, so
  // `string` covers string.yaml and string.value.json while string.key.json
  // and string.escape get their own colors.
  add(syntax.variable, undefined, '', 'identifier', 'variable', 'variable.name', 'variable.parameter')
  add(syntax.comment, 'italic', 'comment', 'comment.doc')
  add(syntax.keyword, undefined, 'keyword', 'keyword.flow', 'keyword.control', 'storage', 'storage.type')
  add(syntax.string, undefined, 'string', 'string.value.json', 'string.quoted', 'string.heredoc', 'attribute.value')
  add(syntax.constant, undefined, 'string.escape', 'constant', 'keyword.json', 'predefined', 'variable.predefined', 'variable.language')
  add(syntax.number, undefined, 'number', 'number.float', 'number.octal', 'number.binary')
  add(syntax.type, undefined, 'type', 'type.identifier', 'type.yaml', 'class', 'namespace', 'annotation', 'metatag')
  add(syntax.function, undefined, 'function', 'entity.name.function', 'support.function', 'identifier.function')
  add(syntax.operator, undefined, 'operator', 'operators', 'operators.yaml', 'keyword.operator')
  add(syntax.delimiter, undefined, 'delimiter', 'delimiter.bracket', 'delimiter.parenthesis', 'delimiter.square', 'delimiter.curly', 'delimiter.angle', '@brackets')
  add(syntax.tag, undefined, 'tag', 'metatag.html')
  add(syntax.attribute, undefined, 'attribute.name', 'key', 'string.key', 'string.key.json')
  add(syntax.regexp, undefined, 'regexp', 'string.regexp')
  return rules
}

/** Grafana's fallback rules, used for its stock themes (no syntax palette). */
function grafanaRules(theme: EditorThemeSource): TokenRule[] {
  const rules: TokenRule[] = []
  const add = (token: string, name: string) => {
    const foreground = toMonacoColor(theme.visualization.getColorByName(name))?.slice(1, 7)
    if (foreground) rules.push({ token, foreground })
  }
  add('predefined', 'purple')
  add('operator', 'orange')
  add('tag', 'green')
  return rules
}

/** Builds the Monaco theme for an app theme. `theme` already carries the
 * selected accent in colors.primary. */
export function buildEditorTheme(definition: ThemeDefinition, theme: EditorThemeSource): ThemeData {
  const { colors } = theme
  const dark = definition.mode === 'dark'
  const canvas = opaqueColor(colors.background.primary, dark ? '#000000' : '#ffffff') ?? (dark ? '#1e1e1e' : '#ffffff')
  const background = opaqueColor(definition.syntax?.background ?? theme.components.input.background, canvas) ?? canvas
  const widget = opaqueColor(colors.background.elevated ?? colors.background.secondary, background) ?? background
  const foreground = opaqueColor(colors.text.primary, background)
  const accent = colors.primary.main
  const subtle = opaqueColor(colors.text.disabled, background)
  const secondary = opaqueColor(colors.text.secondary, background)
  const borderWeak = toMonacoColor(colors.border.weak)
  const borderMedium = toMonacoColor(colors.border.medium)
  const hover = toMonacoColor(colors.action.hover)
  const selected = toMonacoColor(colors.action.selected)
  const accentSelection = withAlpha(accent, dark ? 0.32 : 0.22)

  const entries: Record<string, string | undefined> = {
    'focusBorder': toMonacoColor(accent),
    'editor.background': background,
    'editor.foreground': foreground,
    'editorGutter.background': background,
    'editorLineNumber.foreground': subtle,
    'editorLineNumber.activeForeground': secondary,
    'editor.lineHighlightBackground': hover,
    'editor.lineHighlightBorder': withAlpha(colors.border.weak, 0.5),
    'editor.selectionBackground': accentSelection,
    'editor.inactiveSelectionBackground': withAlpha(accent, dark ? 0.16 : 0.12),
    'editor.selectionHighlightBackground': withAlpha(accent, 0.14),
    'editor.wordHighlightBackground': withAlpha(accent, 0.12),
    'editor.findMatchBackground': withAlpha(accent, 0.4),
    'editor.findMatchHighlightBackground': withAlpha(accent, 0.2),
    'editorCursor.foreground': toMonacoColor(accent),
    'editorIndentGuide.background': borderWeak,
    'editorIndentGuide.activeBackground': borderMedium,
    'editorWhitespace.foreground': borderWeak,
    'editorBracketMatch.background': withAlpha(accent, 0.18),
    'editorBracketMatch.border': withAlpha(accent, 0.6),
    'editorWidget.background': widget,
    'editorWidget.foreground': foreground,
    'editorWidget.border': borderMedium,
    'editorSuggestWidget.background': widget,
    'editorSuggestWidget.foreground': foreground,
    'editorSuggestWidget.border': borderMedium,
    'editorSuggestWidget.selectedBackground': opaqueColor(withAlpha(accent, dark ? 0.28 : 0.18), widget),
    'editorSuggestWidget.highlightForeground': toMonacoColor(colors.primary.text ?? accent),
    'editorHoverWidget.background': widget,
    'editorHoverWidget.foreground': foreground,
    'editorHoverWidget.border': borderMedium,
    'list.hoverBackground': hover,
    'list.activeSelectionBackground': selected,
    'minimap.background': opaqueColor(colors.background.secondary, background),
    'scrollbarSlider.background': withAlpha(colors.text.secondary, 0.2),
    'scrollbarSlider.hoverBackground': withAlpha(colors.text.secondary, 0.32),
    'scrollbarSlider.activeBackground': withAlpha(accent, 0.5),
  }
  const themeColors: Record<string, string> = {}
  for (const [key, value] of Object.entries(entries)) if (value) themeColors[key] = value

  return {
    base: dark ? 'vs-dark' : 'vs',
    inherit: true,
    rules: definition.syntax ? syntaxRules(definition.syntax, background) : grafanaRules(theme),
    colors: themeColors,
  }
}

let current: ThemeData | undefined
let currentKey = ''
const listeners = new Set<() => void>()

/** The Monaco theme for the selected app theme, once ThemeProvider set one. */
export function getEditorTheme(): ThemeData | undefined {
  return current
}

/** Subscribes to editor theme changes; returns the unsubscribe function. */
export function subscribeEditorTheme(listener: () => void): () => void {
  listeners.add(listener)
  return () => { listeners.delete(listener) }
}

/** Rebuilds the editor theme; notifies subscribers only when it changed.
 * Safe to call during render. */
export function setEditorTheme(definition: ThemeDefinition, theme: GrafanaTheme2 | EditorThemeSource): void {
  const next = buildEditorTheme(definition, theme as EditorThemeSource)
  const key = JSON.stringify(next)
  if (key === currentKey) return
  current = next
  currentKey = key
  for (const listener of listeners) listener()
}
