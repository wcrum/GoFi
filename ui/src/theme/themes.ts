import type { AccentDefinition, SyntaxColors, ThemeDefinition, ThemePalette } from './types'

// Compact constructors: the positional order matches the palette tables the
// themes are usually published in, which keeps each entry to a few lines.
function palette(
  [canvas, primary, secondary, elevated]: [string, string, string, string],
  [text, textSecondary, textDisabled]: [string, string, string],
  [borderWeak, borderMedium, borderStrong]: [string, string, string],
  [accent, success, warning, error]: [string, string, string, string],
  extra: Partial<Pick<ThemePalette, 'accentText' | 'info'>> = {},
): ThemePalette {
  return {
    canvas, primary, secondary, elevated, text, textSecondary, textDisabled,
    borderWeak, borderMedium, borderStrong, accent, success, warning, error, ...extra,
  }
}

function syntax(
  comment: string, keyword: string, string: string, number: string, type: string, fn: string,
  variable: string, constant: string, operator: string, delimiter: string, tag: string,
  attribute: string, regexp: string,
): SyntaxColors {
  return { comment, keyword, string, number, type, function: fn, variable, constant, operator, delimiter, tag, attribute, regexp }
}

const oneDark = palette(
  ['#21252b', '#282c34', '#2c313a', '#333842'], ['#abb2bf', '#8b929e', '#5c6370'],
  ['#3a3f4b', '#4b5263', '#5c6370'], ['#61afef', '#98c379', '#e5c07b', '#e06c75'])
const monokai = palette(
  ['#1e1f1c', '#272822', '#31322c', '#3e3d32'], ['#f8f8f2', '#a6a28c', '#75715e'],
  ['#3b3c35', '#49483e', '#75715e'], ['#66d9ef', '#a6e22e', '#fd971f', '#f92672'])
const githubDark = palette(
  ['#010409', '#0d1117', '#161b22', '#161b22'], ['#e6edf3', '#9198a1', '#6e7681'],
  ['#21262d', '#30363d', '#484f58'], ['#1f6feb', '#3fb950', '#d29922', '#f85149'], { accentText: '#4493f8' })
const githubLight = palette(
  ['#f6f8fa', '#ffffff', '#f6f8fa', '#ffffff'], ['#1f2328', '#59636e', '#818b98'],
  ['#d8dee4', '#d1d9e0', '#8c959f'], ['#0969da', '#1a7f37', '#9a6700', '#d1242f'])
const vscodeDark = palette(
  ['#181818', '#1f1f1f', '#252526', '#2b2b2b'], ['#cccccc', '#9d9d9d', '#6e7681'],
  ['#2b2b2b', '#3c3c3c', '#616161'], ['#0078d4', '#2ea043', '#cca700', '#f85149'], { accentText: '#4daafc' })
const vscodeLight = palette(
  ['#f8f8f8', '#ffffff', '#f8f8f8', '#ffffff'], ['#3b3b3b', '#616161', '#8b949e'],
  ['#e5e5e5', '#cecece', '#a0a0a0'], ['#005fb8', '#388a34', '#bf8803', '#e51400'])
const nord = palette(
  ['#242933', '#2e3440', '#3b4252', '#3b4252'], ['#eceff4', '#a3abb9', '#616e88'],
  ['#3f4758', '#434c5e', '#4c566a'], ['#88c0d0', '#a3be8c', '#ebcb8b', '#bf616a'], { info: '#81a1c1' })
const dracula = palette(
  ['#21222c', '#282a36', '#343746', '#343746'], ['#f8f8f2', '#a4a7bd', '#6272a4'],
  ['#3a3c4e', '#44475a', '#6272a4'], ['#bd93f9', '#50fa7b', '#ffb86c', '#ff5555'], { info: '#8be9fd' })
const solarizedDark = palette(
  ['#00212b', '#002b36', '#073642', '#073642'], ['#93a1a1', '#839496', '#586e75'],
  ['#0b3a46', '#2b4f59', '#586e75'], ['#268bd2', '#859900', '#b58900', '#dc322f'], { accentText: '#3d9ee0' })
// Solarized blue sits under 4.5:1 on base3, so links and accent text use a
// deeper tone of the same hue.
const solarizedLight = palette(
  ['#eee8d5', '#fdf6e3', '#eee8d5', '#fdf6e3'], ['#586e75', '#657b83', '#93a1a1'],
  ['#e4ddc8', '#d3cbb7', '#93a1a1'], ['#268bd2', '#859900', '#b58900', '#dc322f'], { accentText: '#1a6aa3' })
