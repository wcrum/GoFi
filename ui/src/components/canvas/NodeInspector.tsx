import { useEffect, useState, type ReactElement, type ReactNode } from 'react'
import { css } from '@emotion/css'
import type { GrafanaTheme2 } from '@grafana/data'
import { Checkbox, Combobox, ControlledCollapse, Field, IconButton, Input, RadioButtonGroup, Stack, TagsInput, Text, TextArea, useStyles2 } from '@grafana/ui'
import { getEndpoints, getMode, type DeployModeInfo, type DiscoveredEndpoint } from '../../api'
import {
  sourceTypeOptions,
  NETWORK_OPTIONS,
  framingOptions,
  parserOptions,
  sinkOptions,
  HTTP_METHOD_OPTIONS,
  httpAuthTypeOptions,
  bodyModeOptions,
  transformOpOptions,
} from './options'
import { t } from '../../i18n'
import { KeyValueEditor } from './KeyValueEditor'
import { SourceSettings, DEFAULT_FETCH, DEFAULT_SUBSCRIBE } from './SourceSettings'
import { DEFAULT_PYTHON_SOURCE } from './sources/pythonSource'
import { CodecListEditor } from './CodecListEditor'
import { ExposureSettings } from './ExposureSettings'
import { codecChain } from '../../lib/codec'
import { SinkTargetTools } from './SinkTargetTools'
import { TlsFields, hasTlsValues } from './TlsFields'
import { useLibraryFunctions, usePythonDefault, functionOptions, pythonValue, parsePythonValue } from './python-functions'
import { PythonStageEditor } from '../python/PythonStageEditor'
import { CelField } from '../cel/CelField'
import { OnErrorControl } from '../cel/OnErrorControl'
import type { DecodeNodeData, EncodeNodeData, FlowNode, FrameNodeData, IngressNodeData, ParseNodeData, StageNodeData, SinkNodeData } from './types'
import { encodingFormat } from './PipelineNodes'
import { pythonRefLabel } from '../../lib/python-ref'
import { ZenohSinkFields } from './sources/ZenohForm'
import { listenOf, sourceTypeOf, NODE_PORT_RANGE, type ServiceType, type Encoding, type ParseFormat, type ParseStageCfg, type Framing, type HTTPAuth, type ListenSource, type NATSAuth, type SinkCfg } from '../../types'

interface Props {
  node: FlowNode | null
  readOnly?: boolean
  onChangeIngress: (data: IngressNodeData) => void
  onChangeFrame: (data: FrameNodeData) => void
  onChangeDecode: (data: DecodeNodeData) => void
  onChangeParse: (data: ParseNodeData) => void
  onChangeStage: (data: StageNodeData) => void
  onChangeEncode: (data: EncodeNodeData) => void
  onChangeSink: (data: SinkNodeData) => void
  /** Every Encode node on the canvas, for a sink's Encoding picker. */
  encoders?: EncoderOption[]
  /** The Encode node the selected sink connects from, if any. */
  encoderId?: string
  /** A sink's choice: an Encode node id, `'new'` for a new one, `''` for none. */
  onSelectEncoder?: (choice: string) => void
  /** The sinks the selected Encode node feeds. */
  fedSinks?: FedSink[]
  onDisconnectSink?: (sinkId: string) => void
  onClose: () => void
}
export interface EncoderOption { id: string; label: string }
export interface FedSink { id: string; sink: SinkCfg; isDeadLetter?: boolean }

// Grafana form building blocks with the inspector's call shape. Every field,
// hint and collapse is a child of one gapped column (DetailSection), so the
// vertical rhythm is the same theme.spacing(2) everywhere instead of a mix of
// Field margins, wrapper divs and the Collapse's own margin.
function FormField({ label, description, children }: { label: ReactNode; description?: ReactNode; children: ReactElement<Record<string, unknown>> }) {
  return <Field label={label} description={description} noMargin>{children}</Field>
}
function Select({ value, onChange, options, disabled }: { value: string; onChange: (value: string) => void; options: Array<{ value: string; label: string }>; disabled?: boolean }) {
  return <Combobox value={value} options={options} disabled={disabled} onChange={(option) => onChange(option.value)} />
}
function DetailSection({ title, children }: { title: string; children: ReactNode }) {
  const styles = useStyles2(getStyles)
  return <section><Text element="h3" variant="h6">{title}</Text><div className={styles.sectionBody}>{children}</div></section>
}
function Hint({ children, tone = 'secondary' }: { children: NonNullable<ReactNode>; tone?: 'secondary' | 'warning' }) {
  return <Text element="p" variant="bodySmall" color={tone}>{children}</Text>
}
/** A collapse that takes its spacing from the surrounding column. */
function Collapse({ label, isOpen, children }: { label: string; isOpen: boolean; children: ReactNode }) {
  const styles = useStyles2(getStyles)
  return <ControlledCollapse label={label} isOpen={isOpen} className={styles.collapse}>{children}</ControlledCollapse>
}

export function NodeInspector({ node, readOnly, onChangeIngress, onChangeFrame, onChangeDecode, onChangeParse, onChangeStage, onChangeEncode, onChangeSink, encoders = [], encoderId, onSelectEncoder, fedSinks = [], onDisconnectSink, onClose }: Props) {
  const styles = useStyles2(getStyles)
  if (!node) return null

  return (
    <aside className={styles.panel} aria-label={t('gofi.canvas.node-inspector.inspector-aria', '{{title}} inspector', { title: inspectorTitle(node) })}>
      <div className={styles.header}>
        <Text element="h2" variant="h5">{inspectorTitle(node)}</Text>
        <IconButton name="times" tooltip={t('gofi.canvas.node-inspector.close-inspector', 'Close inspector')} onClick={onClose} />
      </div>
      <div className={styles.body}>
        {node.type === 'ingress' && (
          <IngressInspector data={node.data} readOnly={readOnly} onChange={onChangeIngress} />
        )}
        {node.type === 'frame' && <FrameInspector data={node.data} readOnly={readOnly} onChange={onChangeFrame} />}
        {node.type === 'decode' && <DecodeInspector data={node.data} readOnly={readOnly} onChange={onChangeDecode} />}
        {node.type === 'parse' && <ParseInspector data={node.data} readOnly={readOnly} onChange={onChangeParse} />}
        {node.type === 'stage' && (
          <StageInspector data={node.data} readOnly={readOnly} onChange={onChangeStage} />
        )}
        {node.type === 'encode' && <EncodeInspector data={node.data} readOnly={readOnly} onChange={onChangeEncode} fedSinks={fedSinks} onDisconnectSink={onDisconnectSink} />}
        {node.type === 'sink' && (
          <SinkInspector data={node.data} readOnly={readOnly} onChange={onChangeSink} encoders={encoders} encoderId={encoderId} onSelectEncoder={onSelectEncoder} />
        )}
      </div>
    </aside>
  )
}

function inspectorTitle(node: FlowNode): string {
  switch (node.type) {
    case 'ingress':
      return t('gofi.canvas.node-inspector.title-source', 'Source')
    case 'frame':
      return t('gofi.canvas.node-inspector.title-frame', 'Frame')
    case 'decode':
      return t('gofi.canvas.node-inspector.title-decode', 'Decode')
    case 'parse':
      return t('gofi.canvas.node-inspector.title-parse', 'Parse')
    case 'stage':
      return t('gofi.canvas.node-inspector.title-stage', 'Stage')
    case 'encode':
      return t('gofi.canvas.node-inspector.title-encode', 'Encode')
    case 'sink':
      return node.data.isDeadLetter ? t('gofi.canvas.node-inspector.title-dead-letter-sink', 'Dead Letter Sink') : t('gofi.canvas.node-inspector.title-sink', 'Sink')
    default:
      return t('gofi.canvas.node-inspector.title-node', 'Node')
  }
}

