import { useEffect, useMemo, useRef, useState, type ReactNode } from 'react'
import { css, cx } from '@emotion/css'
import type { GrafanaTheme2, IconName } from '@grafana/data'
import { Button, ClipboardButton, Icon, IconButton, Text, TextLink, useStyles2 } from '@grafana/ui'
import { getEndpoints, type DeployModeInfo } from '../../api'
import { navigate, paths, to } from '../../router'
import { useGoFi } from '../../state'
import { announceFlowSeed, setFlowSeed } from '../../lib/flow-seed'
import { NODE_PORT_RANGE, listenOf, type FlowDefinition, type ListenSource } from '../../types'
import { t } from '../../i18n'
import { useAssistantReadiness } from '../assistant/useAssistantReadiness'
import { PipelineDiagram } from './PipelineDiagram'
import { TUTORIAL_STEP_IDS, docsUrl, isFirstStep, isLastStep, onStepPage, progress, stepHref, stepId, type StepContext, type TutorialStepId } from './model'
import { ingressKind, portForwardCommand, resolveIngress, testCommand, type IngressPlaceholders } from './commands'
import { TUTORIAL_FLOW_NAME, TUTORIAL_FLOW_PORT, tutorialExampleFlow, tutorialTestCase } from './example'
import type { TutorialController } from './useTutorial'

const WIDTH = 400

const stepTitles = (): Record<TutorialStepId, string> => ({
  welcome: t('gofi.tutorial.panel.step-welcome', 'What GoFi is'),
  navigation: t('gofi.tutorial.panel.step-navigation', 'Finding your way around'),
  'first-flow': t('gofi.tutorial.panel.step-first-flow', 'Make your first flow'),
  ingress: t('gofi.tutorial.panel.step-ingress', 'Send traffic in'),
  monitoring: t('gofi.tutorial.panel.step-monitoring', 'Watch it run'),
  done: t('gofi.tutorial.panel.step-done', 'You are set'),
})

/** The guided tour, docked on the right of the window beside the page (like
 * GoFiBot's panel; a full-width overlay on phones). Each step opens the real
 * page it talks about; the panel says what to look at. Closing keeps the
 * step, so the tour resumes from New › Tutorial or ?tutorial=1. */
