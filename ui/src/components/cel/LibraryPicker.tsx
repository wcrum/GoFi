import { useMemo, useState } from 'react'
import { css } from '@emotion/css'
import type { GrafanaTheme2 } from '@grafana/data'
import { Alert, Badge, Button, EmptyState, FilterInput, Modal, Stack, TagList, Text, useStyles2 } from '@grafana/ui'
import type { CelExpression, ResultType } from '../../expression-api'
import { useExpressions } from './useExpressions'
import { PageLoading } from '../PageLoading'

interface Props {
  isOpen: boolean
  resultType: ResultType
  onDismiss: () => void
  /** Link: the stage follows the entry (latest generation). */
  onLink: (expression: CelExpression) => void
  /** Insert: copy the text; later library edits do not reach this stage. */
  onInsert: (expression: CelExpression) => void
}

/** Pick a CEL library entry to link or insert. Filters needing a bool only
 * list bool entries, matching the server's resolution rule. */
export function LibraryPicker({ isOpen, resultType, onDismiss, onLink, onInsert }: Props) {
  const styles = useStyles2(getStyles)
  const { expressions, error, loading } = useExpressions(isOpen)
  const [query, setQuery] = useState('')
  const [tag, setTag] = useState<string | null>(null)
  const candidates = useMemo(() => expressions
    .filter(e => resultType === 'any' || e.resultType === 'bool')
    .filter(e => !tag || e.tags?.includes(tag))
    .filter(e => !query || `${e.name} ${e.description ?? ''} ${e.expression} ${(e.tags ?? []).join(' ')}`.toLowerCase().includes(query.toLowerCase())), [expressions, resultType, tag, query])
  return <Modal title="CEL library" isOpen={isOpen} onDismiss={onDismiss} className={styles.modal}>
    <Stack direction="column" gap={2}>
      <Text color="secondary" variant="bodySmall">Link keeps this stage in sync with the library entry. Insert copies its current text.</Text>
      <Stack gap={1} alignItems="center">
        <div className={styles.grow}><FilterInput value={query} onChange={setQuery} placeholder="Search name, description, expression, or tag" autoFocus /></div>
        {tag && <Button size="sm" variant="secondary" fill="outline" icon="times" onClick={() => setTag(null)}>{tag}</Button>}
      </Stack>
      {error && <Alert severity="error" title="CEL library unavailable">{error}</Alert>}
      {loading ? <PageLoading text="Loading library..." />
        : !candidates.length ? <EmptyState variant="not-found" message={expressions.length ? 'No matching expressions' : 'The CEL library is empty'}>{resultType === 'bool' && expressions.length > 0 ? 'Only bool expressions can be used here.' : 'Create entries on the CEL library page.'}</EmptyState>
        : <ul className={styles.list}>{candidates.map(e => <li key={e.name} className={styles.item}>
          <Stack justifyContent="space-between" alignItems="flex-start" gap={2}>
            <div className={styles.grow}>
              <Stack gap={1} alignItems="center" wrap="wrap">
                <Text weight="medium">{e.name}</Text>
                <Badge color={e.resultType === 'bool' ? 'blue' : 'purple'} text={e.resultType ?? 'bool'} />
                <Text color="secondary" variant="bodySmall">generation {e.generation}</Text>
              </Stack>
              {e.description && <Text color="secondary" variant="bodySmall" element="p">{e.description}</Text>}
              <code className={styles.code}>{e.expression}</code>
              {!!e.tags?.length && <TagList tags={e.tags} onClick={name => setTag(name)} />}
            </div>
            <Stack gap={1}>
              <Button size="sm" variant="secondary" icon="copy" onClick={() => onInsert(e)}>Insert</Button>
              <Button size="sm" icon="link" onClick={() => onLink(e)}>Link</Button>
            </Stack>
          </Stack>
        </li>)}</ul>}
    </Stack>
  </Modal>
}

const getStyles = (theme: GrafanaTheme2) => ({
  modal: css({ width: 'min(760px, 94vw)' }),
  grow: css({ flex: 1, minWidth: 0 }),
  list: css({ listStyle: 'none', margin: 0, padding: 0, maxHeight: '55vh', overflowY: 'auto' }),
  item: css({ padding: theme.spacing(1.5, 0), borderBottom: `1px solid ${theme.colors.border.weak}` }),
  code: css({ display: 'block', margin: theme.spacing(0.5, 0), fontFamily: theme.typography.fontFamilyMonospace, fontSize: theme.typography.bodySmall.fontSize, color: theme.colors.text.primary, whiteSpace: 'pre-wrap', wordBreak: 'break-word' }),
})