function IngressInspector({
  data,
  readOnly,
  onChange,
}: {
  data: IngressNodeData
  readOnly?: boolean
  onChange: (data: IngressNodeData) => void
}) {
  const [deployment, setDeployment] = useState<DeployModeInfo | null>(null)
  useEffect(() => { getMode().then(setDeployment).catch(() => {}) }, [])
  const isKubernetes = deployment?.driverKind === 'kubernetes' || deployment?.mode === 'kubernetes'

  const listen = listenOf(data)
  const setListen = (next: ListenSource) => onChange({ ...data, source: { listen: next } })
  const exposure = listen.service?.type ?? ''
  const portDescription = !isKubernetes ? t('gofi.canvas.node-inspector.port-description', 'Senders connect to this port.')
    : exposure === '' ? t('gofi.canvas.node-inspector.port-description-internal', 'Senders inside the cluster connect to the flow Pod on this port; no Service is created.')
    : exposure === 'NodePort' ? t('gofi.canvas.node-inspector.port-description-node-port', 'In-cluster Service port. Senders outside the cluster use the NodePort.')
    : exposure === 'LoadBalancer' ? t('gofi.canvas.node-inspector.port-description-load-balancer', 'Port on the load balancer address (and the in-cluster Service).')
    : exposure === 'Gateway' ? t('gofi.canvas.node-inspector.port-description-gateway', 'Port the Gateway forwards to (the in-cluster Service).')
    : t('gofi.canvas.node-inspector.port-description-service', 'Senders inside the cluster connect to the Service on this port.')

  const sourceType = sourceTypeOf(data.source)
  const sourceTypeField = <FormField label={t('gofi.canvas.node-inspector.source-type', 'Source type')}>
    <RadioButtonGroup
      disabled={readOnly}
      value={sourceType}
      onChange={(value) => onChange({ ...data, source: value === 'fetch' ? { fetch: data.source.fetch ?? DEFAULT_FETCH } : value === 'subscribe' ? { subscribe: data.source.subscribe ?? DEFAULT_SUBSCRIBE.nats } : value === 'python' ? { python: data.source.python ?? DEFAULT_PYTHON_SOURCE } : { listen } })}
      options={sourceTypeOptions()}
    />
  </FormField>
  if (sourceType !== 'listen') {
    return <DetailSection title={t('gofi.canvas.node-inspector.title-source', 'Source')}>
      {sourceTypeField}
      <SourceSettings source={data.source} readOnly={readOnly} onChange={(source) => onChange({ ...data, source })} />
    </DetailSection>
  }

  return (
    <DetailSection title={t('gofi.canvas.node-inspector.title-source', 'Source')}>
      {sourceTypeField}
      <Hint>{t('gofi.canvas.node-inspector.listen-hint', 'Listen: senders connect to a GoFi TCP/UDP port.')}</Hint>

      <FormField label={t('gofi.common.port', 'Port')} description={portDescription}>
        <Input
          type="number"
          min={1}
          max={65535}
          disabled={readOnly}
          value={(listen.service?.port || listen.port) || ''}
          // One port: the Service port is also the Pod's listen port.
          onChange={(e) => setListen({ ...listen, port: parseInt(e.currentTarget.value, 10) || 0, service: listen.service ? { ...listen.service, port: undefined } : undefined })}
          placeholder="9000"
        />
      </FormField>

      <FormField label={t('gofi.canvas.node-inspector.network', 'Network')}>
        <RadioButtonGroup
          disabled={readOnly}
          value={listen.network || 'tcp'}
          onChange={(v) => setListen({ ...listen, network: v, tls: v === 'quic' ? listen.tls : undefined })}
          options={NETWORK_OPTIONS}
        />
      </FormField>
      {listen.network === 'quic' && <>
        <Hint>{t('gofi.canvas.node-inspector.quic-hint', 'QUIC runs over UDP on this port (published as UDP everywhere) and always uses TLS: give the listener a certificate and key.')}</Hint>
        <TlsFields tls={listen.tls} role="listen" readOnly={readOnly} onChange={(tls) => setListen({ ...listen, tls })} />
      </>}
      {isKubernetes && <ExposureSettings listen={listen} onChange={setListen} readOnly={readOnly} deployment={deployment} />}
      {!isKubernetes && <Hint>{deployment?.mode === 'binary' ? t('gofi.canvas.node-inspector.binary-mode-hint', 'In binary mode the flow binds a local TCP/UDP port.') : deployment ? t('gofi.canvas.node-inspector.container-mode-hint', 'Container flows bind a TCP/UDP port. Publish it through your container runtime.') : t('gofi.canvas.node-inspector.checking-deployment-mode', 'Checking deployment mode…')}</Hint>}

    </DetailSection>
  )
}

function FrameInspector({ data, readOnly, onChange }: { data: FrameNodeData; readOnly?: boolean; onChange: (data: FrameNodeData) => void }) {
  const { framing } = data
  const { functions } = useLibraryFunctions()
  const defaultLibrary = usePythonDefault()
  const set = (next: Partial<Framing>) => onChange({ ...data, framing: { ...framing, ...next } })
  const framers = functionOptions(functions, 'framer', framing.mode === 'python' ? framing.python : undefined, defaultLibrary)
  return (
    <DetailSection title={t('gofi.canvas.node-inspector.title-frame', 'Frame')}>
      <Hint>{t('gofi.canvas.node-inspector.frame-hint', 'How the incoming byte stream becomes discrete frames. UDP datagrams are independent frames; line and delimiter framing can still split a datagram.')}</Hint>
      <FormField label={t('gofi.canvas.node-inspector.framing', 'Framing')} description={framers.length ? undefined : t('gofi.canvas.node-inspector.python-framers-hint', 'Python framers (@gofi.framer) appear here once a library registering one is launched.')}><Select disabled={readOnly} value={framing.mode === 'python' ? pythonValue(framing.python, defaultLibrary) : framing.mode} onChange={(value) => value.startsWith('python:') ? set({ mode: 'python', python: parsePythonValue(value, defaultLibrary) }) : set({ mode: value, python: undefined })} options={[...framingOptions(), ...framers]} /></FormField>
      {framing.mode === 'python' && <Hint>{t('gofi.canvas.node-inspector.python-framer-hint', 'The framer is called after each read with the buffered bytes and returns complete frames plus the remainder; at the end of the stream it is called once more with final=True.')}</Hint>}
      {framing.mode === 'delimited' && <FormField label={t('gofi.canvas.node-inspector.delimiter', 'Delimiter')}><Input disabled={readOnly} value={framing.delimiter ?? ''} onChange={(e) => set({ delimiter: e.currentTarget.value })} placeholder="</event>" /></FormField>}
      {framing.mode === 'length-prefix' && <FormField label={t('gofi.canvas.node-inspector.prefix-bytes', 'Prefix bytes')}><RadioButtonGroup disabled={readOnly} value={String(framing.prefixLen ?? 4)} options={[{ value: '1', label: t('gofi.canvas.node-inspector.prefix-1-byte', '1 byte') }, { value: '2', label: t('gofi.canvas.node-inspector.prefix-2-bytes', '2 bytes') }, { value: '4', label: t('gofi.canvas.node-inspector.prefix-4-bytes', '4 bytes') }]} onChange={(value) => set({ prefixLen: Number(value) })} /></FormField>}
      <FormField label={t('gofi.canvas.node-inspector.max-frame-size', 'Maximum frame size (bytes)')}><Input type="number" min={1} disabled={readOnly} value={framing.maxBytes ?? ''} placeholder={t('gofi.canvas.node-inspector.default-placeholder', 'Default')} onChange={(e) => set({ maxBytes: Number(e.currentTarget.value) || undefined })} /></FormField>
    </DetailSection>
  )
}