export function TutorialPanel({ tutorial, onOpenAssistant }: { tutorial: TutorialController; onOpenAssistant?: () => void }) {
  const styles = useStyles2(getStyles)
  const { flows, mode } = useGoFi()
  // The flow the later steps talk about: the tutorial's own once saved, else any listen flow.
  const flow = useMemo(() => flows.find(item => item.name === TUTORIAL_FLOW_NAME) ?? flows.find(item => !!item.source.listen), [flows])
  const context: StepContext = { flowName: flow?.name }
  const id = stepId(tutorial.step)
  const titles = stepTitles()
  const { step, total, percent } = progress(tutorial.step)
  const bodyRef = useRef<HTMLDivElement>(null)
  // A new step starts at the top and takes focus, so screen readers announce it.
  useEffect(() => { if (tutorial.open) { bodyRef.current?.scrollTo({ top: 0 }); bodyRef.current?.focus({ preventScroll: true }) } }, [tutorial.step, tutorial.open])

  if (!tutorial.open) return null
  return <aside className={styles.panel} style={{ width: WIDTH }} aria-label={t('gofi.tutorial.panel.title', 'Tutorial')}>
    <header className={styles.header}>
      <Icon name="book" className={styles.headerIcon} />
      <Text element="h2" variant="h5">{t('gofi.tutorial.panel.title', 'Tutorial')}</Text>
      <span className={styles.spacer} />
      <IconButton name="times" tooltip={t('gofi.tutorial.panel.close', 'Close for now')} onClick={tutorial.skip} />
    </header>
    <nav className={styles.progress} aria-label={t('gofi.tutorial.panel.steps', 'Tutorial steps')}>
      <ol className={styles.dots}>{TUTORIAL_STEP_IDS.map((key, index) => {
        const number = index + 1
        const state = number === step ? 'current' : number < step ? 'done' : 'todo'
        return <li key={key}>
          <button type="button" className={cx(styles.dot, state === 'current' && styles.dotCurrent, state === 'done' && styles.dotDone)} aria-current={state === 'current' ? 'step' : undefined}
            aria-label={t('gofi.tutorial.panel.step-label', 'Step {{number}}: {{title}}', { number, title: titles[key] })} onClick={() => tutorial.goTo(number, context)}>
            {state === 'done' ? <Icon name="check" size="sm" /> : number}
          </button>
        </li>
      })}</ol>
      <div className={styles.bar} role="progressbar" aria-valuemin={0} aria-valuemax={100} aria-valuenow={percent} aria-label={t('gofi.tutorial.panel.progress', 'Tutorial progress')}>
        <div className={styles.barFill} style={{ width: `${percent}%` }} />
      </div>
    </nav>
    <div ref={bodyRef} className={styles.body} tabIndex={-1}>
      <Text element="p" variant="bodySmall" color="secondary">{t('gofi.tutorial.panel.counter', 'Step {{step}} of {{total}}', { step, total })}</Text>
      <Text element="h3" variant="h4">{titles[id]}</Text>
      {id === 'welcome' && <WelcomeStep />}
      {id === 'navigation' && <NavigationStep kubernetes={mode?.driverKind === 'kubernetes'} />}
      {id === 'first-flow' && <FirstFlowStep driverKind={mode?.driverKind} />}
      {id === 'ingress' && <IngressStep flow={flow} mode={mode} />}
      {id === 'monitoring' && <MonitoringStep flow={flow} />}
      {id === 'done' && <DoneStep onOpenAssistant={onOpenAssistant} />}
    </div>
    <footer className={styles.footer}>
      <Button variant="secondary" fill="text" size="sm" onClick={tutorial.skip}>{t('gofi.tutorial.panel.skip', 'Skip for now')}</Button>
      <span className={styles.spacer} />
      <Button variant="secondary" icon="arrow-left" disabled={isFirstStep(step)} onClick={() => tutorial.goTo(step - 1, context)}>{t('gofi.tutorial.panel.back', 'Back')}</Button>
      {isLastStep(step)
        ? <Button icon="check" onClick={tutorial.finish}>{t('gofi.tutorial.panel.done', 'Done')}</Button>
        : <Button onClick={() => tutorial.goTo(step + 1, context)}>{t('gofi.tutorial.panel.next', 'Next')}</Button>}
    </footer>
  </aside>
}

/** A label with its one-line explanation. */
function Term({ icon, label, href, external, children }: { icon?: IconName; label: string; href?: string; external?: boolean; children: NonNullable<ReactNode> }) {
  const styles = useStyles2(getStyles)
  return <div className={styles.term}>
    <dt>{icon && <Icon name={icon} className={styles.termIcon} />}{href ? <TextLink href={external ? href : to(href)} external={external} inline={false}>{label}</TextLink> : label}</dt>
    <dd><Text color="secondary" variant="bodySmall">{children}</Text></dd>
  </div>
}

function Paragraph({ children, secondary }: { children: NonNullable<ReactNode>; secondary?: boolean }) {
  return <Text element="p" color={secondary ? 'secondary' : undefined} variant={secondary ? 'bodySmall' : undefined}>{children}</Text>
}

