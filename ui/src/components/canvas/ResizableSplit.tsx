import { useEffect, useState, type CSSProperties, type ReactNode } from 'react'
import { css } from '@emotion/css'
import { useSplitter, useTheme2 } from '@grafana/ui'
import { t } from '../../i18n'

interface ResizableSplitProps {
  /** Which side holds the resizable panel. */
  side: 'left' | 'right'
  /** The panel; when null only `main` renders (no empty strip). */
  panel: ReactNode | null
  main: ReactNode
  /** localStorage key for the panel's share of the width. */
  storageKey: string
  /** Default panel share of the container width (0–1). */
  defaultShare: number
  /** The handle straddles the seam instead of holding the panes 16px apart
   * (the panel's own border is the divider, as with Grafana's Drawer). */
  flush?: boolean
  /** The narrowest the panel may be dragged, in px. Without it the splitter
   * floors the panel at its content's min-content width, which for a form
   * can be the whole default width, so the panel only ever grows. */
  minPanelWidth?: number
}

const MIN_SHARE = 0.15
const MAX_SHARE = 0.7

function readShare(key: string, fallback: number): number {
  try {
    const value = Number(localStorage.getItem(key))
    return value >= MIN_SHARE && value <= MAX_SHARE ? value : fallback
  } catch {
    return fallback
  }
}

/** Grafana's splitter (the same drag handle as Explore and dashboard edit
 * panes) between the flow canvas and a side panel, or the page and the
 * assistant. Narrow screens keep the panels' own overlay layout. A null
 * panel hides its pane without moving `main` in the tree, so a page keeps
 * its state while the panel opens and closes. */
export function ResizableSplit(props: ResizableSplitProps) {
  const theme = useTheme2()
  const query = `(min-width: ${theme.breakpoints.values.md}px)`
  const [wide, setWide] = useState(() => window.matchMedia(query).matches)
  useEffect(() => {
    const media = window.matchMedia(query)
    const onChange = () => setWide(media.matches)
    media.addEventListener('change', onChange)
    return () => media.removeEventListener('change', onChange)
  }, [query])
  if (!wide) return <div className={fill}>{props.panel}{props.main}</div>
  return <Split {...props} />
}

function Split({ side, panel, main, storageKey, defaultShare, flush, minPanelWidth }: ResizableSplitProps) {
  const share = readShare(storageKey, defaultShare)
  // The splitter sizes its first pane: the panel on the left, the main area on the right.
  const { containerProps, primaryProps, secondaryProps, splitterProps } = useSplitter({
    direction: 'row',
    initialSize: side === 'left' ? share : 1 - share,
    dragPosition: 'middle',
    onSizeChanged: flexSize => {
      const next = side === 'left' ? flexSize : 1 - flexSize
      try { localStorage.setItem(storageKey, String(Math.min(MAX_SHARE, Math.max(MIN_SHARE, next)))) } catch { /* storage unavailable */ }
    },
  })
  // The sizes the splitter set stay on the panes' inline styles while the
  // panel is hidden, so reopening it restores the last width.
  const hidden = panel === null
  const panelClass = hidden ? hiddenPane : ''
  const mainClass = hidden ? fullPane : ''
  // The splitter sets min-width: min-content inline; an explicit floor replaces it on the panel pane.
  const panelStyle = (style: CSSProperties | undefined) => minPanelWidth ? { ...style, minWidth: minPanelWidth } : style
  return <div {...containerProps} className={`${containerProps.className} ${fill}`}>
    <div {...primaryProps} style={side === 'left' ? panelStyle(primaryProps.style) : primaryProps.style} className={`${primaryProps.className} ${pane} ${side === 'left' ? panelClass : `${mainClass} ${mainPane}`}`}>{side === 'left' ? panel : main}</div>
    <div {...splitterProps} className={`${splitterProps.className} ${flush ? flushHandle : ''} ${panelClass}`} aria-label={side === 'left' ? t('gofi.canvas.resizable-split.resize-palette', 'Resize stage palette') : t('gofi.canvas.resizable-split.resize-side-panel', 'Resize side panel')} />
    <div {...secondaryProps} style={side === 'right' ? panelStyle(secondaryProps.style) : secondaryProps.style} className={`${secondaryProps.className} ${pane} ${side === 'left' ? `${mainClass} ${mainPane}` : panelClass}`}>{side === 'left' ? main : panel}</div>
  </div>
}

const fill = css({ flex: 1, display: 'flex', minHeight: 0, minWidth: 0, position: 'relative' })
const pane = css({ display: 'flex', minHeight: 0, minWidth: 0, position: 'relative' })
// The splitter gives panes min-width: min-content inline, which a wide page
// (a table) turns into pushing the side panel past the viewport; the main
// area scrolls its own content, so it may shrink.
const mainPane = css({ minWidth: '0 !important' })
const hiddenPane = css({ display: 'none !important' })
// Zero layout width; the 12px hit area lies over the panel's edge only, so it
// never covers the main area's scrollbar at the seam.
const flushHandle = css({ width: '0 !important', margin: '0 -12px 0 0 !important', padding: '0 12px 0 0', boxSizing: 'content-box', position: 'relative', zIndex: 2, flexShrink: 0 })
const fullPane = css({ flex: '1 1 100% !important' })
