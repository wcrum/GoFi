import { css } from '@emotion/css'
import type { GrafanaTheme2 } from '@grafana/data'
import { Field, Input, RadioButtonGroup, Stack, Text, TextArea, useStyles2 } from '@grafana/ui'
import type { FetchSource, FlowSource, HTTPAuth, NATSAuth, StreamSubscribe, SubscribeKind, SubscribeSource } from '../../types'
import { subscribeKindOf } from '../../types'
import { KeyValueEditor } from './KeyValueEditor'

/** Defaults when switching a flow to a pulled source. */
export const DEFAULT_FETCH: FetchSource = { http: { url: '', method: 'GET', every: '30s', timeout: '10s' } }
export const DEFAULT_SUBSCRIBE: Record<SubscribeKind, SubscribeSource> = {
  nats: { nats: { url: 'nats://nats:4222', subject: '' } },
  mqtt: { mqtt: { url: 'tcp://broker:1883', topic: '', qos: 0 } },
  websocket: { websocket: { url: 'wss://' } },
  sse: { sse: { url: 'https://' } },
}

const SUBSCRIBE_OPTIONS: Array<{ value: SubscribeKind; label: string; description: string }> = [
  { value: 'nats', label: 'NATS', description: 'Core NATS subject, optional queue group' },
  { value: 'mqtt', label: 'MQTT', description: 'Topic subscription, QoS 0 or 1' },
  { value: 'websocket', label: 'WebSocket', description: 'Each message is one frame' },
  { value: 'sse', label: 'SSE', description: 'Each Server-Sent Event is one frame' },
]
const METHOD_OPTIONS = ['GET', 'POST', 'PUT', 'PATCH', 'DELETE', 'HEAD'].map((value) => ({ value, label: value }))
const AUTH_OPTIONS = [
  { value: '', label: 'None' },
  { value: 'bearer', label: 'Bearer' },
  { value: 'basic', label: 'Basic' },
  { value: 'apiKey', label: 'API key' },
]

const durationProblem = (value: string | undefined, min = 0): string | undefined => {
  if (!value) return undefined
  const match = /^(\d+(?:\.\d+)?)(ms|s|m|h)$/.exec(value.trim())
  if (!match) return 'Use a duration such as 500ms, 10s, 5m, or 1h.'
  const seconds = Number(match[1]) * ({ ms: 0.001, s: 1, m: 60, h: 3600 } as const)[match[2] as 'ms' | 's' | 'm' | 'h']
  return seconds < min ? `Must be at least ${min}s.` : undefined
}
const splitProblem = (value: string | undefined): string | undefined =>
  !value || /^\$(\.[^.[\]*$]+)*\[\*\]$/.test(value.trim()) ? undefined : 'Use $[*] or a path such as $.items[*].'
const urlProblem = (value: string, schemes: string[]): string | undefined => {
  if (!value) return 'Required.'
  try {
    const url = new URL(value)
    return schemes.includes(url.protocol.replace(':', '')) && url.host ? undefined : `Use ${schemes.map((s) => `${s}://`).join(', ')}.`
  } catch { return 'Enter an absolute URL.' }
}

/** Settings for fetch and subscribe sources. The flow runtime connects
 * outward, so on UDS the flow also needs an egress allowance. */
export function SourceSettings({ source, readOnly, onChange }: { source: FlowSource; readOnly?: boolean; onChange: (source: FlowSource) => void }) {
  const styles = useStyles2(getStyles)
  return <div className={styles.stack}>
    {source.fetch && <FetchSettings fetch={source.fetch} readOnly={readOnly} onChange={(fetch) => onChange({ fetch })} />}
    {source.subscribe && <SubscribeSettings subscribe={source.subscribe} readOnly={readOnly} onChange={(subscribe) => onChange({ subscribe })} />}
    <Text variant="bodySmall" color="secondary">
      Each {source.fetch ? 'response (or split element)' : 'message'} enters Frame as its own stream; keep Frame on Raw for one frame per message. The flow runtime connects outward, so on UDS add an egress allowance for this endpoint.
    </Text>
  </div>
}