function WelcomeStep() {
  const styles = useStyles2(getStyles)
  return <>
    <Paragraph>{t('gofi.tutorial.welcome.intro', 'GoFi connects data that arrives in odd or unknown shapes to the applications that need it. A flow is the whole path: where data comes in, how it is cut into messages and understood, what is kept, and where it goes.')}</Paragraph>
    <PipelineDiagram />
    <dl className={styles.terms}>
      <Term icon="plug" label={t('gofi.tutorial.welcome.source', 'Source')}>{t('gofi.tutorial.welcome.source-text', 'Where data arrives: a TCP or UDP port GoFi listens on, or an endpoint it polls or subscribes to.')}</Term>
      <Term icon="align-left" label={t('gofi.tutorial.welcome.frames', 'Frames')}>{t('gofi.tutorial.welcome.frames-text', 'Bytes become messages: one per line, per delimiter, per length prefix, or per connection.')}</Term>
      <Term icon="brackets-curly" label={t('gofi.tutorial.welcome.decode', 'Decode and parse')}>{t('gofi.tutorial.welcome.decode-text', 'Decoders such as base64 or gzip unwrap the frame; the parser (JSON, XML, text, Python) turns it into fields.')}</Term>
      <Term icon="filter" label={t('gofi.tutorial.welcome.stages', 'Stages')}>{t('gofi.tutorial.welcome.stages-text', 'CEL and regex filters, transforms, schema checks, deduplication and rate limits decide what passes and reshape it.')}</Term>
      <Term icon="lock" label={t('gofi.tutorial.welcome.encode', 'Encode')}>{t('gofi.tutorial.welcome.encode-text', 'Format and encoders before a sink sends: wrapped, fields or raw, then gzip, base64 or a Python encoder. Several sinks can share one.')}</Term>
      <Term icon="share-alt" label={t('gofi.tutorial.welcome.sinks', 'Sinks')}>{t('gofi.tutorial.welcome.sinks-text', 'HTTP, TCP, UDP, NATS, Loki, stdout and more receive the result; a dead-letter sink gets what was rejected.')}</Term>
    </dl>
    <Paragraph secondary>{t('gofi.tutorial.welcome.live', 'Everything a flow sees shows up live in this portal. Home, behind this panel, lists every flow and its traffic.')}</Paragraph>
  </>
}

function NavigationStep({ kubernetes }: { kubernetes: boolean }) {
  const styles = useStyles2(getStyles)
  return <>
    <Paragraph>{t('gofi.tutorial.navigation.intro', 'The main menu on the left (the bars button opens it on small screens) holds every tool:')}</Paragraph>
    <dl className={styles.terms}>
      <Term icon="exchange-alt" label={t('gofi.shell.nav.flows', 'Flows')} href={paths.flows()}>{t('gofi.tutorial.navigation.flows', 'Every flow; open one to edit its pipeline, watch its traffic, and read its problems.')}</Term>
      <Term icon="compass" label={t('gofi.shell.nav.explore', 'Explore')} href={paths.explore()}>{t('gofi.tutorial.navigation.explore', 'Live traffic from all flows at once, searchable and filterable.')}</Term>
      <Term icon="brackets-curly" label={t('gofi.shell.nav.functions', 'Functions')} href={paths.libraries()}>{t('gofi.tutorial.navigation.functions', 'Python function libraries (framers, parsers, transformers, sinks) that run in isolated workers.')}</Term>
      <Term icon="book-open" label={t('gofi.shell.nav.cel-library', 'CEL library')} href={paths.expressions()}>{t('gofi.tutorial.navigation.cel', 'Saved CEL expressions that stages reuse by name.')}</Term>
      <Term icon="adjust-circle" label={t('gofi.shell.nav.connections', 'Connections')} href={paths.endpoints()}>{t('gofi.tutorial.navigation.connections', 'Endpoints GoFi discovered (Services, Pods, containers) to pick as destinations.')}</Term>
      {kubernetes && <Term icon="cloud" label={t('gofi.shell.nav.cluster', 'Cluster')} href={paths.cluster()}>{t('gofi.tutorial.navigation.cluster', 'The namespace GoFi runs in: Pods, packages, and the network policies that affect flows.')}</Term>}
      <Term icon="cog" label={t('gofi.shell.nav.administration', 'Administration')} href={paths.administration()}>{t('gofi.tutorial.navigation.administration', 'Configuration export, deployment details, and GoFiBot settings.')}</Term>
    </dl>
    <Paragraph secondary>{t('gofi.tutorial.navigation.tips', 'The search box in the top bar (Ctrl+K or ⌘K) jumps to any page or flow. The badge at the bottom of the menu shows how this GoFi is deployed.')}</Paragraph>
  </>
}

