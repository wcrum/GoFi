import { css } from '@emotion/css'
import type { GrafanaTheme2 } from '@grafana/data'
import { Field, Input, RadioButtonGroup, Select, Text, TextArea, useStyles2 } from '@grafana/ui'
import { FieldRow } from './sources/FieldRow'
import type { FetchSource, FlowSource, HTTPAuth, NATSAuth, StreamSubscribe, SubscribeKind, SubscribeSource } from '../../types'
import { fetchKindOf, subscribeKindOf } from '../../types'
import { KeyValueEditor } from './KeyValueEditor'
import { DEFAULT_S3, S3Form } from './sources/S3Form'
import { DEFAULT_SQL, SQLForm } from './sources/SQLForm'
import { durationProblem, splitProblem, urlProblem } from './sources/validation'
import { brokerOptions, BrokerSourceForm, DEFAULT_BROKER_SUBSCRIBE, isBrokerKind } from './sources/BrokerForms'
import { DEFAULT_ZENOH_SUBSCRIBE, ZENOH_INTEGRATION, ZenohSourceForm } from './sources/ZenohForm'
import { PythonSourceForm } from './sources/PythonSourceForm'
import { t } from '../../i18n'

/** Defaults when switching a flow to a pulled source. */
export const DEFAULT_FETCH: FetchSource = { http: { url: '', method: 'GET', every: '30s', timeout: '10s' } }
export const DEFAULT_SUBSCRIBE: Record<SubscribeKind, SubscribeSource> = {
  nats: { nats: { url: 'nats://nats:4222', subject: '' } },
  mqtt: { mqtt: { url: 'tcp://broker:1883', topic: '', qos: 0 } },
  websocket: { websocket: { url: 'wss://' } },
  sse: { sse: { url: 'https://' } },
  zenoh: { zenoh: DEFAULT_ZENOH_SUBSCRIBE },
  ...DEFAULT_BROKER_SUBSCRIBE,
}

// Option lists are built at render so their text follows the active language.
const subscribeOptions = (): Array<{ value: SubscribeKind; label: string; description: string; icon?: typeof ZENOH_INTEGRATION.icon }> => [
  { value: 'nats', label: 'NATS', description: t('gofi.canvas.source-settings.nats-description', 'Core NATS subject, optional queue group') },
  { value: 'mqtt', label: 'MQTT', description: t('gofi.canvas.source-settings.mqtt-description', 'Topic subscription, QoS 0 or 1') },
  { value: 'websocket', label: 'WebSocket', description: t('gofi.canvas.source-settings.websocket-description', 'Each message is one frame') },
  { value: 'sse', label: 'SSE', description: t('gofi.canvas.source-settings.sse-description', 'Each Server-Sent Event is one frame') },
  { value: 'zenoh', label: ZENOH_INTEGRATION.label, icon: ZENOH_INTEGRATION.icon, description: t('gofi.canvas.source-settings.zenoh-description', 'Key expression through the router REST plugin; each sample is one frame') },
  ...brokerOptions(),
]
const METHOD_OPTIONS = ['GET', 'POST', 'PUT', 'PATCH', 'DELETE', 'HEAD'].map((value) => ({ value, label: value }))
const authOptions = () => [
  { value: '', label: t('gofi.common.none', 'None') },
  { value: 'bearer', label: t('gofi.canvas.source-settings.auth-bearer', 'Bearer') },
  { value: 'basic', label: t('gofi.canvas.source-settings.auth-basic', 'Basic') },
  { value: 'apiKey', label: t('gofi.canvas.source-settings.auth-api-key', 'API key') },
]

/** Settings for fetch, subscribe, and Python sources. Fetch and subscribe
 * connect outward from the flow runtime, so on UDS the flow also needs an
 * egress allowance; a Python source uses its library's egress instead. */
