import type { ReactNode } from 'react'
import { Checkbox, Field, Input, RadioButtonGroup, Text } from '@grafana/ui'
import type { IconName } from '@grafana/data'
import type { SinkCfg, ZenohSubscribe } from '../../../types'
import { KeyValueEditor } from '../KeyValueEditor'
import { zenohKeyProblem, zenohOriginProblem } from './zenohValidation'
import { t } from '../../../i18n'

/** One label and glyph for Zenoh wherever it appears (source and sink
 * pickers). A neutral Grafana icon, not a vendor logo. */
export const ZENOH_INTEGRATION = { label: 'Zenoh', icon: 'share-alt' as IconName } as const

export const DEFAULT_ZENOH_SUBSCRIBE: ZenohSubscribe = { url: 'http://zenoh-router:8000', keyExpr: '' }

const kindOptions = () => [
  { value: 'PUT', label: t('gofi.canvas.zenoh-form.kind-put', 'PUT only') },
  { value: 'PUT,DELETE', label: t('gofi.canvas.zenoh-form.kind-put-delete', 'PUT and DELETE') },
  { value: 'DELETE', label: t('gofi.canvas.zenoh-form.kind-delete', 'DELETE only') },
]

/** `subscribe.zenoh`: Server-Sent Events from the router's REST plugin. */
export function ZenohSourceForm({ zenoh, readOnly, onChange, auth }: { zenoh: ZenohSubscribe; readOnly?: boolean; onChange: (zenoh: ZenohSubscribe) => void; auth: ReactNode }) {
  const kinds = (zenoh.kinds?.length ? zenoh.kinds : ['PUT']).join(',')
  return <>
    <Field noMargin label={t('gofi.canvas.zenoh-form.router-url', 'Router REST URL')} description={t('gofi.canvas.zenoh-form.router-url-description', "The Zenoh router's REST plugin origin (port 8000 by default).")} invalid={!!zenohOriginProblem(zenoh.url)} error={zenohOriginProblem(zenoh.url)}>
      <Input disabled={readOnly} value={zenoh.url} placeholder="http://zenoh-router:8000" onChange={(e) => onChange({ ...zenoh, url: e.currentTarget.value })} />
    </Field>
    <Field noMargin label={t('gofi.canvas.zenoh-form.key-expression', 'Key expression')} description={t('gofi.canvas.zenoh-form.key-expression-description', '* matches one chunk, ** any number. Each sample is one frame; meta carries zenoh.key, zenoh.kind, zenoh.encoding, and zenoh.timestamp.')} invalid={!!zenohKeyProblem(zenoh.keyExpr, true)} error={zenohKeyProblem(zenoh.keyExpr, true)}>
      <Input disabled={readOnly} value={zenoh.keyExpr} placeholder="demo/sensors/**" onChange={(e) => onChange({ ...zenoh, keyExpr: e.currentTarget.value })} />
    </Field>
    <Field noMargin label={t('gofi.canvas.zenoh-form.sample-kinds', 'Sample kinds')} description={t('gofi.canvas.zenoh-form.sample-kinds-description', 'A DELETE sample has no value; its frame is {"key", "kind", "timestamp"}.')}>
      <RadioButtonGroup disabled={readOnly} value={kinds} options={kindOptions()} onChange={(value) => onChange({ ...zenoh, kinds: value === 'PUT' ? undefined : value.split(',') as ZenohSubscribe['kinds'] })} />
    </Field>
    <Checkbox disabled={readOnly} value={!!zenoh.query} label={t('gofi.canvas.zenoh-form.read-stored-values', 'Read stored values on connect')} description={t('gofi.canvas.zenoh-form.read-stored-values-description', "Also query the router's storages for current values each time the subscription (re)connects. A value may arrive twice.")} onChange={(e) => onChange({ ...zenoh, query: e.currentTarget.checked || undefined })} />
    {auth}
    <Field noMargin label={t('gofi.canvas.zenoh-form.headers', 'Headers')}>
      <KeyValueEditor disabled={readOnly} value={zenoh.headers ?? {}} keyPlaceholder={t('gofi.canvas.zenoh-form.header-placeholder', 'Header')} valuePlaceholder={t('gofi.canvas.zenoh-form.value-placeholder', 'Value')} onChange={(headers) => onChange({ ...zenoh, headers: Object.keys(headers).length ? headers : undefined })} />
    </Field>
  </>
}

const operationOptions = () => [
  { value: 'PUT', label: t('gofi.canvas.zenoh-form.operation-put', 'PUT (publish)') },
  { value: 'DELETE', label: t('gofi.canvas.zenoh-form.operation-delete', 'DELETE (remove key)') },
]

/** Zenoh-specific sink fields; URL, body mode, headers, and auth are the
 * HTTP sink's. */
export function ZenohSinkFields({ sink, readOnly, onChange }: { sink: SinkCfg; readOnly?: boolean; onChange: (sink: SinkCfg) => void }) {
  const operation = (sink.method || 'PUT').toUpperCase()
  return <>
    <Field label={t('gofi.canvas.zenoh-form.zenoh-key', 'Zenoh key')} description={t('gofi.canvas.zenoh-form.zenoh-key-description', 'One concrete key; wildcards and @ admin paths are rejected.')} invalid={!!zenohKeyProblem(sink.subject ?? '', false)} error={zenohKeyProblem(sink.subject ?? '', false)}>
      <Input disabled={readOnly} value={sink.subject ?? ''} placeholder="telemetry/sensors" onChange={(e) => onChange({ ...sink, subject: e.currentTarget.value })} />
    </Field>
    <Field label={t('gofi.canvas.zenoh-form.operation', 'Operation')}>
      <RadioButtonGroup disabled={readOnly} value={operation} options={operationOptions()} onChange={(value) => onChange({ ...sink, method: value === 'PUT' ? undefined : value })} />
    </Field>
    {operation === 'PUT' && <Field label={t('gofi.canvas.zenoh-form.content-type', 'Content type')} description={t('gofi.canvas.zenoh-form.content-type-description', "Becomes the sample's Zenoh encoding.")}>
      <Input disabled={readOnly} value={sink.contentType ?? 'application/octet-stream'} onChange={(e) => onChange({ ...sink, contentType: e.currentTarget.value })} />
    </Field>}
    <Text variant="bodySmall" color="secondary">{t('gofi.canvas.zenoh-form.sent-with-hint', "Sent with HTTP {{operation}} through the router's REST plugin; enable the plugin on your router.", { operation })}</Text>
  </>
}