const catppuccinMocha = palette(
  ['#181825', '#1e1e2e', '#2a2b3c', '#313244'], ['#cdd6f4', '#a6adc8', '#6c7086'],
  ['#313244', '#45475a', '#585b70'], ['#cba6f7', '#a6e3a1', '#fab387', '#f38ba8'], { info: '#89b4fa' })
const gruvboxDark = palette(
  ['#1d2021', '#282828', '#32302f', '#3c3836'], ['#ebdbb2', '#bdae93', '#7c6f64'],
  ['#3c3836', '#504945', '#665c54'], ['#83a598', '#b8bb26', '#fabd2f', '#fb4934'])
const tokyoNight = palette(
  ['#16161e', '#1a1b26', '#1f2335', '#24283b'], ['#c0caf5', '#a9b1d6', '#565f89'],
  ['#232433', '#2f3549', '#414868'], ['#7aa2f7', '#9ece6a', '#e0af68', '#f7768e'])

const solarizedSyntax = (attribute: string, delimiter: string) => syntax(
  '#586e75', '#859900', '#2aa198', '#d33682', '#b58900', '#268bd2', '#268bd2', '#cb4b16', '#859900',
  delimiter, '#268bd2', attribute, '#dc322f')

/** Every selectable theme, dark themes first. Grafana's stock themes have no
 * palette: createTheme's built-in colors and Monaco's vs / vs-dark apply. */
export const THEMES: ThemeDefinition[] = [
  { id: 'grafana-dark', name: 'Grafana Dark', mode: 'dark', family: 'Grafana' },
  {
    id: 'one-dark', name: 'One Dark', mode: 'dark', family: 'Atom', palette: oneDark,
    syntax: syntax('#7f848e', '#c678dd', '#98c379', '#d19a66', '#e5c07b', '#61afef', '#e06c75', '#d19a66', '#56b6c2', oneDark.text, '#e06c75', '#d19a66', '#56b6c2'),
  },
  {
    id: 'monokai', name: 'Monokai', mode: 'dark', family: 'Monokai', palette: monokai,
    syntax: syntax('#75715e', '#f92672', '#e6db74', '#ae81ff', '#66d9ef', '#a6e22e', '#f8f8f2', '#ae81ff', '#f92672', monokai.text, '#f92672', '#a6e22e', '#e6db74'),
  },
  {
    id: 'github-dark', name: 'GitHub Dark', mode: 'dark', family: 'GitHub', palette: githubDark,
    syntax: syntax('#8b949e', '#ff7b72', '#a5d6ff', '#79c0ff', '#ffa657', '#d2a8ff', '#ffa657', '#79c0ff', '#ff7b72', githubDark.text, '#7ee787', '#79c0ff', '#7ee787'),
  },
  {
    id: 'vscode-dark', name: 'VS Code Dark Modern', mode: 'dark', family: 'VS Code', palette: vscodeDark,
    syntax: syntax('#6a9955', '#569cd6', '#ce9178', '#b5cea8', '#4ec9b0', '#dcdcaa', '#9cdcfe', '#4fc1ff', '#d4d4d4', vscodeDark.text, '#569cd6', '#9cdcfe', '#d16969'),
  },
  {
    id: 'nord', name: 'Nord', mode: 'dark', family: 'Nord', palette: nord,
    syntax: syntax('#616e88', '#81a1c1', '#a3be8c', '#b48ead', '#8fbcbb', '#88c0d0', '#d8dee9', '#81a1c1', '#81a1c1', nord.text, '#81a1c1', '#8fbcbb', '#ebcb8b'),
  },
  {
    id: 'dracula', name: 'Dracula', mode: 'dark', family: 'Dracula', palette: dracula,
    syntax: syntax('#6272a4', '#ff79c6', '#f1fa8c', '#bd93f9', '#8be9fd', '#50fa7b', '#f8f8f2', '#bd93f9', '#ff79c6', dracula.text, '#ff79c6', '#50fa7b', '#ff5555'),
  },
  {
    id: 'solarized-dark', name: 'Solarized Dark', mode: 'dark', family: 'Solarized', palette: solarizedDark,
    syntax: solarizedSyntax('#93a1a1', solarizedDark.text),
  },
  {
    id: 'catppuccin-mocha', name: 'Catppuccin Mocha', mode: 'dark', family: 'Catppuccin', palette: catppuccinMocha,
    syntax: syntax('#7f849c', '#cba6f7', '#a6e3a1', '#fab387', '#f9e2af', '#89b4fa', '#cdd6f4', '#fab387', '#89dceb', catppuccinMocha.text, '#89b4fa', '#f9e2af', '#f5c2e7'),
  },
  {
    id: 'gruvbox-dark', name: 'Gruvbox Dark', mode: 'dark', family: 'Gruvbox', palette: gruvboxDark,
    syntax: syntax('#928374', '#fb4934', '#b8bb26', '#d3869b', '#fabd2f', '#8ec07c', '#83a598', '#d3869b', '#fe8019', gruvboxDark.text, '#8ec07c', '#fabd2f', '#fe8019'),
  },
  {
    id: 'tokyo-night', name: 'Tokyo Night', mode: 'dark', family: 'Tokyo Night', palette: tokyoNight,
    syntax: syntax('#565f89', '#bb9af7', '#9ece6a', '#ff9e64', '#2ac3de', '#7aa2f7', '#c0caf5', '#ff9e64', '#89ddff', tokyoNight.text, '#f7768e', '#bb9af7', '#b4f9f8'),
  },
  { id: 'grafana-light', name: 'Grafana Light', mode: 'light', family: 'Grafana' },
  {
    id: 'github-light', name: 'GitHub Light', mode: 'light', family: 'GitHub', palette: githubLight,
    syntax: syntax('#6e7781', '#cf222e', '#0a3069', '#0550ae', '#953800', '#8250df', '#953800', '#0550ae', '#cf222e', githubLight.text, '#116329', '#0550ae', '#116329'),
  },
  {
    id: 'vscode-light', name: 'VS Code Light Modern', mode: 'light', family: 'VS Code', palette: vscodeLight,
    syntax: syntax('#008000', '#0000ff', '#a31515', '#098658', '#267f99', '#795e26', '#001080', '#0070c1', '#000000', vscodeLight.text, '#800000', '#e50000', '#811f3f'),
  },
  {
    id: 'solarized-light', name: 'Solarized Light', mode: 'light', family: 'Solarized', palette: solarizedLight,
    syntax: solarizedSyntax('#586e75', solarizedLight.text),
  },
]

