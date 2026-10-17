import { useState } from 'react'
import { css } from '@emotion/css'
import type { GrafanaTheme2 } from '@grafana/data'
import '../lib/monaco'
import { Badge, ClipboardButton, CodeEditor, ConfirmModal, LinkButton, Button, RadioButtonGroup, Stack, useStyles2 } from '@grafana/ui'
import FlowCanvas from '../components/canvas/FlowCanvas'
import { LogsExplorer } from '../components/logs/LogsExplorer'
import { deleteFlow } from '../api'
import { useToast } from '../components/Toast'
import { flowResource, downloadYAML, manifestHeight, toYAML } from '../lib/resource'
import { navigate, paths, type FlowTab } from '../router'
import { Page, type PageTab } from '../shell/Page'
import { useGoFi } from '../state'
import { listenOf, sourceSummary, type FlowDefinition } from '../types'
import { NotFoundPage } from './NotFoundPage'
import { PageLoading } from '../components/PageLoading'
import { RuntimeFailure } from '../components/RuntimeFailure'
import { RouteFailures } from '../components/RouteFailures'
import { ConnectivityPanel } from '../components/ConnectivityPanel'
import { FlowYamlEditor } from '../components/FlowYamlEditor'

type EditorKind = 'visual' | 'yaml'
const EDITOR_KEY = 'gofi.flow.editor'
const EDITOR_OPTIONS = [{ label: 'Visual', value: 'visual' as const, icon: 'sitemap' as const }, { label: 'YAML', value: 'yaml' as const, icon: 'brackets-curly' as const }]

/** One flow: Flow, Live traffic, and Resource tabs; Edit is a mode. */
export default function FlowPage({ name, tab }: { name: string; tab: FlowTab }) {
  const styles = useStyles2(getStyles)
  const { flows, loading, reload, upsertLocal, mode } = useGoFi()
  const addToast = useToast()
  const [confirmDelete, setConfirmDelete] = useState(false)
  // Edit visually or as the Flow resource; remembered per browser.
  const [editor, setEditorState] = useState<EditorKind>(() => { try { return localStorage.getItem(EDITOR_KEY) === 'yaml' ? 'yaml' : 'visual' } catch { return 'visual' } })
  const setEditor = (next: EditorKind) => { setEditorState(next); try { localStorage.setItem(EDITOR_KEY, next) } catch { /* private mode */ } }
  const definition = flows.find(flow => flow.name === name)
  if (!definition) return loading ? <PageLoading text="Loading flow..." /> : <NotFoundPage path={paths.flow(name)} message={`Flow "${name}" does not exist.`} />

  const listen = listenOf(definition)
  const ingress = definition.source.listen ? `Listen · ${(listen.network || 'tcp').toUpperCase()} :${listen.port}` : sourceSummary(definition.source)
  const tabs: PageTab[] = [
    { label: 'Flow', href: paths.flow(name), active: tab === 'flow', icon: 'sitemap' },
    { label: 'Live traffic', href: paths.flow(name, 'traffic'), active: tab === 'traffic', icon: 'bolt' },
    ...(mode?.driverKind === 'kubernetes' ? [{ label: 'Connectivity', href: paths.flow(name, 'connectivity'), active: tab === 'connectivity', icon: 'shield' as const }] : []),
    { label: 'Resource', href: paths.flow(name, 'resource'), active: tab === 'resource', icon: 'brackets-curly' },
  ]
  const saved = (next: FlowDefinition) => { upsertLocal(next); navigate(paths.flow(next.name)); void reload() }
  async function remove() {
    try { await deleteFlow(name); await reload(); navigate(paths.flows()) }
    catch (error) { addToast(String(error)) }
    finally { setConfirmDelete(false) }
  }
  const editing = tab === 'edit'
  return <Page
    title={editing ? `Edit ${name}` : name}
    icon="exchange-alt"
    subTitle={<Stack direction="column" gap={1}><Stack gap={1} alignItems="center" wrap="wrap"><span>{ingress}</span>{listen.service && <Badge color="blue" text={`${listen.service.type}${listen.service.nodePort ? ` :${listen.service.nodePort}` : ''}`} />}<span>· {definition.stages?.length ?? 0} stages · {definition.sinks?.length ?? 0} destinations</span></Stack></Stack>}
    banner={<><RuntimeFailure name={name} />{tab !== 'connectivity' && <RouteFailures name={name} connectivityHref={mode?.driverKind === 'kubernetes' ? paths.flow(name, 'connectivity') : undefined} />}</>}
    actions={editing ? <RadioButtonGroup options={EDITOR_OPTIONS} value={editor} onChange={setEditor} size="sm" /> : <>
      <LinkButton variant="secondary" icon="compass" href={paths.explore(name)}>Explore</LinkButton>
      <LinkButton variant="secondary" icon="pen" href={paths.flow(name, 'edit')}>Edit</LinkButton>
      <Button variant="destructive" fill="outline" icon="trash-alt" onClick={() => setConfirmDelete(true)}>Delete</Button>
    </>}
    tabs={editing ? undefined : tabs}
    layout={tab === 'resource' || tab === 'connectivity' ? 'standard' : 'canvas'}
  >
    {tab === 'flow' && <FlowCanvas key={JSON.stringify(definition)} mode="view" initial={definition} onClose={() => navigate(paths.flows())} />}
    {tab === 'edit' && editor === 'yaml' && <FlowYamlEditor key={`yaml:${name}`} definition={definition} onSaved={saved} onCancel={() => navigate(paths.flow(name))} />}
    {tab === 'edit' && editor === 'visual' && <FlowCanvas key={`edit:${name}`} mode="edit" initial={structuredClone(definition)} onSaved={saved} onClose={() => navigate(paths.flow(name))} closeLabel="Cancel" />}
    {tab === 'traffic' && <div className={styles.fill}><LogsExplorer flow={name} /></div>}
    {tab === 'connectivity' && <ConnectivityPanel flow={definition} />}
    {tab === 'resource' && <ResourceTab definition={definition} />}
    <ConfirmModal isOpen={confirmDelete} title="Delete flow" body={`Delete "${name}"? Its runtime stops and its desired-state resource is removed.`} confirmText="Delete" onConfirm={() => void remove()} onDismiss={() => setConfirmDelete(false)} />
  </Page>
}

function ResourceTab({ definition }: { definition: FlowDefinition }) {
  const styles = useStyles2(getStyles)
  const { configuration } = useGoFi()
  const manifest = toYAML(flowResource(definition, configuration))
  return <Stack direction="column" gap={2}>
    <Stack gap={1}>
      <ClipboardButton icon="copy" variant="secondary" getText={() => manifest}>Copy</ClipboardButton>
      <Button icon="download-alt" variant="secondary" onClick={() => downloadYAML(`${definition.name}.yaml`, flowResource(definition, configuration))}>Download</Button>
    </Stack>
    <div className={styles.editor}><CodeEditor language="yaml" value={manifest} readOnly showLineNumbers showMiniMap={false} height={manifestHeight(manifest)} width="100%" monacoOptions={{ scrollBeyondLastLine: false }} /></div>
  </Stack>
}
const getStyles = (theme: GrafanaTheme2) => ({
  fill: css({ position: 'absolute', inset: 0, padding: theme.spacing(2, 3), [theme.breakpoints.down('md')]: { padding: theme.spacing(2) } }),
  editor: css({ border: `1px solid ${theme.colors.border.weak}`, borderRadius: theme.shape.radius.default, overflow: 'hidden' }),
})