function FetchSettings({ fetch, readOnly, onChange }: { fetch: FetchSource; readOnly?: boolean; onChange: (fetch: FetchSource) => void }) {
  const http = fetch.http ?? { url: '' }
  const set = (next: Partial<NonNullable<FetchSource['http']>>) => onChange({ ...fetch, http: { ...http, ...next } })
  const method = (http.method || 'GET').toUpperCase()
  return <>
    <Field label="URL" invalid={!!urlProblem(http.url, ['http', 'https'])} error={urlProblem(http.url, ['http', 'https'])}>
      <Input disabled={readOnly} value={http.url} placeholder="https://api.example/items" onChange={(e) => set({ url: e.currentTarget.value })} />
    </Field>
    <Field label="Method">
      <RadioButtonGroup disabled={readOnly} value={method} options={METHOD_OPTIONS} onChange={(value) => set({ method: value })} />
    </Field>
    <Stack gap={2}>
      <Field label="Every" description="Polls never overlap." invalid={!!durationProblem(http.every, 1)} error={durationProblem(http.every, 1)}>
        <Input disabled={readOnly} width={14} value={http.every ?? ''} placeholder="30s" onChange={(e) => set({ every: e.currentTarget.value || undefined })} />
      </Field>
      <Field label="Timeout" invalid={!!durationProblem(http.timeout)} error={durationProblem(http.timeout)}>
        <Input disabled={readOnly} width={14} value={http.timeout ?? ''} placeholder="10s" onChange={(e) => set({ timeout: e.currentTarget.value || undefined })} />
      </Field>
    </Stack>
    <Field label="Split (optional)" description="One frame per element of a JSON array, e.g. $.items[*]. Leave blank for one frame per response." invalid={!!splitProblem(fetch.split)} error={splitProblem(fetch.split)}>
      <Input disabled={readOnly} value={fetch.split ?? ''} placeholder="$.items[*]" onChange={(e) => onChange({ ...fetch, split: e.currentTarget.value || undefined })} />
    </Field>
    {method !== 'GET' && method !== 'HEAD' && <Field label="Request body (optional)">
      <TextArea disabled={readOnly} rows={3} value={http.body ?? ''} onChange={(e) => set({ body: e.currentTarget.value || undefined })} />
    </Field>}
    <HTTPAuthFields auth={http.auth} readOnly={readOnly} onChange={(auth) => set({ auth })} />
    <Field label="Headers">
      <KeyValueEditor disabled={readOnly} value={http.headers ?? {}} keyPlaceholder="Header" valuePlaceholder="Value" onChange={(headers) => set({ headers: Object.keys(headers).length ? headers : undefined })} />
    </Field>
  </>
}

function SubscribeSettings({ subscribe, readOnly, onChange }: { subscribe: SubscribeSource; readOnly?: boolean; onChange: (subscribe: SubscribeSource) => void }) {
  const kind = subscribeKindOf(subscribe)
  return <>
    <Field label="Stream">
      <RadioButtonGroup disabled={readOnly} value={kind} options={SUBSCRIBE_OPTIONS} onChange={(value) => onChange(DEFAULT_SUBSCRIBE[value])} />
    </Field>
    {kind === 'nats' && subscribe.nats && <NATSSettings nats={subscribe.nats} readOnly={readOnly} onChange={(nats) => onChange({ nats })} />}
    {kind === 'mqtt' && subscribe.mqtt && <MQTTSettings mqtt={subscribe.mqtt} readOnly={readOnly} onChange={(mqtt) => onChange({ mqtt })} />}
    {kind === 'websocket' && subscribe.websocket && <StreamSettings kind="websocket" stream={subscribe.websocket} readOnly={readOnly} onChange={(websocket) => onChange({ websocket })} />}
    {kind === 'sse' && subscribe.sse && <StreamSettings kind="sse" stream={subscribe.sse} readOnly={readOnly} onChange={(sse) => onChange({ sse })} />}
  </>
}

