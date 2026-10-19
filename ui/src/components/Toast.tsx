import { createContext, useCallback, useContext, useEffect, useRef, useState } from 'react'
import type { ReactNode } from 'react'
import { css } from '@emotion/css'
import type { GrafanaTheme2 } from '@grafana/data'
import { Alert, useStyles2 } from '@grafana/ui'
import { t } from '../i18n'

type ToastSeverity = 'error' | 'success' | 'warning'

const defaultTitle = (severity: ToastSeverity) => severity === 'error' ? t('gofi.components.toast.title-error', 'Error') : severity === 'success' ? t('gofi.components.toast.title-success', 'Success') : t('gofi.components.toast.title-warning', 'Warning')

interface Toast {
  id: string
  message: string
  severity: ToastSeverity
  title?: string
}

interface ToastContextValue {
  addToast: (message: string, severity?: ToastSeverity, title?: string) => void
}

const ToastContext = createContext<ToastContextValue | null>(null)

export function useToast() {
  const ctx = useContext(ToastContext)
  if (!ctx) throw new Error('useToast must be used inside ToastProvider')
  return ctx.addToast
}

let counter = 0

export function ToastProvider({ children }: { children: ReactNode }) {
  const [toasts, setToasts] = useState<Toast[]>([])
  const timers = useRef<Map<string, number>>(new Map())

  const dismiss = useCallback((id: string) => {
    setToasts((prev) => prev.filter((t) => t.id !== id))
    const timer = timers.current.get(id)
    if (timer) { clearTimeout(timer); timers.current.delete(id) }
  }, [])

  const addToast = useCallback((message: string, severity: ToastSeverity = 'error', title?: string) => {
    const id = String(++counter)
    setToasts((prev) => [...prev, { id, message, severity, title }])
    // Warnings ask for action, so they stay longer.
    const timer = window.setTimeout(() => dismiss(id), severity === 'warning' ? 15000 : 5000)
    timers.current.set(id, timer)
  }, [dismiss])

  useEffect(() => {
    const map = timers.current
    return () => { map.forEach(clearTimeout) }
  }, [])

  const styles = useStyles2(getStyles)
  return (
    <ToastContext.Provider value={{ addToast }}>
      {children}
      {/* A bottom-right stack of Alerts; the newest is nearest the corner. */}
      <div className={styles.list} role="status" aria-live="polite">
        {toasts.map((toast) => (
          <Alert key={toast.id} severity={toast.severity} title={toast.title ?? defaultTitle(toast.severity)} onRemove={() => dismiss(toast.id)} elevated bottomSpacing={0}>
            {toast.message}
          </Alert>
        ))}
      </div>
    </ToastContext.Provider>
  )
}

const getStyles = (theme: GrafanaTheme2) => ({
  list: css({
    position: 'fixed', bottom: theme.spacing(2), right: theme.spacing(2),
    zIndex: theme.zIndex.portal, display: 'flex', flexDirection: 'column', gap: theme.spacing(1),
    width: `min(400px, calc(100vw - ${theme.spacing(4)}))`, wordBreak: 'break-word', whiteSpace: 'pre-line',
    '& > *': { animation: 'gofi-toast-in 220ms ease-out' },
    '@keyframes gofi-toast-in': { from: { opacity: 0, transform: 'translateY(16px)' }, to: { opacity: 1, transform: 'none' } },
    '@media (prefers-reduced-motion: reduce)': { '& > *': { animation: 'none' } },
  }),
})
