import { lazy, Suspense, useCallback, useEffect, useState } from 'react'
import { StatusIndicator } from './StatusIndicator'
import { css } from '@emotion/css'
import type { GrafanaTheme2, IconName } from '@grafana/data'
import { Alert, Badge, Button, Card, ConfirmModal, Dropdown, Field, IconButton, Input, Menu, PanelChrome, Stack, Tab, TabsBar, Text, TextLink, useStyles2 } from '@grafana/ui'
import { applySandbox, deleteSandbox, formatPythonSource, listLibraryFunctions, listSandboxes, validatePythonSource, type FunctionTest, type LibraryFunction, type SandboxDefinition, type SandboxStatus, type SourceDiagnostic } from '../sandbox-api'
import { DEFAULT_LIBRARY, FUNCTION_TEMPLATES, insertTemplate } from './canvas/python-templates'
import { paths, to } from '../router'
import type { RevealRequest, SourceHighlights } from './PythonSourceEditor'
import { EnvironmentTab } from './python/EnvironmentTab'
import { FunctionsTab } from './python/FunctionsTab'
import { NetworkTab } from './python/NetworkTab'
import { functionKey, RunPanel } from './python/RunPanel'
import { TestsTab } from './python/TestsTab'
import { WorkerLogsPanel } from './python/WorkerLogsPanel'
import { PageLoading } from './PageLoading'

const PythonSourceEditor = lazy(() => import('./PythonSourceEditor'))
type LibraryTab = 'source' | 'functions' | 'tests' | 'logs' | 'environment' | 'network'
const newLibrary = (): SandboxDefinition => ({ name: 'my-functions', sourceCode: DEFAULT_LIBRARY, timeoutSeconds: 5, memoryMib: 128, tests: [], egress: [] })
/** The parts of a definition that Save and launch persists. */
const persisted = (library?: SandboxDefinition) => library && JSON.stringify([library.sourceCode, library.timeoutSeconds ?? 5, library.memoryMib ?? 128, library.tests ?? [], library.egress ?? []])
const managedFlow = (library: SandboxDefinition) => library.managedBy?.startsWith('flow:') ? library.managedBy.slice(5) : undefined

