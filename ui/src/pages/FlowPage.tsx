import { lazy, Suspense, useState } from 'react'
import { css } from '@emotion/css'
import type { GrafanaTheme2 } from '@grafana/data'
import { Alert, Badge, Button, ConfirmModal, LinkButton, RadioButtonGroup, Stack, useStyles2 } from '@grafana/ui'
import FlowCanvas from '../components/canvas/FlowCanvas'
import { LogsExplorer } from '../components/logs/LogsExplorer'
import { deleteFlow, getRouteHealth, type RouteHealth } from '../api'
import { useToast } from '../components/Toast'
import { navigate, paths, type FlowTab } from '../router'
import { Page, type PageTab } from '../shell/Page'
import { useGoFi } from '../state'
import { listenOf, sourceSummary, type FlowDefinition } from '../types'
import { NotFoundPage } from './NotFoundPage'
import { PageLoading } from '../components/PageLoading'
import { PageLoadingBar } from '../components/PageLoadingBar'
import { RuntimeFailure } from '../components/RuntimeFailure'
import { RouteFailures } from '../components/RouteFailures'
import { ConnectivityPanel } from '../components/ConnectivityPanel'
import { FlowYamlEditor } from '../components/FlowYamlEditor'
import { RevisionStamp, revisionLineHeight } from '../components/history/RevisionStamp'
import { RunControls, RunStateIndicator } from '../components/operations/RunControls'
import { ProblemsPanel } from '../components/operations/ProblemsPanel'
import { listBulletins, loadFlowStatuses, useFlowStatus, type Bulletin } from '../operations-api'
import { listFlowRevisions, type FlowRevision, type RevisionList } from '../history-api'
import { analyzeConnectivity, type Connectivity } from '../kubernetes-api'
import { loadRuntimeInfo } from '../runtime-api'
import { useInitialLoad } from '../lib/useInitialLoad'
import { t } from '../i18n'

const loadFlowResource = () => import('../components/history/FlowResource')
const FlowResource = lazy(loadFlowResource)

type EditorKind = 'visual' | 'yaml'
const EDITOR_KEY = 'gofi.flow.editor'
const editorOptions = () => [{ label: t('gofi.pages.flow.editor-visual', 'Visual'), value: 'visual' as const, icon: 'sitemap' as const }, { label: 'YAML', value: 'yaml' as const, icon: 'brackets-curly' as const }]

/** Everything the page's first paint shows: the header's run state,
 * revision line and failure banners, plus the data of the tab that is open.
 * Gathered at once, so the page appears whole rather than badge by badge. */
interface FlowPageData {
  revision: FlowRevision | null
  routes: RouteHealth[]
  /** The tab whose data was prefetched; another tab loads its own. */
  tab: FlowTab
  bulletins?: Bulletin[]
  revisions?: RevisionList
  connectivity?: Connectivity
}
const quiet = <T,>(promise: Promise<T>, fallback: T): Promise<T> => promise.catch(() => fallback)

async function loadFlowPage(name: string, tab: FlowTab, definition: FlowDefinition | undefined): Promise<FlowPageData> {
  const prefetch: Promise<Partial<FlowPageData>> = tab === 'problems' ? listBulletins(name, true).then(bulletins => ({ bulletins }))
    : tab === 'resource' ? Promise.all([listFlowRevisions(name), loadFlowResource()]).then(([revisions]) => ({ revisions }))
    : tab === 'connectivity' && definition ? analyzeConnectivity(definition).then(connectivity => ({ connectivity }))
    : Promise.resolve({})
  const [, , revision, routes, extra] = await Promise.all([
    loadFlowStatuses(), // the shared run-state store: RunStateIndicator, RunControls and the Problems counter read it synchronously
    loadRuntimeInfo(name), // the shared runtime cache: RuntimeFailure
    quiet(listFlowRevisions(name, 1).then(list => list.revisions[0] ?? null), null),
    quiet(getRouteHealth(name), []),
    quiet(prefetch, {}),
  ])
  return { revision, routes, tab, ...extra }
}

