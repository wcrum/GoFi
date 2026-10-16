import './index.css'
import { StrictMode } from 'react'
import { ThemeContext } from '@grafana/data'
import { gofiTheme } from './grafana-theme'
import { createRoot } from 'react-dom/client'
import App from './App'
import { ToastProvider } from './components/Toast'

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <ThemeContext.Provider value={gofiTheme}>
    <ToastProvider>
      <App />
    </ToastProvider>
    </ThemeContext.Provider>
  </StrictMode>,
)
