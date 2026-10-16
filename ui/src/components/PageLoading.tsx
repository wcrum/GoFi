import { css } from '@emotion/css'
import { LoadingPlaceholder } from '@grafana/ui'

/** Grafana's loading indicator centered in the space it occupies (a page,
 * panel, drawer, or editor), never pinned to the top-left corner. */
export function PageLoading({ text = 'Loading...', minHeight = 200 }: { text?: string; minHeight?: number }) {
  return <div className={css({ flex: 1, width: '100%', height: '100%', minHeight, display: 'flex', alignItems: 'center', justifyContent: 'center' })}>
    <LoadingPlaceholder text={text} className={css({ marginBottom: 0 })} />
  </div>
}
