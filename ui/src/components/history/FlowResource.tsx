import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { css, cx } from '@emotion/css'
import type { GrafanaTheme2 } from '@grafana/data'
import { Alert, Badge, Button, ClipboardButton, CodeEditor, ConfirmModal, Field, Icon, Input, RadioButtonGroup, Stack, Text, Tooltip, useStyles2 } from '@grafana/ui'
import '../../lib/monaco'
import { getFlowRevision, listFlowRevisions, rollbackFlow, type FlowRevision, type RevisionList } from '../../history-api'
import { downloadYAML, flowResource, toYAML } from '../../lib/resource'
import { previousRevision, shortHash, sourceBadge, type CompareMode } from '../../lib/revisions'
import { navigate, paths, useRevisionParam } from '../../router'
import { useGoFi } from '../../state'
import type { FlowDefinition } from '../../types'
import { useToast } from '../Toast'
import { PageLoading } from '../PageLoading'
import { YamlDiff } from './YamlDiff'
import { FlowDiffGraph } from './FlowDiffGraph'
import { formatDateTime, formatRelativeTime, t, Trans } from '../../i18n'

type DiffView = 'visual' | 'yaml'
const VIEW_KEY = 'gofi.flow.diffView'
const compareOptions = () => [
  { label: t('gofi.history.flow-resource.compare-current', 'Current'), value: 'current' as const, description: t('gofi.history.flow-resource.compare-current-description', 'What has changed since this revision') },
  { label: t('gofi.history.flow-resource.compare-previous', 'Previous revision'), value: 'previous' as const, description: t('gofi.history.flow-resource.compare-previous-description', 'What this revision changed') },
]
const viewOptions = () => [{ label: t('gofi.history.flow-resource.view-visual', 'Visual'), value: 'visual' as const, icon: 'sitemap' as const }, { label: 'YAML', value: 'yaml' as const, icon: 'brackets-curly' as const }]
const layoutOptions = () => [{ label: t('gofi.history.flow-resource.layout-split', 'Split'), value: true, icon: 'columns' as const }, { label: t('gofi.history.flow-resource.layout-unified', 'Unified'), value: false, icon: 'align-left' as const }]

/** The Resource tab: the flow's resource as it is now, beside its revision
 * timeline; picking an older revision diffs it (visually or as YAML) and
 * offers to restore it (docs/versioning.md). The selection is ?rev=N. */