function FirstFlowStep({ driverKind }: { driverKind: string | undefined }) {
  const styles = useStyles2(getStyles)
  const loadExample = () => {
    setFlowSeed(tutorialExampleFlow(driverKind, [tutorialTestCase(t('gofi.tutorial.first-flow.sample-name', 'A sample event'))]))
    // The New flow page swaps its draft when it is already open; otherwise it reads the seed on mount.
    announceFlowSeed()
    if (!onStepPage(window.location.hash, paths.newFlow())) navigate(paths.newFlow())
  }
  return <>
    <Paragraph>{t('gofi.tutorial.first-flow.intro', 'The New flow page is a canvas: the source on the left, stages in the middle, sinks on the right. Select a node to edit it in the inspector.')}</Paragraph>
    <div className={styles.actions}>
      <Button icon="file-alt" onClick={loadExample}>{t('gofi.tutorial.first-flow.load-example', 'Load example')}</Button>
      <Button variant="secondary" icon="exchange-alt" onClick={() => navigate(paths.newFlow())}>{t('gofi.tutorial.first-flow.open-new-flow', 'Open New flow')}</Button>
    </div>
    <Paragraph secondary>{t('gofi.tutorial.first-flow.example-text', 'The example is a flow named {{name}} that listens on TCP {{port}}, reads one JSON object per line, and prints every message to stdout.', { name: TUTORIAL_FLOW_NAME, port: TUTORIAL_FLOW_PORT })}</Paragraph>
    <dl className={styles.terms}>
      <Term label={t('gofi.tutorial.first-flow.name', 'Name')}>{t('gofi.tutorial.first-flow.name-text', 'Identifies the flow everywhere: lowercase letters, digits, and dashes.')}</Term>
      <Term label={t('gofi.tutorial.first-flow.source', 'Source')}>{t('gofi.tutorial.first-flow.source-text', 'The port and network (TCP, UDP, QUIC) to listen on, or an endpoint to pull from.')}</Term>
      <Term label={t('gofi.tutorial.first-flow.framing', 'Framing')}>{t('gofi.tutorial.first-flow.framing-text', 'How bytes split into messages. Line framing takes everything up to a newline.')}</Term>
      <Term label={t('gofi.tutorial.first-flow.parser', 'Parser')}>{t('gofi.tutorial.first-flow.parser-text', 'Turns each frame into fields. JSON keys become field names; nested keys are dotted.')}</Term>
      <Term label={t('gofi.tutorial.first-flow.sink', 'Sink')}>{t('gofi.tutorial.first-flow.sink-text', 'Where accepted messages go. stdout writes a JSON line to the GoFi log, handy while testing.')}</Term>
    </dl>
    <Paragraph secondary>{t('gofi.tutorial.first-flow.save', 'Save creates the flow and starts it. Mock mode, next to Save, runs a sample through the pipeline without sending anything real.')}</Paragraph>
  </>
}