export function SourceSettings({ source, readOnly, onChange }: { source: FlowSource; readOnly?: boolean; onChange: (source: FlowSource) => void }) {
  const styles = useStyles2(getStyles)
  return <div className={styles.stack}>
    {source.fetch && <FetchSettings fetch={source.fetch} readOnly={readOnly} onChange={(fetch) => onChange({ fetch })} />}
    {source.subscribe && <SubscribeSettings subscribe={source.subscribe} readOnly={readOnly} onChange={(subscribe) => onChange({ subscribe })} />}
    {source.python && <PythonSourceForm python={source.python} readOnly={readOnly} onChange={(python) => onChange({ python })} />}
    {source.python
      ? <Text variant="bodySmall" color="secondary">
        {t('gofi.canvas.source-settings.python-source-hint', "Each frame the function returns or yields enters Frame as its own stream; a dict becomes one JSON frame. The function runs in the library's isolated worker, never in the flow runtime or the controller: its network access is the library's egress allowlist (Functions › Network), and this flow needs no egress allowance of its own.")}
      </Text>
      : <Text variant="bodySmall" color="secondary">
        {t('gofi.canvas.source-settings.pulled-source-hint', 'Each {{unit}} enters Frame as its own stream; keep Frame on Raw for one frame per message. The flow runtime connects outward, so in a policy-managed cluster add an egress allowance for this endpoint.', { unit: source.fetch?.sql ? t('gofi.canvas.source-settings.unit-row', 'row') : source.fetch?.s3 ? t('gofi.canvas.source-settings.unit-object', 'object (or split element)') : source.fetch ? t('gofi.canvas.source-settings.unit-response', 'response (or split element)') : t('gofi.canvas.source-settings.unit-message', 'message') })}
      </Text>}
  </div>
}

const fetchOptions = () => [
  { value: 'http', label: 'HTTP', description: t('gofi.canvas.source-settings.fetch-http-description', 'Request a URL each poll') },
  { value: 's3', label: 'S3', description: t('gofi.canvas.source-settings.fetch-s3-description', 'New objects in a bucket prefix (AWS S3, MinIO, Ceph, GCS interop)') },
  { value: 'sql', label: 'SQL', description: t('gofi.canvas.source-settings.fetch-sql-description', 'Rows from a PostgreSQL, MySQL, or SQL Server query') },
] as const

function FetchSettings({ fetch, readOnly, onChange }: { fetch: FetchSource; readOnly?: boolean; onChange: (fetch: FetchSource) => void }) {
  const kind = fetchKindOf(fetch)
  return <>
    <Field noMargin label={t('gofi.canvas.source-settings.fetch-from', 'Fetch from')}>
      <RadioButtonGroup disabled={readOnly} value={kind} options={[...fetchOptions()]} onChange={(value) => onChange(value === 's3' ? { s3: DEFAULT_S3 } : value === 'sql' ? { sql: DEFAULT_SQL } : DEFAULT_FETCH)} />
    </Field>
    {kind === 's3' && <S3Form fetch={fetch} readOnly={readOnly} onChange={onChange} />}
    {kind === 'sql' && <SQLForm fetch={fetch} readOnly={readOnly} onChange={onChange} />}
    {kind === 'http' && <HTTPFetchSettings fetch={fetch} readOnly={readOnly} onChange={onChange} />}
  </>
}

function HTTPFetchSettings({ fetch, readOnly, onChange }: { fetch: FetchSource; readOnly?: boolean; onChange: (fetch: FetchSource) => void }) {
  const http = fetch.http ?? { url: '' }
  const set = (next: Partial<NonNullable<FetchSource['http']>>) => onChange({ ...fetch, http: { ...http, ...next } })
  const method = (http.method || 'GET').toUpperCase()
  return <>
    <Field noMargin label={t('gofi.canvas.source-settings.url', 'URL')} invalid={!!urlProblem(http.url, ['http', 'https'])} error={urlProblem(http.url, ['http', 'https'])}>
      <Input disabled={readOnly} value={http.url} placeholder="https://api.example/items" onChange={(e) => set({ url: e.currentTarget.value })} />
    </Field>
    <Field noMargin label={t('gofi.canvas.source-settings.method', 'Method')}>
      <RadioButtonGroup disabled={readOnly} value={method} options={METHOD_OPTIONS} onChange={(value) => set({ method: value })} />
    </Field>
    <FieldRow min={100}>
      <Field noMargin label={t('gofi.canvas.source-settings.every', 'Every')} description={t('gofi.canvas.source-settings.polls-never-overlap', 'Polls never overlap.')} invalid={!!durationProblem(http.every, 1)} error={durationProblem(http.every, 1)}>
        <Input disabled={readOnly} value={http.every ?? ''} placeholder="30s" onChange={(e) => set({ every: e.currentTarget.value || undefined })} />
      </Field>
      <Field noMargin label={t('gofi.canvas.source-settings.timeout', 'Timeout')} invalid={!!durationProblem(http.timeout)} error={durationProblem(http.timeout)}>
        <Input disabled={readOnly} value={http.timeout ?? ''} placeholder="10s" onChange={(e) => set({ timeout: e.currentTarget.value || undefined })} />
      </Field>
    </FieldRow>
    <Field noMargin label={t('gofi.canvas.source-settings.split', 'Split (optional)')} description={t('gofi.canvas.source-settings.split-http-description', 'One frame per element of a JSON array, e.g. $.items[*]. Leave blank for one frame per response.')} invalid={!!splitProblem(fetch.split)} error={splitProblem(fetch.split)}>
      <Input disabled={readOnly} value={fetch.split ?? ''} placeholder="$.items[*]" onChange={(e) => onChange({ ...fetch, split: e.currentTarget.value || undefined })} />
    </Field>
    {method !== 'GET' && method !== 'HEAD' && <Field noMargin label={t('gofi.canvas.source-settings.request-body', 'Request body (optional)')}>
      <TextArea disabled={readOnly} rows={3} value={http.body ?? ''} onChange={(e) => set({ body: e.currentTarget.value || undefined })} />
    </Field>}
    <HTTPAuthFields auth={http.auth} readOnly={readOnly} onChange={(auth) => set({ auth })} />
    <Field noMargin label={t('gofi.canvas.source-settings.headers', 'Headers')}>
      <KeyValueEditor disabled={readOnly} value={http.headers ?? {}} keyPlaceholder={t('gofi.canvas.source-settings.header-placeholder', 'Header')} valuePlaceholder={t('gofi.canvas.source-settings.value-placeholder', 'Value')} onChange={(headers) => set({ headers: Object.keys(headers).length ? headers : undefined })} />
    </Field>
  </>
}

