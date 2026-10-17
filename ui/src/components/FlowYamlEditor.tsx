import { useEffect, useRef, useState } from 'react'
import { css, cx } from '@emotion/css'
import type { GrafanaTheme2 } from '@grafana/data'
import { Alert, Button, CodeEditor, Icon, Stack, Text, useStyles2, type monacoTypes } from '@grafana/ui'
import '../lib/monaco'
import { saveFlow, validateFlow } from '../api'
import { flowToProto } from '../rpc-model'
import { flowResource, parseFlowResource, toYAML } from '../lib/resource'
import { useGoFi } from '../state'
import type { FlowDefinition } from '../types'
import FlowCanvas from './canvas/FlowCanvas'
import { ResizableSplit } from './canvas/ResizableSplit'
import { StatusIndicator } from './StatusIndicator'
import { useToast } from './Toast'

type Monaco = typeof monacoTypes
type Check = { state: 'valid'; warnings: string[] } | { state: 'invalid'; error: string; line?: number }

/** How long typing pauses before the graph follows the YAML. */
const SYNC_DELAY_MS = 400

/** Edits a flow as its Flow resource (CR) in YAML, beside the live graph of
 * the same flow. Valid YAML redraws the graph; editing the graph rewrites
 * the YAML; the running flow's traffic keeps flowing over the graph. Validate
 * runs every check a save runs, including platform admission, without saving. */
export function FlowYamlEditor({ definition, onSaved, onCancel }: { definition: FlowDefinition; onSaved: (next: FlowDefinition) => void; onCancel: () => void }) {
  const styles = useStyles2(getStyles)
  const addToast = useToast()
  const { configuration } = useGoFi()
  const savedText = toYAML(flowResource(definition, configuration))
  const [text, setText] = useState(savedText)
  const [check, setCheck] = useState<Check | null>(null)
  const [busy, setBusy] = useState<'validate' | 'save' | null>(null)
  // The graph shows the last valid definition; it is remounted only when the
  // YAML (not the graph itself) changed the flow.
  const [graph, setGraph] = useState({ definition, key: 0 })
  const [parseError, setParseError] = useState<{ error: string; line?: number } | null>(null)
  const synced = useRef(JSON.stringify(definition))
  const editor = useRef<{ instance: monacoTypes.editor.IStandaloneCodeEditor; monaco: Monaco } | null>(null)
  const dirty = text !== savedText

  // YAML → graph, once typing pauses.
  useEffect(() => {
    const timer = window.setTimeout(() => {
      const parsed = parseFlowResource(text)
      if (parsed.error !== undefined) { setParseError({ error: parsed.error, line: parsed.line }); return }
      setParseError(null)
      const key = JSON.stringify(parsed.definition)
      if (key === synced.current) return
      synced.current = key
      setGraph(previous => ({ definition: parsed.definition, key: previous.key + 1 }))
    }, SYNC_DELAY_MS)
    return () => window.clearTimeout(timer)
  }, [text])

  // Graph → YAML: the graph's own edits rewrite the resource.
  function onGraphChange(next: FlowDefinition) {
    synced.current = JSON.stringify(next)
    setText(toYAML(flowResource(next, configuration)))
    setCheck(null)
  }

  // Mark the failing line in the editor, as Monaco does for its own errors.
  const marker = check?.state === 'invalid' ? check : parseError
  useEffect(() => {
    const model = editor.current?.instance.getModel()
    if (!model || !editor.current) return
    const line = marker?.line
    editor.current.monaco.editor.setModelMarkers(model, 'gofi-flow', line ? [{ startLineNumber: line, endLineNumber: line, startColumn: 1, endColumn: model.getLineMaxColumn(line), message: marker.error, severity: editor.current.monaco.MarkerSeverity.Error }] : [])
  }, [marker?.line, marker?.error])

  /** The edited definition, or why it is not one. Renaming is refused: a new
   * name would create a second flow rather than edit this one. */
  function parse(): FlowDefinition | null {
    const parsed = parseFlowResource(text)
    if (parsed.error !== undefined) { setCheck({ state: 'invalid', error: parsed.error, line: parsed.line }); return null }
    if (parsed.definition.name !== definition.name) { setCheck({ state: 'invalid', error: `metadata.name must stay "${definition.name}"; create a new flow to use another name`, line: lineOf('name:') }); return null }
    try { flowToProto(parsed.definition) } catch (reason) {
      const message = reason instanceof Error ? reason.message : String(reason)
      const field = /field "?([\w.]+)"?/.exec(message)?.[1]?.split('.').pop()
      setCheck({ state: 'invalid', error: message, line: field ? lineOf(`${field}:`) : undefined }); return null
    }
    return parsed.definition
  }
  const lineOf = (needle: string) => { const index = text.split('\n').findIndex(line => line.trimStart().startsWith(needle)); return index < 0 ? undefined : index + 1 }
  const rpcMessage = (reason: unknown) => String(reason).replace(/^ConnectError: \[\w+\] /, '')

  async function validate() {
    const next = parse()
    if (!next) return
    setBusy('validate')
    try { setCheck({ state: 'valid', warnings: await validateFlow(next) }) }
    catch (reason) { setCheck({ state: 'invalid', error: rpcMessage(reason) }) }
    finally { setBusy(null) }
  }

  async function save() {
    const next = parse()
    if (!next) return
    setBusy('save')
    try {
      const { flow, warnings } = await saveFlow(next)
      if (warnings.length) addToast(warnings.join('\n'), 'warning', `Saved, but ${warnings.length === 1 ? 'a path is' : `${warnings.length} paths are`} not allowed`)
      onSaved(flow)
    } catch (reason) {
      setCheck({ state: 'invalid', error: rpcMessage(reason) })
    } finally { setBusy(null) }
  }

  const yamlPane = <div className={styles.yamlPane}>
    <div className={cx(styles.frame, check?.state === 'valid' && styles.valid, (check?.state === 'invalid') && styles.invalid)}>
      <CodeEditor language="yaml" value={text} onChange={value => { setText(value); setCheck(null) }} showLineNumbers showMiniMap={false} height="100%" width="100%" containerStyles={styles.editor}
        onEditorDidMount={(instance, monaco) => { editor.current = { instance, monaco } }} monacoOptions={{ scrollBeyondLastLine: false, tabSize: 2 }} />
    </div>
    {check?.state === 'valid' && <div className={styles.validLine}><Icon name="check-circle" />Valid: GoFi would accept this flow{check.warnings.length ? ', with warnings' : ''}.</div>}
    {check?.state === 'valid' && check.warnings.length > 0 && <Alert severity="warning" title="Some paths are not allowed" bottomSpacing={0}>{check.warnings.map(warning => <div key={warning}>{warning}</div>)}</Alert>}
    {check?.state === 'invalid' && <div className={styles.invalidLine}><Icon name="exclamation-circle" />{check.line ? `Line ${check.line}: ` : ''}{check.error}</div>}
    {!check && parseError && <div className={styles.invalidLine}><Icon name="exclamation-circle" />{parseError.line ? `Line ${parseError.line}: ` : ''}{parseError.error}</div>}
  </div>

  const graphPane = <div className={styles.graphPane}>
    {parseError && <div className={styles.stale}><StatusIndicator tone="warning" text="Showing the last valid version: fix the YAML to update the graph" /></div>}
    <FlowCanvas key={graph.key} mode="edit" embedded live initial={graph.definition} onChange={onGraphChange} onClose={onCancel} />
  </div>

  return <div className={styles.root}>
    <div className={styles.toolbar}>
      <Text color="secondary" variant="bodySmall">The {flowResource(definition, configuration).apiVersion} Flow resource and its live graph: edit either side.</Text>
      <Stack gap={1} alignItems="center">
        {dirty && <StatusIndicator tone="warning" text="Unsaved changes" />}
        <Button variant="secondary" icon={busy === 'validate' ? 'spinner' : 'check'} disabled={!!busy} onClick={() => void validate()}>Validate</Button>
        <Button variant="secondary" onClick={onCancel}>Cancel</Button>
        <Button icon={busy === 'save' ? 'spinner' : 'save'} disabled={!!busy || !dirty} onClick={() => void save()}>Save flow</Button>
      </Stack>
    </div>
    <div className={styles.body}>
      <ResizableSplit side="right" storageKey="gofi.flow.yamlGraphShare" defaultShare={0.55} main={yamlPane} panel={graphPane} />
    </div>
  </div>
}

