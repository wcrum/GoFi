import { useEffect, useMemo, useState } from 'react'
import { css } from '@emotion/css'
import type { GrafanaTheme2 } from '@grafana/data'
import {
  Alert, Badge, Button, Combobox, ConfirmModal, Drawer, EmptyState, Field, FilterInput, IconButton, Input, InteractiveTable,
  RadioButtonGroup, Stack, Tab, TabsBar, TagList, TagsInput, Text, TextArea, TextLink, useStyles2, type Column,
} from '@grafana/ui'
import { CelEditor } from '../components/cel/CelEditor'
import { useExpressions } from '../components/cel/useExpressions'
import { useToast } from '../components/Toast'
import { applyExpression, deleteExpression, describeEnvironment, evaluateExpression, type CelExpression, type EvaluationResult, type ResultType } from '../expression-api'
import { navigate, paths, to } from '../router'
import { Page } from '../shell/Page'
import { useGoFi } from '../state'
import { PageLoading } from '../components/PageLoading'

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
    { id: 'name', header: 'Name', sortType: 'string', cell: ({ row: { original } }) => <Stack direction="column" gap={0}>
      <TextLink href={to(paths.expressions(original.name))} inline={false} weight="medium">{original.name}</TextLink>
      {original.description && <Text color="secondary" variant="bodySmall" truncate>{original.description}</Text>}
    </Stack> },
    { id: 'expression', header: 'Expression', cell: ({ row: { original } }) => <code className={styles.code} title={original.expression}>{original.expression}</code> },
    { id: 'resultType', header: 'Result', cell: ({ row: { original } }) => <Badge color={original.resultType === 'bool' ? 'blue' : 'purple'} text={original.resultType ?? 'bool'} /> },
    { id: 'tags', header: 'Tags', cell: ({ row: { original } }) => original.tags?.length ? <TagList tags={original.tags} onClick={name => setTag(name)} /> : null },
    { id: 'generation', header: 'Generation', sortType: 'number', cell: ({ row: { original } }) => <Text tabular>{original.generation}{original.pinnedRevisions?.length ? ` (+${original.pinnedRevisions.length} pinned)` : ''}</Text> },
    { id: 'usedBy', header: 'Used by', cell: ({ row: { original } }) => original.usedBy?.length ? <Stack gap={1} wrap="wrap">{original.usedBy.map(name => <TextLink key={name} href={to(paths.flow(name))} inline={false}>{name}</TextLink>)}</Stack> : <Text color="secondary">—</Text> },
    { id: 'actions', header: '', cell: ({ row: { original } }) => <Stack gap={0.5} justifyContent="flex-end">
      <IconButton name="pen" tooltip="Edit and test" onClick={() => navigate(paths.expressions(original.name))} />
      <IconButton name="trash-alt" tooltip={original.usedBy?.length ? 'In use by flows' : 'Delete'} disabled={!!original.usedBy?.length} onClick={() => { setDeleteError(null); setDeleting(original) }} />
    </Stack> },
  ]

  async function remove(expression: CelExpression) {
    try { await deleteExpression(expression.name); setDeleting(null); addToast(`Deleted "${expression.name}"`, 'success'); await refresh() }
    catch (reason) { setDeleteError(String(reason)); setDeleting(null) }
  }

  return <Page title="CEL library" icon="book-open"
    subTitle="Named Common Expression Language expressions that filters, transforms, dedup keys, and sink conditions link to. Editing an entry reloads every flow that follows it; pinned flows keep their revision."
    actions={<Button icon="plus" onClick={() => navigate(paths.expressions('new'))}>New expression</Button>}>
    <Stack direction="column" gap={2}>
      {error && <Alert severity="error" title="CEL library unavailable">{error}</Alert>}
      {deleteError && <Alert severity="error" title="Delete refused" onRemove={() => setDeleteError(null)}>{deleteError}</Alert>}
      <EnvironmentSummary />
      {loading ? <PageLoading text="Loading library..." />
        : !expressions.length ? <EmptyState variant="call-to-action" message="No CEL expressions yet" button={<Button icon="plus" onClick={() => navigate(paths.expressions('new'))}>New expression</Button>}>
          Save an expression once and link it from any flow's filter, transform, dedup, or sink condition.
        </EmptyState>
        : <>
          <Stack gap={1} alignItems="center">
            <div className={styles.grow}><FilterInput value={query} onChange={setQuery} placeholder="Search name, description, expression, or tag" /></div>
            {tag && <Button variant="secondary" fill="outline" icon="times" onClick={() => setTag(null)}>{tag}</Button>}
          </Stack>
          <InteractiveTable columns={columns} data={rows} getRowId={row => row.name} pageSize={25} />
        </>}
    </Stack>
    {selected && (editing || selected === 'new') && <ExpressionDrawer key={selected} initial={editing!} isNew={selected === 'new'} onClose={() => navigate(paths.expressions())} onSaved={name => { void refresh(); navigate(paths.expressions(name), { replace: true }) }} />}
    {selected && selected !== 'new' && !loading && !editing && <Drawer title="Not found" onClose={() => navigate(paths.expressions())} size="sm"><EmptyState variant="not-found" message={`CEL expression "${selected}" does not exist`} /></Drawer>}
    <ConfirmModal isOpen={!!deleting} title="Delete CEL expression" body={`Delete "${deleting?.name}"? This cannot be undone.`} confirmText="Delete" onConfirm={() => { if (deleting) void remove(deleting) }} onDismiss={() => setDeleting(null)} />
  </Page>
}

