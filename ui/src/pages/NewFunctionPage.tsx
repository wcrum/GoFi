import { lazy, Suspense, useEffect, useMemo, useState, type ReactNode } from 'react'
import { css } from '@emotion/css'
import type { GrafanaTheme2, IconName } from '@grafana/data'
import { Alert, Button, Card, Combobox, Field, Icon, Input, LinkButton, RadioButtonGroup, Stack, Text, TextArea, TextLink, Tooltip, useStyles2 } from '@grafana/ui'
import { getAssistantConfiguration } from '../assistant-api'
import { FUNCTION_TEMPLATES } from '../components/canvas/python-templates'
import {
  AI_GOALS, DEFAULT_ANSWERS, DEFAULT_FUNCTION_NAMES, GOALS, GOAL_KIND, assistantMessage, composeSource, conditionToPython, functionNameOf, isAiGoal,
  libraryNameProblem, suggestName, type Composition, type Goal, type ParseFormat, type SinkKind, type SourceKind, type TransformAction, type WizardAnswers,
} from '../components/function-wizard/compose'
import { WizardSteps } from '../components/function-wizard/WizardSteps'
import { PageLoading } from '../components/PageLoading'
import { applySandbox, validatePythonSource, type SandboxDefinition, type SourceDiagnostic } from '../sandbox-api'
import { navigate, paths, to } from '../router'
import { Page } from '../shell/Page'
import { t } from '../i18n'

const PythonSourceEditor = lazy(() => import('../components/PythonSourceEditor'))

/** Fired (on window) to open GoFiBot's panel with a prefilled message. */
export const ASSISTANT_OPEN_EVENT = 'gofi:assistant-open'

type Step = 1 | 2 | 3 | 4
const STEP_COUNT = 4

const goalCards = (): Record<Goal, { icon: IconName; title: string; description: string }> => ({
  parse: { icon: 'brackets-curly', title: t('gofi.function-wizard.goal.parse', 'Parse a format'), description: t('gofi.function-wizard.goal.parse-description', 'Turn the bytes of each frame into fields: JSON, CSV, key=value, or a binary layout.') },
  transform: { icon: 'edit', title: t('gofi.function-wizard.goal.transform', 'Transform or enrich fields'), description: t('gofi.function-wizard.goal.transform-description', 'Change, rename, convert, or add a field on every message.') },
  filter: { icon: 'filter', title: t('gofi.function-wizard.goal.filter', 'Filter or route messages'), description: t('gofi.function-wizard.goal.filter-description', 'Keep only the messages that match a condition and drop the rest.') },
  sink: { icon: 'share-alt', title: t('gofi.function-wizard.goal.sink', 'Deliver to a destination'), description: t('gofi.function-wizard.goal.sink-description', 'A sink: send each accepted message somewhere, such as an HTTP endpoint.') },
  source: { icon: 'plug', title: t('gofi.function-wizard.goal.source', 'Pull data from a source'), description: t('gofi.function-wizard.goal.source-description', 'A fetcher that polls on a schedule, or a streamer that yields frames continuously.') },
  'ai-classify': { icon: 'ai-sparkle', title: t('gofi.function-wizard.goal.ai-classify', 'AI: classify messages'), description: t('gofi.function-wizard.goal.ai-classify-description', 'Have the model pick one label per message, then drop or route by it.') },
  'ai-enrich': { icon: 'ai-sparkle', title: t('gofi.function-wizard.goal.ai-enrich', 'AI: enrich with an answer'), description: t('gofi.function-wizard.goal.ai-enrich-description', 'Ask the model a question about each message and store its answer as a field.') },
  'ai-extract': { icon: 'ai-sparkle', title: t('gofi.function-wizard.goal.ai-extract', 'AI: extract structured data'), description: t('gofi.function-wizard.goal.ai-extract-description', 'Pull named values out of free text into a JSON field.') },
  'ai-sink': { icon: 'ai-sparkle', title: t('gofi.function-wizard.goal.ai-sink', 'AI: deliver to the model'), description: t('gofi.function-wizard.goal.ai-sink-description', 'A sink that hands each message to the model or an agent endpoint with an instruction.') },
})

