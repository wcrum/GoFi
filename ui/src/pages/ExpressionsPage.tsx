import { useEffect, useMemo, useState } from 'react'
import { css } from '@emotion/css'
import type { GrafanaTheme2 } from '@grafana/data'
import {
  Alert, Badge, Button, Combobox, ConfirmModal, Drawer, Field, FilterInput, IconButton, Input, InteractiveTable,
  RadioButtonGroup, Stack, Tab, TabsBar, TagList, TagsInput, Text, TextArea, TextLink, useStyles2, type Column,
} from '@grafana/ui'
import { EmptyState } from '../components/EmptyState'
import { CelEditor } from '../components/cel/CelEditor'
import { useExpressions } from '../components/cel/useExpressions'
import { useToast } from '../components/Toast'
import { applyExpression, deleteExpression, describeEnvironment, evaluateExpression, type CelExpression, type EvaluationResult, type ResultType } from '../expression-api'
import { navigate, paths, to } from '../router'
import { Page } from '../shell/Page'
import { useGoFi } from '../state'
import { PageLoading } from '../components/PageLoading'
import { CelCode } from '../components/cel/CelCode'
import { formatNumber, t } from '../i18n'

const blank = (): CelExpression => ({ name: '', description: '', expression: '', tags: [], resultType: 'bool' })

/** The CEL expression library: named, tagged expressions that stages and
 * sink conditions link to. `selected` is a name to open, or "new". */