const getStyles = (theme: GrafanaTheme2) => ({
  root: css({ position: 'absolute', inset: 0, display: 'flex', flexDirection: 'column', background: theme.colors.background.canvas }),
  toolbar: css({ display: 'flex', alignItems: 'center', justifyContent: 'space-between', flexWrap: 'wrap', gap: theme.spacing(1), padding: theme.spacing(1, 2), background: theme.colors.background.primary, borderBottom: `1px solid ${theme.colors.border.weak}` }),
  body: css({ flex: 1, display: 'flex', minHeight: 0 }),
  yamlPane: css({ flex: 1, display: 'flex', flexDirection: 'column', gap: theme.spacing(1), minWidth: 0, padding: theme.spacing(1.5), background: theme.colors.background.primary }),
  graphPane: css({ flex: 1, position: 'relative', minWidth: 0 }),
  stale: css({ position: 'absolute', zIndex: 5, top: theme.spacing(7), left: '50%', transform: 'translateX(-50%)', padding: theme.spacing(0.5, 1.5), background: theme.colors.background.primary, border: `1px solid ${theme.colors.warning.border}`, borderRadius: theme.shape.radius.default, boxShadow: theme.shadows.z2 }),
  frame: css({ flex: 1, minHeight: 0, border: `1px solid ${theme.colors.border.medium}`, borderRadius: theme.shape.radius.default, overflow: 'hidden', transition: 'border-color 150ms, box-shadow 150ms' }),
  editor: css({ height: '100%' }),
  valid: css({ borderColor: theme.colors.success.border, boxShadow: `0 0 0 1px ${theme.colors.success.border}` }),
  invalid: css({ borderColor: theme.colors.error.border, boxShadow: `0 0 0 1px ${theme.colors.error.border}` }),
  validLine: css({ display: 'flex', alignItems: 'center', gap: theme.spacing(0.5), color: theme.colors.success.text, fontSize: theme.typography.bodySmall.fontSize }),
  invalidLine: css({ display: 'flex', alignItems: 'flex-start', gap: theme.spacing(0.5), color: theme.colors.error.text, fontSize: theme.typography.bodySmall.fontSize, whiteSpace: 'pre-wrap' }),
})