export default function SandboxManager() {
  const styles = useStyles2(getStyles)
  const [libraries, setLibraries] = useState<SandboxDefinition[]>([])
  const [statuses, setStatuses] = useState<SandboxStatus[]>([])
  const [functions, setFunctions] = useState<LibraryFunction[]>([])
  const [draft, setDraft] = useState<SandboxDefinition>(newLibrary)
  const [tab, setTab] = useState<LibraryTab>('source')
  const [loadError, setLoadError] = useState('')
  const [error, setError] = useState('')
  const [message, setMessage] = useState<{ tone: 'success' | 'warning'; text: string } | null>(null)
  const [busy, setBusy] = useState<string | null>(null)
  const [diagnostics, setDiagnostics] = useState<SourceDiagnostic[]>([])
  const [reveal, setReveal] = useState<RevealRequest | null>(null)
  const [runSelection, setRunSelection] = useState('')
  const [confirmDelete, setConfirmDelete] = useState<string | null>(null)
  // Run-panel highlights, overridden by a test's highlights until the next run/edit.
  const [runHighlights, setRunHighlights] = useState<SourceHighlights>({})
  const [testHighlights, setTestHighlights] = useState<SourceHighlights | null>(null)
  const refresh = useCallback(async () => {
    try { const [inventory, registry] = await Promise.all([listSandboxes(), listLibraryFunctions()]); setLibraries(inventory.sandboxes); setStatuses(inventory.statuses); setFunctions(registry); setLoadError('') }
    catch (reason) { setLoadError(String(reason)) }
  }, [])
  useEffect(() => { void refresh(); const timer = setInterval(refresh, 5000); return () => clearInterval(timer) }, [refresh])

  const saved = libraries.find(library => library.name === draft.name)
  const status = statuses.find(item => item.name === draft.name)
  const dirty = !saved || persisted(saved) !== persisted(draft)
  const running = !!saved && !dirty && !status?.error && (status?.phase === 'Running' || status?.phase === 'Ready')
  const managedBy = saved ? managedFlow(saved) : undefined
  const readOnly = !!managedBy
  const registry = functions.filter(fn => fn.library === draft.name)
  const target = { source: draft.sourceCode, savedLibrary: running ? draft.name : undefined, timeoutSeconds: draft.timeoutSeconds, memoryMib: draft.memoryMib }
  const userLibraries = libraries.filter(library => !managedFlow(library))
  const flowLibraries = libraries.filter(library => managedFlow(library))

  const select = (library: SandboxDefinition) => { setDraft(structuredClone({ ...library, tests: library.tests ?? [], egress: library.egress ?? [] })); setDiagnostics([]); setError(''); setMessage(null); setRunSelection(''); setTestHighlights(null) }
  const updateSource = (sourceCode: string) => { setDraft(previous => ({ ...previous, sourceCode })); setDiagnostics([]); setMessage(null); setTestHighlights(null) }
  const onRunHighlight = useCallback((highlights: SourceHighlights, reason: 'static' | 'result') => { setRunHighlights(highlights); if (reason === 'result') setTestHighlights(null) }, [])
  const jumpTo = (line: number, column?: number) => { setTab('source'); setReveal({ line, column, nonce: Date.now() }) }
  async function action(kind: string, work: () => Promise<void>) { setBusy(kind); setError(''); setMessage(null); try { await work() } catch (reason) { setError(String(reason)) } finally { setBusy(null) } }
  const save = () => action('save', async () => { await applySandbox(draft); await refresh(); setMessage({ tone: 'success', text: 'Library saved; worker launch requested. Functions register once it is running.' }) })
  const validate = () => action('validate', async () => {
    const validation = await validatePythonSource(draft.sourceCode)
    setDiagnostics(validation.diagnostics)
    setMessage(validation.valid ? { tone: 'success', text: 'Validation passed.' } : { tone: 'warning', text: `Validation found ${validation.diagnostics.length} issue(s).` })
  })
  const addTest = (test: FunctionTest) => { setDraft(previous => ({ ...previous, tests: [...(previous.tests ?? []), test] })); setMessage({ tone: 'success', text: `Saved "${test.name}" to Tests. Save and launch to keep it.` }) }

  const templates = <Menu>{FUNCTION_TEMPLATES.map(template => <Menu.Item key={template.id} label={template.label} description={template.description} onClick={() => { updateSource(insertTemplate(draft.sourceCode, template.source)); setTab('source') }} />)}</Menu>
  const tabs: { id: LibraryTab; label: string; icon: IconName; counter?: number }[] = [
    { id: 'source', label: 'Source', icon: 'brackets-curly' },
    { id: 'functions', label: 'Functions', icon: 'list-ul', counter: registry.length || undefined },
    { id: 'tests', label: 'Tests', icon: 'vial', counter: draft.tests?.length || undefined },
    { id: 'logs', label: 'Logs', icon: 'gf-logs' },
    { id: 'environment', label: 'Environment', icon: 'cube' },
    { id: 'network', label: 'Network', icon: 'external-link-alt', counter: draft.egress?.length || undefined },
  ]
  const libraryCard = (library: SandboxDefinition) => {
    const libraryStatus = statuses.find(item => item.name === library.name)
    const flow = managedFlow(library)
    return <Card key={library.name} noMargin isCompact isSelected={library.name === draft.name} onClick={busy ? undefined : () => select(library)}>
      <Card.Heading>{library.name}</Card.Heading>
      <Card.Meta>{[<StatusIndicator key="phase" text={libraryStatus?.phase ?? 'Pending'} tone={libraryStatus?.error ? 'error' : libraryStatus?.phase === 'Running' || libraryStatus?.phase === 'Ready' ? 'success' : 'neutral'} tooltip={libraryStatus?.error || undefined} />, ...(library.egress?.length ? [<Badge key="egress" icon="external-link-alt" color="orange" text="egress" />] : [])]}</Card.Meta>
      {libraryStatus?.error && <Card.Description>{libraryStatus.error}</Card.Description>}
      {flow && <Card.Description>Inline stages of <TextLink href={to(paths.flow(flow))} inline>{flow}</TextLink></Card.Description>}
      {!flow && <Card.SecondaryActions><IconButton name="trash-alt" tooltip="Delete library" disabled={!!busy} onClick={() => setConfirmDelete(library.name)} /></Card.SecondaryActions>}
    </Card>
  }

  return <Stack direction="column" gap={2}>
    {loadError && <Alert severity="error" title="Library inventory unavailable" bottomSpacing={0}>{loadError}</Alert>}
    <div className={styles.layout}>
      <PanelChrome title="Libraries" actions={<Button disabled={!!busy} fill="text" size="sm" icon="plus" onClick={() => { select(newLibrary()); setTab('source') }}>New</Button>}>
        <Stack direction="column" gap={1}>
          {userLibraries.map(libraryCard)}
          {!userLibraries.length && <Text color="secondary" variant="bodySmall">No saved libraries.</Text>}
          {flowLibraries.length > 0 && <>
            <div className={styles.groupTitle}><Text variant="bodySmall" weight="medium" color="secondary">Managed by flows</Text></div>
            {flowLibraries.map(libraryCard)}
          </>}
        </Stack>
      </PanelChrome>

      <div className={styles.main}>
        <div className={styles.toolbar}>
          <div className={styles.settings}>
            <Field label="Library name" noMargin><Input aria-label="Library name" disabled={!!busy || readOnly} value={draft.name} onChange={event => setDraft({ ...draft, name: event.currentTarget.value })} placeholder="my-functions" /></Field>
            <Field label="Deadline (seconds)" noMargin><Input aria-label="Worker deadline" disabled={!!busy || readOnly} type="number" min={1} max={30} value={draft.timeoutSeconds ?? 5} onChange={event => setDraft({ ...draft, timeoutSeconds: Number(event.currentTarget.value) })} /></Field>
            <Field label="Memory (MiB)" noMargin><Input aria-label="Worker memory" disabled={!!busy || readOnly} type="number" min={64} max={1024} value={draft.memoryMib ?? 128} onChange={event => setDraft({ ...draft, memoryMib: Number(event.currentTarget.value) })} /></Field>
          </div>
          <Stack gap={1} alignItems="center" wrap="wrap">
            {dirty && !readOnly ? <StatusIndicator tone="warning" text={saved ? 'Unsaved changes' : 'Not saved yet'} /> : status && <StatusIndicator tone={status.error ? 'error' : running ? 'success' : 'neutral'} text={status.phase} tooltip={status.error || undefined} />}
            <Button variant="secondary" icon="brackets-curly" disabled={!!busy || !draft.sourceCode || readOnly} onClick={() => void action('format', async () => { updateSource(await formatPythonSource(draft.sourceCode)); setMessage({ tone: 'success', text: 'Source formatted.' }) })}>{busy === 'format' ? 'Formatting...' : 'Format'}</Button>
            <Button variant="secondary" icon="check" disabled={!!busy || !draft.sourceCode} onClick={() => void validate()}>{busy === 'validate' ? 'Validating...' : 'Validate'}</Button>
            {!readOnly && <Button variant="primary" icon="save" disabled={!!busy || !draft.name.trim() || !dirty} onClick={() => void save()}>{busy === 'save' ? 'Launching worker...' : 'Save and launch'}</Button>}
          </Stack>
        </div>
        {readOnly && <Alert severity="info" title={`Managed by flow ${managedBy}`} bottomSpacing={0}>GoFi generates this library from the inline Python stages of <TextLink href={to(paths.flow(managedBy!))} inline>{managedBy}</TextLink>. Edit those stages in the flow; changes here are not allowed. You can still run and test it.</Alert>}
        {error && <Alert severity="error" title="Python library" bottomSpacing={0} onRemove={() => setError('')}>{error}</Alert>}
        {message && <Alert severity={message.tone} title={message.text} bottomSpacing={0} onRemove={() => setMessage(null)} />}

        <TabsBar>{tabs.map(item => <Tab key={item.id} label={item.label} icon={item.icon} counter={item.counter} active={tab === item.id} onChangeTab={() => setTab(item.id)} />)}</TabsBar>
        <div className={styles.tabBody}>
          {tab === 'source' && <Stack direction="column" gap={2}>
            <Stack gap={1} alignItems="center" justifyContent="space-between" wrap="wrap">
              <Text color="secondary" variant="bodySmall">Formatting and validation run in an isolated tooling worker and never execute the source.</Text>
              {!readOnly && <Dropdown overlay={templates} placement="bottom-end"><Button variant="secondary" icon="plus" size="sm">New function</Button></Dropdown>}
            </Stack>
            <Suspense fallback={<PageLoading text="Loading editor..." />}><PythonSourceEditor source={draft.sourceCode} onChange={updateSource} diagnostics={diagnostics} disabled={!!busy || readOnly} reveal={reveal} highlights={testHighlights ?? runHighlights} /></Suspense>
            {!!diagnostics.length && <Alert severity="error" title={`${diagnostics.length} diagnostic(s)`} bottomSpacing={0}>
              <Stack direction="column" gap={0.5}>{diagnostics.map((diagnostic, index) => <div key={index}><Button size="sm" fill="text" variant="secondary" onClick={() => jumpTo(diagnostic.line, diagnostic.column)}>{diagnostic.line}:{diagnostic.column}</Button> <Text variant="bodySmall"><Text variant="code">{diagnostic.code}</Text> {diagnostic.message}</Text></div>)}</Stack>
            </Alert>}
            <PanelChrome title="Run" description="Run any function in the editor with sample input. Unsaved source runs in a short-lived isolated worker; nothing is saved or sent.">
              <RunPanel target={target} registry={registry} selected={runSelection} onSelect={key => { setRunSelection(key); setTestHighlights(null) }} onJumpToLine={jumpTo} onSaveTest={readOnly ? undefined : addTest} onHighlight={onRunHighlight} disabled={!!busy} />
            </PanelChrome>
          </Stack>}
          {tab === 'functions' && <FunctionsTab library={draft.name} registry={registry} source={draft.sourceCode} saved={!dirty && !!saved} onJumpToLine={line => jumpTo(line)} onRun={(kind, name) => { setRunSelection(functionKey(kind, name)); setTab('source') }} />}
          {tab === 'tests' && <TestsTab tests={draft.tests ?? []} onChange={tests => setDraft(previous => ({ ...previous, tests }))} target={target} readOnly={readOnly} onJumpToLine={jumpTo} onHighlight={setTestHighlights} />}
          {tab === 'logs' && (saved ? <Stack direction="column" gap={1}>
            <Text color="secondary" variant="bodySmall">Live output of the <Text variant="code">{draft.name}</Text> worker: every invocation's prints and errors, including live traffic{managedBy ? ` through ${managedBy}` : ''}. Lines are bounded to the newest 1000.</Text>
            <WorkerLogsPanel key={draft.name} target={{ library: draft.name }} height={480} />
          </Stack> : <Alert severity="info" title="Save and launch to follow worker logs" bottomSpacing={0}>The Logs tab follows a saved library's running worker. Runs of unsaved source stream their own output in the Run panel.</Alert>)}
          {tab === 'environment' && <EnvironmentTab />}
          {tab === 'network' && <NetworkTab rules={draft.egress ?? []} onChange={egress => setDraft(previous => ({ ...previous, egress }))} readOnly={readOnly} />}
        </div>
      </div>
    </div>
    <ConfirmModal isOpen={confirmDelete !== null} title="Delete library" body={`Delete "${confirmDelete}"? Its worker stops and flows that reference its functions will fail.`} confirmText="Delete"
      onConfirm={() => { const name = confirmDelete; setConfirmDelete(null); if (name) void action('delete', async () => { await deleteSandbox(name); if (draft.name === name) select(newLibrary()); await refresh() }) }} onDismiss={() => setConfirmDelete(null)} />
  </Stack>
}

const getStyles = (theme: GrafanaTheme2) => ({
  layout: css({ display: 'grid', gap: theme.spacing(2), gridTemplateColumns: 'minmax(0,1fr)', alignItems: 'start', [theme.breakpoints.up('xl')]: { gridTemplateColumns: '280px minmax(0,1fr)' } }),
  groupTitle: css({ marginTop: theme.spacing(1), paddingTop: theme.spacing(1), borderTop: `1px solid ${theme.colors.border.weak}` }),
  main: css({ minWidth: 0, display: 'flex', flexDirection: 'column', gap: theme.spacing(2) }),
  toolbar: css({ display: 'flex', alignItems: 'flex-end', justifyContent: 'space-between', flexWrap: 'wrap', gap: theme.spacing(2) }),
  settings: css({ display: 'grid', gap: theme.spacing(2), gridTemplateColumns: 'minmax(180px, 2fr) repeat(2, minmax(120px, 1fr))', flex: '1 1 480px', [theme.breakpoints.down('md')]: { gridTemplateColumns: '1fr' } }),
  tabBody: css({ minWidth: 0 }),
})
