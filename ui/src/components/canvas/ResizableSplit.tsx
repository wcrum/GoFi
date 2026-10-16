import { useEffect, useState, type ReactNode } from 'react'
import { css } from '@emotion/css'
import { useSplitter, useTheme2 } from '@grafana/ui'

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
 * panes) between the flow canvas and a side panel. Narrow screens keep the
 * panels' own overlay layout. */
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
  if (props.panel === null || !wide) {
    return <div className={fill}>{props.side === 'left' && wide ? null : props.panel}{props.main}</div>
  }
  return <Split {...props} />
}

function Split({ side, panel, main, storageKey, defaultShare }: ResizableSplitProps) {
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
  return <div {...containerProps} className={`${containerProps.className} ${fill}`}>
    <div {...primaryProps} className={`${primaryProps.className} ${pane}`}>{side === 'left' ? panel : main}</div>
    <div {...splitterProps} aria-label={side === 'left' ? 'Resize stage palette' : 'Resize side panel'} />
    <div {...secondaryProps} className={`${secondaryProps.className} ${pane}`}>{side === 'left' ? main : panel}</div>
  </div>
}

const fill = css({ flex: 1, display: 'flex', minHeight: 0, minWidth: 0, position: 'relative' })
const pane = css({ display: 'flex', minHeight: 0, minWidth: 0, position: 'relative' })