function DecodeInspector({ data, readOnly, onChange }: { data: DecodeNodeData; readOnly?: boolean; onChange: (data: DecodeNodeData) => void }) {
  return (
    <DetailSection title={t('gofi.canvas.node-inspector.title-decode', 'Decode')}>
      <Hint>{t('gofi.canvas.node-inspector.decode-hint', 'Decoders run top to bottom before parsing. For base64-encoded gzip data, add base64 first, then gzip. Python decoders (@gofi.decoder) can unwrap or decrypt. The raw frame is kept for inspection and replay.')}</Hint>
      <CodecListEditor role="decoder" steps={data.decoders} readOnly={readOnly} emptyText={t('gofi.canvas.node-inspector.no-decoders', 'No decoders: frames reach the parser unchanged.')} onChange={(decoders) => onChange({ ...data, decoders })} />
      <FormField label={t('gofi.canvas.node-inspector.decoded-size-limit', 'Decoded size limit (bytes)')} description={t('gofi.canvas.node-inspector.decoded-size-limit-description', "Each decoding step's output is capped at this size.")}><Input type="number" min={1} disabled={readOnly} value={data.maxDecodedBytes ?? ''} placeholder="1048576" onChange={(event) => onChange({ ...data, maxDecodedBytes: Number(event.currentTarget.value) || undefined })} /></FormField>
    </DetailSection>
  )
}

function ParseInspector({ data, readOnly, onChange }: { data: ParseNodeData; readOnly?: boolean; onChange: (data: ParseNodeData) => void }) {
  const { parser } = data
  const { functions, error: registryError } = useLibraryFunctions()
  const defaultLibrary = usePythonDefault()
  return (
    <DetailSection title={t('gofi.canvas.node-inspector.title-parse', 'Parse')}>
      <Hint>{t('gofi.canvas.node-inspector.parse-hint', 'How a decoded frame becomes structured fields for stages and destinations. To parse a field again later (for example a JSON string inside an XML event), add a Parse field stage.')}</Hint>
      <FormField label={t('gofi.canvas.node-inspector.format', 'Format')}>
        <Select
          disabled={readOnly}
          value={parser.format === 'python' ? pythonValue(parser.python, defaultLibrary) : parser.format}
          onChange={(v) => onChange({ ...data, parser: { ...parser, format: v.startsWith('python:') ? 'python' : v, python: parsePythonValue(v, defaultLibrary) } })}
          options={[...parserOptions(), ...(parser.format === 'python' && !parser.python ? [{ value: 'python', label: t('gofi.canvas.node-inspector.choose-python-parser', 'Choose a Python parser…') }] : []), ...functionOptions(functions, 'parser', parser.python, defaultLibrary)]}
        />
      </FormField>
      {registryError && <Hint tone="warning">{t('gofi.canvas.registry-unavailable', 'Python function registry unavailable.')}</Hint>}
      <FormField label={t('gofi.canvas.node-inspector.decode-parse-errors', 'Decode / parse errors')}><RadioButtonGroup disabled={readOnly} value={parser.errorPolicy ?? 'continue'} onChange={(value) => onChange({ ...data, parser: { ...parser, errorPolicy: value as 'continue' | 'drop' } })} options={[{ value: 'continue', label: t('gofi.canvas.node-inspector.error-continue', 'Continue'), description: t('gofi.canvas.node-inspector.error-continue-description', 'Continue and record the parse error') }, { value: 'drop', label: t('gofi.canvas.node-inspector.error-drop', 'Drop'), description: t('gofi.canvas.node-inspector.error-drop-description', 'Drop and send to the dead-letter sink') }]} /></FormField>
    </DetailSection>
  )
}

const parseFormatOptions = (): Array<{ value: ParseFormat; label: string; description: string }> => [
  { value: 'json', label: 'JSON', description: t('gofi.canvas.node-inspector.parse-format-json-description', 'A JSON object or value') },
  { value: 'xml', label: 'XML', description: t('gofi.canvas.node-inspector.parse-format-xml-description', 'An XML document') },
  { value: 'text', label: t('gofi.canvas.node-inspector.parse-format-text', 'Text'), description: t('gofi.canvas.node-inspector.parse-format-text-description', 'The value as a string') },
  { value: 'python', label: 'Python', description: t('gofi.canvas.node-inspector.parse-format-python-description', 'A @gofi.parser function') },
]

/** A `parse` stage: parse a field (or the raw frame) again, anywhere in the
 * stage chain, optionally decoding it first. */
function ParseStageInspector({ value, readOnly, onChange }: { value: ParseStageCfg; readOnly?: boolean; onChange: (value: ParseStageCfg) => void }) {
  const { functions, error: registryError } = useLibraryFunctions()
  const defaultLibrary = usePythonDefault()
  const set = (next: Partial<ParseStageCfg>) => onChange({ ...value, ...next })
  const parsers = functionOptions(functions, 'parser', value.python, defaultLibrary)
  const from = (value.from ?? '').trim()
  const target = value.into?.trim() ? `fields["${value.into.trim()}"]` : t('gofi.canvas.node-inspector.top-level-fields', 'the top-level fields')
  const replace = value.mode === 'replace'
  return (
    <DetailSection title={t('gofi.canvas.node-inspector.title-parse-field', 'Parse field')}>
      <Hint>{t('gofi.canvas.node-inspector.parse-field-hint', 'Parses a value into fields mid-pipeline, for example a JSON string carried inside an already-parsed message.')}</Hint>
      <FormField label={t('gofi.canvas.node-inspector.parse-from', 'From')} description={from && from !== 'raw' ? t('gofi.canvas.node-inspector.parse-from-cel-description', 'CEL expression for the value to parse.') : t('gofi.canvas.node-inspector.parse-from-raw-description', "Empty parses the raw frame (after the flow's decoders).")}>
        <CelField aria-label={t('gofi.canvas.node-inspector.parse-from-aria', 'Parse from')} disabled={readOnly} resultType="any" value={value.from ?? ''} placeholder='fields["message"]' onChange={(next) => set({ from: next })} />
      </FormField>
      <Collapse label={value.decoders?.length ? t('gofi.canvas.node-inspector.decoders-with-chain', 'Decoders ({{chain}})', { chain: codecChain(value.decoders, defaultLibrary) }) : t('gofi.canvas.node-inspector.decoders', 'Decoders')} isOpen={!!value.decoders?.length}>
        <CodecListEditor role="decoder" steps={value.decoders ?? []} readOnly={readOnly} emptyText={t('gofi.canvas.node-inspector.no-decoders-parse-field', 'No decoders: the value is parsed as is.')} onChange={(decoders) => set({ decoders: decoders.length ? decoders : undefined })} />
      </Collapse>
      <FormField label={t('gofi.canvas.node-inspector.format', 'Format')}>
        <RadioButtonGroup disabled={readOnly} value={value.format} options={parseFormatOptions()} onChange={(format) => set({ format, python: format === 'python' ? value.python : undefined })} />
      </FormField>
      {value.format === 'python' && <FormField label={t('gofi.canvas.node-inspector.python-parser', 'Python parser')} description={parsers.length ? undefined : t('gofi.canvas.node-inspector.python-parsers-hint', 'Python parsers (@gofi.parser) appear here once a library registering one is launched.')}>
        <Combobox disabled={readOnly} value={value.python ? pythonValue(value.python, defaultLibrary) : null} placeholder={t('gofi.canvas.node-inspector.choose-python-parser', 'Choose a Python parser…')} options={parsers}
          onChange={(option) => option && set({ python: parsePythonValue(option.value, defaultLibrary) })} />
      </FormField>}
      {value.format === 'python' && registryError && <Hint tone="warning">{t('gofi.canvas.registry-unavailable', 'Python function registry unavailable.')}</Hint>}
      <FormField label={t('gofi.canvas.node-inspector.mode', 'Mode')} description={replace ? t('gofi.canvas.node-inspector.mode-replace-description', 'The parsed result becomes the message fields.') : t('gofi.canvas.node-inspector.mode-merge-description', 'Parsed fields are added to {{target}}; other fields are kept.', { target })}>
        <RadioButtonGroup disabled={readOnly} value={replace ? 'replace' : 'merge'} onChange={(mode) => set(mode === 'replace' ? { mode: 'replace', into: undefined } : { mode: undefined })}
          options={[{ value: 'merge', label: t('gofi.canvas.node-inspector.mode-merge', 'Merge'), description: t('gofi.canvas.node-inspector.mode-merge-option-description', 'Add the parsed fields, keeping the others') }, { value: 'replace', label: t('gofi.canvas.node-inspector.mode-replace', 'Replace'), description: t('gofi.canvas.node-inspector.mode-replace-option-description', 'The parsed result replaces all fields') }]} />
      </FormField>
      {!replace && <FormField label={t('gofi.canvas.node-inspector.into', 'Into')} description={t('gofi.canvas.node-inspector.into-description', 'Field to hold the result. Empty merges at the top level.')}>
        <Input disabled={readOnly} value={value.into ?? ''} placeholder={t('gofi.canvas.node-inspector.top-level-placeholder', 'Top level')} onChange={(event) => set({ into: event.currentTarget.value || undefined })} />
      </FormField>}
      <FormField label={t('gofi.canvas.node-inspector.on-error', 'On error')} description={value.onError === 'drop' ? t('gofi.canvas.node-inspector.on-error-drop-description', 'A value that fails to decode or parse drops the message (and sends it to the dead-letter sink, if configured).') : t('gofi.canvas.node-inspector.on-error-continue-description', 'A value that fails to decode or parse leaves the message unchanged.')}>
        <RadioButtonGroup size="sm" disabled={readOnly} value={value.onError ?? 'continue'} onChange={(onError) => set({ onError: onError === 'drop' ? 'drop' : undefined })}
          options={[{ value: 'continue', label: t('gofi.canvas.node-inspector.error-continue', 'Continue') }, { value: 'drop', label: t('gofi.canvas.node-inspector.error-drop', 'Drop') }]} />
      </FormField>
    </DetailSection>
  )
}

