import { useState } from 'react'
import { RiArrowDownSLine, RiArrowRightSLine } from 'react-icons/ri'
import { Checkbox, DetailSection, FormField, Input, Select, TagsInput, PanelHeader } from '@cnak-us/design-system'
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

      <FormField label="Parser">
        <Select
          disabled={readOnly}
          value={parser.format}
          onChange={(v) => onChange({ ...data, parser: { ...parser, format: v } })}
          options={PARSER_OPTIONS}
        />
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

  switch (spec.kind) {
    case 'filter':
      return (
        <DetailSection title="Filter">
          {spec.type === 'cel' ? (
            <FormField label="CEL Expression">
              <Input
                disabled={readOnly}
                value={spec.expression ?? ''}
                onChange={(e) => onChange({ ...data, spec: { ...spec, expression: e.target.value } })}
                placeholder='fields["type"] == "a-f-G"'
              />
            </FormField>
          ) : (
            <p className="text-sm text-g-text-secondary">Unknown filter type "{spec.type}"</p>
          )}
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
  const [natsAuthOpen, setNatsAuthOpen] = useState<boolean>(() => hasNatsAuthValues(sink.natsAuth))

  return (
    <DetailSection title="Sink">
      <FormField label="Sink Type" className="mb-3">
        <Select
          disabled={readOnly}
          value={sink.type}
          onChange={(v) => onChange({ ...data, sink: { ...sink, type: v } })}
          options={SINK_OPTIONS}
        />
      </FormField>

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

      {(sink.type === 'nats' || sink.type === 'http') && (
        <FormField label="URL" className="mb-3">
          <Input
            disabled={readOnly}
            value={sink.url ?? ''}
            onChange={(e) => onChange({ ...data, sink: { ...sink, url: e.target.value } })}
            placeholder={sink.type === 'nats' ? 'nats://localhost:4222' : 'http://localhost:8080'}
          />
        </FormField>
      )}

      {sink.type === 'http' && (
        <>
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
