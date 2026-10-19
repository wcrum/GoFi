import { useEffect, useMemo, useState } from 'react'
import { FaRobot } from 'react-icons/fa'
import { css } from '@emotion/css'
import type { GrafanaTheme2 } from '@grafana/data'
import { Alert, Badge, Button, Combobox, type ComboboxOption, type Column, ConfirmModal, Field, Input, InteractiveTable, SecretInput, Stack, Switch, Text, TextArea, useStyles2 } from '@grafana/ui'
import {
  applyAssistantConfiguration, checkAssistantConnection, emptyConfiguration, errorMessage, getAssistantConfiguration, isUnimplemented, listAssistantTools,
  type AssistantConfiguration, type AssistantEffort, type AssistantProvider, type AssistantTool, type AssistantToolApproval, type ConnectionCheck,
} from '../../assistant-api'
import { MAX_TOOL_STEPS, MIN_TOOL_STEPS } from '../../components/assistant/helpers'
import { notifyAssistantChanged } from '../../components/assistant/useAssistantReadiness'
import { Page } from '../../shell/Page'
import { PageLoading } from '../../components/PageLoading'
import { useToast } from '../../components/Toast'
import { formatNumber, t } from '../../i18n'

type EffortChoice = 'default' | 'low' | 'medium' | 'high'
const providerOptions = (): Array<ComboboxOption<AssistantProvider>> => [
  { value: 'anthropic', label: t('gofi.pages.administration.provider-anthropic', 'Anthropic Messages API') },
  { value: 'openai', label: t('gofi.pages.administration.provider-openai', 'OpenAI-compatible Chat Completions') },
]
const toolApprovalOptions = (): Array<ComboboxOption<AssistantToolApproval>> => [
  { value: 'changes', label: t('gofi.pages.administration.tool-approval-changes', 'Changes only') },
  { value: 'all', label: t('gofi.pages.administration.tool-approval-all', 'Every tool call') },
]
const effortOptions = (): Array<ComboboxOption<EffortChoice>> => [
  { value: 'default', label: t('gofi.pages.administration.effort-default', 'Model default') },
  { value: 'low', label: t('gofi.pages.administration.effort-low', 'Low') },
  { value: 'medium', label: t('gofi.pages.administration.effort-medium', 'Medium') },
  { value: 'high', label: t('gofi.pages.administration.effort-high', 'High') },
]

interface Readiness { ready: boolean; message: string }
interface Locks { endpoint: boolean; key: boolean; functions: boolean }

const pinnedText = () => t('gofi.pages.administration.pinned-by-environment', "Pinned by the controller's environment (GOFI_ASSISTANT_BASE_URL / GOFI_ASSISTANT_API_KEY).")
const keySourceText = (configuration: AssistantConfiguration, locked: boolean): string => {
  if (locked) return pinnedText()
  if (!configuration.hasApiKey) return t('gofi.pages.administration.api-key-none', 'No key is set. Enter one to enable the assistant.')
  if (configuration.apiKeySource === 'environment') return t('gofi.pages.administration.api-key-environment', 'The current key comes from the environment. Leaving this empty keeps it; a key entered here is stored and takes precedence.')
  return t('gofi.pages.administration.api-key-stored', 'A key is stored. Leaving this empty keeps the stored key.')
}

const noWrap = css({ whiteSpace: 'nowrap' })
const buildToolColumns = (): Array<Column<AssistantTool>> => [
  // Name, Access, and Offered shrink to their content; Description takes the rest and wraps.
  { id: 'name', header: t('gofi.pages.administration.tools-column-name', 'Tool'), sortType: 'string', disableGrow: true, cell: ({ row: { original } }) => <span className={noWrap}><Text variant="code">{original.name}</Text></span> },
  { id: 'description', header: t('gofi.pages.administration.tools-column-description', 'Description'), cell: ({ row: { original } }) => <Text color="secondary">{original.description}</Text> },
  { id: 'mutating', header: t('gofi.pages.administration.tools-column-access', 'Access'), disableGrow: true, cell: ({ row: { original } }) => <span className={noWrap}>{original.mutating ? <Badge color="orange" icon="pen" text={t('gofi.pages.administration.tool-mutating', 'Changes things')} /> : <Badge color="darkgrey" icon="eye" text={t('gofi.pages.administration.tool-read-only', 'Read only')} />}</span> },
  { id: 'enabled', header: t('gofi.pages.administration.tools-column-offered', 'Offered'), disableGrow: true, cell: ({ row: { original } }) => <span className={noWrap}>{original.enabled ? <Badge color="green" icon="check" text={t('gofi.pages.administration.tool-enabled', 'Enabled')} /> : <Badge color="darkgrey" text={t('gofi.pages.administration.tool-disabled', 'Off')} />}</span> },
]