function EnvironmentSummary() {
  const [summary, setSummary] = useState<string | null>(null)
  useEffect(() => { describeEnvironment().then(env => setSummary(`Variables: ${env.variables.map(v => `${v.name} (${v.type})`).join(', ')} · ${env.functions.length} functions · cost limit ${env.costLimit.toLocaleString()} per evaluation · ${env.maxExpressionBytes.toLocaleString()} bytes max`)).catch(() => {}) }, [])
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
      addToast(reloadedFlows.length ? `Saved "${expression.name}" (generation ${expression.generation}); reloading ${reloadedFlows.join(', ')}` : `Saved "${expression.name}" (generation ${expression.generation})`, 'success')
      onSaved(expression.name)
    } catch (reason) { setSaveError(String(reason)) }
    finally { setSaving(false) }
  }

  return <Drawer title={isNew ? 'New CEL expression' : draft.name} subtitle={isNew ? undefined : `Generation ${initial.generation}${initial.usedBy?.length ? ` · used by ${initial.usedBy.join(', ')}` : ' · not used yet'}`} onClose={onClose} size="md"
    tabs={<TabsBar><Tab label="Definition" active={tab === 'definition'} onChangeTab={() => setTab('definition')} /><Tab label="Test" icon="play" active={tab === 'test'} onChangeTab={() => setTab('test')} /></TabsBar>}>
    {tab === 'definition' ? <Stack direction="column" gap={2}>
      {saveError && <Alert severity="error" title="Not saved">{saveError}</Alert>}
      <Field label="Name" description="Lowercase letters, numbers, and hyphens. Flows link to this name." noMargin>
        <Input value={draft.name} disabled={!isNew} placeholder="hot-readings" onChange={event => setDraft({ ...draft, name: event.currentTarget.value })} />
      </Field>
      <Field label="Result type" description="bool for filters and sink conditions; any for transform values and dedup keys." noMargin>
        <RadioButtonGroup value={draft.resultType ?? 'bool'} options={[{ value: 'bool', label: 'bool' }, { value: 'any', label: 'any' }]} onChange={value => setDraft({ ...draft, resultType: value as ResultType })} />
      </Field>
      <Field label="Expression" noMargin>
        <CelEditor value={draft.expression} onChange={expression => setDraft(previous => ({ ...previous, expression }))} resultType={draft.resultType ?? 'bool'} height={140} placeholder={'fields["temperature"] > 30.0'} aria-label="Library expression" />
      </Field>
      <Field label="Description" noMargin>
        <TextArea rows={2} value={draft.description ?? ''} onChange={event => setDraft({ ...draft, description: event.currentTarget.value })} />
      </Field>
      <Field label="Tags" noMargin>
        <TagsInput tags={draft.tags ?? []} onChange={tags => setDraft({ ...draft, tags })} />
      </Field>
      {!!initial.pinnedRevisions?.length && <Field label="Pinned revisions" description="Kept while a flow pins them." noMargin>
        <Stack direction="column" gap={1}>{initial.pinnedRevisions.map(r => <div key={r.generation}><Badge color="orange" icon="lock" text={`gen ${r.generation}`} /> <code className={styles.code}>{r.expression}</code></div>)}</Stack>
      </Field>}
      <Stack gap={1}>
        <Button icon="save" disabled={saving || !dirty || !draft.name || !draft.expression.trim()} onClick={() => void save()}>{saving ? 'Saving...' : 'Save'}</Button>
        <Button variant="secondary" fill="outline" onClick={() => setTab('test')}>Test</Button>
      </Stack>
      {!isNew && initial.usedBy?.length ? <Text color="secondary" variant="bodySmall">Saving a new expression text validates it against every linked flow first; flows following the latest generation reload.</Text> : null}
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
        if (!value || typeof value !== 'object' || Array.isArray(value)) throw new Error('Fields must be a JSON object')
        parsed = value as Record<string, unknown>
      }
      setResult(await evaluateExpression({ expression, resultType, raw, fields: parsed, flow: flow || undefined }))
    } catch (reason) { setError(String(reason)) }
    finally { setRunning(false) }
  }
  return <Stack direction="column" gap={2}>
    <Text color="secondary" variant="bodySmall">Evaluates the expression in the editor (saved or not) with the runtime CEL environment and limits. Nothing is sent.</Text>
    <Field label="Parse with flow" description="Decode and parse the raw sample exactly as that flow would. Without one, fields come from the JSON below." noMargin>
      <Combobox value={flow} options={[{ value: '', label: 'No flow (use fields JSON)' }, ...flows.map(l => ({ value: l.name, label: l.name }))]} onChange={option => setFlow(option.value)} />
    </Field>
    <Field label="Raw frame (raw)" noMargin><TextArea rows={3} className={styles.mono} value={raw} onChange={event => setRaw(event.currentTarget.value)} /></Field>
    {!flow && <Field label="Fields (JSON object)" noMargin><TextArea rows={4} className={styles.mono} value={fields} onChange={event => setFields(event.currentTarget.value)} /></Field>}
    <div><Button icon="play" disabled={running || !expression.trim()} onClick={() => void run()}>{running ? 'Evaluating...' : 'Evaluate'}</Button></div>
    {error && <Alert severity="error" title="Evaluation request failed">{error}</Alert>}
    {result && <div role="status" className={styles.result}>
      <Stack gap={2} wrap="wrap" alignItems="center">
        {result.error ? <Badge color="red" icon="exclamation-triangle" text="Error" /> : resultType === 'bool' ? <Badge color={result.value === true ? 'green' : 'orange'} text={result.value === true ? 'Matched (true)' : 'Not matched (false)'} /> : <Badge color="blue" text="Value" />}
        <Text color="secondary" variant="bodySmall">cost {result.cost.toLocaleString()} · {(result.durationUs / 1000).toFixed(2)} ms</Text>
      </Stack>
      {result.error ? <Text color="error" element="p">{result.error}</Text> : <pre className={styles.pre}>{JSON.stringify(result.value, null, 2)}</pre>}
      <Field label="Bound fields" noMargin><pre className={styles.pre}>{JSON.stringify(result.boundFields, null, 2)}</pre></Field>
      <Field label="Bound meta" noMargin><pre className={styles.pre}>{JSON.stringify(result.boundMeta, null, 2)}</pre></Field>
    </div>}
  </Stack>
}

const getStyles = (theme: GrafanaTheme2) => ({
  grow: css({ flex: 1, minWidth: 0 }),
  code: css({ display: 'inline-block', maxWidth: 420, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', verticalAlign: 'bottom', fontFamily: theme.typography.fontFamilyMonospace, fontSize: theme.typography.bodySmall.fontSize }),
  mono: css({ fontFamily: theme.typography.fontFamilyMonospace }),
  result: css({ display: 'flex', flexDirection: 'column', gap: theme.spacing(1.5), padding: theme.spacing(1.5), border: `1px solid ${theme.colors.border.weak}`, borderRadius: theme.shape.radius.default, background: theme.colors.background.secondary }),
  pre: css({ margin: 0, fontFamily: theme.typography.fontFamilyMonospace, fontSize: theme.typography.bodySmall.fontSize, whiteSpace: 'pre-wrap', wordBreak: 'break-word' }),
})
