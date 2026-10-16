import { createTheme } from '@grafana/data'

// Grafana owns control behavior and styling; GoFi supplies its established
// palette and the same radius token used by the canvas and workspace surfaces.
const radius = Number.parseInt(getComputedStyle(document.documentElement).getPropertyValue('--gofi-radius')) || 4
export const gofiTheme = createTheme({
  name: 'GoFi',
  colors: {
    mode: 'dark',
    primary: { main: '#2ec1a6', text: '#2ec1a6', contrastText: '#0a121e' },
    text: { primary: '#e7eef8', secondary: '#96a8be', disabled: '#64778f', link: '#2ec1a6' },
    background: { canvas: '#0a121e', page: '#0a121e', primary: '#0f1a2a', secondary: '#142335', elevated: '#142335' },
    border: { weak: '#24354a', medium: '#3a506a', strong: '#7087a2' },
    action: { hover: '#1e3045', selected: '#1e3045', selectedBorder: '#2ec1a6' },
  },
  shape: { borderRadius: radius, borderRadiusSm: radius, borderRadiusLg: radius },
  typography: { fontFamily: "Inter, ui-sans-serif, system-ui, -apple-system, BlinkMacSystemFont, 'Segoe UI', sans-serif" },
  components: {
    input: { background: '#0a121e', borderColor: '#3a506a', borderHover: '#7087a2', text: '#e7eef8' },
    panel: { background: '#0f1a2a', borderColor: '#24354a' },
    dropdown: { background: '#142335' },
  },
})