/** The guided way to a new function library: pick a goal, answer a few
 * plain-language questions, review the generated source, save it. */
export default function NewFunctionPage() {
  const styles = useStyles2(getStyles)
  const [step, setStep] = useState<Step>(1)
  const [answers, setAnswers] = useState<WizardAnswers>(DEFAULT_ANSWERS)
  const [assistant, setAssistant] = useState<{ ready: boolean; message: string }>({ ready: false, message: '' })
  const [source, setSource] = useState('')
  const [composition, setComposition] = useState<Composition | null>(null)
  const [edited, setEdited] = useState(false)
  const [diagnostics, setDiagnostics] = useState<SourceDiagnostic[]>([])
  const [validity, setValidity] = useState<'valid' | 'invalid' | undefined>()
  const [busy, setBusy] = useState<'validate' | 'save' | null>(null)
  const [error, setError] = useState('')
  const [name, setName] = useState('')
  const [nameTouched, setNameTouched] = useState(false)
  useEffect(() => {
    let cancelled = false
    getAssistantConfiguration().then(result => { if (!cancelled) setAssistant({ ready: result.ready, message: result.readyMessage }) }).catch(() => { if (!cancelled) setAssistant({ ready: false, message: '' }) })
    return () => { cancelled = true }
  }, [])

  const set = (patch: Partial<WizardAnswers>) => setAnswers(previous => ({ ...previous, ...patch }))
  const updateSource = (next: string) => { setSource(next); setEdited(true); setDiagnostics([]); setValidity(undefined) }
  const generate = () => {
    const next = composeSource(answers, FUNCTION_TEMPLATES)
    setComposition(next); setSource(next.source); setEdited(false); setDiagnostics([]); setValidity(undefined); setError('')
  }
  const goTo = (next: number) => {
    const target = Math.min(Math.max(next, 1), STEP_COUNT) as Step
    if (target === 3 && (step < 3 || !source) && !edited) generate()
    if (target === 4 && !nameTouched) setName(suggestName(answers))
    setError('')
    setStep(target)
  }
  const validate = async () => {
    setBusy('validate'); setError('')
    try { const result = await validatePythonSource(source); setDiagnostics(result.diagnostics); setValidity(result.valid ? 'valid' : 'invalid') }
    catch (reason) { setError(String(reason)) }
    finally { setBusy(null) }
  }
  const askAssistant = () => window.dispatchEvent(new CustomEvent(ASSISTANT_OPEN_EVENT, { detail: { message: assistantMessage(answers, source) } }))
  const nameProblem = libraryNameProblem(name)
  const nameError = nameProblem === 'required' ? t('gofi.common.required', 'Required.') : nameProblem === 'format' ? t('gofi.function-wizard.save.name-format', 'Use lowercase letters, digits, and hyphens, starting with a letter.') : ''
  const save = async () => {
    if (nameProblem) { setNameTouched(true); return }
    setBusy('save'); setError('')
    const definition: SandboxDefinition = {
      name, sourceCode: source, timeoutSeconds: isAiGoal(answers.goal) ? 30 : 5, memoryMib: 128,
      tests: composition?.test && !edited ? [composition.test] : [], egress: [],
    }
    try { await applySandbox(definition); navigate(paths.libraries(name)) }
    catch (reason) { setError(String(reason)); setBusy(null) }
  }

  const steps = useMemo(() => [
    { id: 'goal', title: t('gofi.function-wizard.steps.goal', 'What to build') },
    { id: 'questions', title: t('gofi.function-wizard.steps.questions', 'A few questions') },
    { id: 'source', title: t('gofi.function-wizard.steps.source', 'Review the code') },
    { id: 'save', title: t('gofi.function-wizard.steps.save', 'Name and save') },
  ], [])
  const cards = goalCards()
  const functionName = functionNameOf(answers)

  return <Page title={t('gofi.function-wizard.page.title', 'New function')} icon="brackets-curly" subTitle={t('gofi.function-wizard.page.subtitle', 'Say what the function should do and GoFi writes a starter library you can refine, validate, and save. Flows then call it by name.')}
    actions={<LinkButton variant="secondary" fill="outline" icon="edit" href={paths.libraries()}>{t('gofi.function-wizard.page.open-editor', 'Use the editor instead')}</LinkButton>}>
    <div className={styles.wizard}>
      <WizardSteps steps={steps} current={step} onGoTo={goTo} />
      {error && <Alert severity="error" title={t('gofi.function-wizard.page.error-title', 'New function')} bottomSpacing={0} onRemove={() => setError('')}>{error}</Alert>}

      {step === 1 && <section aria-labelledby="wizard-goal">
        <Text element="h2" variant="h4" id="wizard-goal">{t('gofi.function-wizard.goal.title', 'What do you want to build?')}</Text>
        <div className={styles.cards}>{GOALS.map(goal => {
          const card = cards[goal]
          const ai = isAiGoal(goal)
          return <Card key={goal} noMargin isSelected={answers.goal === goal} onClick={() => set({ goal, outputField: goal === 'ai-extract' ? 'extracted' : 'summary', every: '30s' })} className={styles.card}>
            <Card.Figure><Icon name={card.icon} size="xl" className={styles.figure} /></Card.Figure>
            <Card.Heading>{card.title}</Card.Heading>
            <Card.Description>
              {card.description}
              {ai && !assistant.ready && <span className={styles.note}><Icon name="info-circle" size="sm" /> {t('gofi.function-wizard.goal.ai-not-ready', 'The AI assistant is not ready; an administrator enables it under Administration › AI assistant. You can still write the function now.')}</span>}
            </Card.Description>
          </Card>
        })}</div>
        {AI_GOALS.includes(answers.goal) && !assistant.ready && <Alert severity="info" title={t('gofi.function-wizard.goal.ai-alert-title', 'AI functions need the deployment AI endpoint')} bottomSpacing={0}>
          {t('gofi.function-wizard.goal.ai-alert-body', 'The function will be saved, but it cannot call a model until the assistant is enabled.')} <TextLink href={to(paths.administration('assistant'))} inline>{t('gofi.function-wizard.goal.ai-alert-link', 'Open Administration › AI assistant')}</TextLink>
        </Alert>}
      </section>}

      {step === 2 && <section aria-labelledby="wizard-questions" className={styles.form}>
        <Stack direction="column" gap={0.5}>
          <Text element="h2" variant="h4" id="wizard-questions">{cards[answers.goal].title}</Text>
          <Text color="secondary">{t('gofi.function-wizard.questions.intro', 'A few details shape the starter code. Everything can be changed in the next step.')}</Text>
        </Stack>
        <Questions answers={answers} set={set} assistantReady={assistant.ready} />
        <Field label={t('gofi.function-wizard.questions.function-name', 'Function name')} description={t('gofi.function-wizard.questions.function-name-description', 'How flows refer to it; letters, digits, and underscores. Leave empty for "{{name}}".', { name: DEFAULT_FUNCTION_NAMES[answers.goal] })} noMargin>
          <Input value={answers.functionName} placeholder={DEFAULT_FUNCTION_NAMES[answers.goal]} onChange={event => set({ functionName: event.currentTarget.value })} width={40} />
        </Field>
      </section>}

      {step === 3 && <section aria-labelledby="wizard-source" className={styles.form}>
        <Stack justifyContent="space-between" alignItems="flex-start" wrap="wrap" gap={2}>
          <Stack direction="column" gap={0.5}>
            <Text element="h2" variant="h4" id="wizard-source">{t('gofi.function-wizard.source.title', 'Review the generated code')}</Text>
            <Text color="secondary">{composition?.templateId
              ? t('gofi.function-wizard.source.intro', 'Started from the "{{template}}" template with your answers filled in. Edit freely; keyword-only parameters become per-node settings in flows.', { template: FUNCTION_TEMPLATES.find(template => template.id === composition.templateId)?.label ?? composition.templateId })
              : t('gofi.function-wizard.source.intro-fallback', 'A minimal starter; edit freely. Keyword-only parameters become per-node settings in flows.')}</Text>
          </Stack>
          <Stack gap={1} wrap="wrap">
            <Button variant="secondary" icon="sync" size="sm" disabled={!!busy} onClick={generate}>{t('gofi.function-wizard.source.regenerate', 'Regenerate from answers')}</Button>
            <Button variant="secondary" icon="check" size="sm" disabled={!!busy || !source} onClick={() => void validate()}>{busy === 'validate' ? t('gofi.function-wizard.source.validating', 'Validating...') : t('gofi.common.validate', 'Validate')}</Button>
            {assistant.ready
              ? <Button variant="secondary" icon="ai-sparkle" size="sm" disabled={!source} onClick={askAssistant}>{t('gofi.function-wizard.source.ask-assistant', 'Ask GoFiBot to refine')}</Button>
              : <Tooltip content={t('gofi.function-wizard.source.assistant-not-ready', 'GoFiBot is not ready; an administrator enables it under Administration › AI assistant.')}><span><Button variant="secondary" icon="ai-sparkle" size="sm" disabled>{t('gofi.function-wizard.source.ask-assistant', 'Ask GoFiBot to refine')}</Button></span></Tooltip>}
          </Stack>
        </Stack>
        <Suspense fallback={<PageLoading text={t('gofi.function-wizard.source.loading-editor', 'Loading editor...')} />}>
          <PythonSourceEditor source={source} onChange={updateSource} diagnostics={diagnostics} disabled={!!busy} validity={validity} ariaLabel={t('gofi.function-wizard.source.editor-aria', 'Generated library source')} />
        </Suspense>
        {validity === 'valid' && <div className={styles.valid}><Icon name="check-circle" />{t('gofi.function-wizard.source.validation-passed', 'Validation passed')}</div>}
        {!!diagnostics.length && <Alert severity="error" title={t('gofi.function-wizard.source.diagnostic-count', '{{count}} diagnostic(s)', { count: diagnostics.length })} bottomSpacing={0}>
          <Stack direction="column" gap={0.5}>{diagnostics.map((diagnostic, index) => <Text key={index} variant="bodySmall">{diagnostic.line}:{diagnostic.column} <Text variant="code">{diagnostic.code}</Text> {diagnostic.message}</Text>)}</Stack>
        </Alert>}
        <Text color="secondary" variant="bodySmall">{t('gofi.function-wizard.source.tooling-note', 'Validation runs in an isolated tooling worker and never executes the source. After saving, the Functions page can run it with sample input.')}</Text>
      </section>}

      {step === 4 && <section aria-labelledby="wizard-save" className={styles.form}>
        <Text element="h2" variant="h4" id="wizard-save">{t('gofi.function-wizard.save.title', 'Name and save the library')}</Text>
        <Field label={t('gofi.function-wizard.save.name', 'Library name')} description={t('gofi.function-wizard.save.name-description', 'Flows pick functions as library/function, e.g. {{library}}/{{function}}.', { library: name || suggestName(answers), function: functionName })} invalid={nameTouched && !!nameError} error={nameTouched ? nameError : undefined} noMargin>
          <Input value={name} onChange={event => { setName(event.currentTarget.value); setNameTouched(true) }} onBlur={() => setNameTouched(true)} placeholder={suggestName(answers)} width={40} autoFocus />
        </Field>
        {isAiGoal(answers.goal) && <Alert severity="info" title={t('gofi.function-wizard.save.ai-title', 'AI calls go through the deployment endpoint')} bottomSpacing={0}>
          {t('gofi.function-wizard.save.ai-body', 'gofi.llm calls the model configured under Administration › AI assistant through the controller; the key never reaches the worker. An administrator turns "Functions may use the AI endpoint" on there; libraries have no setting of their own.')} <TextLink href={to(paths.administration('assistant'))} inline>{t('gofi.function-wizard.goal.ai-alert-link', 'Open Administration › AI assistant')}</TextLink>
        </Alert>}
        <dl className={styles.summary}>
          {/* i18n-ignore */}
          <dt>{t('gofi.function-wizard.save.summary-function', 'Function')}</dt><dd><Text variant="code">@gofi.{answers.goal === 'source' ? answers.sourceKind : GOAL_KIND[answers.goal]}("{functionName}")</Text></dd>
          <dt>{t('gofi.function-wizard.save.summary-deadline', 'Worker deadline')}</dt><dd>{t('gofi.function-wizard.save.summary-deadline-value', '{{seconds}} s, 128 MiB (adjustable in the editor)', { seconds: isAiGoal(answers.goal) ? 30 : 5 })}</dd>
          <dt>{t('gofi.function-wizard.save.summary-tests', 'Saved tests')}</dt><dd>{composition?.test && !edited ? t('gofi.function-wizard.save.summary-tests-sample', 'Your sample, as "{{name}}"', { name: composition.test.name }) : t('gofi.function-wizard.save.summary-tests-none', 'None yet; add them on the Tests tab')}</dd>
        </dl>
        {(answers.goal === 'sink' && answers.sinkKind === 'http' || answers.goal === 'source') && <Alert severity="info" title={t('gofi.function-wizard.save.egress-title', 'Network access')} bottomSpacing={0}>{t('gofi.function-wizard.save.egress-body', 'Workers have no outbound network by default. Add an egress rule for the host it calls on the Network tab after saving.')}</Alert>}
      </section>}

      <footer className={styles.footer}>
        <Button variant="secondary" fill="text" onClick={() => navigate(paths.libraries())}>{t('gofi.common.cancel', 'Cancel')}</Button>
        <span className={styles.spacer} />
        <Button variant="secondary" icon="arrow-left" disabled={step === 1 || !!busy} onClick={() => goTo(step - 1)}>{t('gofi.function-wizard.footer.back', 'Back')}</Button>
        {step < STEP_COUNT
          ? <Button icon="arrow-right" onClick={() => goTo(step + 1)}>{step === 2 ? t('gofi.function-wizard.footer.generate', 'Generate') : t('gofi.function-wizard.footer.next', 'Next')}</Button>
          : <Button icon="save" disabled={!!busy || (nameTouched && !!nameError)} onClick={() => void save()}>{busy === 'save' ? t('gofi.function-wizard.footer.saving', 'Saving...') : t('gofi.function-wizard.footer.save', 'Save and launch')}</Button>}
      </footer>
    </div>
  </Page>
}