/** One flow: Flow, Live traffic, and Resource tabs; Edit is a mode. */
export default function FlowPage({ name, tab }: { name: string; tab: FlowTab }) {
  const styles = useStyles2(getStyles)
  const { flows, loading, reload, upsertLocal, mode } = useGoFi()
  const addToast = useToast()
  const status = useFlowStatus(name)
  const [confirmDelete, setConfirmDelete] = useState(false)
  // Edit visually or as the Flow resource; remembered per browser.
  const [editor, setEditorState] = useState<EditorKind>(() => { try { return localStorage.getItem(EDITOR_KEY) === 'yaml' ? 'yaml' : 'visual' } catch { return 'visual' } })
  const setEditor = (next: EditorKind) => { setEditorState(next); try { localStorage.setItem(EDITOR_KEY, next) } catch { /* private mode */ } }
  const definition = flows.find(flow => flow.name === name)
  // The flow inventory is shared and polled; the page's own data is gathered
  // once per flow name behind the loading bar.
  const initial = useInitialLoad(name, key => loadFlowPage(key, tab, definition))
  const kubernetes = mode?.driverKind === 'kubernetes'
  const editing = tab === 'edit'
  const title = editing ? t('gofi.pages.flow.edit-title', 'Edit {{name}}', { name }) : name
  const layout = tab === 'connectivity' || tab === 'problems' ? 'standard' : 'canvas'
  const tabs = (problems?: number): PageTab[] => [
    { label: t('gofi.pages.flow.tab-flow', 'Flow'), href: paths.flow(name), active: tab === 'flow', icon: 'sitemap' },
    { label: t('gofi.pages.flow.tab-live-traffic', 'Live traffic'), href: paths.flow(name, 'traffic'), active: tab === 'traffic', icon: 'bolt' },
    { label: t('gofi.pages.flow.tab-problems', 'Problems'), href: paths.flow(name, 'problems'), active: tab === 'problems', icon: 'exclamation-triangle', counter: problems || undefined },
    ...(kubernetes ? [{ label: t('gofi.pages.flow.tab-connectivity', 'Connectivity'), href: paths.flow(name, 'connectivity'), active: tab === 'connectivity', icon: 'shield' as const }] : []),
    { label: t('gofi.pages.flow.tab-resource', 'Resource'), href: paths.flow(name, 'resource'), active: tab === 'resource', icon: 'brackets-curly' },
  ]

  async function remove() {
    try { await deleteFlow(name); await reload(); navigate(paths.flows()) }
    catch (error) { addToast(String(error)) }
    finally { setConfirmDelete(false) }
  }
  const deleteModal = <ConfirmModal isOpen={confirmDelete} title={t('gofi.pages.flow.delete-title', 'Delete flow')} body={t('gofi.pages.flow.delete-body', 'Delete "{{name}}"? Its runtime stops and its desired-state resource is removed.', { name })} confirmText={t('gofi.common.delete', 'Delete')} onConfirm={() => void remove()} onDismiss={() => setConfirmDelete(false)} />
  // Explore, Edit and Delete need only the name, so they are part of the chrome from the first paint; Start/Stop/Restart join them once the run state is known.
  const staticActions = <>
    <LinkButton variant="secondary" icon="compass" href={paths.explore(name)}>{t('gofi.common.explore', 'Explore')}</LinkButton>
    <LinkButton variant="secondary" icon="pen" href={paths.flow(name, 'edit')}>{t('gofi.common.edit', 'Edit')}</LinkButton>
    <Button variant="destructive" fill="outline" icon="trash-alt" onClick={() => setConfirmDelete(true)}>{t('gofi.common.delete', 'Delete')}</Button>
  </>

  if (!definition && !loading) return <NotFoundPage path={paths.flow(name)} message={t('gofi.pages.flow.not-found', 'Flow "{{name}}" does not exist.', { name })} />
  if (!definition || initial.status !== 'ready' || !initial.data) {
    // The chrome at its final size (title, two subtitle lines, actions, tabs)
    // with the loading bar along the content's top edge; nothing below it
    // until the whole page can appear at once.
    return <Page title={title} icon="exchange-alt" tabs={editing ? undefined : tabs()} layout={layout}
      subTitle={<Stack direction="column" gap={1}><span className={styles.statusLine} /><span className={styles.revisionLine} /></Stack>}
      actions={editing ? <RadioButtonGroup options={editorOptions()} value={editor} onChange={setEditor} size="sm" /> : staticActions}>
      {initial.status === 'error'
        ? <Alert severity="error" title={t('gofi.pages.flow.load-failed', 'Could not load this flow')} bottomSpacing={0}>
          <Stack direction="column" gap={1} alignItems="flex-start"><span>{initial.error}</span><Button size="sm" variant="secondary" icon="sync" onClick={() => void initial.reload()}>{t('gofi.common.retry', 'Retry')}</Button></Stack>
        </Alert>
        : <PageLoadingBar label={t('gofi.pages.flow.loading-flow', 'Loading flow...')} />}
      {deleteModal}
    </Page>
  }

  const data = initial.data
  const prefetched = data.tab === tab ? data : undefined
  const listen = listenOf(definition)
  const ingress = definition.source.listen ? t('gofi.pages.flow.listen-summary', 'Listen · {{network}} :{{port}}', { network: (listen.network || 'tcp').toUpperCase(), port: listen.port }) : sourceSummary(definition.source)
  const saved = (next: FlowDefinition) => { upsertLocal(next); navigate(paths.flow(next.name)); void reload() }
  const stageCount = t('gofi.pages.flow.stage-count', '{{count}} stages', { count: definition.stages?.length ?? 0, defaultValue_one: '{{count}} stage' })
  const destinationCount = t('gofi.pages.flow.destination-count', '{{count}} destinations', { count: definition.sinks?.length ?? 0, defaultValue_one: '{{count}} destination' })
  return <Page
    title={title}
    icon="exchange-alt"
    subTitle={<Stack direction="column" gap={1}><Stack gap={1} alignItems="center" wrap="wrap"><RunStateIndicator name={name} /><span>{ingress}</span>{listen.service && <Badge color="blue" text={`${listen.service.type}${listen.service.nodePort ? ` :${listen.service.nodePort}` : ''}`} />}<span>{t('gofi.pages.flow.shape-summary', '· {{stages}} · {{destinations}}', { stages: stageCount, destinations: destinationCount })}</span></Stack><RevisionStamp definition={definition} initial={data.revision} /></Stack>}
    banner={<><RuntimeFailure name={name} />{tab !== 'connectivity' && <RouteFailures name={name} initial={data.routes} connectivityHref={kubernetes ? paths.flow(name, 'connectivity') : undefined} />}</>}
    actions={editing ? <RadioButtonGroup options={editorOptions()} value={editor} onChange={setEditor} size="sm" /> : <><RunControls name={name} />{staticActions}</>}
    tabs={editing ? undefined : tabs(status?.problems)}
    layout={layout}
  >
    {tab === 'flow' && <FlowCanvas key={JSON.stringify(definition)} mode="view" initial={definition} onClose={() => navigate(paths.flows())} />}
    {tab === 'edit' && editor === 'yaml' && <FlowYamlEditor key={`yaml:${name}`} definition={definition} onSaved={saved} onCancel={() => navigate(paths.flow(name))} />}
    {tab === 'edit' && editor === 'visual' && <FlowCanvas key={`edit:${name}`} mode="edit" initial={structuredClone(definition)} onSaved={saved} onClose={() => navigate(paths.flow(name))} closeLabel={t('gofi.common.cancel', 'Cancel')} />}
    {tab === 'traffic' && <div className={styles.fill}><LogsExplorer flow={name} /></div>}
    {tab === 'connectivity' && <ConnectivityPanel flow={definition} initial={prefetched?.connectivity} />}
    {tab === 'problems' && <ProblemsPanel flow={name} initial={prefetched?.bulletins} />}
    {tab === 'resource' && <Suspense fallback={<PageLoading text={t('gofi.pages.flow.loading-resource', 'Loading resource...')} />}><FlowResource key={name} definition={definition} initialList={prefetched?.revisions} /></Suspense>}
    {deleteModal}
  </Page>
}

const getStyles = (theme: GrafanaTheme2) => ({
  fill: css({ position: 'absolute', inset: 0, padding: theme.spacing(2, 3), [theme.breakpoints.down('md')]: { padding: theme.spacing(2) } }),
  // The same heights as the run-state line (a StatusIndicator) and the revision line.
  statusLine: css({ display: 'block', height: theme.spacing(theme.components.height.md) }),
  revisionLine: css({ display: 'block', height: revisionLineHeight(theme) }),
})
