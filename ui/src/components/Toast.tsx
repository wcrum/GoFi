import { createContext, useCallback, useContext, useEffect, useRef, useState } from 'react'
import type { ReactNode } from 'react'
import { css } from '@emotion/css'
import type { GrafanaTheme2 } from '@grafana/data'
import { Alert, useStyles2 } from '@grafana/ui'

type ToastSeverity = 'error' | 'success' | 'warning'

const TITLES: Record<ToastSeverity, string> = { error: 'Error', success: 'Success', warning: 'Warning' }

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
      {/* Grafana's AppNotificationList: a top-right stack of Alerts. */}
      <div className={styles.list} role="status" aria-live="polite">
        {toasts.map((t) => (
          <Alert key={t.id} severity={t.severity} title={t.title ?? TITLES[t.severity]} onRemove={() => dismiss(t.id)} elevated bottomSpacing={0}>
            {t.message}
          </Alert>
        ))}
      </div>
    </ToastContext.Provider>
  )
}

const getStyles = (theme: GrafanaTheme2) => ({
  list: css({
    position: 'fixed', top: `calc(var(--gofi-topbar-height) + ${theme.spacing(1)})`, right: theme.spacing(2),
    zIndex: theme.zIndex.portal, display: 'flex', flexDirection: 'column', gap: theme.spacing(1),
    width: `min(400px, calc(100vw - ${theme.spacing(4)}))`, wordBreak: 'break-word', whiteSpace: 'pre-line',
  }),
})