/** The per-goal questions of step 2. */
function Questions({ answers, set, assistantReady }: { answers: WizardAnswers; set: (patch: Partial<WizardAnswers>) => void; assistantReady: boolean }) {
  const sampleJson = (label = t('gofi.function-wizard.questions.sample-message', 'Sample message (optional)')) => <Field label={label} description={t('gofi.function-wizard.questions.sample-message-description', 'One message as a JSON object. It is saved as a test so you can run the function right after saving.')} noMargin>
    <TextArea value={answers.sample} rows={4} placeholder={'{"sensor": "a1", "temperature": 31.5, "level": "warn"}'} onChange={event => set({ sample: event.currentTarget.value })} className={fontMono} />
  </Field>
  switch (answers.goal) {
    case 'parse': {
      const formats: { value: ParseFormat; label: string }[] = [
        { value: 'json', label: t('gofi.function-wizard.parse.format-json', 'JSON') }, { value: 'csv', label: t('gofi.function-wizard.parse.format-csv', 'CSV') },
        { value: 'keyvalue', label: t('gofi.function-wizard.parse.format-keyvalue', 'key=value') }, { value: 'binary', label: t('gofi.function-wizard.parse.format-binary', 'Binary') },
      ]
      return <>
        <Field label={t('gofi.function-wizard.parse.format', 'What does the input look like?')} noMargin><RadioButtonGroup value={answers.parseFormat} options={formats} onChange={value => set({ parseFormat: value })} /></Field>
        {answers.parseFormat === 'csv' && <Field label={t('gofi.function-wizard.parse.columns', 'Column names, in order')} description={t('gofi.function-wizard.parse.columns-description', 'Comma separated; each row becomes one message with these fields.')} noMargin>
          {/* i18n-ignore */}
          <Input value={answers.columns} placeholder="time, host, value" onChange={event => set({ columns: event.currentTarget.value })} />
        </Field>}
        {answers.parseFormat === 'binary' && <Text color="secondary" variant="bodySmall">{t('gofi.function-wizard.parse.binary-hint', 'The starter unpacks a big-endian header with struct; adjust the format string to your layout. The construct package is also available.')}</Text>}
        <Field label={t('gofi.function-wizard.parse.sample', 'Sample input (optional)')} description={t('gofi.function-wizard.parse.sample-description', 'One frame as text. It is documented in the function and saved as a test.')} noMargin>
          {/* i18n-ignore */}
          <TextArea value={answers.sample} rows={3} placeholder={answers.parseFormat === 'csv' ? '2026-01-01T00:00:00Z,host-1,31.5' : answers.parseFormat === 'keyvalue' ? 'sensor=a1 temperature=31.5 level=warn' : '{"sensor": "a1", "temperature": 31.5}'} onChange={event => set({ sample: event.currentTarget.value })} className={fontMono} />
        </Field>
      </>
    }
    case 'transform': {
      const actions: { value: TransformAction; label: string }[] = [
        { value: 'uppercase', label: t('gofi.function-wizard.transform.action-uppercase', 'Upper-case it') },
        { value: 'lowercase', label: t('gofi.function-wizard.transform.action-lowercase', 'Lower-case it') },
        { value: 'number', label: t('gofi.function-wizard.transform.action-number', 'Convert it to a number') },
        { value: 'rename', label: t('gofi.function-wizard.transform.action-rename', 'Rename it') },
        { value: 'default', label: t('gofi.function-wizard.transform.action-default', 'Set a value when it is missing') },
        { value: 'threshold', label: t('gofi.function-wizard.transform.action-threshold', 'Label the message when it reaches a threshold') },
        { value: 'describe', label: t('gofi.function-wizard.transform.action-describe', 'Something else (describe it)') },
      ]
      const valueField: Record<TransformAction, { label: string; placeholder: string } | null> = {
        uppercase: null, lowercase: null, number: null,
        // i18n-ignore
        rename: { label: t('gofi.function-wizard.transform.new-name', 'New field name'), placeholder: 'temperature' },
        // i18n-ignore
        default: { label: t('gofi.function-wizard.transform.default-value', 'Value when missing'), placeholder: 'unknown' },
        threshold: { label: t('gofi.function-wizard.transform.threshold', 'Threshold'), placeholder: '30' },
        describe: { label: t('gofi.function-wizard.transform.describe', 'What should happen?'), placeholder: t('gofi.function-wizard.transform.describe-placeholder', 'Round the temperature to one decimal and add a unit field') },
      }
      const extra = valueField[answers.transformAction]
      return <>
        {/* i18n-ignore */}
        <Field label={t('gofi.function-wizard.transform.field', 'Which field?')} noMargin><Input value={answers.field} placeholder="temperature" width={40} onChange={event => set({ field: event.currentTarget.value })} /></Field>
        <Field label={t('gofi.function-wizard.transform.action', 'What should happen to it?')} noMargin><Combobox value={answers.transformAction} options={actions} onChange={option => set({ transformAction: option.value })} width={40} /></Field>
        {extra && <Field label={extra.label} noMargin>{answers.transformAction === 'describe'
          ? <TextArea value={answers.transformValue} rows={3} placeholder={extra.placeholder} onChange={event => set({ transformValue: event.currentTarget.value })} />
          : <Input value={answers.transformValue} placeholder={extra.placeholder} width={40} onChange={event => set({ transformValue: event.currentTarget.value })} />}</Field>}
        {sampleJson()}
      </>
    }
    case 'filter': {
      const translated = conditionToPython(answers.condition)
      return <>
        <Field label={t('gofi.function-wizard.filter.condition', 'Keep a message when...')} description={t('gofi.function-wizard.filter.condition-description', 'Field, comparison, value: "temperature > 30", "level is error", "message contains timeout". Anything else becomes a TODO in the code.')} noMargin>
          {/* i18n-ignore */}
          <Input value={answers.condition} placeholder="temperature > 30" onChange={event => set({ condition: event.currentTarget.value })} />
        </Field>
        {answers.condition.trim() && <Text color={translated ? 'secondary' : 'warning'} variant="bodySmall">{translated
          ? t('gofi.function-wizard.filter.translated', 'In Python: {{expression}}', { expression: translated })
          : t('gofi.function-wizard.filter.not-translated', 'GoFi cannot translate that sentence; the code will carry it as a TODO for you (or GoFiBot) to fill in.')}</Text>}
        {sampleJson()}
      </>
    }
    case 'sink': {
      const kinds: { value: SinkKind; label: string }[] = [{ value: 'print', label: t('gofi.function-wizard.sink.kind-print', 'Print to the worker log') }, { value: 'http', label: t('gofi.function-wizard.sink.kind-http', 'POST JSON to an HTTP endpoint') }]
      return <>
        <Field label={t('gofi.function-wizard.sink.kind', 'Where should messages go?')} noMargin><RadioButtonGroup value={answers.sinkKind} options={kinds} onChange={value => set({ sinkKind: value })} /></Field>
        {answers.sinkKind === 'http' && <Field label={t('gofi.function-wizard.sink.url', 'Endpoint URL')} description={t('gofi.function-wizard.sink.url-description', 'A per-node setting in flows; this is its default.')} noMargin>
          <Input value={answers.url} placeholder="https://hooks.example/in" onChange={event => set({ url: event.currentTarget.value })} />
        </Field>}
        {sampleJson()}
      </>
    }
    case 'source': {
      const kinds: { value: SourceKind; label: string }[] = [{ value: 'fetcher', label: t('gofi.function-wizard.source-goal.kind-fetcher', 'Poll on a schedule') }, { value: 'streamer', label: t('gofi.function-wizard.source-goal.kind-streamer', 'Stream continuously') }]
      return <>
        <Field label={t('gofi.function-wizard.source-goal.kind', 'How does data arrive?')} noMargin><RadioButtonGroup value={answers.sourceKind} options={kinds} onChange={value => set({ sourceKind: value, every: value === 'streamer' ? '5' : '30s' })} /></Field>
        {answers.sourceKind === 'fetcher' ? <>
          <Field label={t('gofi.function-wizard.source-goal.url', 'URL to poll')} noMargin><Input value={answers.url} placeholder="https://api.example/items" onChange={event => set({ url: event.currentTarget.value })} /></Field>
          <Field label={t('gofi.function-wizard.source-goal.every', 'How often?')} description={t('gofi.function-wizard.source-goal.every-description', 'A Go duration: 10s, 30s, 5m, 1h.')} noMargin><Input value={answers.every} placeholder="30s" width={20} onChange={event => set({ every: event.currentTarget.value })} /></Field>
        </> : <Field label={t('gofi.function-wizard.source-goal.interval', 'Seconds between frames')} description={t('gofi.function-wizard.source-goal.interval-description', 'The starter yields a counter on this interval; replace its loop with your subscription.')} noMargin>
          <Input value={answers.every} placeholder="5" width={20} onChange={event => set({ every: event.currentTarget.value })} />
        </Field>}
      </>
    }
    case 'ai-classify': return <>
      <AiNote ready={assistantReady} />
      {/* i18n-ignore */}
      <Field label={t('gofi.function-wizard.ai.labels', 'The labels, comma separated')} description={t('gofi.function-wizard.ai.labels-description', 'The model must pick exactly one; it lands in the "label" field.')} noMargin><Input value={answers.labels} placeholder="normal, suspicious, critical" onChange={event => set({ labels: event.currentTarget.value })} /></Field>
      {/* i18n-ignore */}
      <Field label={t('gofi.function-wizard.ai.drop-labels', 'Drop messages labelled...')} description={t('gofi.function-wizard.ai.drop-labels-description', 'Comma separated, from the labels above; the rest continue through the flow. Empty keeps everything.')} noMargin><Input value={answers.dropLabels} placeholder="normal" onChange={event => set({ dropLabels: event.currentTarget.value })} /></Field>
      <Field label={t('gofi.function-wizard.ai.instructions', 'What do the labels mean? (optional)')} noMargin><TextArea value={answers.question} rows={3} placeholder={t('gofi.function-wizard.ai.instructions-placeholder', 'Classify this event by how urgently an operator must look at it.')} onChange={event => set({ question: event.currentTarget.value })} /></Field>
      {sampleJson()}
    </>
    case 'ai-enrich': return <>
      <AiNote ready={assistantReady} />
      <Field label={t('gofi.function-wizard.ai.question', 'What should the model answer about each message?')} noMargin><TextArea value={answers.question} rows={3} placeholder={t('gofi.function-wizard.ai.question-placeholder', 'Summarize this event in one sentence.')} onChange={event => set({ question: event.currentTarget.value })} /></Field>
      {/* i18n-ignore */}
      <Field label={t('gofi.function-wizard.ai.output-field', 'Store the answer in field')} noMargin><Input value={answers.outputField} placeholder="summary" width={30} onChange={event => set({ outputField: event.currentTarget.value })} /></Field>
      {sampleJson()}
    </>
    case 'ai-extract': return <>
      <AiNote ready={assistantReady} />
      {/* i18n-ignore */}
      <Field label={t('gofi.function-wizard.ai.extract-fields', 'Values to extract, comma separated')} description={t('gofi.function-wizard.ai.extract-fields-description', 'The model answers with a JSON object with exactly these keys.')} noMargin><Input value={answers.question} placeholder="host, error code, affected user" onChange={event => set({ question: event.currentTarget.value })} /></Field>
      {/* i18n-ignore */}
      <Field label={t('gofi.function-wizard.ai.output-field', 'Store the answer in field')} noMargin><Input value={answers.outputField} placeholder="extracted" width={30} onChange={event => set({ outputField: event.currentTarget.value })} /></Field>
      {sampleJson()}
    </>
    case 'ai-sink': return <>
      <AiNote ready={assistantReady} />
      <Field label={t('gofi.function-wizard.ai.sink-instruction', 'What should the model do with each message?')} description={t('gofi.function-wizard.ai.sink-instruction-description', 'A failure counts as a delivery failure for this sink.')} noMargin><TextArea value={answers.question} rows={3} placeholder={t('gofi.function-wizard.ai.sink-instruction-placeholder', 'Handle this event: open a ticket if it is critical, otherwise reply in one line.')} onChange={event => set({ question: event.currentTarget.value })} /></Field>
      {sampleJson()}
    </>
  }
}