/** Administration › AI assistant: GoFiBot's model endpoint, behaviour, and
 * the tools it is offered (listed once the connection is verified). */
export default function AssistantAdminPage() {
  const styles = useStyles2(getStyles)
  const addToast = useToast()
  const [form, setForm] = useState<AssistantConfiguration>(emptyConfiguration)
  const [backend, setBackend] = useState('')
  const [readiness, setReadiness] = useState<Readiness | null>(null)
  const [locks, setLocks] = useState<Locks>({ endpoint: false, key: false, functions: false })
  const [confirmClear, setConfirmClear] = useState(false)
  const [clearing, setClearing] = useState(false)
  const [loaded, setLoaded] = useState(false)
  const [unimplemented, setUnimplemented] = useState(false)
  const [error, setError] = useState('')
  const [saving, setSaving] = useState(false)
  const [testing, setTesting] = useState(false)
  const [check, setCheck] = useState<ConnectionCheck | null>(null)
  const [checkError, setCheckError] = useState('')
  const [replacingKey, setReplacingKey] = useState(false)
  const [tools, setTools] = useState<AssistantTool[] | null>(null)
  const [toolsError, setToolsError] = useState('')
  const toolColumns = useMemo(buildToolColumns, [])
  const set = (patch: Partial<AssistantConfiguration>) => setForm(current => ({ ...current, ...patch }))

  /** Lists the tools; a failure shows in the tools section instead of a spinner. */
  const loadTools = () => { setToolsError(''); return listAssistantTools().then(setTools).catch(reason => { setTools(null); setToolsError(errorMessage(reason)) }) }
  /** Re-reads the controller's cached readiness and locks without touching
   * unsaved edits; resolves with whether the assistant is ready. */
  const refreshReadiness = () => getAssistantConfiguration().then(state => { setReadiness({ ready: state.ready, message: state.readyMessage }); setLocks({ endpoint: state.endpointLocked, key: state.keyLocked, functions: state.functionsLocked }); return state.ready }).catch(() => false)

  useEffect(() => {
    getAssistantConfiguration().then(state => {
      setForm(state.configuration); setBackend(state.backend); setReadiness({ ready: state.ready, message: state.readyMessage }); setLocks({ endpoint: state.endpointLocked, key: state.keyLocked, functions: state.functionsLocked }); setLoaded(true)
      if (state.ready) void loadTools()
    }).catch(reason => { if (isUnimplemented(reason)) setUnimplemented(true); else setError(errorMessage(reason)); setLoaded(true) })
  }, [])

  /** Saves the form; with `clearApiKey` the stored key is forgotten too. */
  async function save(options: { clearApiKey?: boolean } = {}) {
    const setBusy = options.clearApiKey ? setClearing : setSaving
    setBusy(true); setError('')
    try {
      const saved = await applyAssistantConfiguration(form, options)
      setForm(saved); setReplacingKey(false)
      addToast(options.clearApiKey ? t('gofi.pages.administration.key-cleared', 'Stored API key cleared.') : t('gofi.pages.administration.saved', 'Assistant configuration saved.'), 'success')
      notifyAssistantChanged()
      // The offered tools follow "Allow changes", so re-list them when the connection works.
      if (await refreshReadiness()) await loadTools()
    } catch (reason) {
      setError(errorMessage(reason))
    } finally { setBusy(false) }
  }
  async function test() {
    setTesting(true); setCheck(null); setCheckError('')
    try {
      setCheck(await checkAssistantConnection(form))
      notifyAssistantChanged()
      if (await refreshReadiness() && !tools) await loadTools()
    } catch (reason) {
      setCheckError(errorMessage(reason))
    } finally { setTesting(false) }
  }

  const stepsInvalid = !Number.isInteger(form.maxToolSteps) || form.maxToolSteps < MIN_TOOL_STEPS || form.maxToolSteps > MAX_TOOL_STEPS
  const ready = loaded && !unimplemented && !!readiness?.ready
  return <Page title={t('gofi.pages.administration.assistant-title', 'AI assistant')} icon={<FaRobot size={24} aria-hidden />}
    subTitle={t('gofi.pages.administration.assistant-intro', 'GoFiBot answers questions about this GoFi and drafts flows, CEL expressions, and Python libraries through a model endpoint: the Anthropic Messages API or any OpenAI-compatible service.')}>
    <section className={styles.section} aria-labelledby="gofi-admin-assistant">
      <div className={styles.sectionHeader}>
        <Text element="h2" variant="h4" id="gofi-admin-assistant">{t('gofi.pages.administration.assistant-section', 'Model endpoint')}</Text>
      </div>
      {!loaded ? <PageLoading text={t('gofi.pages.administration.loading', 'Loading configuration...')} />
        : unimplemented ? <Alert severity="info" title={t('gofi.pages.administration.unimplemented-title', 'No assistant in this controller')}>{t('gofi.pages.administration.unimplemented-body', 'This GoFi controller was built without the assistant, so there is nothing to configure here.')}</Alert>
        : <Stack direction="column" gap={2}>
          {error && <Alert severity="error" title={t('gofi.pages.administration.failed', 'Could not update the assistant')} onRemove={() => setError('')} bottomSpacing={0}>{error}</Alert>}
          <div className={styles.groups}>
          <div className={styles.column}>
            <Text element="h3" variant="h5">{t('gofi.pages.administration.group-endpoint', 'Endpoint')}</Text>
            <Field label={t('gofi.pages.administration.enabled', 'Enabled')} description={t('gofi.pages.administration.enabled-description', 'Shows the chat control in the header once a working endpoint answers.')}>
              <Stack alignItems="center" gap={2}>
                <Switch value={form.enabled} onChange={event => set({ enabled: event.currentTarget.checked })} />
                {readiness && (readiness.ready
                  ? <Badge color="green" icon="check" text={t('gofi.pages.administration.ready', 'Ready: {{model}}', { model: readiness.message })} />
                  : <Badge color="orange" icon="exclamation-triangle" text={t('gofi.pages.administration.not-ready', 'Not ready: {{message}}', { message: readiness.message || t('gofi.pages.administration.not-ready-unknown', 'not probed yet') })} />)}
              </Stack>
            </Field>
            <Field label={t('gofi.pages.administration.provider', 'Provider')} description={t('gofi.pages.administration.provider-description', 'The protocol the endpoint speaks. OpenAI-compatible covers Groq, vLLM, LiteLLM, Ollama, OpenRouter, and similar gateways.')}>
              <Combobox<AssistantProvider> options={providerOptions()} value={form.provider} onChange={option => set({ provider: option.value })} />
            </Field>
            <Field label={t('gofi.pages.administration.base-url', 'Endpoint (base URL)')} description={locks.endpoint ? pinnedText() : form.provider === 'openai'
              ? t('gofi.pages.administration.base-url-description-openai', 'The API root, usually ending in /v1, for example https://api.groq.com/openai/v1.')
              : t('gofi.pages.administration.base-url-description', 'Any gateway or proxy speaking the Anthropic Messages API.')}>
              <Input value={form.baseUrl} disabled={locks.endpoint} onChange={event => set({ baseUrl: event.currentTarget.value })} placeholder={form.provider === 'openai' ? 'https://api.openai.com/v1' : t('gofi.pages.administration.base-url-placeholder', 'Leave empty for the Anthropic API')} spellCheck={false} />
            </Field>
            <Field label={t('gofi.pages.administration.model', 'Model')} description={form.provider === 'openai' ? t('gofi.pages.administration.model-description-openai', 'The model id the endpoint expects.') : t('gofi.pages.administration.model-description', 'Empty uses the server default.')}>
              <Input value={form.model} onChange={event => set({ model: event.currentTarget.value })} placeholder={form.provider === 'openai' ? 'openai/gpt-oss-120b' : 'claude-opus-5-5'} spellCheck={false} />
            </Field>
            <Field label={t('gofi.pages.administration.api-key', 'API key')} description={keySourceText(form, locks.key)}>
              <Stack gap={1} alignItems="flex-start" wrap="wrap">
                <div className={styles.grow}>
                  <SecretInput revealable disabled={locks.key} isConfigured={form.hasApiKey && !replacingKey && !form.apiKey} onReset={() => setReplacingKey(true)} value={form.apiKey} onChange={event => set({ apiKey: event.currentTarget.value })}
                    placeholder={t('gofi.pages.administration.api-key-placeholder', 'Paste a new key')} autoComplete="off" spellCheck={false} />
                </div>
                {form.apiKeySource === 'stored' && !locks.key && <Button variant="secondary" icon={clearing ? 'spinner' : 'trash-alt'} disabled={clearing || saving} onClick={() => setConfirmClear(true)}>{t('gofi.pages.administration.clear-key', 'Clear stored key')}</Button>}
              </Stack>
            </Field>
          </div>
          <div className={styles.column}>
            <Text element="h3" variant="h5">{t('gofi.pages.administration.group-behaviour', 'What it may do')}</Text>
            <Field label={t('gofi.pages.administration.allow-functions', 'Functions may use the AI endpoint')} description={locks.functions
              ? t('gofi.pages.administration.allow-functions-pinned', "Pinned by the controller's environment (GOFI_ASSISTANT_ALLOW_FUNCTIONS).")
              : t('gofi.pages.administration.allow-functions-description', 'When on, every Python library can call this model through gofi.llm via the controller; the key never reaches a worker, and each call is checked here. Off: gofi.llm raises in every library. One switch for the deployment; libraries have no setting of their own.')}>
              <Switch value={form.allowFunctions} disabled={locks.functions} onChange={event => set({ allowFunctions: event.currentTarget.checked })} />
            </Field>
            <Field label={t('gofi.pages.administration.allow-changes', 'Allow changes')} description={t('gofi.pages.administration.allow-changes-description', 'When on, the assistant can propose creating or replacing flows, CEL expressions, and Python libraries; each proposal is applied only when a user approves it in the chat. Off: it only reads, validates, and drafts.')}>
              <Switch value={form.allowChanges} onChange={event => set({ allowChanges: event.currentTarget.checked })} />
            </Field>
            <Field label={t('gofi.pages.administration.tool-approval', 'Tool approval')} description={t('gofi.pages.administration.tool-approval-description', 'Changes only: reads run freely and apply tools become proposals decided after the reply. Every tool call: each call pauses the reply until a user approves or denies it in the chat (two minutes, then it is denied).')}>
              <Combobox<AssistantToolApproval> options={toolApprovalOptions()} value={form.toolApproval} onChange={option => set({ toolApproval: option.value })} />
            </Field>
            <Field label={t('gofi.pages.administration.effort', 'Effort')} description={t('gofi.pages.administration.effort-description', 'How hard the model thinks before answering; higher is slower and costs more.')}>
              <Combobox<EffortChoice> options={effortOptions()} value={form.effort || 'default'} onChange={option => set({ effort: option.value === 'default' ? '' : option.value as AssistantEffort })} />
            </Field>
            <Field label={t('gofi.pages.administration.max-tool-steps', 'Max tool steps')} description={t('gofi.pages.administration.max-tool-steps-description', 'Tool calls per message before the assistant must answer ({{min}}–{{max}}).', { min: MIN_TOOL_STEPS, max: MAX_TOOL_STEPS })}
              invalid={stepsInvalid} error={stepsInvalid ? t('gofi.pages.administration.max-tool-steps-invalid', 'Enter a whole number from {{min}} to {{max}}.', { min: MIN_TOOL_STEPS, max: MAX_TOOL_STEPS }) : undefined}>
              <Input type="number" min={MIN_TOOL_STEPS} max={MAX_TOOL_STEPS} step={1} width={12} value={Number.isFinite(form.maxToolSteps) ? form.maxToolSteps : ''} onChange={event => set({ maxToolSteps: event.currentTarget.valueAsNumber })} />
            </Field>
          </div>
          <div className={styles.wide}>
            <Field label={t('gofi.pages.administration.instructions', 'Operator instructions')} description={t('gofi.pages.administration.instructions-description', 'Appended to the system prompt: house rules, naming conventions, what to avoid.')}>
              <TextArea rows={5} value={form.instructions} onChange={event => set({ instructions: event.currentTarget.value })} placeholder={t('gofi.pages.administration.instructions-placeholder', 'Always name flows after the producing team. Never suggest NodePort exposure.')} />
            </Field>
          </div>
          </div>
          <Stack gap={1} alignItems="center" wrap="wrap">
            <Button icon={saving ? 'spinner' : 'save'} disabled={saving || stepsInvalid} onClick={() => void save()}>{saving ? t('gofi.pages.administration.saving', 'Saving...') : t('gofi.common.save', 'Save')}</Button>
            <Button variant="secondary" icon={testing ? 'spinner' : 'plug'} disabled={testing} onClick={() => void test()}>{testing ? t('gofi.pages.administration.testing', 'Testing...') : t('gofi.pages.administration.test-connection', 'Test connection')}</Button>
            {backend && <Text color="secondary" variant="bodySmall">{t('gofi.pages.administration.backend', 'Stored in {{backend}}', { backend })}</Text>}
          </Stack>
          {check && (check.ok
            ? <Alert severity="success" title={t('gofi.pages.administration.check-ok', 'Connected to {{model}} in {{latency}} ms', { model: check.model, latency: formatNumber(check.latencyMs) })} onRemove={() => setCheck(null)} bottomSpacing={0}>{check.message}</Alert>
            : <Alert severity="error" title={t('gofi.pages.administration.check-failed', 'Connection failed')} onRemove={() => setCheck(null)} bottomSpacing={0}>{check.message || t('gofi.pages.administration.check-failed-unknown', 'The endpoint did not answer as expected.')}</Alert>)}
          {checkError && <Alert severity="error" title={t('gofi.pages.administration.check-failed', 'Connection failed')} onRemove={() => setCheckError('')} bottomSpacing={0}>{checkError}</Alert>}
          {!ready && <Text color="secondary" variant="bodySmall">{t('gofi.pages.administration.tools-after-connection', 'Tools are listed once the connection is verified.')}</Text>}
        </Stack>}
    </section>
    {ready && <section className={styles.toolsSection} aria-labelledby="gofi-admin-tools">
      <div className={styles.sectionHeader}>
        <Text element="h2" variant="h4" id="gofi-admin-tools">{t('gofi.pages.administration.tools-section', 'Assistant tools')}</Text>
        <Text color="secondary" variant="bodySmall">{t('gofi.pages.administration.tools-intro', 'What GoFiBot can do. Tools that change things are offered only while “Allow changes” is on.')}</Text>
      </div>
      {toolsError ? <Alert severity="error" title={t('gofi.pages.administration.tools-failed', 'Could not list tools')} bottomSpacing={0}>
          <Stack direction="column" gap={1} alignItems="flex-start">{toolsError}<Button size="sm" variant="secondary" icon="sync" onClick={() => void loadTools()}>{t('gofi.pages.administration.tools-retry', 'Try again')}</Button></Stack>
        </Alert>
        : !tools ? <PageLoading text={t('gofi.pages.administration.tools-loading', 'Listing tools...')} />
        : !tools.length ? <Text color="secondary">{t('gofi.pages.administration.tools-empty', 'This controller offers no tools.')}</Text>
        : <InteractiveTable className={styles.tools} columns={toolColumns} data={tools} getRowId={tool => tool.name} />}
    </section>}
    <ConfirmModal isOpen={confirmClear} title={t('gofi.pages.administration.clear-key-title', 'Clear the stored API key')}
      body={t('gofi.pages.administration.clear-key-body', 'The key stored by this controller is forgotten and the form is saved as shown. GoFiBot stops answering until a new key is entered, unless the environment supplies one.')}
      confirmText={t('gofi.pages.administration.clear-key-confirm', 'Clear key')} onConfirm={() => { setConfirmClear(false); void save({ clearApiKey: true }) }} onDismiss={() => setConfirmClear(false)} />
  </Page>
}

const getStyles = (theme: GrafanaTheme2) => ({
  // The page is the width of the content area; the form fills it in columns.
  section: css({ marginBottom: theme.spacing(4) }),
  // The tools table spans the page; only the form is constrained.
  toolsSection: css({ marginBottom: theme.spacing(4) }),
  tools: css({ width: '100%', tableLayout: 'auto' }),
  sectionHeader: css({ display: 'flex', flexDirection: 'column', gap: theme.spacing(0.5), marginBottom: theme.spacing(2), h2: { margin: 0 } }),
  // One full-width column: every field stretches to the content area.
  groups: css({ display: 'flex', flexDirection: 'column', gap: theme.spacing(3) }),
  column: css({ minWidth: 0, display: 'flex', flexDirection: 'column', h3: { margin: theme.spacing(0, 0, 2) } }),
  wide: css({ minWidth: 0, textarea: { minHeight: 120 } }),
  grow: css({ flex: '1 1 240px', minWidth: 0 }),
})