function IngressStep({ flow, mode }: { flow: FlowDefinition | undefined; mode: DeployModeInfo | null }) {
  const styles = useStyles2(getStyles)
  const driverKind = mode?.driverKind
  const kind = ingressKind(driverKind)
  const listen: ListenSource = flow ? listenOf(flow) : listenOf(tutorialExampleFlow(driverKind))
  const network = listen.network || 'tcp'
  const flowName = flow?.name ?? TUTORIAL_FLOW_NAME
  // On Kubernetes the Service the cluster created may already carry the assigned NodePort and an external address.
  const [discovered, setDiscovered] = useState<{ host?: string; nodePort?: number }>({})
  useEffect(() => {
    if (kind !== 'kubernetes' || !flow) return
    let cancelled = false
    getEndpoints().then(endpoints => {
      if (cancelled) return
      const byName = endpoints.find(endpoint => endpoint.kind === 'kubernetes' && endpoint.name === flow.name)
      const match = byName ?? endpoints.find(endpoint => endpoint.kind === 'kubernetes' && endpoint.ports.some(port => port.port === listen.port && !!port.nodePort))
      const port = match?.ports.find(item => item.port === listen.port)
      setDiscovered({ host: match?.externalAddresses?.[0], nodePort: port?.nodePort })
    }).catch(() => {})
    return () => { cancelled = true }
  }, [kind, flow, listen.port])
  const placeholders: IngressPlaceholders = {
    nodeAddress: t('gofi.tutorial.ingress.placeholder-node', '<node-address>'),
    nodePort: t('gofi.tutorial.ingress.placeholder-node-port', '<node-port>'),
    loadBalancerAddress: t('gofi.tutorial.ingress.placeholder-load-balancer', '<load-balancer-address>'),
    gatewayAddress: t('gofi.tutorial.ingress.placeholder-gateway', '<gateway-address>'),
    clusterService: t('gofi.tutorial.ingress.placeholder-service', '<service-address>'),
  }
  const ingress = resolveIngress({ driverKind, listen, host: discovered.host, assignedNodePort: discovered.nodePort }, placeholders)
  const command = testCommand(network, ingress.host, ingress.port)
  const forward = kind === 'kubernetes' && ingress.via === 'cluster-ip' ? portForwardCommand(flowName, listen.port) : null
  const localCommand = forward ? testCommand(network, '127.0.0.1', listen.port) : null
  const hasPlaceholder = typeof ingress.port === 'string' || ingress.host.startsWith('<')
  const range = { min: mode?.nodePortMin || NODE_PORT_RANGE.min, max: mode?.nodePortMax || NODE_PORT_RANGE.max }
  const environment = kind === 'kubernetes' ? t('gofi.tutorial.ingress.env-kubernetes', 'Kubernetes') : kind === 'docker' ? t('gofi.tutorial.ingress.env-docker', 'Docker') : t('gofi.tutorial.ingress.env-host', 'Binary on a host')
  const viaText: Record<typeof ingress.via, string> = {
    host: t('gofi.tutorial.ingress.via-host', 'bound on this host'),
    docker: t('gofi.tutorial.ingress.via-docker', 'published on the Docker host'),
    'node-port': t('gofi.tutorial.ingress.via-node-port', 'NodePort on every node'),
    'load-balancer': t('gofi.tutorial.ingress.via-load-balancer', 'LoadBalancer address'),
    gateway: t('gofi.tutorial.ingress.via-gateway', 'Gateway route'),
    'cluster-ip': t('gofi.tutorial.ingress.via-cluster-ip', 'inside the cluster only'),
  }
  const editorHref = stepHref('ingress', { flowName: flow?.name }) ?? paths.newFlow()
  return <>
    <div className={styles.environment}>
      <Icon name={kind === 'kubernetes' ? 'kubernetes' : kind === 'docker' ? 'docker' : 'monitor'} size="lg" />
      <div>
        <Text weight="medium">{t('gofi.tutorial.ingress.environment', 'This GoFi: {{environment}}', { environment })}</Text>
        <div><Text color="secondary" variant="bodySmall">{t('gofi.tutorial.ingress.flow-line', 'Flow {{name}} listens on {{network}} {{port}} · {{via}}', { name: flowName, network: network.toUpperCase(), port: listen.port, via: viaText[ingress.via] })}</Text></div>
      </div>
    </div>
    {kind === 'kubernetes' && <>
      <Paragraph>{t('gofi.tutorial.ingress.kubernetes-text', 'Each flow is a Pod with its own Service. How far the port reaches is the flow\'s exposure: Internal or ClusterIP (inside the cluster), NodePort (a port between {{min}} and {{max}} on every node), LoadBalancer (an external address), or a Gateway route (TCPRoute/UDPRoute on a Gateway listener).', { min: range.min, max: range.max })}</Paragraph>
      <Paragraph secondary>{t('gofi.tutorial.ingress.kubernetes-where', 'Set it in the flow editor: select the source node, then Exposure in the inspector. The Connections page lists the addresses the cluster assigned.')}</Paragraph>
    </>}
    {kind === 'docker' && <Paragraph>{t('gofi.tutorial.ingress.docker-text', 'Each flow is a container that publishes its port one-to-one on the Docker host, so senders use the Docker host\'s address with the flow\'s port. Containers on the shared GoFi network can use the flow container\'s name instead.')}</Paragraph>}
    {kind === 'host' && <Paragraph>{t('gofi.tutorial.ingress.host-text', 'Each flow binds its port directly on the machine running GoFi, so senders use that machine\'s address with the flow\'s port (127.0.0.1 from the same machine). Kubernetes exposure settings do not apply here.')}</Paragraph>}
    {!flow && <Paragraph secondary>{t('gofi.tutorial.ingress.no-flow', 'No flow is saved yet; the command below targets the example from the previous step.')}</Paragraph>}
    {forward && localCommand && <>
      <Paragraph secondary>{t('gofi.tutorial.ingress.port-forward', 'With no exposure, reach the flow from your workstation through a port-forward to its Pod, then send to the forwarded port:')}</Paragraph>
      <CommandBlock command={forward} />
      <CommandBlock command={localCommand} />
      <Paragraph secondary>{t('gofi.tutorial.ingress.in-cluster', 'From inside the cluster, send to the flow\'s Service instead:')}</Paragraph>
    </>}
    {command
      ? <>
        {!forward && <Paragraph secondary>{t('gofi.tutorial.ingress.send-text', 'Send one test frame from a terminal (the trailing newline completes the line frame):')}</Paragraph>}
        <CommandBlock command={command} />
        {hasPlaceholder && <Paragraph secondary>{t('gofi.tutorial.ingress.placeholders', 'Replace the values in angle brackets with your cluster\'s: a node or load-balancer address, and the NodePort shown on the flow once it is saved.')}</Paragraph>}
      </>
      : <Paragraph secondary>{t('gofi.tutorial.ingress.quic', 'This flow listens on QUIC, which netcat cannot speak; see the QUIC guide for a client.')} <TextLink href={docsUrl('quic.md')} external inline>{t('gofi.tutorial.ingress.quic-docs', 'QUIC guide')}</TextLink></Paragraph>}
    <div className={styles.actions}>
      <Button variant="secondary" icon="pen" onClick={() => navigate(editorHref)}>{flow ? t('gofi.tutorial.ingress.open-editor', 'Open the flow editor') : t('gofi.tutorial.first-flow.open-new-flow', 'Open New flow')}</Button>
    </div>
  </>
}

