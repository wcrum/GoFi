import { createTheme } from '@grafana/data'

// index.css is the palette source for Grafana, the shell, and React Flow.
const styles = getComputedStyle(document.documentElement)
const color = (name: string) => `rgb(${styles.getPropertyValue(name).trim().split(/\s+/).join(',')})`
const canvas = color('--g-canvas')
const panel = color('--g-primary')
const elevated = color('--g-secondary')
const accent = color('--g-accent-main')
const text = color('--g-text-primary')
const weakBorder = color('--g-border-weak')
const mediumBorder = color('--g-border-medium')
const strongBorder = color('--g-border-strong')
const hover = color('--g-hover')
const radius = Number.parseInt(styles.getPropertyValue('--gofi-radius'))
export const gofiTheme = createTheme({
  name: 'GoFi',
  colors: {
    mode: 'dark',
    primary: { main: accent, text: accent, contrastText: canvas },
    text: { primary: text, secondary: color('--g-text-secondary'), disabled: color('--g-text-disabled'), link: accent },
    background: { canvas, page: canvas, primary: panel, secondary: elevated, elevated },
    border: { weak: weakBorder, medium: mediumBorder, strong: strongBorder },
    action: { hover, selected: hover, selectedBorder: accent },
  },
  shape: { borderRadius: radius, borderRadiusSm: radius, borderRadiusLg: radius },
  typography: { fontFamily: styles.fontFamily },
  components: {
    input: { background: canvas, borderColor: mediumBorder, borderHover: strongBorder, text },
    panel: { background: panel, borderColor: weakBorder },
    dropdown: { background: elevated },
  },
})
