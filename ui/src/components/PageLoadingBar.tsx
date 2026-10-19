import { useLayoutEffect, useRef, useState } from 'react'
import { css } from '@emotion/css'
import { LoadingBar, useStyles2 } from '@grafana/ui'
import { t } from '../i18n'

/** Grafana's loading bar along the top edge of a page's content while the
 * page gathers its initial data. It is absolutely positioned inside the
 * content area, so the chrome around it (title, tabs) keeps its size and the
 * content appears in place, all at once, when the data is ready. */
export function PageLoadingBar({ label }: { label?: string }) {
  const styles = useStyles2(getStyles)
  const ref = useRef<HTMLDivElement>(null)
  const [width, setWidth] = useState(960)
  useLayoutEffect(() => {
    const element = ref.current
    if (!element) return
    const measure = () => setWidth(element.clientWidth || 960)
    measure()
    const observer = new ResizeObserver(measure)
    observer.observe(element)
    return () => observer.disconnect()
  }, [])
  return <div ref={ref} className={styles.root} data-testid="page-loading-bar" aria-busy="true">
    <LoadingBar width={width} delay={0} ariaLabel={label ?? t('gofi.common.loading', 'Loading...')} />
  </div>
}

const getStyles = () => ({
  root: css({ position: 'absolute', top: 0, left: 0, right: 0, zIndex: 1, pointerEvents: 'none' }),
})