function SubscribeSettings({ subscribe, readOnly, onChange }: { subscribe: SubscribeSource; readOnly?: boolean; onChange: (subscribe: SubscribeSource) => void }) {
  const kind = subscribeKindOf(subscribe)
  return <>
    <Field noMargin label={t('gofi.canvas.source-settings.stream', 'Stream')}>
      {/* Nine kinds: a Select (with descriptions) instead of a radio row. */}
      <Select disabled={readOnly} value={kind} options={subscribeOptions()} onChange={(option) => option.value && option.value !== kind && onChange(DEFAULT_SUBSCRIBE[option.value])} />
    </Field>
    {kind === 'nats' && subscribe.nats && <NATSSettings nats={subscribe.nats} readOnly={readOnly} onChange={(nats) => onChange({ nats })} />}
    {kind === 'mqtt' && subscribe.mqtt && <MQTTSettings mqtt={subscribe.mqtt} readOnly={readOnly} onChange={(mqtt) => onChange({ mqtt })} />}
    {kind === 'websocket' && subscribe.websocket && <StreamSettings kind="websocket" stream={subscribe.websocket} readOnly={readOnly} onChange={(websocket) => onChange({ websocket })} />}
    {kind === 'sse' && subscribe.sse && <StreamSettings kind="sse" stream={subscribe.sse} readOnly={readOnly} onChange={(sse) => onChange({ sse })} />}
    {isBrokerKind(kind) && <BrokerSourceForm kind={kind} subscribe={subscribe} readOnly={readOnly} onChange={onChange} />}
    {kind === 'zenoh' && subscribe.zenoh && <ZenohSourceForm zenoh={subscribe.zenoh} readOnly={readOnly} onChange={(zenoh) => onChange({ zenoh })} auth={<HTTPAuthFields auth={subscribe.zenoh.auth} readOnly={readOnly} onChange={(auth) => onChange({ zenoh: { ...subscribe.zenoh!, auth } })} />} />}
  </>
}