export default function ExpressionsPage({ selected }: { selected: string }) {
  const styles = useStyles2(getStyles)
  const { expressions, error, loading, refresh } = useExpressions()
  const [query, setQuery] = useState('')
  const [tag, setTag] = useState<string | null>(null)
  const [deleting, setDeleting] = useState<CelExpression | null>(null)
  const [deleteError, setDeleteError] = useState<string | null>(null)
  const addToast = useToast()
  const editing = selected === 'new' ? blank() : expressions.find(e => e.name === selected)
  const rows = useMemo(() => expressions
    .filter(e => !tag || e.tags?.includes(tag))
    .filter(e => !query || `${e.name} ${e.description ?? ''} ${e.expression} ${(e.tags ?? []).join(' ')}`.toLowerCase().includes(query.toLowerCase())), [expressions, query, tag])

  const columns: Array<Column<CelExpression>> = [
    { id: 'name', header: t('gofi.pages.expressions.column-name', 'Name'), sortType: 'string', cell: ({ row: { original } }) => <Stack direction="column" gap={0}>
      <TextLink href={to(paths.expressions(original.name))} inline={false} weight="medium">{original.name}</TextLink>
      {original.description && <Text color="secondary" variant="bodySmall" truncate>{original.description}</Text>}
    </Stack> },
    { id: 'expression', header: t('gofi.pages.expressions.column-expression', 'Expression'), cell: ({ row: { original } }) => <CelCode className={styles.code} title={original.expression} expression={original.expression} /> },
    { id: 'resultType', header: t('gofi.pages.expressions.column-result', 'Result'), cell: ({ row: { original } }) => <Badge color={original.resultType === 'bool' ? 'blue' : 'purple'} text={original.resultType ?? 'bool'} /> },
    { id: 'tags', header: t('gofi.pages.expressions.column-tags', 'Tags'), cell: ({ row: { original } }) => original.tags?.length ? <TagList className={styles.tags} tags={original.tags} onClick={name => setTag(name)} /> : null },
    { id: 'generation', header: t('gofi.pages.expressions.column-generation', 'Generation'), sortType: 'number', cell: ({ row: { original } }) => <Text tabular>{original.pinnedRevisions?.length ? t('gofi.pages.expressions.generation-with-pinned', '{{generation}} (+{{count}} pinned)', { generation: original.generation, count: original.pinnedRevisions.length }) : original.generation ?? ''}</Text> },
    { id: 'usedBy', header: t('gofi.pages.expressions.column-used-by', 'Used by'), cell: ({ row: { original } }) => original.usedBy?.length ? <Stack gap={1} wrap="wrap">{original.usedBy.map(name => <TextLink key={name} href={to(paths.flow(name))} inline={false}>{name}</TextLink>)}</Stack> : <Text color="secondary">—</Text> },
    { id: 'actions', header: '', cell: ({ row: { original } }) => <Stack gap={0.5} justifyContent="flex-end">
      <IconButton name="pen" tooltip={t('gofi.pages.expressions.edit-and-test', 'Edit and test')} onClick={() => navigate(paths.expressions(original.name))} />
      <IconButton name="trash-alt" tooltip={original.usedBy?.length ? t('gofi.pages.expressions.in-use-by-flows', 'In use by flows') : t('gofi.common.delete', 'Delete')} disabled={!!original.usedBy?.length} onClick={() => { setDeleteError(null); setDeleting(original) }} />
    </Stack> },
  ]

  async function remove(expression: CelExpression) {
    try { await deleteExpression(expression.name); setDeleting(null); addToast(t('gofi.pages.expressions.deleted', 'Deleted "{{name}}"', { name: expression.name }), 'success'); await refresh() }
    catch (reason) { setDeleteError(String(reason)); setDeleting(null) }
  }

  return <Page title={t('gofi.pages.expressions.title', 'CEL library')} icon="book-open"
    subTitle={t('gofi.pages.expressions.subtitle', 'Named Common Expression Language expressions that filters, transforms, dedup keys, and sink conditions link to. Editing an entry reloads every flow that follows it; pinned flows keep their revision.')}
    actions={<Button icon="plus" onClick={() => navigate(paths.expressions('new'))}>{t('gofi.pages.expressions.new-expression', 'New expression')}</Button>}>
    <Stack direction="column" gap={2}>
      {error && <Alert severity="error" title={t('gofi.pages.expressions.library-unavailable', 'CEL library unavailable')}>{error}</Alert>}
      {deleteError && <Alert severity="error" title={t('gofi.pages.expressions.delete-refused', 'Delete refused')} onRemove={() => setDeleteError(null)}>{deleteError}</Alert>}
      <EnvironmentSummary />
      {loading ? <PageLoading text={t('gofi.pages.expressions.loading-library', 'Loading library...')} />
        : !expressions.length ? <EmptyState variant="call-to-action" message={t('gofi.pages.expressions.empty-title', 'No CEL expressions yet')} button={<Button icon="plus" onClick={() => navigate(paths.expressions('new'))}>{t('gofi.pages.expressions.new-expression', 'New expression')}</Button>}>
          {t('gofi.pages.expressions.empty-body', "Save an expression once and link it from any flow's filter, transform, dedup, or sink condition.")}
        </EmptyState>
        : <>
          <Stack gap={1} alignItems="center">
            <div className={styles.grow}><FilterInput value={query} onChange={setQuery} placeholder={t('gofi.pages.expressions.search-placeholder', 'Search name, description, expression, or tag')} /></div>
            {tag && <Button variant="secondary" fill="outline" icon="times" onClick={() => setTag(null)}>{tag}</Button>}
          </Stack>
          <InteractiveTable columns={columns} data={rows} getRowId={row => row.name} pageSize={25} />
        </>}
    </Stack>
    {selected && (editing || selected === 'new') && <ExpressionDrawer key={selected} initial={editing!} isNew={selected === 'new'} onClose={() => navigate(paths.expressions())} onSaved={name => { void refresh(); navigate(paths.expressions(name), { replace: true }) }} />}
    {selected && selected !== 'new' && !loading && !editing && <Drawer title={t('gofi.pages.expressions.not-found-title', 'Not found')} onClose={() => navigate(paths.expressions())} size="sm"><EmptyState variant="not-found" message={t('gofi.pages.expressions.not-found-message', 'CEL expression "{{name}}" does not exist', { name: selected })} /></Drawer>}
    <ConfirmModal isOpen={!!deleting} title={t('gofi.pages.expressions.delete-title', 'Delete CEL expression')} body={t('gofi.pages.expressions.delete-body', 'Delete "{{name}}"? This cannot be undone.', { name: deleting?.name })} confirmText={t('gofi.common.delete', 'Delete')} onConfirm={() => { if (deleting) void remove(deleting) }} onDismiss={() => setDeleting(null)} />
  </Page>
}