function NATSSettings({ nats, readOnly, onChange }: { nats: NonNullable<SubscribeSource['nats']>; readOnly?: boolean; onChange: (nats: NonNullable<SubscribeSource['nats']>) => void }) {
  const auth: NATSAuth = nats.auth ?? {}
  const setAuth = (next: Partial<NATSAuth>) => {
    const merged = { ...auth, ...next }
    onChange({ ...nats, auth: Object.values(merged).some(Boolean) ? merged : undefined })
  }
  return <>
    <Field label="Server URL" invalid={!!urlProblem(nats.url, ['nats', 'tls'])} error={urlProblem(nats.url, ['nats', 'tls'])}>
      <Input disabled={readOnly} value={nats.url} placeholder="nats://nats:4222" onChange={(e) => onChange({ ...nats, url: e.currentTarget.value })} />
    </Field>
    <Field label="Subject" description="Wildcards: * matches one token, > the rest." invalid={!nats.subject.trim()} error={nats.subject.trim() ? undefined : 'Required.'}>
      <Input disabled={readOnly} value={nats.subject} placeholder="sensors.>" onChange={(e) => onChange({ ...nats, subject: e.currentTarget.value })} />
    </Field>
    <Field label="Queue group (optional)" description="Replicas in the same group share messages instead of each receiving all.">
      <Input disabled={readOnly} value={nats.queue ?? ''} placeholder="gofi" onChange={(e) => onChange({ ...nats, queue: e.currentTarget.value || undefined })} />
    </Field>
    <Stack gap={2}>
      <Field label="Username"><Input disabled={readOnly} value={auth.username ?? ''} onChange={(e) => setAuth({ username: e.currentTarget.value || undefined })} /></Field>
      <Field label="Password"><Input disabled={readOnly} type="password" value={auth.password ?? ''} onChange={(e) => setAuth({ password: e.currentTarget.value || undefined })} /></Field>
    </Stack>
    <Field label="Token"><Input disabled={readOnly} type="password" value={auth.token ?? ''} onChange={(e) => setAuth({ token: e.currentTarget.value || undefined })} /></Field>
    <Field label="Credentials file contents (.creds)" description="Takes precedence over the other credentials. TLS options are shared with the NATS sink.">
      <TextArea disabled={readOnly} rows={3} value={auth.creds ?? ''} onChange={(e) => setAuth({ creds: e.currentTarget.value || undefined })} />
    </Field>
  </>
}

function MQTTSettings({ mqtt, readOnly, onChange }: { mqtt: NonNullable<SubscribeSource['mqtt']>; readOnly?: boolean; onChange: (mqtt: NonNullable<SubscribeSource['mqtt']>) => void }) {
  const schemes = ['tcp', 'ssl', 'tls', 'mqtt', 'mqtts', 'ws', 'wss']
  return <>
    <Field label="Broker URL" invalid={!!urlProblem(mqtt.url, schemes)} error={urlProblem(mqtt.url, schemes)}>
      <Input disabled={readOnly} value={mqtt.url} placeholder="tcp://broker:1883" onChange={(e) => onChange({ ...mqtt, url: e.currentTarget.value })} />
    </Field>
    <Field label="Topic" description="Wildcards: + matches one level, # the rest." invalid={!mqtt.topic.trim()} error={mqtt.topic.trim() ? undefined : 'Required.'}>
      <Input disabled={readOnly} value={mqtt.topic} placeholder="devices/+/telemetry" onChange={(e) => onChange({ ...mqtt, topic: e.currentTarget.value })} />
    </Field>
    <Field label="QoS">
      <RadioButtonGroup disabled={readOnly} value={String(mqtt.qos ?? 0)} options={[{ value: '0', label: '0 · at most once' }, { value: '1', label: '1 · at least once' }]} onChange={(value) => onChange({ ...mqtt, qos: Number(value) })} />
    </Field>
    <Field label="Client ID (optional)" description="Defaults to gofi-<flow name>.">
      <Input disabled={readOnly} value={mqtt.clientId ?? ''} onChange={(e) => onChange({ ...mqtt, clientId: e.currentTarget.value || undefined })} />
    </Field>
    <Stack gap={2}>
      <Field label="Username"><Input disabled={readOnly} value={mqtt.username ?? ''} onChange={(e) => onChange({ ...mqtt, username: e.currentTarget.value || undefined })} /></Field>
      <Field label="Password"><Input disabled={readOnly} type="password" value={mqtt.password ?? ''} onChange={(e) => onChange({ ...mqtt, password: e.currentTarget.value || undefined })} /></Field>
    </Stack>
  </>
}