function StageInspector({
  data,
  readOnly,
  onChange,
}: {
  data: StageNodeData
  readOnly?: boolean
  onChange: (data: StageNodeData) => void
}) {
  const { spec } = data
  const { functions, error: registryError } = useLibraryFunctions()

  switch (spec.kind) {
    case 'parse':
      return <ParseStageInspector value={spec.parse ?? { format: 'json' }} readOnly={readOnly} onChange={(parse) => onChange({ ...data, spec: { ...spec, parse } })} />
    case 'python':
      return <DetailSection title={t('gofi.canvas.node-inspector.title-python-transformer', 'Python Transformer')}><PythonStageEditor stageId={spec.id} value={spec.python} functions={functions} registryError={registryError} readOnly={readOnly} onChange={python => onChange({ ...data, spec: { ...spec, python } })} /></DetailSection>
    case 'filter':
      return (
        <DetailSection title={t('gofi.canvas.node-inspector.title-filter', 'Filter')}>
          <FormField label={t('gofi.canvas.node-inspector.expression-language', 'Expression language')}><RadioButtonGroup disabled={readOnly} value={spec.type ?? 'cel'} onChange={(value) => onChange({ ...data, spec: { ...spec, type: value } })} options={[{ value: 'cel', label: 'CEL', description: t('gofi.canvas.node-inspector.cel-description', 'Common Expression Language') }, { value: 'regex', label: t('gofi.canvas.node-inspector.regex', 'Regex'), description: t('gofi.canvas.node-inspector.regex-description', 'Regular expression (RE2)') }]} /></FormField>
          {spec.type === 'regex'
            ? <FormField label={t('gofi.canvas.node-inspector.regular-expression', 'Regular expression')}><Input disabled={readOnly} value={spec.expression ?? ''} onChange={(event) => onChange({ ...data, spec: { ...spec, expression: event.currentTarget.value, expressionRef: undefined, onError: undefined } })} placeholder="^event-" /></FormField>
            : <><FormField label={t('gofi.canvas.node-inspector.cel-expression', 'CEL expression')}><CelField aria-label={t('gofi.canvas.node-inspector.cel-expression', 'CEL expression')} disabled={readOnly} resultType="bool" value={spec.expression ?? ''} onChange={(expression) => onChange({ ...data, spec: { ...spec, expression } })} placeholder='fields["type"] == "a-f-G"' libraryRef={spec.expressionRef} onLibraryRefChange={(expressionRef) => onChange({ ...data, spec: { ...spec, expressionRef } })} /></FormField>
              <OnErrorControl disabled={readOnly} value={spec.onError} onChange={(onError) => onChange({ ...data, spec: { ...spec, onError } })} /></>}
          {spec.type === 'regex' && <FormField label={t('gofi.canvas.node-inspector.regex-field', 'Field (blank uses original raw frame)')}><Input disabled={readOnly} value={spec.field ?? ''} placeholder={'type' /* i18n-ignore: example identifier */} onChange={(event) => onChange({ ...data, spec: { ...spec, field: event.currentTarget.value } })} /></FormField>}
          <Hint>{spec.type === 'regex' ? t('gofi.canvas.node-inspector.regex-hint', 'Matching messages continue. Use a parsed string field or match the original frame.') : t('gofi.canvas.node-inspector.cel-filter-hint', 'A true result keeps the message. Use fields for parsed data, meta for metadata, and raw for the original frame.')} {t('gofi.canvas.node-inspector.test-before-deploying', 'Test an example in Test flow before deploying.')}</Hint>
        </DetailSection>
      )

    case 'transform': {
      const op = spec.transformOp ?? 'set'
      const fieldLabel = op === 'rename' ? t('gofi.canvas.node-inspector.source-field', 'Source Field') : t('gofi.canvas.node-inspector.field', 'Field')
      return (
        <DetailSection title={t('gofi.canvas.node-inspector.title-transform', 'Transform')}>
          <FormField label={t('gofi.canvas.node-inspector.operation', 'Operation')}>
            <RadioButtonGroup
              disabled={readOnly}
              value={op}
              onChange={(v) => onChange({ ...data, spec: { ...spec, transformOp: v as 'set' | 'rename' | 'remove' } })}
              options={transformOpOptions()}
            />
          </FormField>

          <FormField label={fieldLabel}>
            <Input
              disabled={readOnly}
              value={spec.field ?? ''}
              onChange={(e) => onChange({ ...data, spec: { ...spec, field: e.currentTarget.value } })}
              placeholder="fieldName"
            />
          </FormField>

          {op === 'rename' && (
            <FormField label={t('gofi.canvas.node-inspector.new-field', 'New Field')}>
              <Input
                disabled={readOnly}
                value={spec.newField ?? ''}
                onChange={(e) => onChange({ ...data, spec: { ...spec, newField: e.currentTarget.value } })}
                placeholder="newFieldName"
              />
            </FormField>
          )}

          {op === 'set' && (
            <>
              <FormField label={t('gofi.canvas.node-inspector.value-expression', 'Value Expression')}>
                <CelField
                  aria-label={t('gofi.canvas.node-inspector.value-expression-aria', 'Value expression')}
                  disabled={readOnly}
                  resultType="any"
                  value={spec.valueExpr ?? ''}
                  onChange={(valueExpr) => onChange({ ...data, spec: { ...spec, valueExpr } })}
                  placeholder='fields["a"] + fields["b"]'
                  libraryRef={spec.expressionRef}
                  onLibraryRefChange={(expressionRef) => onChange({ ...data, spec: { ...spec, expressionRef } })}
                />
              </FormField>
              <OnErrorControl disabled={readOnly} value={spec.onError} onChange={(onError) => onChange({ ...data, spec: { ...spec, onError } })} />
            </>
          )}
        </DetailSection>
      )
    }

    case 'rate-limit':
      return (
        <DetailSection title={t('gofi.canvas.node-inspector.title-rate-limit', 'Rate Limit')}>
          <FormField label={t('gofi.canvas.node-inspector.max-per-second', 'Max Per Second')}>
            <Input
              type="number"
              disabled={readOnly}
              value={spec.maxPerSecond ?? ''}
              onChange={(e) => onChange({ ...data, spec: { ...spec, maxPerSecond: parseInt(e.currentTarget.value, 10) || 0 } })}
              placeholder="10"
            />
          </FormField>
        </DetailSection>
      )

    case 'dedup':
      return (
        <DetailSection title={t('gofi.canvas.node-inspector.title-dedup', 'Deduplicate')}>
          <FormField label={t('gofi.canvas.node-inspector.key-expression', 'Key Expression')}>
            <CelField
              aria-label={t('gofi.canvas.node-inspector.key-expression-aria', 'Key expression')}
              disabled={readOnly}
              resultType="any"
              value={spec.keyExpr ?? ''}
              onChange={(keyExpr) => onChange({ ...data, spec: { ...spec, keyExpr } })}
              placeholder='fields["id"]'
              libraryRef={spec.expressionRef}
              onLibraryRefChange={(expressionRef) => onChange({ ...data, spec: { ...spec, expressionRef } })}
            />
          </FormField>
          <OnErrorControl disabled={readOnly} value={spec.onError} onChange={(onError) => onChange({ ...data, spec: { ...spec, onError } })} />

          <FormField label={t('gofi.canvas.node-inspector.window', 'Window')}>
            <Input
              disabled={readOnly}
              value={spec.window ?? ''}
              onChange={(e) => onChange({ ...data, spec: { ...spec, window: e.currentTarget.value } })}
              placeholder="30s"
            />
          </FormField>
        </DetailSection>
      )

    case 'schema':
      return (
        <DetailSection title={t('gofi.canvas.node-inspector.title-schema', 'Schema Validator')}>
          <FormField label={t('gofi.canvas.node-inspector.required-fields', 'Required Fields')}>
            <TagsInput
              disabled={readOnly}
              tags={spec.requiredFields ?? []}
              onChange={(requiredFields) => onChange({ ...data, spec: { ...spec, requiredFields } })}
              placeholder={t('gofi.canvas.node-inspector.add-field-name', 'Add a field name...')}
            />
          </FormField>
        </DetailSection>
      )

    default:
      return (
        <DetailSection title={t('gofi.canvas.node-inspector.title-stage', 'Stage')}>
          <Text color="secondary">{t('gofi.canvas.node-inspector.unknown-stage-kind', 'Unknown stage kind "{{kind}}"', { kind: spec.kind })}</Text>
        </DetailSection>
      )
  }
}