export const DEFAULT_ACCENT = 'gofi'

/** Accent presets. "theme" has no shades and uses the theme's own accent. */
export const ACCENTS: AccentDefinition[] = [
  { id: 'theme', name: 'Theme default' },
  { id: 'gofi', name: 'GoFi teal', dark: { main: '#2ec1a6', text: '#4fd8bf' }, light: { main: '#0e8a74', text: '#0b7a66' } },
  { id: 'blue', name: 'Blue', dark: { main: '#3d71d9', text: '#6e9fff' }, light: { main: '#1f62e0', text: '#1f62e0' } },
  { id: 'purple', name: 'Purple', dark: { main: '#a970ff', text: '#c39bff' }, light: { main: '#7b3fd1', text: '#6e32c4' } },
  { id: 'pink', name: 'Pink', dark: { main: '#e04f9f', text: '#f07ab8' }, light: { main: '#c4307f', text: '#b02470' } },
  { id: 'red', name: 'Red', dark: { main: '#e5484d', text: '#ff6b6f' }, light: { main: '#c62a2f', text: '#b8252a' } },
  { id: 'orange', name: 'Orange', dark: { main: '#ff8833', text: '#ffa463' }, light: { main: '#c45a00', text: '#ad4f00' } },
  { id: 'yellow', name: 'Yellow', dark: { main: '#e9b949', text: '#f2cc6b' }, light: { main: '#946c00', text: '#7a5900' } },
  { id: 'green', name: 'Green', dark: { main: '#4fb460', text: '#73cc82' }, light: { main: '#1a7f37', text: '#1a7f37' } },
]

export function findTheme(id: string | null | undefined): ThemeDefinition | undefined {
  return id ? THEMES.find(theme => theme.id === id) : undefined
}

export function findAccent(id: string | null | undefined): AccentDefinition | undefined {
  return id ? ACCENTS.find(accent => accent.id === id) : undefined
}
