import { useEffect, useState } from 'react'
import { getEndpoints, getMode, type DeployModeInfo, type DiscoveredEndpoint } from '../../api'
import { RiArrowDownSLine, RiArrowRightSLine } from 'react-icons/ri'
import { Checkbox, DetailSection, FormField, Input, Select, TagsInput, PanelHeader } from '../primitives'
import {
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
import { SinkTargetTools } from './SinkTargetTools'
import { useLibraryFunctions, functionOptions, pythonValue, parsePythonValue } from './python-functions'
import type { FlowNode, IngressNodeData, StageNodeData, SinkNodeData } from './types'
import type { HTTPAuth, NATSAuth } from '../../types'

interface Props {
  node: FlowNode | null
  readOnly?: boolean
  onChangeIngress: (data: IngressNodeData) => void
  onChangeStage: (data: StageNodeData) => void
  onChangeSink: (data: SinkNodeData) => void
  onClose: () => void
}

export function NodeInspector({ node, readOnly, onChangeIngress, onChangeStage, onChangeSink, onClose }: Props) {
  if (!node) return null

  return (
    <aside className="w-80 shrink-0 border-l border-g-border-weak bg-g-primary flex flex-col overflow-y-auto">
      <PanelHeader title={inspectorTitle(node)} onClose={onClose} />
      <div className="p-4">
        {node.type === 'ingress' && (
          <IngressInspector data={node.data} readOnly={readOnly} onChange={onChangeIngress} />
        )}
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
      return 'Ingress'
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
  const { source, framing, parser } = data
  const { functions, error: registryError } = useLibraryFunctions()
  const [deployment, setDeployment] = useState<DeployModeInfo | null>(null)
  useEffect(() => { getMode().then(setDeployment).catch(() => {}) }, [])
  const isKubernetes = deployment?.driverKind === 'kubernetes' || deployment?.mode === 'kubernetes'

  return (
    <DetailSection title="Source">
      <FormField label="Port" className="mb-3">
        <Input
          type="number"
          disabled={readOnly}
          value={source.port || ''}
          onChange={(e) => onChange({ ...data, source: { ...source, port: parseInt(e.target.value, 10) || 0 } })}
          placeholder="9000"
        />
      </FormField>

      <FormField label="Network" className="mb-3">
        <Select
          disabled={readOnly}
          value={source.network}
          onChange={(v) => onChange({ ...data, source: { ...source, network: v } })}
          options={NETWORK_OPTIONS}
        />
      </FormField>

      <FormField label="Python listener hook (optional)" className="mb-3"><Select disabled={readOnly} value={source.python ? pythonValue(source.python) : ''} options={[{ value: '', label: 'Built-in socket listener' }, ...functionOptions(functions, 'listener', source.python)]} onChange={value => onChange({ ...data, source: { ...source, python: parsePythonValue(value) } })} /></FormField>
      {registryError && <p className="text-xs text-amber-300 mb-3">Python function registry unavailable.</p>}
      {isKubernetes && <>
      <FormField label="Kubernetes exposure" className="mb-3">
        <Select disabled={readOnly} value={source.service?.type ?? ''} options={[{ value: '', label: 'Internal listener only' }, { value: 'ClusterIP', label: 'ClusterIP service' }, { value: 'NodePort', label: 'NodePort service' }]} onChange={(value) => onChange({ ...data, source: { ...source, service: value ? { ...source.service, type: value as 'ClusterIP' | 'NodePort' } : undefined } })} />
      </FormField>
      {source.service && <FormField label="Service port (defaults to listener port)" className="mb-3"><Input disabled={readOnly} type="number" min={1} max={65535} value={source.service.port ?? ''} placeholder={String(source.port || 9000)} onChange={(event) => onChange({ ...data, source: { ...source, service: { ...source.service!, port: Number(event.target.value) || undefined } } })} /></FormField>}
      {source.service?.type === 'NodePort' && <FormField label="NodePort (blank assigns automatically)" className="mb-3"><Input disabled={readOnly} type="number" min={1} max={65535} value={source.service.nodePort ?? ''} placeholder="30090" onChange={(event) => onChange({ ...data, source: { ...source, service: { ...source.service!, nodePort: Number(event.target.value) || undefined } } })} /></FormField>}
      </>}
      <p className="text-xs text-g-text-secondary mb-4 leading-relaxed">{isKubernetes ? 'Expose this listener through a ClusterIP or NodePort service.' : deployment?.mode === 'binary' ? 'Binary listeners bind a local TCP/UDP port.' : deployment ? 'Container listeners bind a TCP/UDP port. Publish it through your container runtime.' : 'Checking deployment mode…'}</p>

      <FormField label="Framing" className="mb-3">
        <Select
          disabled={readOnly}
          value={framing.mode}
          onChange={(v) => onChange({ ...data, framing: { ...framing, mode: v } })}
          options={FRAMING_OPTIONS}
        />
      </FormField>

      {framing.mode === 'delimited' && (
        <FormField label="Delimiter" className="mb-3">
          <Input
            disabled={readOnly}
            value={framing.delimiter ?? ''}
            onChange={(e) => onChange({ ...data, framing: { ...framing, delimiter: e.target.value } })}
            placeholder="</event>"
          />
        </FormField>
      )}

      {framing.mode === 'length-prefix' && <FormField label="Prefix bytes" className="mb-3"><Select disabled={readOnly} value={String(framing.prefixLen ?? 4)} options={[{ value: '1', label: '1 byte' }, { value: '2', label: '2 bytes' }, { value: '4', label: '4 bytes' }]} onChange={(value) => onChange({ ...data, framing: { ...framing, prefixLen: Number(value) } })} /></FormField>}
      <FormField label="Parser" className="mb-3">
        <Select
          disabled={readOnly}
          value={parser.format === 'python' ? pythonValue(parser.python) : parser.format}
          onChange={(v) => onChange({ ...data, parser: { ...parser, format: v.startsWith('python:') ? 'python' : v, python: parsePythonValue(v) } })}
          options={[...PARSER_OPTIONS, ...(parser.format === 'python' && !parser.python ? [{ value: 'python', label: 'Choose a Python parser…' }] : []), ...functionOptions(functions, 'parser', parser.python)]}
        />
      </FormField>
      <FormField label="Decoders (in order)" className="mb-3">
        <Input disabled={readOnly} value={(parser.decoders ?? []).join(', ')} placeholder="base64, gzip" onChange={(event) => onChange({ ...data, parser: { ...parser, decoders: event.target.value.split(',').map((value) => value.trim()) } })} />
      </FormField>
      <p className="text-xs text-g-text-secondary mb-4 leading-relaxed">Apply base64, base64url, hex, gzip, zlib, or url before parsing. Comma-separated; for base64 of gzip data, use base64, gzip. Raw traffic is preserved.</p>
      <FormField label="Decoded size limit (bytes)" className="mb-3"><Input type="number" min={1} disabled={readOnly} value={parser.maxDecodedBytes ?? ''} placeholder="1048576" onChange={(event) => onChange({ ...data, parser: { ...parser, maxDecodedBytes: Number(event.target.value) || undefined } })} /></FormField>
      <FormField label="Decode / parse errors"><Select disabled={readOnly} value={parser.errorPolicy ?? 'continue'} onChange={(value) => onChange({ ...data, parser: { ...parser, errorPolicy: value as 'continue' | 'drop' } })} options={[{ value: 'continue', label: 'Continue and record parse error' }, { value: 'drop', label: 'Drop and send to dead-letter sink' }]} /></FormField>
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
    case 'python':
      return <DetailSection title="Python Transformer"><FormField label="Library function"><Select disabled={readOnly} value={pythonValue(spec.python)} options={[{ value: 'python', label: 'Choose a transformer…' }, ...functionOptions(functions, 'transformer', spec.python)]} onChange={value => onChange({ ...data, spec: { ...spec, python: parsePythonValue(value) } })} /></FormField><p className="mt-3 text-xs text-g-text-secondary">Functions run in GoFi's separate Python worker.</p>{registryError && <p className="mt-2 text-xs text-amber-300">Python function registry unavailable.</p>}</DetailSection>
    case 'filter':
      return (
        <DetailSection title="Filter">
          <FormField label="Expression library" className="mb-3"><Select disabled={readOnly} value={spec.type ?? 'cel'} onChange={(value) => onChange({ ...data, spec: { ...spec, type: value } })} options={[{ value: 'cel', label: 'Common Expression Language (CEL)' }, { value: 'regex', label: 'Regular expression (RE2)' }]} /></FormField>
          <FormField label={spec.type === 'regex' ? 'Regular expression' : 'CEL expression'} className="mb-3"><Input disabled={readOnly} value={spec.expression ?? ''} onChange={(event) => onChange({ ...data, spec: { ...spec, expression: event.target.value } })} placeholder={spec.type === 'regex' ? '^event-' : 'fields["type"] == "a-f-G"'} /></FormField>
          {spec.type === 'regex' && <FormField label="Field (blank uses original raw frame)" className="mb-3"><Input disabled={readOnly} value={spec.field ?? ''} placeholder="type" onChange={(event) => onChange({ ...data, spec: { ...spec, field: event.target.value } })} /></FormField>}
          <p className="text-xs text-g-text-secondary leading-relaxed">{spec.type === 'regex' ? 'Matching messages continue. Use a parsed string field or match the original frame.' : 'A true result keeps the message. Use fields for parsed data, meta for metadata, and raw for the original frame.'} Test an example in Test flow before deploying.</p>
        </DetailSection>
      )

    case 'transform': {
      const op = spec.transformOp ?? 'set'
      const fieldLabel = op === 'rename' ? 'Source Field' : 'Field'
      return (
        <DetailSection title="Transform">
          <FormField label="Operation" className="mb-3">
            <Select
              disabled={readOnly}
              value={op}
              onChange={(v) => onChange({ ...data, spec: { ...spec, transformOp: v as 'set' | 'rename' | 'remove' } })}
              options={TRANSFORM_OP_OPTIONS}
            />
          </FormField>

          <FormField label={fieldLabel} className="mb-3">
            <Input
              disabled={readOnly}
              value={spec.field ?? ''}
              onChange={(e) => onChange({ ...data, spec: { ...spec, field: e.target.value } })}
              placeholder="fieldName"
            />
          </FormField>

          {op === 'rename' && (
            <FormField label="New Field" className="mb-3">
              <Input
                disabled={readOnly}
                value={spec.newField ?? ''}
                onChange={(e) => onChange({ ...data, spec: { ...spec, newField: e.target.value } })}
                placeholder="newFieldName"
              />
            </FormField>
          )}

          {op === 'set' && (
            <FormField label="Value Expression">
              <Input
                disabled={readOnly}
                value={spec.valueExpr ?? ''}
                onChange={(e) => onChange({ ...data, spec: { ...spec, valueExpr: e.target.value } })}
                placeholder='fields["a"] + fields["b"]'
              />
            </FormField>
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
              onChange={(e) => onChange({ ...data, spec: { ...spec, maxPerSecond: parseInt(e.target.value, 10) || 0 } })}
              placeholder="10"
            />
          </FormField>
        </DetailSection>
      )

    case 'dedup':
      return (
        <DetailSection title="Deduplicate">
          <FormField label="Key Expression" className="mb-3">
            <Input
              disabled={readOnly}
              value={spec.keyExpr ?? ''}
              onChange={(e) => onChange({ ...data, spec: { ...spec, keyExpr: e.target.value } })}
              placeholder='fields["id"]'
            />
          </FormField>

          <FormField label="Window">
            <Input
              disabled={readOnly}
              value={spec.window ?? ''}
              onChange={(e) => onChange({ ...data, spec: { ...spec, window: e.target.value } })}
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
              value={spec.requiredFields ?? []}
              onChange={(requiredFields) => onChange({ ...data, spec: { ...spec, requiredFields } })}
              placeholder="Add a field name..."
            />
          </FormField>
        </DetailSection>
      )

    default:
      return (
        <DetailSection title="Stage">
          <p className="text-sm text-g-text-secondary">Unknown stage kind "{spec.kind}"</p>
        </DetailSection>
      )
  }
}

// Shared visual treatment for plain <textarea> fields used for multi-line
// PEM/creds blobs, since Input has no textarea mode. Mirrors Input's classes.
// Exported so other canvas components (e.g. MockPanel) reuse the exact same
// textarea styling instead of inventing their own.
export const TEXTAREA_CLASSNAME =
  'w-full bg-g-secondary border border-g-border-medium rounded px-3 py-2 text-xs text-g-text font-mono ' +
  'placeholder:text-g-text-disabled focus:border-g-accent-main focus:outline-none focus:ring-2 focus:ring-g-accent-main/40 ' +
  'disabled:opacity-50 disabled:cursor-not-allowed'

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
  const [natsAuthOpen, setNatsAuthOpen] = useState<boolean>(() => hasNatsAuthValues(sink.natsAuth))

  return (
    <DetailSection title="Sink">
      <FormField label="Sink Type" className="mb-3">
        <Select
          disabled={readOnly}
          value={sink.type === 'python' ? pythonValue(sink.python) : sink.type}
          onChange={(v) => onChange({ ...data, sink: v === 'loki' ? { type: 'loki', url: 'http://loki:3100', bodyMode: 'wrapped', when: sink.when } : v === 'zenoh' ? { type: 'zenoh', url: 'http://zenoh:8000', subject: '', contentType: 'application/octet-stream', bodyMode: 'raw', when: sink.when } : v.startsWith('python:') ? { type: 'python', python: parsePythonValue(v), when: sink.when } : { ...sink, type: v, python: undefined, kubernetes: ['tcp', 'udp'].includes(v) ? sink.kubernetes : undefined } })}
          options={[...SINK_OPTIONS, ...(sink.type === 'python' && !sink.python ? [{ value: 'python', label: 'Choose a Python sink…' }] : []), ...functionOptions(functions, 'sink', sink.python)]}
        />
      </FormField>

      {sink.type === 'python' && <p className="mb-3 text-xs text-g-text-secondary">This sink executes in GoFi's separate Python worker.</p>}
      {registryError && <p className="mb-3 text-xs text-amber-300">Python function registry unavailable.</p>}
      {!data.isDeadLetter && (
        <FormField label="Send When (optional)" className="mb-3">
          <Input
            disabled={readOnly}
            value={sink.when ?? ''}
            onChange={(e) => onChange({ ...data, sink: { ...sink, when: e.target.value } })}
            placeholder='fields["severity"] == "critical"  (blank = always send)'
          />
        </FormField>
      )}

      {(['nats', 'http', 'tcp', 'udp', 'aster', 'zenoh', 'loki'].includes(sink.type) && !sink.kubernetes) && (
        <FormField label={sink.type === 'zenoh' ? 'Zenoh REST router URL' : sink.type === 'loki' ? 'Loki URL' : 'URL'} className="mb-3">
          <Input
            disabled={readOnly}
            value={sink.url ?? ''}
            onChange={(e) => onChange({ ...data, sink: { ...sink, url: e.target.value } })}
            placeholder={sink.type === 'nats' ? 'nats://broker:4222' : sink.type === 'tcp' ? 'tcp://application:9000' : sink.type === 'udp' ? 'udp://application:9000' : sink.type === 'zenoh' ? 'http://zenoh:8000' : sink.type === 'aster' ? 'http://127.0.0.1:8181' : 'http://application:8080'}
          />
        </FormField>
      )}

      {!sink.kubernetes && ['tcp', 'udp', 'http', 'nats'].includes(sink.type) && endpoints.length > 0 && !readOnly && <FormField label="Discover a destination" className="mb-3"><Select value="" options={[{ value: '', label: 'Choose an endpoint…' }, ...endpoints.flatMap((endpoint) => endpoint.ports.filter((port) => sink.type === 'udp' ? port.protocol === 'UDP' : port.protocol === 'TCP').map((port) => ({ value: `${sink.type}://${endpoint.host}:${port.port}`, label: `${endpoint.name}${endpoint.namespace ? ` (${endpoint.namespace})` : ''} :${port.port}` })))]} onChange={(value) => { if (value) onChange({ ...data, sink: { ...sink, url: value } }) }} /></FormField>}
      {['tcp', 'udp'].includes(sink.type) && <SinkTargetTools sink={sink} readOnly={readOnly} onChange={value => onChange({ ...data, sink: value })} />}
      {['tcp', 'udp'].includes(sink.type) && <><FormField label="Delimiter appended to each message" className="mb-3"><Input disabled={readOnly} value={sink.delimiter ?? ''} placeholder="Optional delimiter" onChange={(event) => onChange({ ...data, sink: { ...sink, delimiter: event.target.value } })} /></FormField><p className="text-xs text-g-text-secondary mb-4 leading-relaxed">Use a direct destination address or select matching Kubernetes pods. The delimiter is appended to each forwarded message.</p></>}

      {sink.type === 'zenoh' && <><FormField label="Zenoh key expression" className="mb-3"><Input disabled={readOnly} value={sink.subject ?? ''} placeholder="telemetry/sensors" onChange={event => onChange({ ...data, sink: { ...sink, subject: event.target.value } })} /></FormField><FormField label="Content type" className="mb-3"><Input disabled={readOnly} value={sink.contentType ?? 'application/octet-stream'} onChange={event => onChange({ ...data, sink: { ...sink, contentType: event.target.value } })} /></FormField><p className="text-xs text-g-text-secondary mb-4">Publishes raw frames with HTTP PUT through Zenoh's REST plugin. Enable the REST plugin on your router.</p></>}
      {sink.type === 'aster' && <>
        <p className="text-xs text-g-text-secondary mb-4 leading-relaxed">Send through an Aster agent in this workload's network namespace. Its API must stay on loopback.</p>
        <FormField label="Topic" className="mb-3"><Input disabled={readOnly} value={sink.aster?.topic ?? ''} onChange={(event) => onChange({ ...data, sink: { ...sink, aster: { scope: '', ...sink.aster, topic: event.target.value } } })} /></FormField>
        <FormField label="Scope" className="mb-3"><Input disabled={readOnly} value={sink.aster?.scope ?? ''} onChange={(event) => onChange({ ...data, sink: { ...sink, aster: { topic: '', ...sink.aster, scope: event.target.value } } })} /></FormField>
        <FormField label="Priority" className="mb-3"><Select disabled={readOnly} value={sink.aster?.priority ?? 'routine'} options={['routine', 'priority', 'immediate', 'flash'].map((value) => ({ value, label: value }))} onChange={(value) => onChange({ ...data, sink: { ...sink, aster: { topic: '', scope: '', ...sink.aster, priority: value as 'routine' | 'priority' | 'immediate' | 'flash' } } })} /></FormField>
        <FormField label="Logical key" className="mb-3"><Input disabled={readOnly} value={sink.aster?.logicalKey ?? ''} onChange={(event) => onChange({ ...data, sink: { ...sink, aster: { topic: '', scope: '', ...sink.aster, logicalKey: event.target.value, logicalKeyField: undefined } } })} /></FormField>
        <FormField label="Logical key field (instead of static key)" className="mb-3"><Input disabled={readOnly} value={sink.aster?.logicalKeyField ?? ''} onChange={(event) => onChange({ ...data, sink: { ...sink, aster: { topic: '', scope: '', ...sink.aster, logicalKeyField: event.target.value, logicalKey: undefined } } })} /></FormField>
        <FormField label="Token file path" className="mb-3"><Input disabled={readOnly} value={sink.aster?.tokenFile ?? ''} placeholder="Uses GOFI_ASTER_TOKEN_FILE when empty" onChange={(event) => onChange({ ...data, sink: { ...sink, aster: { topic: '', scope: '', ...sink.aster, tokenFile: event.target.value, token: undefined } } })} /></FormField>
        <FormField label="Bearer token (instead of token file)" className="mb-3"><Input type="password" disabled={readOnly} value={sink.aster?.token ?? ''} autoComplete="off" onChange={(event) => onChange({ ...data, sink: { ...sink, aster: { topic: '', scope: '', ...sink.aster, token: event.target.value, tokenFile: undefined } } })} /></FormField>
        <FormField label="Timeout (seconds)" className="mb-3"><Input type="number" min={1} max={30} disabled={readOnly} value={sink.aster?.timeoutSeconds ?? ''} placeholder="10" onChange={(event) => onChange({ ...data, sink: { ...sink, aster: { topic: '', scope: '', ...sink.aster, timeoutSeconds: Number(event.target.value) || undefined } } })} /></FormField>
        <FormField label="Body mode" className="mb-3"><Select disabled={readOnly} value={sink.bodyMode ?? 'raw'} options={BODY_MODE_OPTIONS} onChange={(value) => onChange({ ...data, sink: { ...sink, bodyMode: value as 'raw' | 'wrapped' } })} /></FormField>
      </>}

      {sink.type === 'loki' && <><FormField label="Static stream labels" className="mb-3"><KeyValueEditor disabled={readOnly} value={sink.labels ?? {}} onChange={labels => onChange({ ...data, sink: { ...sink, labels } })} keyPlaceholder="Label name" valuePlaceholder="Label value" /></FormField><p className="text-xs text-g-text-secondary mb-4">The app=gofi and listener labels are included by default. Use a Loki base URL or its push endpoint.</p></>}
      {['http', 'loki'].includes(sink.type) && (
        <>
          {sink.type === 'http' && <>
          <FormField label="Method" className="mb-3">
            <Select
              disabled={readOnly}
              value={sink.method || 'POST'}
              onChange={(v) => onChange({ ...data, sink: { ...sink, method: v } })}
              options={HTTP_METHOD_OPTIONS}
            />
          </FormField>

          <FormField label="Content-Type" className="mb-3">
            <Input
              disabled={readOnly}
              value={sink.contentType ?? ''}
              onChange={(e) => onChange({ ...data, sink: { ...sink, contentType: e.target.value } })}
              placeholder="application/json"
            />
          </FormField>

          </>}
          <FormField label="Body Mode" className="mb-3">
            <Select
              disabled={readOnly}
              value={sink.bodyMode || 'wrapped'}
              onChange={(v) => onChange({ ...data, sink: { ...sink, bodyMode: v as 'wrapped' | 'raw' } })}
              options={BODY_MODE_OPTIONS}
            />
          </FormField>

          <FormField label="Headers" className="mb-3">
            <KeyValueEditor
              disabled={readOnly}
              value={sink.headers ?? {}}
              onChange={(headers) => onChange({ ...data, sink: { ...sink, headers } })}
              keyPlaceholder="Header name"
              valuePlaceholder="Header value"
            />
          </FormField>

          <FormField label="Auth Type" className="mb-3">
            <Select
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
            <FormField label="Token" className="mb-3">
              <Input
                disabled={readOnly}
                value={sink.auth.token ?? ''}
                onChange={(e) =>
                  onChange({ ...data, sink: { ...sink, auth: { ...sink.auth, type: 'bearer', token: e.target.value } } })
                }
                placeholder="Bearer token"
              />
            </FormField>
          )}

          {sink.auth?.type === 'basic' && (
            <>
              <FormField label="Username" className="mb-3">
                <Input
                  disabled={readOnly}
                  value={sink.auth.username ?? ''}
                  onChange={(e) =>
                    onChange({ ...data, sink: { ...sink, auth: { ...sink.auth, type: 'basic', username: e.target.value } } })
                  }
                  placeholder="Username"
                />
              </FormField>
              <FormField label="Password" className="mb-3">
                <Input
                  type="password"
                  disabled={readOnly}
                  value={sink.auth.password ?? ''}
                  onChange={(e) =>
                    onChange({ ...data, sink: { ...sink, auth: { ...sink.auth, type: 'basic', password: e.target.value } } })
                  }
                  placeholder="Password"
                />
              </FormField>
            </>
          )}

          {sink.auth?.type === 'apiKey' && (
            <>
              <FormField label="Header Name" className="mb-3">
                <Input
                  disabled={readOnly}
                  value={sink.auth.headerName ?? ''}
                  onChange={(e) =>
                    onChange({ ...data, sink: { ...sink, auth: { ...sink.auth, type: 'apiKey', headerName: e.target.value } } })
                  }
                  placeholder="X-API-Key"
                />
              </FormField>
              <FormField label="API Key" className="mb-3">
                <Input
                  disabled={readOnly}
                  value={sink.auth.apiKey ?? ''}
                  onChange={(e) =>
                    onChange({ ...data, sink: { ...sink, auth: { ...sink.auth, type: 'apiKey', apiKey: e.target.value } } })
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
          <FormField label="Subject" className="mb-3">
            <Input
              disabled={readOnly}
              value={sink.subject ?? ''}
              onChange={(e) => onChange({ ...data, sink: { ...sink, subject: e.target.value } })}
              placeholder="ingress.events"
            />
          </FormField>

          <div className="mt-2 -mb-2">
            <button
              type="button"
              onClick={() => setNatsAuthOpen((open) => !open)}
              className="flex items-center gap-1 text-xs text-g-text-secondary hover:text-g-text mb-3"
            >
              {natsAuthOpen ? <RiArrowDownSLine size={14} /> : <RiArrowRightSLine size={14} />}
              Authentication (optional)
            </button>

            {natsAuthOpen && (
              <NatsAuthFields
                natsAuth={sink.natsAuth}
                readOnly={readOnly}
                onChange={(natsAuth) => onChange({ ...data, sink: { ...sink, natsAuth } })}
              />
            )}
          </div>
        </>
      )}
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
  const auth = natsAuth ?? {}

  return (
    <div className="flex flex-col">
      <FormField label="Username" className="mb-3">
        <Input
          disabled={readOnly}
          value={auth.username ?? ''}
          onChange={(e) => onChange({ ...auth, username: e.target.value })}
          placeholder="Username"
        />
      </FormField>

      <FormField label="Password" className="mb-3">
        <Input
          type="password"
          disabled={readOnly}
          value={auth.password ?? ''}
          onChange={(e) => onChange({ ...auth, password: e.target.value })}
          placeholder="Password"
        />
      </FormField>

      <FormField label="Token" className="mb-3">
        <Input
          disabled={readOnly}
          value={auth.token ?? ''}
          onChange={(e) => onChange({ ...auth, token: e.target.value })}
          placeholder="Auth token"
        />
      </FormField>

      <FormField label="NKey Seed" className="mb-3">
        <Input
          disabled={readOnly}
          value={auth.nkeySeed ?? ''}
          onChange={(e) => onChange({ ...auth, nkeySeed: e.target.value })}
          placeholder="SUAC5..."
        />
      </FormField>

      <FormField label="Creds File Contents" className="mb-3">
        <textarea
          disabled={readOnly}
          rows={4}
          className={TEXTAREA_CLASSNAME}
          value={auth.creds ?? ''}
          onChange={(e) => onChange({ ...auth, creds: e.target.value })}
          placeholder={'-----BEGIN NATS USER JWT-----\n...\n------END NATS USER JWT------'}
        />
      </FormField>

      <FormField label="TLS Client Cert" className="mb-3">
        <textarea
          disabled={readOnly}
          rows={4}
          className={TEXTAREA_CLASSNAME}
          value={auth.tlsCert ?? ''}
          onChange={(e) => onChange({ ...auth, tlsCert: e.target.value })}
          placeholder="-----BEGIN CERTIFICATE-----"
        />
      </FormField>

      <FormField label="TLS Client Key" className="mb-3">
        <textarea
          disabled={readOnly}
          rows={4}
          className={TEXTAREA_CLASSNAME}
          value={auth.tlsKey ?? ''}
          onChange={(e) => onChange({ ...auth, tlsKey: e.target.value })}
          placeholder="-----BEGIN PRIVATE KEY-----"
        />
      </FormField>

      <FormField label="TLS CA Cert" className="mb-3">
        <textarea
          disabled={readOnly}
          rows={4}
          className={TEXTAREA_CLASSNAME}
          value={auth.tlsCa ?? ''}
          onChange={(e) => onChange({ ...auth, tlsCa: e.target.value })}
          placeholder="-----BEGIN CERTIFICATE-----"
        />
      </FormField>

      <Checkbox
        disabled={readOnly}
        checked={auth.insecureSkipVerify ?? false}
        onChange={(checked) => onChange({ ...auth, insecureSkipVerify: checked })}
        label="Insecure Skip Verify"
        description="Skip TLS certificate verification (not recommended)"
      />
    </div>
  )
}