function NATSSettings({ nats, readOnly, onChange }: { nats: NonNullable<SubscribeSource['nats']>; readOnly?: boolean; onChange: (nats: NonNullable<SubscribeSource['nats']>) => void }) {
  const auth: NATSAuth = nats.auth ?? {}
  const setAuth = (next: Partial<NATSAuth>) => {
    const merged = { ...auth, ...next }
    onChange({ ...nats, auth: Object.values(merged).some(Boolean) ? merged : undefined })
  }
  return <>
    <Field noMargin label={t('gofi.canvas.source-settings.server-url', 'Server URL')} invalid={!!urlProblem(nats.url, ['nats', 'tls'])} error={urlProblem(nats.url, ['nats', 'tls'])}>
      <Input disabled={readOnly} value={nats.url} placeholder="nats://nats:4222" onChange={(e) => onChange({ ...nats, url: e.currentTarget.value })} />
    </Field>
    <Field noMargin label={t('gofi.canvas.source-settings.subject', 'Subject')} description={t('gofi.canvas.source-settings.nats-subject-description', 'Wildcards: * matches one token, > the rest.')} invalid={!nats.subject.trim()} error={nats.subject.trim() ? undefined : t('gofi.common.required', 'Required.')}>
      <Input disabled={readOnly} value={nats.subject} placeholder="sensors.>" onChange={(e) => onChange({ ...nats, subject: e.currentTarget.value })} />
    </Field>
    <Field noMargin label={t('gofi.canvas.source-settings.queue-group', 'Queue group (optional)')} description={t('gofi.canvas.source-settings.queue-group-description', 'Replicas in the same group share messages instead of each receiving all.')}>
      <Input disabled={readOnly} value={nats.queue ?? ''} placeholder={'gofi' /* i18n-ignore: example identifier */} onChange={(e) => onChange({ ...nats, queue: e.currentTarget.value || undefined })} />
    </Field>
    <FieldRow>
      <Field noMargin label={t('gofi.common.username', 'Username')}><Input disabled={readOnly} value={auth.username ?? ''} onChange={(e) => setAuth({ username: e.currentTarget.value || undefined })} /></Field>
      <Field noMargin label={t('gofi.common.password', 'Password')}><Input disabled={readOnly} type="password" value={auth.password ?? ''} onChange={(e) => setAuth({ password: e.currentTarget.value || undefined })} /></Field>
    </FieldRow>
    <Field noMargin label={t('gofi.canvas.source-settings.token', 'Token')}><Input disabled={readOnly} type="password" value={auth.token ?? ''} onChange={(e) => setAuth({ token: e.currentTarget.value || undefined })} /></Field>
    <Field noMargin label={t('gofi.canvas.source-settings.creds-file', 'Credentials file contents (.creds)')} description={t('gofi.canvas.source-settings.nats-creds-description', 'Takes precedence over the other credentials. TLS options are shared with the NATS sink.')}>
      <TextArea disabled={readOnly} rows={3} value={auth.creds ?? ''} onChange={(e) => setAuth({ creds: e.currentTarget.value || undefined })} />
    </Field>
  </>
}

function MQTTSettings({ mqtt, readOnly, onChange }: { mqtt: NonNullable<SubscribeSource['mqtt']>; readOnly?: boolean; onChange: (mqtt: NonNullable<SubscribeSource['mqtt']>) => void }) {
  const schemes = ['tcp', 'ssl', 'tls', 'mqtt', 'mqtts', 'ws', 'wss']
  return <>
    <Field noMargin label={t('gofi.canvas.source-settings.broker-url', 'Broker URL')} invalid={!!urlProblem(mqtt.url, schemes)} error={urlProblem(mqtt.url, schemes)}>
      <Input disabled={readOnly} value={mqtt.url} placeholder="tcp://broker:1883" onChange={(e) => onChange({ ...mqtt, url: e.currentTarget.value })} />
    </Field>
    <Field noMargin label={t('gofi.canvas.source-settings.topic', 'Topic')} description={t('gofi.canvas.source-settings.mqtt-topic-description', 'Wildcards: + matches one level, # the rest.')} invalid={!mqtt.topic.trim()} error={mqtt.topic.trim() ? undefined : t('gofi.common.required', 'Required.')}>
      <Input disabled={readOnly} value={mqtt.topic} placeholder="devices/+/telemetry" onChange={(e) => onChange({ ...mqtt, topic: e.currentTarget.value })} />
    </Field>
    <Field noMargin label="QoS">
      <RadioButtonGroup disabled={readOnly} value={String(mqtt.qos ?? 0)} options={[{ value: '0', label: t('gofi.canvas.source-settings.qos-0', '0 · at most once') }, { value: '1', label: t('gofi.canvas.source-settings.qos-1', '1 · at least once') }]} onChange={(value) => onChange({ ...mqtt, qos: Number(value) })} />
    </Field>
    <Field noMargin label={t('gofi.canvas.source-settings.client-id', 'Client ID (optional)')} description={t('gofi.canvas.source-settings.client-id-description', 'Defaults to gofi-<flow name>.')}>
      <Input disabled={readOnly} value={mqtt.clientId ?? ''} onChange={(e) => onChange({ ...mqtt, clientId: e.currentTarget.value || undefined })} />
    </Field>
    <FieldRow>
      <Field noMargin label={t('gofi.common.username', 'Username')}><Input disabled={readOnly} value={mqtt.username ?? ''} onChange={(e) => onChange({ ...mqtt, username: e.currentTarget.value || undefined })} /></Field>
      <Field noMargin label={t('gofi.common.password', 'Password')}><Input disabled={readOnly} type="password" value={mqtt.password ?? ''} onChange={(e) => onChange({ ...mqtt, password: e.currentTarget.value || undefined })} /></Field>
    </FieldRow>
  </>
}

