import './index.css'
import { StrictMode } from 'react'
import { ThemeProvider } from './theme/ThemeProvider'
import { createRoot } from 'react-dom/client'
import App from './App'
import { ToastProvider } from './components/Toast'
import { GoFiProvider } from './state'
import { HashRouter } from 'react-router-dom-v5-compat'
import { I18nProvider, initI18n } from './i18n'

// Translations are ready before the first render (en-US is bundled, so this
// is a no-op round trip for the default locale). A failed init still renders:
// every t()/<Trans> falls back to its English default.
void initI18n().catch(error => console.error('i18n failed to initialise', error)).then(() => {
  createRoot(document.getElementById('root')!).render(
    <StrictMode>
      <ThemeProvider>
      <ToastProvider>
        <HashRouter>
          <I18nProvider>
            <GoFiProvider>
              <App />
            </GoFiProvider>
          </I18nProvider>
        </HashRouter>
      </ToastProvider>
      </ThemeProvider>
    </StrictMode>,
  )
})
