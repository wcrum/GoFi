import { useEffect, useState, type ReactElement, type ReactNode } from 'react'
import { css } from '@emotion/css'
import type { GrafanaTheme2 } from '@grafana/data'
import { Checkbox, Combobox, ControlledCollapse, Field, IconButton, Input, RadioButtonGroup, TagsInput, Text, TextArea, useStyles2 } from '@grafana/ui'
import { getEndpoints, getMode, type DeployModeInfo, type DiscoveredEndpoint } from '../../api'
import {
  SOURCE_TYPE_OPTIONS,
  NETWORK_OPTIONS,
  FRAMING_OPTIONS,
  PARSER_OPTIONS,
  SINK_OPTIONS,
  HTTP_METHOD_OPTIONS,
  HTTP_AUTH_TYPE_OPTIONS,
  BODY_MODE_OPTIONS,
  TRANSFORM_OP_OPTIONS,
} from './options'
import { KeyValueEditor } from './KeyValueEditor'
import { SourceSettings, DEFAULT_FETCH, DEFAULT_SUBSCRIBE } from './SourceSettings'
import { CodecListEditor } from './CodecListEditor'
import { ExposureSettings } from './ExposureSettings'
import { codecChain } from '../../lib/codec'
import { SinkTargetTools } from './SinkTargetTools'
import { useLibraryFunctions, functionOptions, pythonValue, parsePythonValue } from './python-functions'
import { PythonStageEditor } from '../python/PythonStageEditor'
import { CelField } from '../cel/CelField'
import { OnErrorControl } from '../cel/OnErrorControl'
import type { DecodeNodeData, FlowNode, FrameNodeData, IngressNodeData, ParseNodeData, StageNodeData, SinkNodeData } from './types'
import { listenOf, sourceTypeOf, NODE_PORT_RANGE, type ServiceType, type CodecStep, type Encoding, type Framing, type HTTPAuth, type ListenSource, type NATSAuth } from '../../types'

interface Props {
  node: FlowNode | null
  readOnly?: boolean
  onChangeIngress: (data: IngressNodeData) => void
  onChangeFrame: (data: FrameNodeData) => void
  onChangeDecode: (data: DecodeNodeData) => void
  onChangeParse: (data: ParseNodeData) => void
  onChangeStage: (data: StageNodeData) => void
  onChangeSink: (data: SinkNodeData) => void
  onClose: () => void
}

// Grafana form building blocks with the inspector's call shape: a Field with
// Grafana's standard spacing, a Combobox taking/returning plain strings, a
// section heading, and secondary helper text.
function FormField({ label, description, children }: { label: ReactNode; description?: ReactNode; children: ReactElement<Record<string, unknown>> }) {
  return <Field label={label} description={description}>{children}</Field>
}
function Select({ value, onChange, options, disabled }: { value: string; onChange: (value: string) => void; options: Array<{ value: string; label: string }>; disabled?: boolean }) {
  return <Combobox value={value} options={options} disabled={disabled} onChange={(option) => onChange(option.value)} />
}
function DetailSection({ title, children }: { title: string; children: ReactNode }) {
  const styles = useStyles2(getStyles)
  return <section><Text element="h3" variant="h6">{title}</Text><div className={styles.sectionBody}>{children}</div></section>
}
function Hint({ children, tone = 'secondary' }: { children: NonNullable<ReactNode>; tone?: 'secondary' | 'warning' }) {
  const styles = useStyles2(getStyles)
  return <div className={styles.block}><Text element="p" variant="bodySmall" color={tone}>{children}</Text></div>
}

export function NodeInspector({ node, readOnly, onChangeIngress, onChangeFrame, onChangeDecode, onChangeParse, onChangeStage, onChangeSink, onClose }: Props) {
  const styles = useStyles2(getStyles)
  if (!node) return null

  return (
    <aside className={styles.panel} aria-label={`${inspectorTitle(node)} inspector`}>
      <div className={styles.header}>
        <Text element="h2" variant="h5">{inspectorTitle(node)}</Text>
        <IconButton name="times" tooltip="Close inspector" onClick={onClose} />
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
        {node.type === 'sink' && (
          <SinkInspector data={node.data} readOnly={readOnly} onChange={onChangeSink} />
        )}
      </div>
    </aside>
  )
}

