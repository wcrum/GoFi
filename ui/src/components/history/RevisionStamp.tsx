import { useEffect, useRef, useState } from 'react'
import { css } from '@emotion/css'
import type { GrafanaTheme2 } from '@grafana/data'
import { TextLink, useStyles2 } from '@grafana/ui'
import { listFlowRevisions, type FlowRevision } from '../../history-api'
import { revisionStamp } from '../../lib/revisions'
import { paths, to } from '../../router'
import type { FlowDefinition } from '../../types'

/** "Revision 7 · saved by ada, 5 minutes ago" for a flow page header,
 * linking to its Resource tab (which holds the history). The line keeps its
 * height without history, so the header never shifts. `initial` is the latest
 * revision a page loaded up front (null when there is none). */
export function RevisionStamp({ definition, initial }: { definition: FlowDefinition; initial?: FlowRevision | null }) {
  const styles = useStyles2(getStyles)
  const [latest, setLatest] = useState<FlowRevision | undefined>(initial ?? undefined)
  const seeded = useRef(initial !== undefined)
  const key = JSON.stringify(definition)
  useEffect(() => {
    // The page already read the latest revision; read again when the flow changes.
    if (seeded.current) { seeded.current = false; return }
    let live = true
    listFlowRevisions(definition.name, 1).then(list => { if (live) setLatest(list.revisions[0]) }).catch(() => { if (live) setLatest(undefined) })
    return () => { live = false }
    // Refetch when the flow's content changes, not on every poll.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [key])
  const shown = latest && latest.flow === definition.name && !latest.deleted ? latest : undefined
  return <span className={styles.line}>
    {shown && <TextLink href={to(paths.flowRevision(definition.name))} color="secondary" variant="bodySmall" inline={false}>{revisionStamp(shown)}</TextLink>}
  </span>
}

/** The fixed height of the revision line (a bodySmall TextLink is one body
 * line tall), shared with the page's loading chrome. */
export const revisionLineHeight = (theme: GrafanaTheme2) => theme.spacing(2.75)

const getStyles = (theme: GrafanaTheme2) => ({
  line: css({ display: 'block', minHeight: revisionLineHeight(theme) }),
})