function StreamSettings({ kind, stream, readOnly, onChange }: { kind: 'websocket' | 'sse'; stream: StreamSubscribe; readOnly?: boolean; onChange: (stream: StreamSubscribe) => void }) {
  const schemes = kind === 'websocket' ? ['ws', 'wss'] : ['http', 'https']
  return <>
    <Field label="URL" description={kind === 'sse' ? 'Reconnects resend Last-Event-ID so the server can resume.' : 'Reconnects with capped backoff.'} invalid={!!urlProblem(stream.url, schemes)} error={urlProblem(stream.url, schemes)}>
      <Input disabled={readOnly} value={stream.url} placeholder={kind === 'websocket' ? 'wss://feed.example/ws' : 'https://feed.example/events'} onChange={(e) => onChange({ ...stream, url: e.currentTarget.value })} />
    </Field>
    {kind === 'websocket' && <Field label="Send after connecting (optional)" description="A text message such as a subscription request.">
      <TextArea disabled={readOnly} rows={2} value={stream.send ?? ''} placeholder='{"subscribe": "telemetry"}' onChange={(e) => onChange({ ...stream, send: e.currentTarget.value || undefined })} />
    </Field>}
    <HTTPAuthFields auth={stream.auth} readOnly={readOnly} onChange={(auth) => onChange({ ...stream, auth })} />
    <Field label="Headers">
      <KeyValueEditor disabled={readOnly} value={stream.headers ?? {}} keyPlaceholder="Header" valuePlaceholder="Value" onChange={(headers) => onChange({ ...stream, headers: Object.keys(headers).length ? headers : undefined })} />
    </Field>
  </>
}

/** Same auth shape as the HTTP sink. */
function HTTPAuthFields({ auth, readOnly, onChange }: { auth?: HTTPAuth; readOnly?: boolean; onChange: (auth: HTTPAuth | undefined) => void }) {
  return <>
    <Field label="Authentication">
      <RadioButtonGroup disabled={readOnly} value={auth?.type ?? ''} options={AUTH_OPTIONS} onChange={(type) => onChange(type ? { ...auth, type: type as HTTPAuth['type'] } : undefined)} />
    </Field>
    {auth?.type === 'bearer' && <Field label="Token"><Input disabled={readOnly} type="password" value={auth.token ?? ''} onChange={(e) => onChange({ ...auth, token: e.currentTarget.value })} /></Field>}
    {auth?.type === 'basic' && <Stack gap={2}>
      <Field label="Username"><Input disabled={readOnly} value={auth.username ?? ''} onChange={(e) => onChange({ ...auth, username: e.currentTarget.value })} /></Field>
      <Field label="Password"><Input disabled={readOnly} type="password" value={auth.password ?? ''} onChange={(e) => onChange({ ...auth, password: e.currentTarget.value })} /></Field>
    </Stack>}
    {auth?.type === 'apiKey' && <Stack gap={2}>
      <Field label="Header"><Input disabled={readOnly} value={auth.headerName ?? ''} placeholder="X-API-Key" onChange={(e) => onChange({ ...auth, headerName: e.currentTarget.value || undefined })} /></Field>
      <Field label="Key"><Input disabled={readOnly} type="password" value={auth.apiKey ?? ''} onChange={(e) => onChange({ ...auth, apiKey: e.currentTarget.value })} /></Field>
    </Stack>}
  </>
}

const getStyles = (theme: GrafanaTheme2) => ({
  stack: css({ display: 'flex', flexDirection: 'column', gap: theme.spacing(0.5) }),
})