function SinkInspector({
  data,
  readOnly,
  onChange,
  encoders,
  encoderId,
  onSelectEncoder,
}: {
  data: SinkNodeData
  readOnly?: boolean
  onChange: (data: SinkNodeData) => void
  encoders: EncoderOption[]
  encoderId?: string
  onSelectEncoder?: (choice: string) => void
}) {
  const { sink } = data
  const { functions, error: registryError } = useLibraryFunctions()
  const defaultLibrary = usePythonDefault()
  const [endpoints, setEndpoints] = useState<DiscoveredEndpoint[]>([])
  useEffect(() => { if (!readOnly) getEndpoints().then(setEndpoints).catch(() => {}) }, [readOnly])

  return (
    <DetailSection title={t('gofi.canvas.node-inspector.title-sink', 'Sink')}>
      <FormField label={t('gofi.canvas.node-inspector.sink-type', 'Sink Type')}>
        <Select
          disabled={readOnly}
          value={sink.type === 'python' ? pythonValue(sink.python, defaultLibrary) : sink.type}
          onChange={(v) => onChange({ ...data, sink: v === 'loki' ? { type: 'loki', url: 'http://loki:3100', bodyMode: 'wrapped', when: sink.when, whenRef: sink.whenRef, encoding: sink.encoding } : v === 'zenoh' ? { type: 'zenoh', url: 'http://zenoh:8000', subject: '', contentType: 'application/octet-stream', bodyMode: 'raw', when: sink.when, whenRef: sink.whenRef, encoding: sink.encoding } : v.startsWith('python:') ? { type: 'python', python: parsePythonValue(v, defaultLibrary), when: sink.when, whenRef: sink.whenRef, encoding: sink.encoding } : { ...sink, type: v, python: undefined, kubernetes: ['tcp', 'udp', 'quic'].includes(v) ? sink.kubernetes : undefined, tls: v === 'quic' ? sink.tls : undefined } })}
          options={[...sinkOptions(), ...(sink.type === 'python' && !sink.python ? [{ value: 'python', label: t('gofi.canvas.node-inspector.choose-python-sink', 'Choose a Python sink…') }] : []), ...functionOptions(functions, 'sink', sink.python, defaultLibrary)]}
        />
      </FormField>

      {sink.type === 'python' && <Hint>{t('gofi.canvas.node-inspector.python-sink-hint', "This sink executes in GoFi's separate Python worker.")}</Hint>}
      {registryError && <Hint tone="warning">{t('gofi.canvas.registry-unavailable', 'Python function registry unavailable.')}</Hint>}
      {!data.isDeadLetter && (
        <FormField label={t('gofi.canvas.node-inspector.send-when', 'Send When (optional)')}>
          <CelField
            aria-label={t('gofi.canvas.node-inspector.send-when-aria', 'Send when')}
            disabled={readOnly}
            resultType="bool"
            value={sink.when ?? ''}
            onChange={(when) => onChange({ ...data, sink: { ...sink, when } })}
            placeholder={t('gofi.canvas.node-inspector.send-when-placeholder', 'fields["severity"] == "critical"  (blank = always send)')}
            libraryRef={sink.whenRef}
            onLibraryRefChange={(whenRef) => onChange({ ...data, sink: { ...sink, whenRef } })}
          />
        </FormField>
      )}

      {(['nats', 'http', 'tcp', 'udp', 'quic', 'aster', 'zenoh', 'loki'].includes(sink.type) && !sink.kubernetes) && (
        <FormField label={sink.type === 'zenoh' ? t('gofi.canvas.node-inspector.zenoh-url', 'Zenoh REST router URL') : sink.type === 'loki' ? t('gofi.canvas.node-inspector.loki-url', 'Loki URL') : t('gofi.canvas.node-inspector.url', 'URL')}>
          <Input
            disabled={readOnly}
            value={sink.url ?? ''}
            onChange={(e) => onChange({ ...data, sink: { ...sink, url: e.currentTarget.value } })}
            placeholder={sink.type === 'nats' ? 'nats://broker:4222' : sink.type === 'tcp' ? 'tcp://application:9000' : sink.type === 'udp' ? 'udp://application:9000' : sink.type === 'quic' ? 'quic://application:4433' : sink.type === 'zenoh' ? 'http://zenoh:8000' : sink.type === 'aster' ? 'http://127.0.0.1:8181' : 'http://application:8080'}
          />
        </FormField>
      )}

      {!sink.kubernetes && ['tcp', 'udp', 'quic', 'http', 'nats'].includes(sink.type) && endpoints.length > 0 && !readOnly && <FormField label={t('gofi.canvas.node-inspector.discover-destination', 'Discover a destination')}><Select value="" options={[{ value: '', label: t('gofi.canvas.node-inspector.choose-endpoint', 'Choose an endpoint…') }, ...endpoints.flatMap((endpoint) => endpoint.ports.filter((port) => ['udp', 'quic'].includes(sink.type) ? port.protocol === 'UDP' : port.protocol === 'TCP').map((port) => ({ value: `${sink.type}://${endpoint.host}:${port.port}`, label: `${endpoint.name}${endpoint.namespace ? ` (${endpoint.namespace})` : ''} :${port.port}` })))]} onChange={(value) => { if (value) onChange({ ...data, sink: { ...sink, url: value } }) }} /></FormField>}
      {['tcp', 'udp', 'quic'].includes(sink.type) && <SinkTargetTools sink={sink} readOnly={readOnly} onChange={value => onChange({ ...data, sink: value })} />}
      {sink.type === 'quic' && <Collapse label={t('gofi.canvas.node-inspector.tls-optional', 'TLS (optional)')} isOpen={hasTlsValues(sink.tls)}>
        <TlsFields tls={sink.tls} role="sink" readOnly={readOnly} onChange={(tls) => onChange({ ...data, sink: { ...sink, tls } })} />
      </Collapse>}
      {['tcp', 'udp'].includes(sink.type) && <FormField label={t('gofi.canvas.node-inspector.delimiter-appended', 'Delimiter appended to each message')} description={t('gofi.canvas.node-inspector.delimiter-appended-description', 'Use a direct destination address or select matching Kubernetes pods. The delimiter is appended to each forwarded message.')}><Input disabled={readOnly} value={sink.delimiter ?? ''} placeholder={t('gofi.canvas.node-inspector.optional-delimiter', 'Optional delimiter')} onChange={(event) => onChange({ ...data, sink: { ...sink, delimiter: event.currentTarget.value } })} /></FormField>}

      {sink.type === 'zenoh' && <ZenohSinkFields sink={sink} readOnly={readOnly} onChange={(value) => onChange({ ...data, sink: value })} />}
      {sink.type === 'aster' && <>
        <Hint>{t('gofi.canvas.node-inspector.aster-hint', "Send through an Aster agent in this workload's network namespace. Its API must stay on loopback.")}</Hint>
        <FormField label={t('gofi.canvas.node-inspector.aster-topic', 'Topic')}><Input disabled={readOnly} value={sink.aster?.topic ?? ''} onChange={(event) => onChange({ ...data, sink: { ...sink, aster: { scope: '', ...sink.aster, topic: event.currentTarget.value } } })} /></FormField>
        <FormField label={t('gofi.canvas.node-inspector.aster-scope', 'Scope')}><Input disabled={readOnly} value={sink.aster?.scope ?? ''} onChange={(event) => onChange({ ...data, sink: { ...sink, aster: { topic: '', ...sink.aster, scope: event.currentTarget.value } } })} /></FormField>
        <FormField label={t('gofi.canvas.node-inspector.aster-priority', 'Priority')}><RadioButtonGroup disabled={readOnly} value={sink.aster?.priority ?? 'routine'} options={['routine', 'priority', 'immediate', 'flash'].map((value) => ({ value, label: value }))} onChange={(value) => onChange({ ...data, sink: { ...sink, aster: { topic: '', scope: '', ...sink.aster, priority: value as 'routine' | 'priority' | 'immediate' | 'flash' } } })} /></FormField>
        <FormField label={t('gofi.canvas.node-inspector.aster-logical-key', 'Logical key')}><Input disabled={readOnly} value={sink.aster?.logicalKey ?? ''} onChange={(event) => onChange({ ...data, sink: { ...sink, aster: { topic: '', scope: '', ...sink.aster, logicalKey: event.currentTarget.value, logicalKeyField: undefined } } })} /></FormField>
        <FormField label={t('gofi.canvas.node-inspector.aster-logical-key-field', 'Logical key field (instead of static key)')}><Input disabled={readOnly} value={sink.aster?.logicalKeyField ?? ''} onChange={(event) => onChange({ ...data, sink: { ...sink, aster: { topic: '', scope: '', ...sink.aster, logicalKeyField: event.currentTarget.value, logicalKey: undefined } } })} /></FormField>
        <FormField label={t('gofi.canvas.node-inspector.aster-token-file', 'Token file path')}><Input disabled={readOnly} value={sink.aster?.tokenFile ?? ''} placeholder={t('gofi.canvas.node-inspector.aster-token-file-placeholder', 'Uses GOFI_ASTER_TOKEN_FILE when empty')} onChange={(event) => onChange({ ...data, sink: { ...sink, aster: { topic: '', scope: '', ...sink.aster, tokenFile: event.currentTarget.value, token: undefined } } })} /></FormField>
        <FormField label={t('gofi.canvas.node-inspector.aster-bearer-token', 'Bearer token (instead of token file)')}><Input type="password" disabled={readOnly} value={sink.aster?.token ?? ''} autoComplete="off" onChange={(event) => onChange({ ...data, sink: { ...sink, aster: { topic: '', scope: '', ...sink.aster, token: event.currentTarget.value, tokenFile: undefined } } })} /></FormField>
        <FormField label={t('gofi.canvas.node-inspector.aster-timeout', 'Timeout (seconds)')}><Input type="number" min={1} max={30} disabled={readOnly} value={sink.aster?.timeoutSeconds ?? ''} placeholder="10" onChange={(event) => onChange({ ...data, sink: { ...sink, aster: { topic: '', scope: '', ...sink.aster, timeoutSeconds: Number(event.currentTarget.value) || undefined } } })} /></FormField>
        <FormField label={t('gofi.canvas.node-inspector.aster-body-mode', 'Body mode')}><RadioButtonGroup disabled={readOnly} value={sink.bodyMode ?? 'raw'} options={bodyModeOptions()} onChange={(value) => onChange({ ...data, sink: { ...sink, bodyMode: value as 'raw' | 'wrapped' } })} /></FormField>
      </>}

      {sink.type === 'loki' && <FormField label={t('gofi.canvas.node-inspector.loki-labels', 'Static stream labels')} description={t('gofi.canvas.node-inspector.loki-labels-description', 'The app=gofi and listener labels are included by default. Use a Loki base URL or its push endpoint.')}><KeyValueEditor disabled={readOnly} value={sink.labels ?? {}} onChange={labels => onChange({ ...data, sink: { ...sink, labels } })} keyPlaceholder={t('gofi.canvas.node-inspector.label-name', 'Label name')} valuePlaceholder={t('gofi.canvas.node-inspector.label-value', 'Label value')} /></FormField>}
      {['http', 'loki', 'zenoh'].includes(sink.type) && (
        <>
          {sink.type === 'http' && <>
          <FormField label={t('gofi.canvas.node-inspector.method', 'Method')}>
            <Select
              disabled={readOnly}
              value={sink.method || 'POST'}
              onChange={(v) => onChange({ ...data, sink: { ...sink, method: v } })}
              options={HTTP_METHOD_OPTIONS}
            />
          </FormField>

          <FormField label={t('gofi.canvas.node-inspector.content-type', 'Content-Type')}>
            <Input
              disabled={readOnly}
              value={sink.contentType ?? ''}
              onChange={(e) => onChange({ ...data, sink: { ...sink, contentType: e.currentTarget.value } })}
              placeholder="application/json"
            />
          </FormField>

          </>}
          <FormField label={t('gofi.canvas.node-inspector.body-mode', 'Body Mode')}>
            <RadioButtonGroup
              disabled={readOnly}
              value={sink.bodyMode || 'wrapped'}
              onChange={(v) => onChange({ ...data, sink: { ...sink, bodyMode: v as 'wrapped' | 'raw' } })}
              options={bodyModeOptions()}
            />
          </FormField>

          <FormField label={t('gofi.canvas.node-inspector.headers', 'Headers')}>
            <KeyValueEditor
              disabled={readOnly}
              value={sink.headers ?? {}}
              onChange={(headers) => onChange({ ...data, sink: { ...sink, headers } })}
              keyPlaceholder={t('gofi.canvas.node-inspector.header-name', 'Header name')}
              valuePlaceholder={t('gofi.canvas.node-inspector.header-value', 'Header value')}
            />
          </FormField>

          <FormField label={t('gofi.canvas.node-inspector.auth-type', 'Auth Type')}>
            <RadioButtonGroup
              disabled={readOnly}
              value={sink.auth?.type ?? ''}
              onChange={(v) => {
                if (v === '') {
                  onChange({ ...data, sink: { ...sink, auth: undefined } })
                } else {
                  onChange({ ...data, sink: { ...sink, auth: { ...sink.auth, type: v as HTTPAuth['type'] } } })
                }
              }}
              options={httpAuthTypeOptions()}
            />
          </FormField>

          {sink.auth?.type === 'bearer' && (
            <FormField label={t('gofi.canvas.node-inspector.token', 'Token')}>
              <Input
                disabled={readOnly}
                value={sink.auth.token ?? ''}
                onChange={(e) =>
                  onChange({ ...data, sink: { ...sink, auth: { ...sink.auth, type: 'bearer', token: e.currentTarget.value } } })
                }
                placeholder={t('gofi.canvas.node-inspector.bearer-token-placeholder', 'Bearer token')}
              />
            </FormField>
          )}

          {sink.auth?.type === 'basic' && (
            <>
              <FormField label={t('gofi.common.username', 'Username')}>
                <Input
                  disabled={readOnly}
                  value={sink.auth.username ?? ''}
                  onChange={(e) =>
                    onChange({ ...data, sink: { ...sink, auth: { ...sink.auth, type: 'basic', username: e.currentTarget.value } } })
                  }
                  placeholder={t('gofi.common.username', 'Username')}
                />
              </FormField>
              <FormField label={t('gofi.common.password', 'Password')}>
                <Input
                  type="password"
                  disabled={readOnly}
                  value={sink.auth.password ?? ''}
                  onChange={(e) =>
                    onChange({ ...data, sink: { ...sink, auth: { ...sink.auth, type: 'basic', password: e.currentTarget.value } } })
                  }
                  placeholder={t('gofi.common.password', 'Password')}
                />
              </FormField>
            </>
          )}

          {sink.auth?.type === 'apiKey' && (
            <>
              <FormField label={t('gofi.canvas.node-inspector.header-name-label', 'Header Name')}>
                <Input
                  disabled={readOnly}
                  value={sink.auth.headerName ?? ''}
                  onChange={(e) =>
                    onChange({ ...data, sink: { ...sink, auth: { ...sink.auth, type: 'apiKey', headerName: e.currentTarget.value } } })
                  }
                  placeholder="X-API-Key"
                />
              </FormField>
              <FormField label={t('gofi.canvas.node-inspector.api-key', 'API Key')}>
                <Input
                  disabled={readOnly}
                  value={sink.auth.apiKey ?? ''}
                  onChange={(e) =>
                    onChange({ ...data, sink: { ...sink, auth: { ...sink.auth, type: 'apiKey', apiKey: e.currentTarget.value } } })
                  }
                  placeholder={t('gofi.canvas.node-inspector.api-key-placeholder', 'API key value')}
                />
              </FormField>
            </>
          )}
        </>
      )}

      {sink.type === 'nats' && (
        <>
          <FormField label={t('gofi.canvas.node-inspector.nats-subject', 'Subject')}>
            <Input
              disabled={readOnly}
              value={sink.subject ?? ''}
              onChange={(e) => onChange({ ...data, sink: { ...sink, subject: e.currentTarget.value } })}
              placeholder="ingress.events"
            />
          </FormField>

          <Collapse label={t('gofi.canvas.node-inspector.authentication-optional', 'Authentication (optional)')} isOpen={hasNatsAuthValues(sink.natsAuth)}>
            <NatsAuthFields
              natsAuth={sink.natsAuth}
              readOnly={readOnly}
              onChange={(natsAuth) => onChange({ ...data, sink: { ...sink, natsAuth } })}
            />
          </Collapse>
        </>
      )}
      <FormField label={t('gofi.canvas.node-inspector.encoding', 'Encoding')} description={t('gofi.canvas.node-inspector.encoding-description', 'An Encode step on the canvas serializes the message and applies encoders before this sink sends it; several sinks can share one. None keeps the sink\'s default payload.')}>
        <Select disabled={readOnly || !onSelectEncoder} value={encoderId ?? ''} onChange={(value) => onSelectEncoder?.(value)} options={[
          { value: '', label: t('gofi.canvas.node-inspector.encoding-none', 'None (sink default)') },
          ...encoders.map((encoder) => ({ value: encoder.id, label: t('gofi.canvas.node-inspector.encoding-option', 'Encode · {{chain}}', { chain: encoder.label }) })),
          ...(readOnly ? [] : [{ value: 'new', label: t('gofi.canvas.node-inspector.encoding-new', 'New Encode step…') }]),
        ]} />
      </FormField>
    </DetailSection>
  )
}