function inspectorTitle(node: FlowNode): string {
  switch (node.type) {
    case 'ingress':
      return 'Source'
    case 'frame':
      return 'Frame'
    case 'decode':
      return 'Decode'
    case 'parse':
      return 'Parse'
    case 'stage':
      return 'Stage'
    case 'sink':
      return node.data.isDeadLetter ? 'Dead Letter Sink' : 'Sink'
    default:
      return 'Node'
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
  const portDescription = !isKubernetes ? 'Senders connect to this port.'
    : exposure === '' ? 'Senders inside the cluster connect to the flow Pod on this port; no Service is created.'
    : exposure === 'NodePort' ? 'In-cluster Service port. Senders outside the cluster use the NodePort.'
    : exposure === 'LoadBalancer' ? 'Port on the load balancer address (and the in-cluster Service).'
    : exposure === 'Gateway' ? 'Port the Gateway forwards to (the in-cluster Service).'
    : 'Senders inside the cluster connect to the Service on this port.'

  const sourceType = sourceTypeOf(data.source)
  const sourceTypeField = <FormField label="Source type">
    <RadioButtonGroup
      disabled={readOnly}
      value={sourceType}
      onChange={(value) => onChange({ ...data, source: value === 'fetch' ? { fetch: data.source.fetch ?? DEFAULT_FETCH } : value === 'subscribe' ? { subscribe: data.source.subscribe ?? DEFAULT_SUBSCRIBE.nats } : { listen } })}
      options={SOURCE_TYPE_OPTIONS}
      disabledOptions={['python']}
    />
  </FormField>
  if (sourceType !== 'listen') {
    return <DetailSection title="Source">
      {sourceTypeField}
      <SourceSettings source={data.source} readOnly={readOnly} onChange={(source) => onChange({ ...data, source })} />
    </DetailSection>
  }

  return (
    <DetailSection title="Source">
      {sourceTypeField}
      <Hint>Listen: senders connect to a GoFi TCP/UDP port.</Hint>

      <FormField label="Port" description={portDescription}>
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

      <FormField label="Network">
        <RadioButtonGroup
          disabled={readOnly}
          value={listen.network || 'tcp'}
          onChange={(v) => setListen({ ...listen, network: v })}
          options={NETWORK_OPTIONS}
        />
      </FormField>
      {isKubernetes && <ExposureSettings listen={listen} onChange={setListen} readOnly={readOnly} deployment={deployment} />}
      {!isKubernetes && <Hint>{deployment?.mode === 'binary' ? 'In binary mode the flow binds a local TCP/UDP port.' : deployment ? 'Container flows bind a TCP/UDP port. Publish it through your container runtime.' : 'Checking deployment mode…'}</Hint>}

    </DetailSection>
  )
}

function FrameInspector({ data, readOnly, onChange }: { data: FrameNodeData; readOnly?: boolean; onChange: (data: FrameNodeData) => void }) {
  const { framing } = data
  const { functions } = useLibraryFunctions()
  const set = (next: Partial<Framing>) => onChange({ ...data, framing: { ...framing, ...next } })
  const framers = functionOptions(functions, 'framer', framing.mode === 'python' ? framing.python : undefined)
  return (
    <DetailSection title="Frame">
      <Hint>How the incoming byte stream becomes discrete frames. UDP datagrams are independent frames; line and delimiter framing can still split a datagram.</Hint>
      <FormField label="Framing" description={framers.length ? undefined : 'Python framers (@gofi.framer) appear here once a library registering one is launched.'}><Select disabled={readOnly} value={framing.mode === 'python' ? pythonValue(framing.python) : framing.mode} onChange={(value) => value.startsWith('python:') ? set({ mode: 'python', python: parsePythonValue(value) }) : set({ mode: value, python: undefined })} options={[...FRAMING_OPTIONS, ...framers]} /></FormField>
      {framing.mode === 'python' && <Hint>The framer is called after each read with the buffered bytes and returns complete frames plus the remainder; at the end of the stream it is called once more with final=True.</Hint>}
      {framing.mode === 'delimited' && <FormField label="Delimiter"><Input disabled={readOnly} value={framing.delimiter ?? ''} onChange={(e) => set({ delimiter: e.currentTarget.value })} placeholder="</event>" /></FormField>}
      {framing.mode === 'length-prefix' && <FormField label="Prefix bytes"><RadioButtonGroup disabled={readOnly} value={String(framing.prefixLen ?? 4)} options={[{ value: '1', label: '1 byte' }, { value: '2', label: '2 bytes' }, { value: '4', label: '4 bytes' }]} onChange={(value) => set({ prefixLen: Number(value) })} /></FormField>}
      <FormField label="Maximum frame size (bytes)"><Input type="number" min={1} disabled={readOnly} value={framing.maxBytes ?? ''} placeholder="Default" onChange={(e) => set({ maxBytes: Number(e.currentTarget.value) || undefined })} /></FormField>
    </DetailSection>
  )
}

function DecodeInspector({ data, readOnly, onChange }: { data: DecodeNodeData; readOnly?: boolean; onChange: (data: DecodeNodeData) => void }) {
  return (
    <DetailSection title="Decode">
      <Hint>Decoders run top to bottom before parsing. For base64-encoded gzip data, add base64 first, then gzip. Python decoders (@gofi.decoder) can unwrap or decrypt. The raw frame is kept for inspection and replay.</Hint>
      <CodecListEditor role="decoder" steps={data.decoders} readOnly={readOnly} emptyText="No decoders: frames reach the parser unchanged." onChange={(decoders) => onChange({ ...data, decoders })} />
      <FormField label="Decoded size limit (bytes)" description="Each decoding step's output is capped at this size."><Input type="number" min={1} disabled={readOnly} value={data.maxDecodedBytes ?? ''} placeholder="1048576" onChange={(event) => onChange({ ...data, maxDecodedBytes: Number(event.currentTarget.value) || undefined })} /></FormField>
    </DetailSection>
  )
}

function ParseInspector({ data, readOnly, onChange }: { data: ParseNodeData; readOnly?: boolean; onChange: (data: ParseNodeData) => void }) {
  const { parser } = data
  const { functions, error: registryError } = useLibraryFunctions()
  return (
    <DetailSection title="Parse">
      <Hint>How a decoded frame becomes structured fields for stages and destinations.</Hint>
      <FormField label="Format">
        <Select
          disabled={readOnly}
          value={parser.format === 'python' ? pythonValue(parser.python) : parser.format}
          onChange={(v) => onChange({ ...data, parser: { ...parser, format: v.startsWith('python:') ? 'python' : v, python: parsePythonValue(v) } })}
          options={[...PARSER_OPTIONS, ...(parser.format === 'python' && !parser.python ? [{ value: 'python', label: 'Choose a Python parser…' }] : []), ...functionOptions(functions, 'parser', parser.python)]}
        />
      </FormField>
      {registryError && <Text variant="bodySmall" color="warning">Python function registry unavailable.</Text>}
      <FormField label="Decode / parse errors"><RadioButtonGroup disabled={readOnly} value={parser.errorPolicy ?? 'continue'} onChange={(value) => onChange({ ...data, parser: { ...parser, errorPolicy: value as 'continue' | 'drop' } })} options={[{ value: 'continue', label: 'Continue', description: 'Continue and record the parse error' }, { value: 'drop', label: 'Drop', description: 'Drop and send to the dead-letter sink' }]} /></FormField>
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
  const styles = useStyles2(getStyles)
  const { functions, error: registryError } = useLibraryFunctions()

  switch (spec.kind) {
    case 'python':
      return <DetailSection title="Python Transformer"><PythonStageEditor stageId={spec.id} value={spec.python} functions={functions} registryError={registryError} readOnly={readOnly} onChange={python => onChange({ ...data, spec: { ...spec, python } })} /></DetailSection>
    case 'filter':
      return (
        <DetailSection title="Filter">
          <FormField label="Expression language"><RadioButtonGroup disabled={readOnly} value={spec.type ?? 'cel'} onChange={(value) => onChange({ ...data, spec: { ...spec, type: value } })} options={[{ value: 'cel', label: 'CEL', description: 'Common Expression Language' }, { value: 'regex', label: 'Regex', description: 'Regular expression (RE2)' }]} /></FormField>
          {spec.type === 'regex'
            ? <FormField label="Regular expression"><Input disabled={readOnly} value={spec.expression ?? ''} onChange={(event) => onChange({ ...data, spec: { ...spec, expression: event.currentTarget.value, expressionRef: undefined, onError: undefined } })} placeholder="^event-" /></FormField>
            : <><FormField label="CEL expression"><CelField aria-label="CEL expression" disabled={readOnly} resultType="bool" value={spec.expression ?? ''} onChange={(expression) => onChange({ ...data, spec: { ...spec, expression } })} placeholder='fields["type"] == "a-f-G"' libraryRef={spec.expressionRef} onLibraryRefChange={(expressionRef) => onChange({ ...data, spec: { ...spec, expressionRef } })} /></FormField>
              <div className={styles.block}><OnErrorControl disabled={readOnly} value={spec.onError} onChange={(onError) => onChange({ ...data, spec: { ...spec, onError } })} /></div></>}
          {spec.type === 'regex' && <FormField label="Field (blank uses original raw frame)"><Input disabled={readOnly} value={spec.field ?? ''} placeholder="type" onChange={(event) => onChange({ ...data, spec: { ...spec, field: event.currentTarget.value } })} /></FormField>}
          <Hint>{spec.type === 'regex' ? 'Matching messages continue. Use a parsed string field or match the original frame.' : 'A true result keeps the message. Use fields for parsed data, meta for metadata, and raw for the original frame.'} Test an example in Test flow before deploying.</Hint>
        </DetailSection>
      )

    case 'transform': {
      const op = spec.transformOp ?? 'set'
      const fieldLabel = op === 'rename' ? 'Source Field' : 'Field'
      return (
        <DetailSection title="Transform">
          <FormField label="Operation">
            <RadioButtonGroup
              disabled={readOnly}
              value={op}
              onChange={(v) => onChange({ ...data, spec: { ...spec, transformOp: v as 'set' | 'rename' | 'remove' } })}
              options={TRANSFORM_OP_OPTIONS}
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
            <FormField label="New Field">
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
              <FormField label="Value Expression">
                <CelField
                  aria-label="Value expression"
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
        <DetailSection title="Rate Limit">
          <FormField label="Max Per Second">
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
        <DetailSection title="Deduplicate">
          <FormField label="Key Expression">
            <CelField
              aria-label="Key expression"
              disabled={readOnly}
              resultType="any"
              value={spec.keyExpr ?? ''}
              onChange={(keyExpr) => onChange({ ...data, spec: { ...spec, keyExpr } })}
              placeholder='fields["id"]'
              libraryRef={spec.expressionRef}
              onLibraryRefChange={(expressionRef) => onChange({ ...data, spec: { ...spec, expressionRef } })}
            />
          </FormField>
          <div className={styles.block}><OnErrorControl disabled={readOnly} value={spec.onError} onChange={(onError) => onChange({ ...data, spec: { ...spec, onError } })} /></div>

          <FormField label="Window">
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
        <DetailSection title="Schema Validator">
          <FormField label="Required Fields">
            <TagsInput
              disabled={readOnly}
              tags={spec.requiredFields ?? []}
              onChange={(requiredFields) => onChange({ ...data, spec: { ...spec, requiredFields } })}
              placeholder="Add a field name..."
            />
          </FormField>
        </DetailSection>
      )

    default:
      return (
        <DetailSection title="Stage">
          <Text color="secondary">Unknown stage kind "{spec.kind}"</Text>
        </DetailSection>
      )
  }
}

function SinkInspector({
  data,
  readOnly,
  onChange,
}: {
  data: SinkNodeData
  readOnly?: boolean
  onChange: (data: SinkNodeData) => void
}) {
  const { sink } = data
  const { functions, error: registryError } = useLibraryFunctions()
  const [endpoints, setEndpoints] = useState<DiscoveredEndpoint[]>([])
  useEffect(() => { if (!readOnly) getEndpoints().then(setEndpoints).catch(() => {}) }, [readOnly])

  return (
    <DetailSection title="Sink">
      <FormField label="Sink Type">
        <Select
          disabled={readOnly}
          value={sink.type === 'python' ? pythonValue(sink.python) : sink.type}
          onChange={(v) => onChange({ ...data, sink: v === 'loki' ? { type: 'loki', url: 'http://loki:3100', bodyMode: 'wrapped', when: sink.when, whenRef: sink.whenRef, encoding: sink.encoding } : v === 'zenoh' ? { type: 'zenoh', url: 'http://zenoh:8000', subject: '', contentType: 'application/octet-stream', bodyMode: 'raw', when: sink.when, whenRef: sink.whenRef, encoding: sink.encoding } : v.startsWith('python:') ? { type: 'python', python: parsePythonValue(v), when: sink.when, whenRef: sink.whenRef, encoding: sink.encoding } : { ...sink, type: v, python: undefined, kubernetes: ['tcp', 'udp'].includes(v) ? sink.kubernetes : undefined } })}
          options={[...SINK_OPTIONS, ...(sink.type === 'python' && !sink.python ? [{ value: 'python', label: 'Choose a Python sink…' }] : []), ...functionOptions(functions, 'sink', sink.python)]}
        />
      </FormField>

      {sink.type === 'python' && <p className="mb-3 text-xs text-g-text-secondary">This sink executes in GoFi's separate Python worker.</p>}
      {registryError && <p className="mb-3 text-xs text-g-warning-text">Python function registry unavailable.</p>}
      {!data.isDeadLetter && (
        <FormField label="Send When (optional)">
          <CelField
            aria-label="Send when"
            disabled={readOnly}
            resultType="bool"
            value={sink.when ?? ''}
            onChange={(when) => onChange({ ...data, sink: { ...sink, when } })}
            placeholder='fields["severity"] == "critical"  (blank = always send)'
            libraryRef={sink.whenRef}
            onLibraryRefChange={(whenRef) => onChange({ ...data, sink: { ...sink, whenRef } })}
          />
        </FormField>
      )}

      {(['nats', 'http', 'tcp', 'udp', 'aster', 'zenoh', 'loki'].includes(sink.type) && !sink.kubernetes) && (
        <FormField label={sink.type === 'zenoh' ? 'Zenoh REST router URL' : sink.type === 'loki' ? 'Loki URL' : 'URL'}>
          <Input
            disabled={readOnly}
            value={sink.url ?? ''}
            onChange={(e) => onChange({ ...data, sink: { ...sink, url: e.currentTarget.value } })}
            placeholder={sink.type === 'nats' ? 'nats://broker:4222' : sink.type === 'tcp' ? 'tcp://application:9000' : sink.type === 'udp' ? 'udp://application:9000' : sink.type === 'zenoh' ? 'http://zenoh:8000' : sink.type === 'aster' ? 'http://127.0.0.1:8181' : 'http://application:8080'}
          />
        </FormField>
      )}

      {!sink.kubernetes && ['tcp', 'udp', 'http', 'nats'].includes(sink.type) && endpoints.length > 0 && !readOnly && <FormField label="Discover a destination"><Select value="" options={[{ value: '', label: 'Choose an endpoint…' }, ...endpoints.flatMap((endpoint) => endpoint.ports.filter((port) => sink.type === 'udp' ? port.protocol === 'UDP' : port.protocol === 'TCP').map((port) => ({ value: `${sink.type}://${endpoint.host}:${port.port}`, label: `${endpoint.name}${endpoint.namespace ? ` (${endpoint.namespace})` : ''} :${port.port}` })))]} onChange={(value) => { if (value) onChange({ ...data, sink: { ...sink, url: value } }) }} /></FormField>}
      {['tcp', 'udp'].includes(sink.type) && <SinkTargetTools sink={sink} readOnly={readOnly} onChange={value => onChange({ ...data, sink: value })} />}
      {['tcp', 'udp'].includes(sink.type) && <FormField label="Delimiter appended to each message" description="Use a direct destination address or select matching Kubernetes pods. The delimiter is appended to each forwarded message."><Input disabled={readOnly} value={sink.delimiter ?? ''} placeholder="Optional delimiter" onChange={(event) => onChange({ ...data, sink: { ...sink, delimiter: event.currentTarget.value } })} /></FormField>}

      {sink.type === 'zenoh' && <><FormField label="Zenoh key expression"><Input disabled={readOnly} value={sink.subject ?? ''} placeholder="telemetry/sensors" onChange={event => onChange({ ...data, sink: { ...sink, subject: event.currentTarget.value } })} /></FormField><FormField label="Content type" description="Publishes raw frames with HTTP PUT through Zenoh's REST plugin. Enable the REST plugin on your router."><Input disabled={readOnly} value={sink.contentType ?? 'application/octet-stream'} onChange={event => onChange({ ...data, sink: { ...sink, contentType: event.currentTarget.value } })} /></FormField></>}
      {sink.type === 'aster' && <>
        <Hint>Send through an Aster agent in this workload's network namespace. Its API must stay on loopback.</Hint>
        <FormField label="Topic"><Input disabled={readOnly} value={sink.aster?.topic ?? ''} onChange={(event) => onChange({ ...data, sink: { ...sink, aster: { scope: '', ...sink.aster, topic: event.currentTarget.value } } })} /></FormField>
        <FormField label="Scope"><Input disabled={readOnly} value={sink.aster?.scope ?? ''} onChange={(event) => onChange({ ...data, sink: { ...sink, aster: { topic: '', ...sink.aster, scope: event.currentTarget.value } } })} /></FormField>
        <FormField label="Priority"><RadioButtonGroup disabled={readOnly} value={sink.aster?.priority ?? 'routine'} options={['routine', 'priority', 'immediate', 'flash'].map((value) => ({ value, label: value }))} onChange={(value) => onChange({ ...data, sink: { ...sink, aster: { topic: '', scope: '', ...sink.aster, priority: value as 'routine' | 'priority' | 'immediate' | 'flash' } } })} /></FormField>
        <FormField label="Logical key"><Input disabled={readOnly} value={sink.aster?.logicalKey ?? ''} onChange={(event) => onChange({ ...data, sink: { ...sink, aster: { topic: '', scope: '', ...sink.aster, logicalKey: event.currentTarget.value, logicalKeyField: undefined } } })} /></FormField>
        <FormField label="Logical key field (instead of static key)"><Input disabled={readOnly} value={sink.aster?.logicalKeyField ?? ''} onChange={(event) => onChange({ ...data, sink: { ...sink, aster: { topic: '', scope: '', ...sink.aster, logicalKeyField: event.currentTarget.value, logicalKey: undefined } } })} /></FormField>
        <FormField label="Token file path"><Input disabled={readOnly} value={sink.aster?.tokenFile ?? ''} placeholder="Uses GOFI_ASTER_TOKEN_FILE when empty" onChange={(event) => onChange({ ...data, sink: { ...sink, aster: { topic: '', scope: '', ...sink.aster, tokenFile: event.currentTarget.value, token: undefined } } })} /></FormField>
        <FormField label="Bearer token (instead of token file)"><Input type="password" disabled={readOnly} value={sink.aster?.token ?? ''} autoComplete="off" onChange={(event) => onChange({ ...data, sink: { ...sink, aster: { topic: '', scope: '', ...sink.aster, token: event.currentTarget.value, tokenFile: undefined } } })} /></FormField>
        <FormField label="Timeout (seconds)"><Input type="number" min={1} max={30} disabled={readOnly} value={sink.aster?.timeoutSeconds ?? ''} placeholder="10" onChange={(event) => onChange({ ...data, sink: { ...sink, aster: { topic: '', scope: '', ...sink.aster, timeoutSeconds: Number(event.currentTarget.value) || undefined } } })} /></FormField>
        <FormField label="Body mode"><RadioButtonGroup disabled={readOnly} value={sink.bodyMode ?? 'raw'} options={BODY_MODE_OPTIONS} onChange={(value) => onChange({ ...data, sink: { ...sink, bodyMode: value as 'raw' | 'wrapped' } })} /></FormField>
      </>}

      {sink.type === 'loki' && <FormField label="Static stream labels" description="The app=gofi and listener labels are included by default. Use a Loki base URL or its push endpoint."><KeyValueEditor disabled={readOnly} value={sink.labels ?? {}} onChange={labels => onChange({ ...data, sink: { ...sink, labels } })} keyPlaceholder="Label name" valuePlaceholder="Label value" /></FormField>}
      {['http', 'loki'].includes(sink.type) && (
        <>
          {sink.type === 'http' && <>
          <FormField label="Method">
            <Select
              disabled={readOnly}
              value={sink.method || 'POST'}
              onChange={(v) => onChange({ ...data, sink: { ...sink, method: v } })}
              options={HTTP_METHOD_OPTIONS}
            />
          </FormField>

          <FormField label="Content-Type">
            <Input
              disabled={readOnly}
              value={sink.contentType ?? ''}
              onChange={(e) => onChange({ ...data, sink: { ...sink, contentType: e.currentTarget.value } })}
              placeholder="application/json"
            />
          </FormField>

          </>}
          <FormField label="Body Mode">
            <RadioButtonGroup
              disabled={readOnly}
              value={sink.bodyMode || 'wrapped'}
              onChange={(v) => onChange({ ...data, sink: { ...sink, bodyMode: v as 'wrapped' | 'raw' } })}
              options={BODY_MODE_OPTIONS}
            />
          </FormField>

          <FormField label="Headers">
            <KeyValueEditor
              disabled={readOnly}
              value={sink.headers ?? {}}
              onChange={(headers) => onChange({ ...data, sink: { ...sink, headers } })}
              keyPlaceholder="Header name"
              valuePlaceholder="Header value"
            />
          </FormField>

          <FormField label="Auth Type">
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
              options={HTTP_AUTH_TYPE_OPTIONS}
            />
          </FormField>

          {sink.auth?.type === 'bearer' && (
            <FormField label="Token">
              <Input
                disabled={readOnly}
                value={sink.auth.token ?? ''}
                onChange={(e) =>
                  onChange({ ...data, sink: { ...sink, auth: { ...sink.auth, type: 'bearer', token: e.currentTarget.value } } })
                }
                placeholder="Bearer token"
              />
            </FormField>
          )}

          {sink.auth?.type === 'basic' && (
            <>
              <FormField label="Username">
                <Input
                  disabled={readOnly}
                  value={sink.auth.username ?? ''}
                  onChange={(e) =>
                    onChange({ ...data, sink: { ...sink, auth: { ...sink.auth, type: 'basic', username: e.currentTarget.value } } })
                  }
                  placeholder="Username"
                />
              </FormField>
              <FormField label="Password">
                <Input
                  type="password"
                  disabled={readOnly}
                  value={sink.auth.password ?? ''}
                  onChange={(e) =>
                    onChange({ ...data, sink: { ...sink, auth: { ...sink.auth, type: 'basic', password: e.currentTarget.value } } })
                  }
                  placeholder="Password"
                />
              </FormField>
            </>
          )}

          {sink.auth?.type === 'apiKey' && (
            <>
              <FormField label="Header Name">
                <Input
                  disabled={readOnly}
                  value={sink.auth.headerName ?? ''}
                  onChange={(e) =>
                    onChange({ ...data, sink: { ...sink, auth: { ...sink.auth, type: 'apiKey', headerName: e.currentTarget.value } } })
                  }
                  placeholder="X-API-Key"
                />
              </FormField>
              <FormField label="API Key">
                <Input
                  disabled={readOnly}
                  value={sink.auth.apiKey ?? ''}
                  onChange={(e) =>
                    onChange({ ...data, sink: { ...sink, auth: { ...sink.auth, type: 'apiKey', apiKey: e.currentTarget.value } } })
                  }
                  placeholder="API key value"
                />
              </FormField>
            </>
          )}
        </>
      )}

      {sink.type === 'nats' && (
        <>
          <FormField label="Subject">
            <Input
              disabled={readOnly}
              value={sink.subject ?? ''}
              onChange={(e) => onChange({ ...data, sink: { ...sink, subject: e.currentTarget.value } })}
              placeholder="ingress.events"
            />
          </FormField>

          <ControlledCollapse label="Authentication (optional)" isOpen={hasNatsAuthValues(sink.natsAuth)}>
            <NatsAuthFields
              natsAuth={sink.natsAuth}
              readOnly={readOnly}
              onChange={(natsAuth) => onChange({ ...data, sink: { ...sink, natsAuth } })}
            />
          </ControlledCollapse>
        </>
      )}
      <EncodeSection encoding={sink.encoding} readOnly={readOnly} onChange={(encoding) => onChange({ ...data, sink: { ...sink, encoding } })} />
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
    <div>
      <FormField label="Username">
        <Input
          disabled={readOnly}
          value={auth.username ?? ''}
          onChange={(e) => onChange({ ...auth, username: e.currentTarget.value })}
          placeholder="Username"
        />
      </FormField>

      <FormField label="Password">
        <Input
          type="password"
          disabled={readOnly}
          value={auth.password ?? ''}
          onChange={(e) => onChange({ ...auth, password: e.currentTarget.value })}
          placeholder="Password"
        />
      </FormField>

      <FormField label="Token">
        <Input
          disabled={readOnly}
          value={auth.token ?? ''}
          onChange={(e) => onChange({ ...auth, token: e.currentTarget.value })}
          placeholder="Auth token"
        />
      </FormField>

      <FormField label="NKey Seed">
        <Input
          disabled={readOnly}
          value={auth.nkeySeed ?? ''}
          onChange={(e) => onChange({ ...auth, nkeySeed: e.currentTarget.value })}
          placeholder="SUAC5..."
        />
      </FormField>

      <FormField label="Creds File Contents">
        <TextArea
          disabled={readOnly}
          rows={4}
          className={styles.mono}
          value={auth.creds ?? ''}
          onChange={(e) => onChange({ ...auth, creds: e.currentTarget.value })}
          placeholder={'-----BEGIN NATS USER JWT-----\n...\n------END NATS USER JWT------'}
        />
      </FormField>

      <FormField label="TLS Client Cert">
        <TextArea
          disabled={readOnly}
          rows={4}
          className={styles.mono}
          value={auth.tlsCert ?? ''}
          onChange={(e) => onChange({ ...auth, tlsCert: e.currentTarget.value })}
          placeholder="-----BEGIN CERTIFICATE-----"
        />
      </FormField>

      <FormField label="TLS Client Key">
        <TextArea
          disabled={readOnly}
          rows={4}
          className={styles.mono}
          value={auth.tlsKey ?? ''}
          onChange={(e) => onChange({ ...auth, tlsKey: e.currentTarget.value })}
          placeholder="-----BEGIN PRIVATE KEY-----"
        />
      </FormField>

      <FormField label="TLS CA Cert">
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
        label="Insecure Skip Verify"
        description="Skip TLS certificate verification (not recommended)"
      />
    </div>
  )
}

const getStyles = (theme: GrafanaTheme2) => ({
  panel: css({ width: '100%', flexShrink: 0, display: 'flex', flexDirection: 'column', minHeight: 0, background: theme.colors.background.primary, borderLeft: `1px solid ${theme.colors.border.weak}`, [theme.breakpoints.down('md')]: { width: '100%', position: 'absolute', inset: 0, zIndex: 5 } }),
  header: css({ display: 'flex', alignItems: 'center', justifyContent: 'space-between', padding: theme.spacing(1, 1, 1, 2), borderBottom: `1px solid ${theme.colors.border.weak}`, h2: { margin: 0 } }),
  body: css({ flex: 1, minHeight: 0, overflowY: 'auto', padding: theme.spacing(2) }),
  sectionBody: css({ marginTop: theme.spacing(2) }),
  block: css({ marginBottom: theme.spacing(2) }),
  mono: css({ fontFamily: theme.typography.fontFamilyMonospace, fontSize: theme.typography.bodySmall.fontSize }),
})

/** How the message becomes the bytes this sink sends: a format, then encoders
 * (built-in or @gofi.encoder) in order. Unset keeps the sink's default payload. */
function EncodeSection({ encoding, readOnly, onChange }: { encoding?: Encoding; readOnly?: boolean; onChange: (encoding: Encoding | undefined) => void }) {
  const format = encoding?.format ?? (encoding ? 'wrapped' : '')
  const encoders = encoding?.encoders ?? []
  const set = (next: { format: string; encoders: CodecStep[] }) => onChange(!next.format && !next.encoders.length ? undefined : { format: (next.format || 'wrapped') as Encoding['format'], encoders: next.encoders })
  return <ControlledCollapse label={`Encode${encoding ? ` · ${format}${encoders.length ? ` → ${codecChain(encoders)}` : ''}` : ''}`} isOpen={!!encoding}>
    <Hint>Serialize the message, then apply encoders in order before this sink sends it. Leave unset to keep the sink's default payload.</Hint>
    <FormField label="Format">
      <RadioButtonGroup disabled={readOnly} value={format} onChange={(value) => set({ format: value, encoders })} options={[
        { value: '', label: 'Sink default' },
        { value: 'wrapped', label: 'Wrapped JSON', description: 'JSON with id, fields, and meta' },
        { value: 'fields', label: 'Fields JSON', description: 'JSON of the parsed fields' },
        { value: 'raw', label: 'Raw frame', description: 'The original frame bytes' },
      ]} />
    </FormField>
    <CodecListEditor role="encoder" steps={encoders} readOnly={readOnly} emptyText="No encoders: the formatted payload is sent as is." onChange={(next) => set({ format: format || (next.length ? 'wrapped' : ''), encoders: next })} />
  </ControlledCollapse>
}