/** `initialList` is the revision list a page loaded up front, shown at once. */
export default function FlowResource({ definition, initialList }: { definition: FlowDefinition; initialList?: RevisionList }) {
  const styles = useStyles2(getStyles)
  const { configuration, reload } = useGoFi()
  const addToast = useToast()
  const name = definition.name
  const rev = useRevisionParam()
  const [list, setList] = useState<RevisionList | null>(initialList ?? null)
  const seeded = useRef(initialList !== undefined)
  const [error, setError] = useState<string>()
  const [compare, setCompare] = useState<CompareMode>('current')
  const [view, setViewState] = useState<DiffView>(() => { try { return localStorage.getItem(VIEW_KEY) === 'yaml' ? 'yaml' : 'visual' } catch { return 'visual' } })
  const setView = (next: DiffView) => { setViewState(next); try { localStorage.setItem(VIEW_KEY, next) } catch { /* private mode */ } }
  const [sideBySide, setSideBySide] = useState(true)
  const [definitions, setDefinitions] = useState<Record<number, FlowDefinition | null>>({})
  const [restoring, setRestoring] = useState(false)
  const [message, setMessage] = useState('')
  const [busy, setBusy] = useState(false)
  const inflight = useRef(new Set<number>())

  const load = useCallback(async () => {
    try {
      setList(await listFlowRevisions(name))
      setError(undefined)
    } catch (err) { setError(String(err)) }
  }, [name])
  // Reload whenever the flow itself changes (a save, restore, or outside edit).
  const definitionKey = JSON.stringify(definition)
  useEffect(() => { if (seeded.current) seeded.current = false; else void load() }, [load, definitionKey])

  const select = (revision?: number) => navigate(paths.flowRevision(name, revision), { replace: true })
  const revisions = list?.revisions ?? []
  const latest = revisions[0]
  const currentRevision = latest && !latest.deleted ? latest.revision : undefined
  // The latest revision is the flow as it is now: selecting it shows Current.
  const selectedRev = rev !== undefined && rev !== currentRevision ? revisions.find(r => r.revision === rev) : undefined
  const missing = rev !== undefined && rev !== currentRevision && !!list && !selectedRev
  const base = selectedRev && compare === 'previous' ? previousRevision(revisions, selectedRev.revision) : undefined

  // Fetch the definitions the diff needs (cached; revisions are immutable).
  useEffect(() => {
    const wanted = [selectedRev, base].filter((r): r is FlowRevision => !!r && !r.deleted && !(r.revision in definitions) && !inflight.current.has(r.revision))
    for (const r of wanted) {
      inflight.current.add(r.revision)
      getFlowRevision(name, r.revision)
        .then(full => setDefinitions(cache => ({ ...cache, [r.revision]: full.definition ?? null })))
        .catch(err => { setDefinitions(cache => ({ ...cache, [r.revision]: null })); setError(String(err)) })
        .finally(() => inflight.current.delete(r.revision))
    }
  }, [name, selectedRev, base, definitions])

  const yamlOf = useCallback((def: FlowDefinition | null | undefined) => def ? toYAML(flowResource(def, configuration)) : '', [configuration])
  const currentYAML = useMemo(() => yamlOf(definition), [yamlOf, definition])
  const selectedDef = selectedRev && !selectedRev.deleted ? definitions[selectedRev.revision] ?? null : null
  const ready = (!selectedRev || selectedRev.deleted || selectedRev.revision in definitions) && (!base || base.revision in definitions)
  // Diffs always read older -> newer: "Current" shows what has changed since
  // the selected revision; "Previous" shows what the selected revision changed.
  // The oldest revision compared with "Previous" has no base: show it alone
  // rather than a diff against an empty document.
  const firstRevision = !!selectedRev && compare === 'previous' && !base
  const revisionLabel = selectedRev
    ? (selectedRev.deleted ? t('gofi.history.flow-resource.revision-deleted-label', 'Revision {{revision}} (deleted)', { revision: selectedRev.revision }) : t('gofi.history.flow-resource.revision-label', 'Revision {{revision}}', { revision: selectedRev.revision }))
    : ''
  const selectedYAML = yamlOf(selectedDef)
  const baseDef = base ? definitions[base.revision] ?? null : null
  const older = compare === 'current' ? selectedDef : baseDef
  const newer = compare === 'current' ? definition : selectedDef
  const olderLabel = compare === 'current' ? revisionLabel : base ? t('gofi.history.flow-resource.revision-label', 'Revision {{revision}}', { revision: base.revision }) : t('gofi.history.flow-resource.empty-label', 'Empty')
  const newerLabel = compare === 'current' ? t('gofi.history.flow-resource.current', 'Current') : revisionLabel
  const olderYAML = compare === 'current' ? selectedYAML : yamlOf(baseDef)
  const newerYAML = compare === 'current' ? currentYAML : selectedYAML

  async function restore() {
    if (!selectedRev) return
    setBusy(true)
    try {
      const result = await rollbackFlow(name, selectedRev.revision, message.trim())
      addToast(result.created ? t('gofi.history.flow-resource.restored', 'Restored revision {{from}} as revision {{to}}.', { from: selectedRev.revision, to: result.revision?.revision }) : t('gofi.history.flow-resource.already-matches', 'The flow already matches this revision.'), 'success')
      setRestoring(false)
      setMessage('')
      await reload()
      await load()
      select(undefined)
    } catch (err) { addToast(String(err)) }
    finally { setBusy(false) }
  }

  return <div className={styles.fill}>
    {error && <Alert severity="error" title={t('gofi.history.flow-resource.history-unavailable', 'Flow history unavailable')} onRemove={() => setError(undefined)}>{error}</Alert>}
    <div className={styles.layout}>
      <section className={styles.main} aria-label={selectedRev ? revisionLabel : t('gofi.history.flow-resource.current-resource', 'Current resource')}>
        {missing && <Alert severity="warning" title={t('gofi.history.flow-resource.revision-unavailable-title', 'Revision {{revision}} is not available', { revision: rev })} onRemove={() => select(undefined)}>{t('gofi.history.flow-resource.revision-unavailable-body', 'It may have aged out of the last {{limit}} kept revisions. Showing the current resource.', { limit: list?.retentionLimit })}</Alert>}
        {!selectedRev ? <>
          <Stack justifyContent="space-between" alignItems="center" wrap="wrap" gap={1}>
            <Stack alignItems="center" gap={1}>
              <Text variant="h5">{t('gofi.history.flow-resource.current', 'Current')}</Text>
              {currentRevision !== undefined && <Text color="secondary" variant="bodySmall">{t('gofi.history.flow-resource.revision-label', 'Revision {{revision}}', { revision: currentRevision })}</Text>}
            </Stack>
            <Stack gap={1}>
              <ClipboardButton icon="copy" variant="secondary" size="sm" getText={() => currentYAML}>{t('gofi.common.copy', 'Copy')}</ClipboardButton>
              <Button icon="download-alt" variant="secondary" size="sm" onClick={() => downloadYAML(`${name}.yaml`, flowResource(definition, configuration))}>{t('gofi.common.download', 'Download')}</Button>
            </Stack>
          </Stack>
          <div className={styles.editor}>
            <CodeEditor language="yaml" value={currentYAML} readOnly showLineNumbers showMiniMap={false} height="100%" width="100%" monacoOptions={{ scrollBeyondLastLine: false }} containerStyles={styles.editorFill} />
          </div>
        </> : <>
          <Stack justifyContent="space-between" alignItems="center" wrap="wrap" gap={1}>
            <Stack alignItems="center" gap={1} wrap="wrap">
              <Text variant="h5">{revisionLabel}</Text>
              <Text color="secondary" variant="bodySmall">{selectedRev.author} · {formatRelativeTime(selectedRev.createdAt)}</Text>
            </Stack>
            <Stack gap={1}>
              <ClipboardButton icon="copy" variant="secondary" size="sm" disabled={!selectedYAML} getText={() => selectedYAML}>{t('gofi.history.flow-resource.copy-yaml', 'Copy YAML')}</ClipboardButton>
              <Button icon="history" variant="primary" size="sm" disabled={selectedRev.deleted} tooltip={selectedRev.deleted ? t('gofi.history.flow-resource.deleted-revision-tooltip', 'This revision records the deletion; restore an earlier one') : undefined} onClick={() => setRestoring(true)}>{t('gofi.history.flow-resource.restore-this-version', 'Restore this version')}</Button>
            </Stack>
          </Stack>
          <Stack alignItems="center" gap={2} wrap="wrap">
            <Stack alignItems="center" gap={1}>
              <Text color="secondary" variant="bodySmall">{t('gofi.history.flow-resource.compare-with', 'Compare with')}</Text>
              <RadioButtonGroup size="sm" options={compareOptions()} value={compare} onChange={setCompare} />
            </Stack>
            <RadioButtonGroup size="sm" options={viewOptions()} value={view} onChange={setView} />
            {view === 'yaml' && <RadioButtonGroup size="sm" options={layoutOptions()} value={sideBySide} onChange={setSideBySide} />}
          </Stack>
          <Text color="secondary" variant="bodySmall">
            {firstRevision ? t('gofi.history.flow-resource.first-revision-note', 'Revision {{revision}} is the first recorded revision, so there is nothing earlier to compare it with.', { revision: selectedRev.revision })
              : compare === 'current' ? t('gofi.history.flow-resource.changed-since-note', 'What changed since revision {{revision}}: {{older}} → {{newer}}.', { revision: selectedRev.revision, older: olderLabel, newer: newerLabel })
              : t('gofi.history.flow-resource.revision-changed-note', 'What revision {{revision}} changed: {{older}} → {{newer}}.', { revision: selectedRev.revision, older: olderLabel, newer: newerLabel })}
          </Text>
          {!ready ? <PageLoading text={t('gofi.history.flow-resource.loading-revision', 'Loading revision...')} />
            : view === 'visual'
              ? <FlowDiffGraph key={`${compare}:${selectedRev.revision}`} before={older} after={newer} beforeLabel={olderLabel} afterLabel={newerLabel} plain={firstRevision} />
              : firstRevision
                ? <div className={styles.editor}><CodeEditor language="yaml" value={selectedYAML} readOnly showLineNumbers showMiniMap={false} height="100%" width="100%" monacoOptions={{ scrollBeyondLastLine: false }} containerStyles={styles.editorFill} /></div>
                : <>
                <div className={styles.labels}>
                  <Text color="secondary" variant="bodySmall">{olderLabel}</Text>
                  {sideBySide && <Text color="secondary" variant="bodySmall">{newerLabel}</Text>}
                </div>
                <YamlDiff original={olderYAML} modified={newerYAML} sideBySide={sideBySide} />
              </>}
        </>}
      </section>
      <nav className={styles.list} aria-label={t('gofi.history.flow-resource.revisions', 'Revisions')}>
        <div className={styles.listHeader}>
          <Text variant="h5">{t('gofi.history.flow-resource.history', 'History')}</Text>
          {list && <Text color="secondary" variant="bodySmall">{t('gofi.history.flow-resource.last-kept', 'Last {{count}} kept', { count: list.retentionLimit })}</Text>}
        </div>
        {!list && !error && <div className={styles.empty}><Text color="secondary" variant="bodySmall">{t('gofi.history.flow-resource.loading-revisions', 'Loading revisions...')}</Text></div>}
        {list && currentRevision === undefined && <button type="button" className={cx(styles.row, !selectedRev && styles.rowSelected)} onClick={() => select(undefined)} aria-current={!selectedRev}>
          <Stack alignItems="center" gap={1}><Badge color="green" text={t('gofi.history.flow-resource.current', 'Current')} /></Stack>
          <span className={styles.meta}>{t('gofi.history.flow-resource.no-revisions', 'No revisions recorded yet. Every save creates one.')}</span>
        </button>}
        {revisions.map(r => {
          const badge = sourceBadge(r.source)
          const isCurrent = r.revision === currentRevision
          const active = isCurrent ? !selectedRev : r.revision === selectedRev?.revision
          return <button key={r.revision} type="button" className={cx(styles.row, active && styles.rowSelected)} onClick={() => select(isCurrent ? undefined : r.revision)} aria-current={active}>
            <Stack justifyContent="space-between" alignItems="center" gap={1}>
              <Stack alignItems="center" gap={1}>
                <span className={styles.number}>#{r.revision}</span>
                {r.deleted ? <Badge color="red" text={t('gofi.history.flow-resource.deleted', 'Deleted')} icon="trash-alt" /> : <Tooltip content={badge.tooltip || badge.text}><span><Badge color={badge.color} text={badge.text} /></span></Tooltip>}
                {isCurrent && <Badge color="green" text={t('gofi.history.flow-resource.current', 'Current')} />}
              </Stack>
              <span title={formatDateTime(r.createdAt)} className={styles.meta}>{formatRelativeTime(r.createdAt)}</span>
            </Stack>
            {r.message && <div className={styles.message}>{r.message}</div>}
            <Stack justifyContent="space-between" gap={1}>
              <span className={styles.meta}><Icon name="user" size="xs" /> {r.author}</span>
              {r.hash && <span className={styles.hash} title={r.hash}>{shortHash(r.hash)}</span>}
            </Stack>
          </button>
        })}
      </nav>
    </div>
    <ConfirmModal
      isOpen={restoring}
      title={t('gofi.history.flow-resource.restore-title', 'Restore revision {{revision}}', { revision: selectedRev?.revision ?? '' })}
      body={<Stack direction="column" gap={2}>
        <Text><Trans i18nKey="gofi.history.flow-resource.restore-body" values={{ revision: selectedRev?.revision, name }}>Save revision {'{{revision}}'} of "{'{{name}}'}" as its current definition? It is validated like any save, recorded as a new revision, and the flow's runtime restarts with it.</Trans></Text>
        <Field label={t('gofi.history.flow-resource.restore-message', 'Message')} description={t('gofi.history.flow-resource.restore-message-description', 'Optional; recorded on the new revision')} noMargin>
          <Input value={message} placeholder={t('gofi.history.flow-resource.restore-title', 'Restore revision {{revision}}', { revision: selectedRev?.revision ?? '' })} maxLength={1024} onChange={event => setMessage(event.currentTarget.value)} />
        </Field>
      </Stack>}
      confirmText={busy ? t('gofi.history.flow-resource.restoring', 'Restoring...') : t('gofi.history.flow-resource.restore', 'Restore')}
      confirmVariant="primary"
      disabled={busy}
      onConfirm={() => void restore()}
      onDismiss={() => { setRestoring(false); setMessage('') }}
    />
  </div>
}