function CommandBlock({ command }: { command: string }) {
  const styles = useStyles2(getStyles)
  return <div className={styles.command}>
    <pre className={styles.pre}><code>{command}</code></pre>
    <ClipboardButton getText={() => command} icon="copy" size="sm" variant="secondary" fill="text" className={styles.copy}>{t('gofi.tutorial.ingress.copy', 'Copy')}</ClipboardButton>
  </div>
}

function MonitoringStep({ flow }: { flow: FlowDefinition | undefined }) {
  const styles = useStyles2(getStyles)
  const metricsUrl = `${window.location.origin}/metrics`
  const metricNames = 'gofi_frames_received_total · gofi_messages_rejected_total · gofi_sink_dispatch_total'
  return <>
    <Paragraph>{t('gofi.tutorial.monitoring.intro', 'Now send the test frame and watch it arrive. Live views are bounded and best effort: they show what flows see right now, not durable history.')}</Paragraph>
    <dl className={styles.terms}>
      <Term icon="compass" label={t('gofi.shell.nav.explore', 'Explore')} href={paths.explore(flow?.name)}>{t('gofi.tutorial.monitoring.explore', 'Live traffic from all flows or one. Search fields and raw bytes; filter by level and by accepted or rejected.')}</Term>
      <Term icon="eye" label={t('gofi.tutorial.monitoring.live-traffic', 'Flow › Live traffic')} href={flow ? paths.flow(flow.name, 'traffic') : paths.flows()}>{t('gofi.tutorial.monitoring.live-traffic-text', 'One flow\'s messages with the inspector: parsed fields, metadata, and which stage rejected a message.')}</Term>
      <Term icon="bell" label={t('gofi.tutorial.monitoring.problems', 'Flow › Problems')} href={flow ? paths.flow(flow.name, 'problems') : paths.flows()}>{t('gofi.tutorial.monitoring.problems-text', 'Runtime problems per component (source, parser, stage, sink) with counts and when they last happened; also a badge in the top bar.')}</Term>
      <Term icon="chart-line" label={t('gofi.tutorial.monitoring.metrics', 'Prometheus metrics')} href={metricsUrl} external>{t('gofi.tutorial.monitoring.metrics-text', 'Scrape {{url}}: frames received, parse errors, rejected messages, sink dispatches and latency, each labelled with the flow name.', { url: metricsUrl })} <span className={styles.mono}>{metricNames}</span></Term>
      <Term icon="graph-bar" label={t('gofi.tutorial.monitoring.dashboard', 'Grafana dashboard')} href={docsUrl('grafana-dashboard.json')} external>{t('gofi.tutorial.monitoring.dashboard-text', 'Import docs/grafana-dashboard.json for traffic, parser errors, rejections, sink failures, latency, and Loki logs; pick your data sources after import.')}</Term>
    </dl>
    <Paragraph secondary>{t('gofi.tutorial.monitoring.logs', 'GoFi itself logs one JSON object per line on stdout, and the stdout sink prints each message there too.')} <TextLink href={docsUrl('observability.md')} external inline>{t('gofi.tutorial.monitoring.logs-docs', 'Logs and metrics guide')}</TextLink></Paragraph>
  </>
}