function hasNatsAuthValues(auth: NATSAuth | undefined): boolean {
  if (!auth) return false
  return Boolean(
    auth.username ||
      auth.password ||
      auth.token ||
      auth.nkeySeed ||
      auth.creds ||
      auth.tlsCert ||
      auth.tlsKey ||
      auth.tlsCa ||
      auth.insecureSkipVerify,
  )
}

function NatsAuthFields({
  natsAuth,
  readOnly,
  onChange,
}: {
  natsAuth: NATSAuth | undefined
  readOnly?: boolean
  onChange: (natsAuth: NATSAuth) => void
}) {
  const styles = useStyles2(getStyles)
  const auth = natsAuth ?? {}

  return (
    <Stack direction="column" gap={2}>
      <FormField label={t('gofi.common.username', 'Username')}>
        <Input
          disabled={readOnly}
          value={auth.username ?? ''}
          onChange={(e) => onChange({ ...auth, username: e.currentTarget.value })}
          placeholder={t('gofi.common.username', 'Username')}
        />
      </FormField>

      <FormField label={t('gofi.common.password', 'Password')}>
        <Input
          type="password"
          disabled={readOnly}
          value={auth.password ?? ''}
          onChange={(e) => onChange({ ...auth, password: e.currentTarget.value })}
          placeholder={t('gofi.common.password', 'Password')}
        />
      </FormField>

      <FormField label={t('gofi.canvas.node-inspector.token', 'Token')}>
        <Input
          disabled={readOnly}
          value={auth.token ?? ''}
          onChange={(e) => onChange({ ...auth, token: e.currentTarget.value })}
          placeholder={t('gofi.canvas.node-inspector.auth-token-placeholder', 'Auth token')}
        />
      </FormField>

      <FormField label={t('gofi.canvas.node-inspector.nkey-seed', 'NKey Seed')}>
        <Input
          disabled={readOnly}
          value={auth.nkeySeed ?? ''}
          onChange={(e) => onChange({ ...auth, nkeySeed: e.currentTarget.value })}
          placeholder="SUAC5..."
        />
      </FormField>

      <FormField label={t('gofi.canvas.node-inspector.creds-file-contents', 'Creds File Contents')}>
        <TextArea
          disabled={readOnly}
          rows={4}
          className={styles.mono}
          value={auth.creds ?? ''}
          onChange={(e) => onChange({ ...auth, creds: e.currentTarget.value })}
          placeholder={'-----BEGIN NATS USER JWT-----\n...\n------END NATS USER JWT------'}
        />
      </FormField>

      <FormField label={t('gofi.canvas.node-inspector.tls-client-cert', 'TLS Client Cert')}>
        <TextArea
          disabled={readOnly}
          rows={4}
          className={styles.mono}
          value={auth.tlsCert ?? ''}
          onChange={(e) => onChange({ ...auth, tlsCert: e.currentTarget.value })}
          placeholder="-----BEGIN CERTIFICATE-----"
        />
      </FormField>

      <FormField label={t('gofi.canvas.node-inspector.tls-client-key', 'TLS Client Key')}>
        <TextArea
          disabled={readOnly}
          rows={4}
          className={styles.mono}
          value={auth.tlsKey ?? ''}
          onChange={(e) => onChange({ ...auth, tlsKey: e.currentTarget.value })}
          placeholder="-----BEGIN PRIVATE KEY-----"
        />
      </FormField>

      <FormField label={t('gofi.canvas.node-inspector.tls-ca-cert', 'TLS CA Cert')}>
        <TextArea
          disabled={readOnly}
          rows={4}
          className={styles.mono}
          value={auth.tlsCa ?? ''}
          onChange={(e) => onChange({ ...auth, tlsCa: e.currentTarget.value })}
          placeholder="-----BEGIN CERTIFICATE-----"
        />
      </FormField>

      <Checkbox
        disabled={readOnly}
        value={auth.insecureSkipVerify ?? false}
        onChange={(event) => onChange({ ...auth, insecureSkipVerify: event.currentTarget.checked })}
        label={t('gofi.canvas.node-inspector.insecure-skip-verify', 'Insecure Skip Verify')}
        description={t('gofi.canvas.node-inspector.insecure-skip-verify-description', 'Skip TLS certificate verification (not recommended)')}
      />
    </Stack>
  )
}

