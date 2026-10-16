import './index.css'
import { StrictMode } from 'react'
import { ThemeProvider } from './theme/ThemeProvider'
import { createRoot } from 'react-dom/client'
import App from './App'
import { ToastProvider } from './components/Toast'
import { GoFiProvider } from './state'
import { HashRouter } from 'react-router-dom-v5-compat'

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <ThemeProvider>
    <ToastProvider>
      <HashRouter>
        <GoFiProvider>
          <App />
        </GoFiProvider>
      </HashRouter>
    </ToastProvider>
    </ThemeProvider>
  </StrictMode>,
)
