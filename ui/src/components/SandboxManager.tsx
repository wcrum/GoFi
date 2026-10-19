import { lazy, Suspense, useCallback, useEffect, useRef, useState } from 'react'
import { StatusIndicator } from './StatusIndicator'
import { css } from '@emotion/css'
import type { GrafanaTheme2, IconName } from '@grafana/data'
import { Alert, Badge, Button, Card, ConfirmModal, ControlledCollapse, Dropdown, Field, Grid, Icon, IconButton, Input, Menu, Modal, PanelChrome, Stack, Tab, TabsBar, Text, TextLink, useStyles2 } from '@grafana/ui'
import { applySandbox, deleteSandbox, formatPythonSource, listLibraryFunctions, listSandboxes, validatePythonSource, type FunctionTest, type LibraryFunction, type SandboxDefinition, type SandboxStatus, type SourceDiagnostic } from '../sandbox-api'
import { DEFAULT_LIBRARY, FUNCTION_TEMPLATES, TEMPLATE_GROUPS, insertTemplate } from './canvas/python-templates'
import { cardSurface } from '../theme/surfaces'
import { navigate, paths, to } from '../router'
import type { RevealRequest, SourceHighlights } from './PythonSourceEditor'
import { EnvironmentTab } from './python/EnvironmentTab'
import { FunctionsTab } from './python/FunctionsTab'
import { NetworkTab } from './python/NetworkTab'
import { functionKey, RunPanel } from './python/RunPanel'
import { TestsTab } from './python/TestsTab'
import { WorkerLogsPanel } from './python/WorkerLogsPanel'
import { PageLoading } from './PageLoading'
import { CodeView } from './CodeView'
import { useGoFi } from '../state'
import { inlineBlocks } from '../lib/inline-python'
import { t, Trans } from '../i18n'

const PythonSourceEditor = lazy(() => import('./PythonSourceEditor'))
type LibraryTab = 'source' | 'functions' | 'tests' | 'logs' | 'environment' | 'network'
const newLibrary = (): SandboxDefinition => ({ name: 'my-functions', sourceCode: DEFAULT_LIBRARY, timeoutSeconds: 5, memoryMib: 128, tests: [], egress: [] })
/** The parts of a definition that Save and launch persists. */
const persisted = (library?: SandboxDefinition) => library && JSON.stringify([library.sourceCode, library.timeoutSeconds ?? 5, library.memoryMib ?? 128, library.tests ?? [], library.egress ?? []])
const managedFlow = (library: SandboxDefinition) => library.managedBy?.startsWith('flow:') ? library.managedBy.slice(5) : undefined

/** `initialSelected` names a library to open once the inventory loads (the
 * New function wizard lands here after saving). */