function StreamSettings({ kind, stream, readOnly, onChange }: { kind: 'websocket' | 'sse'; stream: StreamSubscribe; readOnly?: boolean; onChange: (stream: StreamSubscribe) => void }) {
  const schemes = kind === 'websocket' ? ['ws', 'wss'] : ['http', 'https']
  return <>
    <Field noMargin label={t('gofi.canvas.source-settings.url', 'URL')} description={kind === 'sse' ? t('gofi.canvas.source-settings.sse-reconnect-description', 'Reconnects resend Last-Event-ID so the server can resume.') : t('gofi.canvas.source-settings.websocket-reconnect-description', 'Reconnects with capped backoff.')} invalid={!!urlProblem(stream.url, schemes)} error={urlProblem(stream.url, schemes)}>
      <Input disabled={readOnly} value={stream.url} placeholder={kind === 'websocket' ? 'wss://feed.example/ws' : 'https://feed.example/events'} onChange={(e) => onChange({ ...stream, url: e.currentTarget.value })} />
    </Field>
    {kind === 'websocket' && <Field noMargin label={t('gofi.canvas.source-settings.send-after-connecting', 'Send after connecting (optional)')} description={t('gofi.canvas.source-settings.send-after-connecting-description', 'A text message such as a subscription request.')}>
      <TextArea disabled={readOnly} rows={2} value={stream.send ?? ''} placeholder='{"subscribe": "telemetry"}' onChange={(e) => onChange({ ...stream, send: e.currentTarget.value || undefined })} />
    </Field>}
    <HTTPAuthFields auth={stream.auth} readOnly={readOnly} onChange={(auth) => onChange({ ...stream, auth })} />
    <Field noMargin label={t('gofi.canvas.source-settings.headers', 'Headers')}>
      <KeyValueEditor disabled={readOnly} value={stream.headers ?? {}} keyPlaceholder={t('gofi.canvas.source-settings.header-placeholder', 'Header')} valuePlaceholder={t('gofi.canvas.source-settings.value-placeholder', 'Value')} onChange={(headers) => onChange({ ...stream, headers: Object.keys(headers).length ? headers : undefined })} />
    </Field>
  </>
}

/** Same auth shape as the HTTP sink. */
function HTTPAuthFields({ auth, readOnly, onChange }: { auth?: HTTPAuth; readOnly?: boolean; onChange: (auth: HTTPAuth | undefined) => void }) {
  return <>
    <Field noMargin label={t('gofi.canvas.source-settings.authentication', 'Authentication')}>
      <RadioButtonGroup disabled={readOnly} value={auth?.type ?? ''} options={authOptions()} onChange={(type) => onChange(type ? { ...auth, type: type as HTTPAuth['type'] } : undefined)} />
    </Field>
    {auth?.type === 'bearer' && <Field noMargin label={t('gofi.canvas.source-settings.token', 'Token')}><Input disabled={readOnly} type="password" value={auth.token ?? ''} onChange={(e) => onChange({ ...auth, token: e.currentTarget.value })} /></Field>}
    {auth?.type === 'basic' && <FieldRow>
      <Field noMargin label={t('gofi.common.username', 'Username')}><Input disabled={readOnly} value={auth.username ?? ''} onChange={(e) => onChange({ ...auth, username: e.currentTarget.value })} /></Field>
      <Field noMargin label={t('gofi.common.password', 'Password')}><Input disabled={readOnly} type="password" value={auth.password ?? ''} onChange={(e) => onChange({ ...auth, password: e.currentTarget.value })} /></Field>
    </FieldRow>}
    {auth?.type === 'apiKey' && <FieldRow>
      <Field noMargin label={t('gofi.canvas.source-settings.api-key-header', 'Header')}><Input disabled={readOnly} value={auth.headerName ?? ''} placeholder="X-API-Key" onChange={(e) => onChange({ ...auth, headerName: e.currentTarget.value || undefined })} /></Field>
      <Field noMargin label={t('gofi.canvas.source-settings.api-key', 'Key')}><Input disabled={readOnly} type="password" value={auth.apiKey ?? ''} onChange={(e) => onChange({ ...auth, apiKey: e.currentTarget.value })} /></Field>
    </FieldRow>}
  </>
}

const getStyles = (theme: GrafanaTheme2) => ({
  stack: css({
    display: 'flex', flexDirection: 'column', gap: theme.spacing(2),
    // A bare Checkbox is an inline grid; stretched to the column it would
    // split the spare width between the box and the label.
    '& > label': { alignSelf: 'flex-start' },
  }),
})