function AiNote({ ready }: { ready: boolean }) {
  if (ready) return null
  return <Alert severity="info" title={t('gofi.function-wizard.ai.not-ready-title', 'The AI assistant is not ready')} bottomSpacing={0}>
    {t('gofi.function-wizard.ai.not-ready-body', 'An administrator enables it under Administration › AI assistant. The function can be written and saved now and will work once the endpoint is configured.')} <TextLink href={to(paths.administration('assistant'))} inline>{t('gofi.function-wizard.goal.ai-alert-link', 'Open Administration › AI assistant')}</TextLink>
  </Alert>
}

const fontMono = css({ fontFamily: 'monospace' })

const getStyles = (theme: GrafanaTheme2) => ({
  wizard: css({ display: 'flex', flexDirection: 'column', gap: theme.spacing(3), maxWidth: 1100 }),
  cards: css({ display: 'grid', gridTemplateColumns: 'repeat(auto-fill, minmax(300px, 1fr))', gap: theme.spacing(2), marginTop: theme.spacing(2), marginBottom: theme.spacing(2) }),
  card: css({ alignItems: 'start' }),
  figure: css({ color: theme.colors.text.secondary }),
  note: css({ display: 'block', marginTop: theme.spacing(1), color: theme.colors.warning.text, fontSize: theme.typography.bodySmall.fontSize }),
  form: css({ display: 'flex', flexDirection: 'column', gap: theme.spacing(2), maxWidth: 720 }),
  valid: css({ display: 'flex', alignItems: 'center', gap: theme.spacing(0.5), marginTop: theme.spacing(-1.5), color: theme.colors.success.text, fontSize: theme.typography.bodySmall.fontSize }),
  summary: css({
    display: 'grid', gridTemplateColumns: 'max-content 1fr', gap: theme.spacing(0.5, 2), margin: 0,
    padding: theme.spacing(2), background: theme.colors.background.secondary, borderRadius: theme.shape.radius.default,
    dt: { color: theme.colors.text.secondary, fontSize: theme.typography.bodySmall.fontSize }, dd: { margin: 0 },
  }),
  footer: css({ display: 'flex', alignItems: 'center', gap: theme.spacing(1), paddingTop: theme.spacing(2), borderTop: `1px solid ${theme.colors.border.weak}` }),
  spacer: css({ flex: 1 }),
})