const getStyles = (theme: GrafanaTheme2) => ({
  fill: css({ position: 'absolute', inset: 0, padding: theme.spacing(2, 3), display: 'flex', flexDirection: 'column', gap: theme.spacing(2), [theme.breakpoints.down('md')]: { position: 'static', padding: theme.spacing(2) } }),
  layout: css({ flex: 1, minHeight: 0, display: 'grid', gridTemplateColumns: 'minmax(0, 1fr) 300px', gap: theme.spacing(2), [theme.breakpoints.down('md')]: { gridTemplateColumns: 'minmax(0, 1fr)' } }),
  main: css({ minHeight: 0, minWidth: 0, display: 'flex', flexDirection: 'column', gap: theme.spacing(1), [theme.breakpoints.down('md')]: { minHeight: 520 } }),
  editor: css({ flex: 1, minHeight: 240, border: `1px solid ${theme.colors.border.weak}`, borderRadius: theme.shape.radius.default, overflow: 'hidden' }),
  editorFill: css({ height: '100%' }),
  list: css({ minHeight: 0, overflowY: 'auto', border: `1px solid ${theme.colors.border.weak}`, borderRadius: theme.shape.radius.default, background: theme.colors.background.primary, [theme.breakpoints.down('md')]: { maxHeight: 360 } }),
  listHeader: css({ display: 'flex', justifyContent: 'space-between', alignItems: 'baseline', padding: theme.spacing(1, 1.5), borderBottom: `1px solid ${theme.colors.border.weak}`, position: 'sticky', top: 0, background: theme.colors.background.primary, zIndex: 1 }),
  row: css({
    display: 'flex', flexDirection: 'column', gap: theme.spacing(0.5), width: '100%', textAlign: 'left', padding: theme.spacing(1, 1.5), border: '1px solid transparent',
    borderBottomColor: theme.colors.border.weak, background: 'transparent', color: theme.colors.text.primary, cursor: 'pointer',
    '&:hover': { background: theme.colors.action.hover },
    '&:focus-visible': { outline: `2px solid ${theme.colors.primary.main}`, outlineOffset: -2 },
  }),
  rowSelected: css({ background: theme.colors.primary.transparent, borderColor: theme.colors.primary.border, '&:hover': { background: theme.colors.primary.transparent } }),
  number: css({ fontFamily: theme.typography.fontFamilyMonospace, fontWeight: theme.typography.fontWeightMedium }),
  meta: css({ color: theme.colors.text.secondary, fontSize: theme.typography.bodySmall.fontSize, whiteSpace: 'nowrap' }),
  message: css({ fontSize: theme.typography.bodySmall.fontSize, overflow: 'hidden', textOverflow: 'ellipsis', display: '-webkit-box', WebkitLineClamp: 2, WebkitBoxOrient: 'vertical' }),
  hash: css({ fontFamily: theme.typography.fontFamilyMonospace, fontSize: theme.typography.bodySmall.fontSize, color: theme.colors.text.secondary }),
  labels: css({ display: 'grid', gridTemplateColumns: '1fr 1fr', padding: theme.spacing(0, 0.5) }),
  empty: css({ padding: theme.spacing(3), textAlign: 'center' }),
})