export default function SandboxManager({ initialSelected }: { initialSelected?: string } = {}) {
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
  // The last validation's verdict; any edit clears it.
  const [validity, setValidity] = useState<'valid' | 'invalid' | undefined>()
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
  // Open the requested library once it appears in the inventory (only once:
  // later edits or selections must not be overridden by the next poll).
  const pendingSelect = useRef(initialSelected)
  useEffect(() => {
    const wanted = pendingSelect.current
    if (!wanted) return
    const library = libraries.find(item => item.name === wanted)
    if (library) { pendingSelect.current = undefined; select(library) }
  }, [libraries]) // eslint-disable-line react-hooks/exhaustive-deps

  const saved = libraries.find(library => library.name === draft.name)
  const status = statuses.find(item => item.name === draft.name)
  const dirty = !saved || persisted(saved) !== persisted(draft)
  const running = !!saved && !dirty && !status?.error && (status?.phase === 'Running' || status?.phase === 'Ready')
  const managedBy = saved ? managedFlow(saved) : undefined
  const readOnly = !!managedBy
  // A flow-managed library is generated from the flow's inline Python: show
  // that code as written, not the generated wrapper module.
  const { flows } = useGoFi()
  const managedFlowDef = managedBy ? flows.find(flow => flow.name === managedBy) : undefined
  const inline = managedFlowDef ? inlineBlocks(managedFlowDef) : []
  const registry = functions.filter(fn => fn.library === draft.name)
  const target = { source: draft.sourceCode, savedLibrary: running ? draft.name : undefined, timeoutSeconds: draft.timeoutSeconds, memoryMib: draft.memoryMib }
  const userLibraries = libraries.filter(library => !managedFlow(library))
  const flowLibraries = libraries.filter(library => managedFlow(library))

  const select = (library: SandboxDefinition) => { setDraft(structuredClone({ ...library, tests: library.tests ?? [], egress: library.egress ?? [] })); setDiagnostics([]); setValidity(undefined); setError(''); setMessage(null); setRunSelection(''); setTestHighlights(null) }
  const updateSource = (sourceCode: string) => { setDraft(previous => ({ ...previous, sourceCode })); setDiagnostics([]); setValidity(undefined); setMessage(null); setTestHighlights(null) }
  const onRunHighlight = useCallback((highlights: SourceHighlights, reason: 'static' | 'result') => { setRunHighlights(highlights); if (reason === 'result') setTestHighlights(null) }, [])
  const jumpTo = (line: number, column?: number) => { setTab('source'); setReveal({ line, column, nonce: Date.now() }) }
  async function action(kind: string, work: () => Promise<void>) { setBusy(kind); setError(''); setMessage(null); try { await work() } catch (reason) { setError(String(reason)) } finally { setBusy(null) } }
  const save = () => action('save', async () => { await applySandbox(draft); await refresh(); setMessage({ tone: 'success', text: t('gofi.sandbox.manager.saved-message', 'Library saved; worker launch requested. Functions register once it is running.') }) })
  const validate = () => action('validate', async () => {
    const validation = await validatePythonSource(draft.sourceCode)
    setDiagnostics(validation.diagnostics)
    setValidity(validation.valid ? 'valid' : 'invalid')
  })
  const addTest = (test: FunctionTest) => { setDraft(previous => ({ ...previous, tests: [...(previous.tests ?? []), test] })); setMessage({ tone: 'success', text: t('gofi.sandbox.manager.test-saved-message', 'Saved "{{name}}" to Tests. Save and launch to keep it.', { name: test.name }) }) }

  // Fifteen templates are a picker, not a menu: a modal with grouped cards
  // scrolls inside itself. Grafana's Dropdown only offsets its overlay, so a
  // menu this long ran past the viewport, grew the document, and scrolled
  // the whole shell up over a grey gap.
  const [templatePicker, setTemplatePicker] = useState(false)
  const pickTemplate = (source: string) => { updateSource(insertTemplate(draft.sourceCode, source)); setTab('source'); setTemplatePicker(false) }
  const tabs: { id: LibraryTab; label: string; icon: IconName; counter?: number }[] = [
    { id: 'source', label: t('gofi.sandbox.manager.tab-source', 'Source'), icon: 'brackets-curly' },
    { id: 'functions', label: t('gofi.sandbox.manager.tab-functions', 'Functions'), icon: 'list-ul', counter: registry.length || undefined },
    { id: 'tests', label: t('gofi.sandbox.manager.tab-tests', 'Tests'), icon: 'vial', counter: draft.tests?.length || undefined },
    { id: 'logs', label: t('gofi.sandbox.manager.tab-logs', 'Logs'), icon: 'gf-logs' },
    { id: 'environment', label: t('gofi.sandbox.manager.tab-environment', 'Environment'), icon: 'cube' },
    { id: 'network', label: t('gofi.sandbox.manager.tab-network', 'Network'), icon: 'external-link-alt', counter: draft.egress?.length || undefined },
  ]
  const libraryCard = (library: SandboxDefinition) => {
    const libraryStatus = statuses.find(item => item.name === library.name)
    const flow = managedFlow(library)
    return <Card key={library.name} noMargin isCompact isSelected={library.name === draft.name} className={styles.card} onClick={busy ? undefined : () => select(library)}>
      <Card.Heading>{flow ?? library.name}</Card.Heading>
      <Card.Meta>{[...(flow ? [<Badge key="inline" icon="lock" color="blue" text={t('gofi.sandbox.manager.badge-inline-read-only', 'inline · read-only')} />] : []), <StatusIndicator key="phase" text={libraryStatus?.phase ?? t('gofi.sandbox.manager.phase-pending', 'Pending')} tone={libraryStatus?.error ? 'error' : libraryStatus?.phase === 'Running' || libraryStatus?.phase === 'Ready' ? 'success' : 'neutral'} tooltip={libraryStatus?.error || undefined} />, ...(library.egress?.length ? [<Badge key="egress" icon="external-link-alt" color="orange" text={t('gofi.sandbox.manager.badge-egress', 'egress')} />] : [])]}</Card.Meta>
      {libraryStatus?.error && <Card.Description>{libraryStatus.error}</Card.Description>}
      <Card.SecondaryActions>{flow
        ? <IconButton name="arrow-right" tooltip={t('gofi.sandbox.manager.open-flow', 'Open flow {{name}}', { name: flow })} onClick={event => { event.stopPropagation(); navigate(paths.flow(flow)) }} />
        : <IconButton name="trash-alt" tooltip={t('gofi.sandbox.manager.delete-library', 'Delete library')} disabled={!!busy} onClick={() => setConfirmDelete(library.name)} />}</Card.SecondaryActions>
    </Card>
  }

  return <Stack direction="column" gap={2}>
    {loadError && <Alert severity="error" title={t('gofi.sandbox.manager.inventory-unavailable', 'Library inventory unavailable')} bottomSpacing={0}>{loadError}</Alert>}
    {/* The split and the settings grid follow this column's width, not the
        viewport: with GoFiBot or the tutorial docked the page is wide but the
        column is narrow, and viewport breakpoints would still lay out side by side. */}
    <div className={styles.layoutContainer}><div className={styles.layout}>
      {/* Its own column: PanelChrome fills whatever height it is given, so
          without this wrapper the list grew and shrank with the editor. */}
      <div className={styles.libraryColumn}>
      <PanelChrome title={t('gofi.sandbox.manager.libraries', 'Libraries')} actions={<Dropdown placement="bottom-end" overlay={<Menu>
        <Menu.Item label={t('gofi.sandbox.manager.new-guided', 'Guided: tell GoFi what to build')} description={t('gofi.sandbox.manager.new-guided-description', 'A few questions, then a starter you can edit')} icon="compass" onClick={() => navigate(paths.newFunction())} />
        <Menu.Item label={t('gofi.sandbox.manager.new-blank', 'Blank library')} description={t('gofi.sandbox.manager.new-blank-description', 'Start from the example source in the editor')} icon="file-blank" onClick={() => { select(newLibrary()); setTab('source') }} />
      </Menu>}><Button disabled={!!busy} fill="text" size="sm" icon="plus">{t('gofi.common.new', 'New')}</Button></Dropdown>}>
        <Stack direction="column" gap={1}>
          {userLibraries.map(libraryCard)}
          {!userLibraries.length && <Text color="secondary" variant="bodySmall">{t('gofi.sandbox.manager.no-saved-libraries', 'No saved libraries.')}</Text>}
          {/* Inline Python belongs to its flow: selecting it opens the
              generated library read-only; the arrow opens the flow. */}
          {flowLibraries.length > 0 && <>
            <div className={styles.groupTitle}><Text variant="bodySmall" weight="medium" color="secondary">{t('gofi.sandbox.manager.inline-python-in-flows', 'Inline Python in flows')}</Text></div>
            {flowLibraries.map(libraryCard)}
          </>}
        </Stack>
      </PanelChrome>
      </div>

      <div className={styles.main}>
        <div className={styles.toolbar}>
          <div className={styles.settings}><Grid minColumnWidth={21} gap={2} alignItems="start">
            <Field label={t('gofi.sandbox.manager.field-library-name', 'Library name')} noMargin><Input aria-label={t('gofi.sandbox.manager.field-library-name', 'Library name')} disabled={!!busy || readOnly} value={draft.name} onChange={event => setDraft({ ...draft, name: event.currentTarget.value })} placeholder="my-functions" /></Field>
            <Field label={t('gofi.sandbox.manager.field-deadline', 'Deadline (seconds)')} noMargin><Input aria-label={t('gofi.sandbox.manager.field-deadline-aria', 'Worker deadline')} disabled={!!busy || readOnly} type="number" min={1} max={30} value={draft.timeoutSeconds ?? 5} onChange={event => setDraft({ ...draft, timeoutSeconds: Number(event.currentTarget.value) })} /></Field>
            <Field label={t('gofi.sandbox.manager.field-memory', 'Memory (MiB)')} noMargin><Input aria-label={t('gofi.sandbox.manager.field-memory-aria', 'Worker memory')} disabled={!!busy || readOnly} type="number" min={64} max={1024} value={draft.memoryMib ?? 128} onChange={event => setDraft({ ...draft, memoryMib: Number(event.currentTarget.value) })} /></Field>
          </Grid></div>
          <Stack gap={1} alignItems="center" wrap="wrap">
            {dirty && !readOnly ? <StatusIndicator tone="warning" text={saved ? t('gofi.common.unsaved-changes', 'Unsaved changes') : t('gofi.sandbox.manager.not-saved-yet', 'Not saved yet')} /> : status && <StatusIndicator tone={status.error ? 'error' : running ? 'success' : 'neutral'} text={status.phase} tooltip={status.error || undefined} />}
            <Button variant="secondary" icon="brackets-curly" disabled={!!busy || !draft.sourceCode || readOnly} onClick={() => void action('format', async () => { updateSource(await formatPythonSource(draft.sourceCode)); setMessage({ tone: 'success', text: t('gofi.sandbox.manager.source-formatted', 'Source formatted.') }) })}>{busy === 'format' ? t('gofi.sandbox.manager.formatting', 'Formatting...') : t('gofi.sandbox.manager.format', 'Format')}</Button>
            <Button variant="secondary" icon="check" disabled={!!busy || !draft.sourceCode} onClick={() => void validate()}>{busy === 'validate' ? t('gofi.sandbox.manager.validating', 'Validating...') : t('gofi.common.validate', 'Validate')}</Button>
            {!readOnly && <Button variant="primary" icon="save" disabled={!!busy || !draft.name.trim() || !dirty} onClick={() => void save()}>{busy === 'save' ? t('gofi.sandbox.manager.launching-worker', 'Launching worker...') : t('gofi.sandbox.manager.save-and-launch', 'Save and launch')}</Button>}
          </Stack>
        </div>
        {readOnly && <Alert severity="info" title={t('gofi.sandbox.manager.managed-by-flow-title', 'Managed by flow {{name}}', { name: managedBy })} bottomSpacing={0}>
          <Trans i18nKey="gofi.sandbox.manager.managed-by-flow-body" values={{ name: managedBy }}>GoFi generates this library from the inline Python stages of <TextLink href={to(paths.flow(managedBy!))} inline>{'{{name}}'}</TextLink>. Edit those stages in the flow; changes here are not allowed. You can still run and test it.</Trans>
        </Alert>}
        {error && <Alert severity="error" title={t('gofi.sandbox.manager.error-title', 'Python library')} bottomSpacing={0} onRemove={() => setError('')}>{error}</Alert>}
        {message && <Alert severity={message.tone} title={message.text} bottomSpacing={0} onRemove={() => setMessage(null)} />}

        <TabsBar>{tabs.map(item => <Tab key={item.id} label={item.label} icon={item.icon} counter={item.counter} active={tab === item.id} onChangeTab={() => setTab(item.id)} />)}</TabsBar>
        <div className={styles.tabBody}>
          {tab === 'source' && <Stack direction="column" gap={2}>
            <Stack gap={1} alignItems="center" justifyContent="space-between" wrap="wrap">
              <Text color="secondary" variant="bodySmall">{t('gofi.sandbox.manager.tooling-note', 'Formatting and validation run in an isolated tooling worker and never execute the source.')}</Text>
              {!readOnly && <Button variant="secondary" icon="plus" size="sm" onClick={() => setTemplatePicker(true)}>{t('gofi.sandbox.manager.new-function', 'New function')}</Button>}
            </Stack>
            {inline.length > 0 ? <>
              {inline.map(block => <Stack key={block.label} direction="column" gap={0.5}>
                <Text variant="bodySmall" weight="medium">{block.label}</Text>
                <CodeView source={block.source} language="python" maxHeight={480} ariaLabel={t('gofi.sandbox.manager.inline-python-aria', 'Inline Python: {{label}}', { label: block.label })} />
              </Stack>)}
              <ControlledCollapse label={t('gofi.sandbox.manager.generated-module', 'Generated library module')} isOpen={false}>
                <Suspense fallback={<PageLoading text={t('gofi.sandbox.manager.loading-editor', 'Loading editor...')} />}><PythonSourceEditor source={draft.sourceCode} onChange={updateSource} diagnostics={diagnostics} disabled reveal={reveal} highlights={testHighlights ?? runHighlights} validity={validity} /></Suspense>
              </ControlledCollapse>
            </> : <Suspense fallback={<PageLoading text={t('gofi.sandbox.manager.loading-editor', 'Loading editor...')} />}><PythonSourceEditor source={draft.sourceCode} onChange={updateSource} diagnostics={diagnostics} disabled={!!busy || readOnly} reveal={reveal} highlights={testHighlights ?? runHighlights} validity={validity} /></Suspense>}
            {validity === 'valid' && <div className={styles.valid}><Icon name="check-circle" />{t('gofi.sandbox.manager.validation-passed', 'Validation passed')}</div>}
            {!!diagnostics.length && <Alert severity="error" title={t('gofi.sandbox.manager.diagnostic-count', '{{count}} diagnostic(s)', { count: diagnostics.length })} bottomSpacing={0}>
              <Stack direction="column" gap={0.5}>{diagnostics.map((diagnostic, index) => <div key={index}><Button size="sm" fill="text" variant="secondary" onClick={() => jumpTo(diagnostic.line, diagnostic.column)}>{diagnostic.line}:{diagnostic.column}</Button> <Text variant="bodySmall"><Text variant="code">{diagnostic.code}</Text> {diagnostic.message}</Text></div>)}</Stack>
            </Alert>}
            <PanelChrome title={t('gofi.sandbox.manager.run-title', 'Run')} description={t('gofi.sandbox.manager.run-description', 'Run any function in the editor with sample input. Unsaved source runs in a short-lived isolated worker; nothing is saved or sent.')}>
              <RunPanel target={target} registry={registry} selected={runSelection} onSelect={key => { setRunSelection(key); setTestHighlights(null) }} onJumpToLine={jumpTo} onSaveTest={readOnly ? undefined : addTest} onHighlight={onRunHighlight} disabled={!!busy} />
            </PanelChrome>
          </Stack>}
          {tab === 'functions' && <FunctionsTab library={draft.name} registry={registry} source={draft.sourceCode} saved={!dirty && !!saved} onJumpToLine={line => jumpTo(line)} onRun={(kind, name) => { setRunSelection(functionKey(kind, name)); setTab('source') }} />}
          {tab === 'tests' && <TestsTab tests={draft.tests ?? []} onChange={tests => setDraft(previous => ({ ...previous, tests }))} target={target} readOnly={readOnly} onJumpToLine={jumpTo} onHighlight={setTestHighlights} />}
          {tab === 'logs' && (saved ? <Stack direction="column" gap={1}>
            <Text color="secondary" variant="bodySmall">{managedBy
              ? <Trans i18nKey="gofi.sandbox.manager.logs-intro-managed" values={{ library: draft.name, flow: managedBy }}>Live output of the <Text variant="code">{'{{library}}'}</Text> worker: every invocation's prints and errors, including live traffic through {'{{flow}}'}. Lines are bounded to the newest 1000.</Trans>
              : <Trans i18nKey="gofi.sandbox.manager.logs-intro" values={{ library: draft.name }}>Live output of the <Text variant="code">{'{{library}}'}</Text> worker: every invocation's prints and errors, including live traffic. Lines are bounded to the newest 1000.</Trans>}</Text>
            <WorkerLogsPanel key={draft.name} target={{ library: draft.name }} height={480} />
          </Stack> : <Alert severity="info" title={t('gofi.sandbox.manager.logs-unsaved-title', 'Save and launch to follow worker logs')} bottomSpacing={0}>{t('gofi.sandbox.manager.logs-unsaved-body', "The Logs tab follows a saved library's running worker. Runs of unsaved source stream their own output in the Run panel.")}</Alert>)}
          {tab === 'environment' && <EnvironmentTab />}
          {tab === 'network' && <NetworkTab rules={draft.egress ?? []} onChange={egress => setDraft(previous => ({ ...previous, egress }))} readOnly={readOnly} />}
        </div>
      </div>
    </div></div>
    <Modal title={t('gofi.sandbox.manager.template-picker-title', 'Add a function')} isOpen={templatePicker} onDismiss={() => setTemplatePicker(false)}>
      <Stack direction="column" gap={3}>
        <Text color="secondary">{t('gofi.sandbox.manager.template-picker-intro', 'Pick a starting point; it is appended to the library source for you to edit.')}</Text>
        {TEMPLATE_GROUPS.map(group => <Stack key={group.id} direction="column" gap={1}>
          <Text element="h3" variant="h6">{group.label}</Text>
          <div className={styles.templateCards}>
            {FUNCTION_TEMPLATES.filter(template => group.ids.includes(template.id)).map(template => <Card key={template.id} noMargin isCompact className={styles.card} onClick={() => pickTemplate(template.source)}>
              <Card.Heading>{template.label}</Card.Heading>
              <Card.Description>{template.description}</Card.Description>
            </Card>)}
          </div>
        </Stack>)}
      </Stack>
    </Modal>
    <ConfirmModal isOpen={confirmDelete !== null} title={t('gofi.sandbox.manager.delete-title', 'Delete library')} body={t('gofi.sandbox.manager.delete-body', 'Delete "{{name}}"? Its worker stops and flows that reference its functions will fail.', { name: confirmDelete })} confirmText={t('gofi.common.delete', 'Delete')}
      onConfirm={() => { const name = confirmDelete; setConfirmDelete(null); if (name) void action('delete', async () => { await deleteSandbox(name); if (draft.name === name) select(newLibrary()); await refresh() }) }} onDismiss={() => setConfirmDelete(null)} />
  </Stack>
}

const getStyles = (theme: GrafanaTheme2) => ({
  layoutContainer: css({ containerType: 'inline-size', minWidth: 0 }),
  templateCards: css({ display: 'grid', gap: theme.spacing(1), gridTemplateColumns: 'repeat(auto-fill, minmax(min(100%, 240px), 1fr))' }),
  layout: css({ display: 'grid', gap: theme.spacing(2), gridTemplateColumns: 'minmax(0,1fr)', alignItems: 'start', '@container (min-width: 880px)': { gridTemplateColumns: '280px minmax(0,1fr)' } }),
  // Sized by its list. Beside the editor it stays in view while the editor
  // side scrolls; stacked above it (a narrow column, say with GoFiBot
  // docked) it must scroll away like everything else, or it would sit on
  // top of the tab bar.
  libraryColumn: css({ minWidth: 0, alignSelf: 'start', '@container (min-width: 880px)': { position: 'sticky', top: theme.spacing(2) } }),
  card: cardSurface(theme),
  groupTitle: css({ marginTop: theme.spacing(1), paddingTop: theme.spacing(1), borderTop: `1px solid ${theme.colors.border.weak}` }),
  main: css({ minWidth: 0, display: 'flex', flexDirection: 'column', gap: theme.spacing(2) }),
  // Grafana's field validation line, tucked under the editor frame.
  valid: css({ display: 'flex', alignItems: 'center', gap: theme.spacing(0.5), marginTop: theme.spacing(-1.5), color: theme.colors.success.text, fontSize: theme.typography.bodySmall.fontSize }),
  toolbar: css({ display: 'flex', alignItems: 'flex-end', justifyContent: 'space-between', flexWrap: 'wrap', gap: theme.spacing(2) }),
  settings: css({ flex: '1 1 320px', minWidth: 0, maxWidth: 720 }),
  tabBody: css({ minWidth: 0 }),
})