const getStyles = (theme: GrafanaTheme2) => ({
  panel: css({
    width: '100%', flexShrink: 0, display: 'flex', flexDirection: 'column', minHeight: 0, background: theme.colors.background.primary, borderLeft: `1px solid ${theme.colors.border.weak}`,
    [theme.breakpoints.down('md')]: { width: '100%', position: 'absolute', inset: 0, zIndex: 5 },
    // Full-width radio groups (source type, parse format, …) truncate their
    // labels in a narrow inspector or on phones («Прослухов…»): let the
    // options flow onto a second row instead.
    '& [role=radiogroup]': { flexWrap: 'wrap' },
    '& [role=radiogroup] > div': { flexBasis: 'auto', minWidth: 'max-content' },
  }),
  // The same side padding as the body, so the title and the close button line
  // up with the left and right edges of the fields below.
  header: css({ display: 'flex', alignItems: 'center', justifyContent: 'space-between', padding: theme.spacing(1, 2), borderBottom: `1px solid ${theme.colors.border.weak}`, h2: { margin: 0 } }),
  body: css({ flex: 1, minHeight: 0, minWidth: 0, overflowY: 'auto', overflowX: 'auto', padding: theme.spacing(2) }),
  // One column with one gap: fields, hints and collapses never bring their own
  // bottom margin (Field noMargin, Collapse marginBottom 0 below).
  sectionBody: css({ display: 'flex', flexDirection: 'column', gap: theme.spacing(2), marginTop: theme.spacing(2) }),
  collapse: css({ marginBottom: 0 }),
  mono: css({ fontFamily: theme.typography.fontFamilyMonospace, fontSize: theme.typography.bodySmall.fontSize }),
  list: css({ display: 'flex', flexDirection: 'column', gap: theme.spacing(0.5), width: '100%' }),
  listRow: css({ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: theme.spacing(1), minHeight: theme.spacing(theme.components.height.md), padding: theme.spacing(0, 1), border: `1px solid ${theme.colors.border.weak}`, borderRadius: theme.shape.radius.default, background: theme.colors.background.secondary }),
})