function EnvironmentSummary() {
  const [summary, setSummary] = useState<string | null>(null)
  useEffect(() => { describeEnvironment().then(env => setSummary(t('gofi.pages.expressions.environment-summary', 'Variables: {{variables}} · {{functions}} functions · cost limit {{costLimit}} per evaluation · {{maxBytes}} bytes max', {
    variables: env.variables.map(v => `${v.name} (${v.type})`).join(', '), functions: env.functions.length, costLimit: formatNumber(env.costLimit), maxBytes: formatNumber(env.maxExpressionBytes),
  }))).catch(() => {}) }, [])
  return summary ? <Text color="secondary" variant="bodySmall">{summary}</Text> : null
}

function ExpressionDrawer({ initial, isNew, onClose, onSaved }: { initial: CelExpression; isNew: boolean; onClose: () => void; onSaved: (name: string) => void }) {
  const styles = useStyles2(getStyles)
  const addToast = useToast()
  const [draft, setDraft] = useState<CelExpression>(initial)
  const [tab, setTab] = useState<'definition' | 'test'>('definition')
  const [saving, setSaving] = useState(false)
  const [saveError, setSaveError] = useState<string | null>(null)
  const dirty = isNew || draft.expression !== initial.expression || draft.description !== initial.description || draft.resultType !== initial.resultType || (draft.tags ?? []).join() !== (initial.tags ?? []).join()

  async function save() {
    setSaving(true); setSaveError(null)
    try {
      const { expression, reloadedFlows } = await applyExpression(draft)
      addToast(reloadedFlows.length
        ? t('gofi.pages.expressions.saved-reloading', 'Saved "{{name}}" (generation {{generation}}); reloading {{flows}}', { name: expression.name, generation: expression.generation, flows: reloadedFlows.join(', ') })
        : t('gofi.pages.expressions.saved', 'Saved "{{name}}" (generation {{generation}})', { name: expression.name, generation: expression.generation }), 'success')
      onSaved(expression.name)
    } catch (reason) { setSaveError(String(reason)) }
    finally { setSaving(false) }
  }

  const subtitle = isNew ? undefined : initial.usedBy?.length
    ? t('gofi.pages.expressions.drawer-subtitle-used-by', 'Generation {{generation}} · used by {{flows}}', { generation: initial.generation, flows: initial.usedBy.join(', ') })
    : t('gofi.pages.expressions.drawer-subtitle-unused', 'Generation {{generation}} · not used yet', { generation: initial.generation })
  return <Drawer title={isNew ? t('gofi.pages.expressions.new-cel-expression', 'New CEL expression') : draft.name} subtitle={subtitle} onClose={onClose} size="md"
    tabs={<TabsBar><Tab label={t('gofi.pages.expressions.tab-definition', 'Definition')} active={tab === 'definition'} onChangeTab={() => setTab('definition')} /><Tab label={t('gofi.pages.expressions.tab-test', 'Test')} icon="play" active={tab === 'test'} onChangeTab={() => setTab('test')} /></TabsBar>}>
    {tab === 'definition' ? <Stack direction="column" gap={2}>
      {saveError && <Alert severity="error" title={t('gofi.pages.expressions.not-saved', 'Not saved')}>{saveError}</Alert>}
      <Field label={t('gofi.pages.expressions.field-name', 'Name')} description={t('gofi.pages.expressions.field-name-description', 'Lowercase letters, numbers, and hyphens. Flows link to this name.')} noMargin>
        <Input value={draft.name} disabled={!isNew} placeholder="hot-readings" onChange={event => setDraft({ ...draft, name: event.currentTarget.value })} />
      </Field>
      <Field label={t('gofi.pages.expressions.field-result-type', 'Result type')} description={t('gofi.pages.expressions.field-result-type-description', 'bool for filters and sink conditions; any for transform values and dedup keys.')} noMargin>
        <RadioButtonGroup value={draft.resultType ?? 'bool'} options={[{ value: 'bool', label: 'bool' }, { value: 'any', label: 'any' }]} onChange={value => setDraft({ ...draft, resultType: value as ResultType })} />
      </Field>
      <Field label={t('gofi.pages.expressions.field-expression', 'Expression')} noMargin>
        <CelEditor value={draft.expression} onChange={expression => setDraft(previous => ({ ...previous, expression }))} resultType={draft.resultType ?? 'bool'} height={140} placeholder={'fields["temperature"] > 30.0'} aria-label={t('gofi.pages.expressions.library-expression', 'Library expression')} />
      </Field>
      <Field label={t('gofi.pages.expressions.field-description', 'Description')} noMargin>
        <TextArea rows={2} value={draft.description ?? ''} onChange={event => setDraft({ ...draft, description: event.currentTarget.value })} />
      </Field>
      <Field label={t('gofi.pages.expressions.field-tags', 'Tags')} noMargin>
        <TagsInput tags={draft.tags ?? []} onChange={tags => setDraft({ ...draft, tags })} />
      </Field>
      {!!initial.pinnedRevisions?.length && <Field label={t('gofi.pages.expressions.field-pinned-revisions', 'Pinned revisions')} description={t('gofi.pages.expressions.field-pinned-revisions-description', 'Kept while a flow pins them.')} noMargin>
        <Stack direction="column" gap={1}>{initial.pinnedRevisions.map(r => <div key={r.generation}><Badge color="orange" icon="lock" text={t('gofi.pages.expressions.generation-badge', 'gen {{generation}}', { generation: r.generation })} /> <CelCode className={styles.code} expression={r.expression} /></div>)}</Stack>
      </Field>}
      <Stack gap={1}>
        <Button icon="save" disabled={saving || !dirty || !draft.name || !draft.expression.trim()} onClick={() => void save()}>{saving ? t('gofi.common.saving', 'Saving...') : t('gofi.common.save', 'Save')}</Button>
        <Button variant="secondary" fill="outline" onClick={() => setTab('test')}>{t('gofi.pages.expressions.test', 'Test')}</Button>
      </Stack>
      {!isNew && initial.usedBy?.length ? <Text color="secondary" variant="bodySmall">{t('gofi.pages.expressions.save-validates-note', 'Saving a new expression text validates it against every linked flow first; flows following the latest generation reload.')}</Text> : null}
    </Stack> : <TestPanel expression={draft.expression} resultType={draft.resultType ?? 'bool'} />}
  </Drawer>
}