function DoneStep({ onOpenAssistant }: { onOpenAssistant?: () => void }) {
  const styles = useStyles2(getStyles)
  const assistant = useAssistantReadiness()
  return <>
    <Paragraph>{t('gofi.tutorial.done.intro', 'That is the loop: build a flow, send it traffic, watch what it does. Everything else, decoders, Python functions, layered parsing, Kubernetes destinations, builds on it.')}</Paragraph>
    <dl className={styles.terms}>
      <Term icon="book" label={t('gofi.tutorial.done.docs-application', 'Application walkthrough')} href={docsUrl('application.md')} external>{t('gofi.tutorial.done.docs-application-text', 'Receive, decode, filter, deliver, with a complete example.')}</Term>
      <Term icon="book" label={t('gofi.tutorial.done.docs-flows', 'Flows and sources')} href={docsUrl('flows.md')} external>{t('gofi.tutorial.done.docs-flows-text', 'The full resource: every source, stage, and sink.')}</Term>
      <Term icon="book" label={t('gofi.tutorial.done.docs-deployment', 'Deployment')} href={docsUrl('deployment.md')} external>{t('gofi.tutorial.done.docs-deployment-text', 'Binary, Docker Compose, Kubernetes and policy-managed clusters; runbooks.')}</Term>
      <Term icon="book" label={t('gofi.tutorial.done.docs-python', 'Python libraries')} href={docsUrl('python.md')} external>{t('gofi.tutorial.done.docs-python-text', 'Framers, parsers, transformers, and sinks in isolated workers.')}</Term>
    </dl>
    {assistant.ready && onOpenAssistant
      ? <>
        <Paragraph>{t('gofi.tutorial.done.assistant-ready', 'GoFiBot is connected. It reads your flows, CEL, and Python libraries, answers questions, and drafts changes you approve.')}</Paragraph>
        <div className={styles.actions}><Button icon="comment-alt-message" onClick={onOpenAssistant}>{t('gofi.shell.chrome.ask-gofibot', 'Ask GoFiBot')}</Button></div>
      </>
      : <Paragraph secondary>{t('gofi.tutorial.done.assistant-off', 'GoFiBot, the built-in assistant, is not enabled here. An administrator can connect a model under Administration › AI assistant.')} <TextLink href={to(paths.administration('assistant'))} inline>{t('gofi.shell.nav.administration-assistant', 'AI assistant')}</TextLink></Paragraph>}
    <Paragraph secondary>{t('gofi.tutorial.done.reopen', 'Reopen this tutorial any time from New › Tutorial in the top bar, or by adding ?tutorial=1 to the address.')}</Paragraph>
  </>
}