/** Encode: how a message becomes the bytes the connected sinks send — a
 * format, then encoders (built-in or @gofi.encoder) in order. */
function EncodeInspector({ data, readOnly, onChange, fedSinks, onDisconnectSink }: { data: EncodeNodeData; readOnly?: boolean; onChange: (data: EncodeNodeData) => void; fedSinks: FedSink[]; onDisconnectSink?: (sinkId: string) => void }) {
  const styles = useStyles2(getStyles)
  const defaultLibrary = usePythonDefault()
  const { encoding } = data
  const encoders = encoding.encoders ?? []
  const set = (next: Encoding) => onChange({ ...data, encoding: next })
  return <DetailSection title={t('gofi.canvas.node-inspector.title-encode', 'Encode')}>
    <Hint>{t('gofi.canvas.node-inspector.encode-node-hint', 'Serializes each message, then applies the encoders in order, for every sink connected from this step. Connect a sink here (or pick this step in its Encoding field) to encode it; a sink connected from the last stage directly keeps its default payload.')}</Hint>
    <FormField label={t('gofi.canvas.node-inspector.format', 'Format')}>
      <RadioButtonGroup disabled={readOnly} value={encodingFormat(encoding)} onChange={(value) => set({ ...encoding, format: value as Encoding['format'] })} options={[
        { value: 'wrapped', label: t('gofi.canvas.node-inspector.encode-wrapped', 'Wrapped JSON'), description: t('gofi.canvas.node-inspector.encode-wrapped-description', 'JSON with id, fields, and meta') },
        { value: 'fields', label: t('gofi.canvas.node-inspector.encode-fields', 'Fields JSON'), description: t('gofi.canvas.node-inspector.encode-fields-description', 'JSON of the parsed fields') },
        { value: 'raw', label: t('gofi.canvas.node-inspector.encode-raw', 'Raw frame'), description: t('gofi.canvas.node-inspector.encode-raw-description', 'The original frame bytes') },
      ]} />
    </FormField>
    <CodecListEditor role="encoder" steps={encoders} readOnly={readOnly} emptyText={t('gofi.canvas.node-inspector.no-encoders', 'No encoders: the formatted payload is sent as is.')} onChange={(next) => set({ ...encoding, encoders: next })} />
    <FormField label={t('gofi.canvas.node-inspector.encode-feeds', 'Sinks encoded by this step')} description={fedSinks.length ? undefined : t('gofi.canvas.node-inspector.encode-feeds-none', 'None yet. Connect a sink from this step, or choose it in a sink\'s Encoding field. An Encode step that feeds no sink is ignored when saving.')}>
      <div className={styles.list}>
        {fedSinks.map(({ id, sink, isDeadLetter }) => <div key={id} className={styles.listRow}>
          <Text variant="bodySmall" truncate>{isDeadLetter ? t('gofi.canvas.node-inspector.fed-dead-letter', 'Dead letter · {{sink}}', { sink: sinkLabel(sink, defaultLibrary) }) : sinkLabel(sink, defaultLibrary)}</Text>
          {!readOnly && onDisconnectSink && <IconButton name="link-broken" tooltip={isDeadLetter ? t('gofi.canvas.node-inspector.disconnect-dead-letter', 'Disconnect: the dead letter keeps its default payload') : t('gofi.canvas.node-inspector.disconnect-sink', 'Disconnect: the sink keeps its default payload')} onClick={() => onDisconnectSink(id)} />}
        </div>)}
      </div>
    </FormField>
  </DetailSection>
}

/** "tcp · tcp://host:9000": a sink's type and where it delivers. */
function sinkLabel(sink: SinkCfg, defaultLibrary?: string): string {
  const where = sink.kubernetes ? t('gofi.canvas.sink-node.pods', 'Pods :{{port}} · {{selector}}', { port: sink.kubernetes.port, selector: Object.entries(sink.kubernetes.selector).map(([key, value]) => `${key}=${value}`).join(', ') })
    : sink.python ? pythonRefLabel(sink.python, defaultLibrary) : sink.url || sink.subject || ''
  return where ? `${sink.type} · ${where}` : sink.type
}
