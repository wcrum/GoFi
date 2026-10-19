import { useEffect, useRef, useState, type PointerEvent as ReactPointerEvent, type ReactNode } from 'react'
import { css } from '@emotion/css'
import type { GrafanaTheme2 } from '@grafana/data'
import { useStyles2 } from '@grafana/ui'

interface Rect { x: number; y: number; w: number; h: number }
const MIN_W = 320
const MIN_H = 320

const clamp = (rect: Rect): Rect => {
  const w = Math.max(MIN_W, Math.min(rect.w, window.innerWidth))
  const h = Math.max(MIN_H, Math.min(rect.h, window.innerHeight))
  return { w, h, x: Math.max(0, Math.min(rect.x, window.innerWidth - w)), y: Math.max(0, Math.min(rect.y, window.innerHeight - h)) }
}
function readRect(key: string): Rect {
  const fallback: Rect = { w: 440, h: Math.min(640, window.innerHeight - 96), x: window.innerWidth - 440 - 24, y: 72 }
  try {
    const stored = JSON.parse(localStorage.getItem(key) ?? '') as Partial<Rect>
    if ([stored.x, stored.y, stored.w, stored.h].every(value => typeof value === 'number' && Number.isFinite(value))) return clamp(stored as Rect)
  } catch { /* nothing stored */ }
  return clamp(fallback)
}
const saveRect = (key: string, rect: Rect) => { try { localStorage.setItem(key, JSON.stringify(rect)) } catch { /* storage unavailable */ } }

interface Props {
  /** localStorage key for the remembered position and size. */
  storageKey: string
  ariaLabel: string
  /** The content; `drag` goes on whatever should move the window (a header). */
  children: (drag: { onPointerDown: (event: ReactPointerEvent<HTMLElement>) => void }) => ReactNode
}

/** A floating window over the page (no backdrop, so the page stays usable):
 * dragged by its handle, resized from its bottom-right corner, position and
 * size remembered. It stays within the viewport. */
export function FloatingWindow({ storageKey, ariaLabel, children }: Props) {
  const styles = useStyles2(getStyles)
  const [rect, setRect] = useState(() => readRect(storageKey))
  const ref = useRef<HTMLDivElement>(null)
  const latest = useRef(rect)
  latest.current = rect

  // The CSS resize handle changes width/height outside React: observe and
  // remember. Border-box sizes, as the inline width/height are (contentRect
  // would be smaller by the border and shrink the window every cycle).
  useEffect(() => {
    const element = ref.current
    if (!element || typeof ResizeObserver === 'undefined') return
    const observer = new ResizeObserver(() => {
      const w = Math.round(element.offsetWidth), h = Math.round(element.offsetHeight)
      if (!w || !h) return
      if (w === latest.current.w && h === latest.current.h) return
      const next = clamp({ ...latest.current, w, h })
      setRect(next); saveRect(storageKey, next)
    })
    observer.observe(element)
    return () => observer.disconnect()
  }, [storageKey])
  useEffect(() => {
    const onResize = () => setRect(current => { const next = clamp(current); return next.x === current.x && next.y === current.y && next.w === current.w && next.h === current.h ? current : next })
    window.addEventListener('resize', onResize)
    return () => window.removeEventListener('resize', onResize)
  }, [])

  const onPointerDown = (event: ReactPointerEvent<HTMLElement>) => {
    if (event.button !== 0 || (event.target as HTMLElement).closest('button, a, input, textarea, select')) return
    event.preventDefault()
    const handle = event.currentTarget
    const start = { x: event.clientX - latest.current.x, y: event.clientY - latest.current.y }
    handle.setPointerCapture(event.pointerId)
    const move = (e: PointerEvent) => setRect(current => clamp({ ...current, x: e.clientX - start.x, y: e.clientY - start.y }))
    const up = () => {
      handle.removeEventListener('pointermove', move); handle.removeEventListener('pointerup', up); handle.removeEventListener('pointercancel', up)
      saveRect(storageKey, latest.current)
    }
    handle.addEventListener('pointermove', move); handle.addEventListener('pointerup', up); handle.addEventListener('pointercancel', up)
  }

  return <div ref={ref} className={styles.window} style={{ left: rect.x, top: rect.y, width: rect.w, height: rect.h }} role="dialog" aria-label={ariaLabel} aria-modal={false}>
    {children({ onPointerDown })}
  </div>
}

const getStyles = (theme: GrafanaTheme2) => ({
  window: css({
    position: 'fixed', zIndex: theme.zIndex.navbarFixed + 2, display: 'flex', flexDirection: 'column', minWidth: MIN_W, minHeight: MIN_H, maxWidth: '100vw', maxHeight: '100vh',
    background: theme.colors.background.primary, border: `1px solid ${theme.colors.border.medium}`, borderRadius: theme.shape.radius.default, boxShadow: theme.shadows.z3,
    overflow: 'hidden', resize: 'both',
  }),
})