const getStyles = (theme: GrafanaTheme2) => ({
  panel: css({
    position: 'relative', flexShrink: 0, display: 'flex', flexDirection: 'column', minWidth: 0, height: '100%',
    background: theme.colors.background.primary, borderLeft: `1px solid ${theme.colors.border.weak}`,
    // Phones: cover the page instead of squeezing it.
    [theme.breakpoints.down('md')]: { position: 'absolute', top: 0, bottom: 0, right: 0, width: '100% !important', zIndex: theme.zIndex.navbarFixed + 1, boxShadow: theme.shadows.z3 },
  }),
  header: css({ display: 'flex', alignItems: 'center', gap: theme.spacing(1), padding: theme.spacing(1, 1, 1, 2), borderBottom: `1px solid ${theme.colors.border.weak}`, h2: { margin: 0 } }),
  headerIcon: css({ color: theme.colors.primary.text }),
  spacer: css({ flex: 1 }),
  progress: css({ padding: theme.spacing(1.5, 2, 0), display: 'flex', flexDirection: 'column', gap: theme.spacing(1) }),
  dots: css({ display: 'flex', justifyContent: 'space-between', listStyle: 'none', margin: 0, padding: 0 }),
  dot: css({
    width: 28, height: 28, display: 'flex', alignItems: 'center', justifyContent: 'center', borderRadius: theme.shape.radius.circle,
    border: `1px solid ${theme.colors.border.medium}`, background: theme.colors.background.secondary, color: theme.colors.text.secondary,
    fontSize: theme.typography.bodySmall.fontSize, fontWeight: theme.typography.fontWeightMedium, cursor: 'pointer',
    '&:hover': { borderColor: theme.colors.primary.border, color: theme.colors.text.primary },
    '&:focus-visible': { outline: `2px solid ${theme.colors.primary.main}`, outlineOffset: 1 },
  }),
  dotCurrent: css({ background: theme.colors.primary.main, borderColor: theme.colors.primary.main, color: theme.colors.primary.contrastText, '&:hover': { color: theme.colors.primary.contrastText } }),
  dotDone: css({ background: theme.colors.primary.transparent, borderColor: theme.colors.primary.border, color: theme.colors.primary.text }),
  bar: css({ height: 4, borderRadius: theme.shape.radius.pill, background: theme.colors.background.secondary, overflow: 'hidden' }),
  barFill: css({ height: '100%', background: theme.colors.primary.main, [theme.transitions.handleMotion('no-preference')]: { transition: 'width 200ms ease-out' } }),
  body: css({
    flex: 1, minHeight: 0, overflowY: 'auto', padding: theme.spacing(2), display: 'flex', flexDirection: 'column', gap: theme.spacing(1.5), overscrollBehavior: 'contain',
    p: { margin: 0 }, h3: { margin: 0 }, '&:focus': { outline: 'none' },
  }),
  terms: css({ margin: 0, display: 'flex', flexDirection: 'column', gap: theme.spacing(1.25) }),
  term: css({
    dt: { display: 'flex', alignItems: 'center', gap: theme.spacing(0.75), fontWeight: theme.typography.fontWeightMedium, color: theme.colors.text.primary },
    dd: { margin: 0, paddingLeft: theme.spacing(0.25) },
  }),
  termIcon: css({ color: theme.colors.text.secondary, flexShrink: 0 }),
  actions: css({ display: 'flex', flexWrap: 'wrap', gap: theme.spacing(1) }),
  environment: css({
    display: 'flex', alignItems: 'center', gap: theme.spacing(1.5), padding: theme.spacing(1, 1.5),
    border: `1px solid ${theme.colors.border.weak}`, borderRadius: theme.shape.radius.default, background: theme.colors.background.secondary,
    svg: { color: theme.colors.primary.text, flexShrink: 0 },
  }),
  command: css({ position: 'relative' }),
  pre: css({
    margin: 0, padding: theme.spacing(1, 1.5), paddingRight: theme.spacing(9), whiteSpace: 'pre-wrap', overflowWrap: 'anywhere',
    fontSize: theme.typography.bodySmall.fontSize, fontFamily: theme.typography.fontFamilyMonospace, background: theme.colors.background.canvas,
    border: `1px solid ${theme.colors.border.weak}`, borderRadius: theme.shape.radius.default, color: theme.colors.text.primary,
  }),
  copy: css({ position: 'absolute', top: theme.spacing(0.5), right: theme.spacing(0.5) }),
  mono: css({ fontFamily: theme.typography.fontFamilyMonospace, fontSize: theme.typography.bodySmall.fontSize, color: theme.colors.text.secondary, overflowWrap: 'anywhere' }),
  footer: css({ display: 'flex', alignItems: 'center', gap: theme.spacing(1), padding: theme.spacing(1.5, 2), borderTop: `1px solid ${theme.colors.border.weak}` }),
})