function TestPanel({ expression, resultType }: { expression: string; resultType: ResultType }) {
  const styles = useStyles2(getStyles)
  const { flows } = useGoFi()
  const [flow, setFlow] = useState('')
  const [raw, setRaw] = useState('{"sensor":"alpha","temperature":31.5}')
  const [fields, setFields] = useState('{"sensor":"alpha","temperature":31.5}')
  const [result, setResult] = useState<EvaluationResult | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [running, setRunning] = useState(false)
  async function run() {
    setRunning(true); setError(null); setResult(null)
    try {
      let parsed: Record<string, unknown> | undefined
      if (!flow) {
        const value: unknown = JSON.parse(fields || '{}')
        if (!value || typeof value !== 'object' || Array.isArray(value)) throw new Error(t('gofi.pages.expressions.fields-must-be-object', 'Fields must be a JSON object'))
        parsed = value as Record<string, unknown>
      }
      setResult(await evaluateExpression({ expression, resultType, raw, fields: parsed, flow: flow || undefined }))
    } catch (reason) { setError(String(reason)) }
    finally { setRunning(false) }
  }
  return <Stack direction="column" gap={2}>
    <Text color="secondary" variant="bodySmall">{t('gofi.pages.expressions.test-intro', 'Evaluates the expression in the editor (saved or not) with the runtime CEL environment and limits. Nothing is sent.')}</Text>
    <Field label={t('gofi.pages.expressions.field-parse-with-flow', 'Parse with flow')} description={t('gofi.pages.expressions.field-parse-with-flow-description', 'Decode and parse the raw sample exactly as that flow would. Without one, fields come from the JSON below.')} noMargin>
      <Combobox value={flow} options={[{ value: '', label: t('gofi.pages.expressions.no-flow-option', 'No flow (use fields JSON)') }, ...flows.map(l => ({ value: l.name, label: l.name }))]} onChange={option => setFlow(option.value)} />
    </Field>
    <Field label={t('gofi.pages.expressions.field-raw-frame', 'Raw frame (raw)')} noMargin><TextArea rows={3} className={styles.mono} value={raw} onChange={event => setRaw(event.currentTarget.value)} /></Field>
    {!flow && <Field label={t('gofi.pages.expressions.field-fields-json', 'Fields (JSON object)')} noMargin><TextArea rows={4} className={styles.mono} value={fields} onChange={event => setFields(event.currentTarget.value)} /></Field>}
    <div><Button icon="play" disabled={running || !expression.trim()} onClick={() => void run()}>{running ? t('gofi.pages.expressions.evaluating', 'Evaluating...') : t('gofi.pages.expressions.evaluate', 'Evaluate')}</Button></div>
    {error && <Alert severity="error" title={t('gofi.pages.expressions.evaluation-failed', 'Evaluation request failed')}>{error}</Alert>}
    {result && <div role="status" className={styles.result}>
      <Stack gap={2} wrap="wrap" alignItems="center">
        {result.error ? <Badge color="red" icon="exclamation-triangle" text={t('gofi.pages.expressions.result-error', 'Error')} /> : resultType === 'bool' ? <Badge color={result.value === true ? 'green' : 'orange'} text={result.value === true ? t('gofi.pages.expressions.result-matched', 'Matched (true)') : t('gofi.pages.expressions.result-not-matched', 'Not matched (false)')} /> : <Badge color="blue" text={t('gofi.pages.expressions.result-value', 'Value')} />}
        <Text color="secondary" variant="bodySmall">{t('gofi.pages.expressions.result-cost', 'cost {{cost}} · {{ms}} ms', { cost: formatNumber(result.cost), ms: formatNumber(result.durationUs / 1000, { minimumFractionDigits: 2, maximumFractionDigits: 2 }) })}</Text>
      </Stack>
      {result.error ? <Text color="error" element="p">{result.error}</Text> : <pre className={styles.pre}>{JSON.stringify(result.value, null, 2)}</pre>}
      <Field label={t('gofi.pages.expressions.field-bound-fields', 'Bound fields')} noMargin><pre className={styles.pre}>{JSON.stringify(result.boundFields, null, 2)}</pre></Field>
      <Field label={t('gofi.pages.expressions.field-bound-meta', 'Bound meta')} noMargin><pre className={styles.pre}>{JSON.stringify(result.boundMeta, null, 2)}</pre></Field>
    </div>}
  </Stack>
}

const getStyles = (theme: GrafanaTheme2) => ({
  grow: css({ flex: 1, minWidth: 0 }),
  // Grafana's TagList right-aligns; tags read with the rest of the row.
  tags: css({ justifyContent: 'flex-start' }),
  code: css({ display: 'inline-block', maxWidth: 420, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', verticalAlign: 'bottom', fontFamily: theme.typography.fontFamilyMonospace, fontSize: theme.typography.bodySmall.fontSize }),
  mono: css({ fontFamily: theme.typography.fontFamilyMonospace }),
  result: css({ display: 'flex', flexDirection: 'column', gap: theme.spacing(1.5), padding: theme.spacing(1.5), border: `1px solid ${theme.colors.border.weak}`, borderRadius: theme.shape.radius.default, background: theme.colors.background.secondary }),
  pre: css({ margin: 0, fontFamily: theme.typography.fontFamilyMonospace, fontSize: theme.typography.bodySmall.fontSize, whiteSpace: 'pre-wrap', wordBreak: 'break-word' }),
})
