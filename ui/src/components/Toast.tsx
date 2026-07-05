import { createContext, useCallback, useContext, useEffect, useRef, useState } from 'react'
import type { ReactNode } from 'react'
import { RiCloseLine, RiErrorWarningLine, RiCheckboxCircleLine } from 'react-icons/ri'

type ToastSeverity = 'error' | 'success'

interface Toast {
  id: string
  message: string
  severity: ToastSeverity
}

interface ToastContextValue {
  addToast: (message: string, severity?: ToastSeverity) => void
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

  const addToast = useCallback((message: string, severity: ToastSeverity = 'error') => {
    const id = String(++counter)
    setToasts((prev) => [...prev, { id, message, severity }])
    const timer = window.setTimeout(() => dismiss(id), 5000)
    timers.current.set(id, timer)
  }, [dismiss])

  useEffect(() => {
    const map = timers.current
    return () => { map.forEach(clearTimeout) }
  }, [])

  return (
    <ToastContext.Provider value={{ addToast }}>
      {children}
      <div className="fixed bottom-4 right-4 z-[200] flex flex-col gap-2 pointer-events-none">
        {toasts.map((t) => (
          <ToastItem key={t.id} toast={t} onDismiss={dismiss} />
        ))}
      </div>
    </ToastContext.Provider>
  )
}

function ToastItem({ toast, onDismiss }: { toast: Toast; onDismiss: (id: string) => void }) {
  const Icon = toast.severity === 'error' ? RiErrorWarningLine : RiCheckboxCircleLine
  const accent = toast.severity === 'error' ? 'text-red-400' : 'text-green-400'
  const border = toast.severity === 'error' ? 'border-red-500/30' : 'border-green-500/30'

  return (
    <div
      className={`pointer-events-auto flex items-start gap-2.5 min-w-64 max-w-sm px-3.5 py-3 rounded-lg border bg-g-secondary shadow-lg ${border}`}
    >
      <Icon className={`shrink-0 mt-0.5 text-base ${accent}`} />
      <span className="flex-1 text-sm text-g-text leading-snug">{toast.message}</span>
      <button
        onClick={() => onDismiss(toast.id)}
        className="shrink-0 text-g-text-secondary hover:text-g-text transition-colors"
      >
        <RiCloseLine size={16} />
      </button>
    </div>
  )
}
