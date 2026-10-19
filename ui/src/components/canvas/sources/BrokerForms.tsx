import { Checkbox, Field, Input, RadioButtonGroup, Stack, TagsInput, Text, TextArea } from '@grafana/ui'
import type { AMQPSubscribe, JetStreamSubscribe, KafkaSubscribe, NATSAuth, RedisSubscribe, SourceTLS, SubscribeSource } from '../../../types'
import { hostPortProblem, jetStreamNameProblem, kafkaTopicProblem } from './brokerValidation'
import { durationProblem, urlProblem } from './validation'
import { FieldRow } from './FieldRow'
import { t } from '../../../i18n'

/** Broker subscribe sources: each message is acknowledged only after its
 * pipeline run, so a crash redelivers it (docs/flows.md). */
export type BrokerKind = 'kafka' | 'amqp' | 'jetstream' | 'redis'
export const BROKER_KINDS: BrokerKind[] = ['kafka', 'amqp', 'jetstream', 'redis']
export const isBrokerKind = (kind: string): kind is BrokerKind => (BROKER_KINDS as string[]).includes(kind)

export const DEFAULT_BROKER_SUBSCRIBE: Record<BrokerKind, SubscribeSource> = {
  kafka: { kafka: { brokers: ['kafka:9092'], topics: [] } },
  amqp: { amqp: { url: 'amqp://rabbitmq:5672/', queue: '' } },
  jetstream: { jetstream: { url: 'nats://nats:4222', stream: '' } },
  redis: { redis: { url: 'redis://redis:6379/0', stream: '' } },
}

/** Built at render so descriptions follow the active language. */
export const brokerOptions = (): Array<{ value: BrokerKind; label: string; description: string }> => [
  { value: 'kafka', label: 'Kafka', description: t('gofi.canvas.broker-forms.kafka-description', 'Consumer group; offsets commit after each record') },
  { value: 'amqp', label: 'AMQP', description: t('gofi.canvas.broker-forms.amqp-description', 'RabbitMQ queue or exchange binding; acked after each delivery') },
  { value: 'jetstream', label: 'JetStream', description: t('gofi.canvas.broker-forms.jetstream-description', 'Durable NATS JetStream consumer with explicit acks') },
  { value: 'redis', label: 'Redis', description: t('gofi.canvas.broker-forms.redis-description', 'Stream consumer group (XACK) or Pub/Sub channels') },
]

type FormProps<T> = { value: T; readOnly?: boolean; onChange: (value: T) => void }

/** The form for a broker subscribe source. */
export function BrokerSourceForm({ kind, subscribe, readOnly, onChange }: { kind: BrokerKind; subscribe: SubscribeSource; readOnly?: boolean; onChange: (subscribe: SubscribeSource) => void }) {
  return <>
    {kind === 'kafka' && subscribe.kafka && <KafkaForm value={subscribe.kafka} readOnly={readOnly} onChange={(kafka) => onChange({ kafka })} />}
    {kind === 'amqp' && subscribe.amqp && <AMQPForm value={subscribe.amqp} readOnly={readOnly} onChange={(amqp) => onChange({ amqp })} />}
    {kind === 'jetstream' && subscribe.jetstream && <JetStreamForm value={subscribe.jetstream} readOnly={readOnly} onChange={(jetstream) => onChange({ jetstream })} />}
    {kind === 'redis' && subscribe.redis && <RedisForm value={subscribe.redis} readOnly={readOnly} onChange={(redis) => onChange({ redis })} />}
    <Text variant="bodySmall" color="secondary">
      {t('gofi.canvas.broker-forms.ack-hint', 'Each message is acknowledged after its pipeline run returns (sinks included), so a crash or restart redelivers unacknowledged messages. Sink errors are logged, not retried. Credentials are stored inline in the flow for now.')}
    </Text>
  </>
}

const startOptions = () => [{ value: 'latest', label: t('gofi.canvas.broker-forms.start-latest', 'Latest') }, { value: 'earliest', label: t('gofi.canvas.broker-forms.start-earliest', 'Earliest') }]
const saslOptions = () => [{ value: '', label: t('gofi.common.none', 'None') }, { value: 'PLAIN', label: 'PLAIN' }, { value: 'SCRAM-SHA-256', label: 'SCRAM-SHA-256' }, { value: 'SCRAM-SHA-512', label: 'SCRAM-SHA-512' }]

export function KafkaForm({ value: kafka, readOnly, onChange }: FormProps<KafkaSubscribe>) {
  const set = (next: Partial<KafkaSubscribe>) => onChange({ ...kafka, ...next })
  // Empty lists are omitted on the wire, so they can arrive undefined.
  const brokers = kafka.brokers ?? [], topics = kafka.topics ?? []
  const brokerProblem = brokers.length ? brokers.map(hostPortProblem).find(Boolean) : t('gofi.canvas.broker-forms.add-broker', 'Add at least one broker.')
  const topicProblem = topics.length ? topics.map(kafkaTopicProblem).find(Boolean) : t('gofi.canvas.broker-forms.add-topic', 'Add at least one topic.')
  const sasl = kafka.sasl
  return <>
    <Field noMargin label={t('gofi.canvas.broker-forms.brokers', 'Brokers')} description={t('gofi.canvas.broker-forms.brokers-description', 'Seed brokers as host:port; press Enter after each.')} invalid={!!brokerProblem} error={brokerProblem}>
      <TagsInput disabled={readOnly} tags={brokers} placeholder="kafka:9092" onChange={(brokers) => set({ brokers })} />
    </Field>
    <Field noMargin label={t('gofi.canvas.broker-forms.topics', 'Topics')} invalid={!!topicProblem} error={topicProblem}>
      <TagsInput disabled={readOnly} tags={topics} placeholder={'orders' /* i18n-ignore: example identifier */} onChange={(topics) => set({ topics })} />
    </Field>
    <Field noMargin label={t('gofi.canvas.broker-forms.consumer-group', 'Consumer group (optional)')} description={t('gofi.canvas.broker-forms.consumer-group-description', 'Defaults to gofi-<flow name>. Replicas in one group share partitions.')}>
      <Input disabled={readOnly} value={kafka.group ?? ''} placeholder="gofi-orders" onChange={(e) => set({ group: e.currentTarget.value || undefined })} />
    </Field>
    <Field noMargin label={t('gofi.canvas.broker-forms.start-offset', 'Start offset')} description={t('gofi.canvas.broker-forms.start-offset-description', 'Only for a group with no committed offset; afterwards the group resumes where it left off.')}>
      <RadioButtonGroup disabled={readOnly} value={kafka.startOffset || 'latest'} options={startOptions()} onChange={(startOffset) => set({ startOffset: startOffset === 'latest' ? undefined : startOffset as 'earliest' })} />
    </Field>
    <Field noMargin label="SASL">
      <RadioButtonGroup disabled={readOnly} value={sasl?.mechanism ?? ''} options={saslOptions()}
        onChange={(mechanism) => set({ sasl: mechanism ? { username: '', password: '', ...sasl, mechanism: mechanism as NonNullable<KafkaSubscribe['sasl']>['mechanism'] } : undefined })} />
    </Field>
    {sasl && <FieldRow>
      <Field noMargin label={t('gofi.common.username', 'Username')} invalid={!sasl.username} error={sasl.username ? undefined : t('gofi.common.required', 'Required.')}><Input disabled={readOnly} value={sasl.username} onChange={(e) => set({ sasl: { ...sasl, username: e.currentTarget.value } })} /></Field>
      <Field noMargin label={t('gofi.common.password', 'Password')} invalid={!sasl.password} error={sasl.password ? undefined : t('gofi.common.required', 'Required.')}><Input disabled={readOnly} type="password" value={sasl.password} onChange={(e) => set({ sasl: { ...sasl, password: e.currentTarget.value } })} /></Field>
    </FieldRow>}
    <Checkbox disabled={readOnly} label="TLS" description={t('gofi.canvas.broker-forms.tls-description', 'Encrypt broker connections.')} value={!!kafka.tls?.enabled} onChange={(e) => set({ tls: e.currentTarget.checked ? { ...kafka.tls, enabled: true } : undefined })} />
    {kafka.tls?.enabled && <TLSFields tls={kafka.tls} readOnly={readOnly} onChange={(tls) => set({ tls: { ...tls, enabled: true } })} />}
    <Text variant="bodySmall" color="secondary">{t('gofi.canvas.broker-forms.kafka-egress-hint', 'In a policy-managed cluster, egress is allowed to the seed brokers only; brokers the cluster advertises on other hosts need their own allowance.')}</Text>
  </>
}

export function AMQPForm({ value: amqp, readOnly, onChange }: FormProps<AMQPSubscribe>) {
  const set = (next: Partial<AMQPSubscribe>) => onChange({ ...amqp, ...next })
  const mode = amqp.exchange !== undefined ? 'exchange' : 'queue'
  const isTLS = amqp.url.startsWith('amqps:')
  return <>
    <Field noMargin label={t('gofi.canvas.broker-forms.url', 'URL')} description={t('gofi.canvas.broker-forms.amqp-url-description', 'amqp:// or amqps://, with the vhost as the path.')} invalid={!!urlProblem(amqp.url, ['amqp', 'amqps'])} error={urlProblem(amqp.url, ['amqp', 'amqps'])}>
      <Input disabled={readOnly} value={amqp.url} placeholder="amqp://rabbitmq:5672/" onChange={(e) => set({ url: e.currentTarget.value })} />
    </Field>
    <Field noMargin label={t('gofi.canvas.broker-forms.consume-from', 'Consume from')}>
      <RadioButtonGroup disabled={readOnly} value={mode} options={[{ value: 'queue', label: t('gofi.canvas.broker-forms.queue', 'Queue') }, { value: 'exchange', label: t('gofi.canvas.broker-forms.exchange-binding', 'Exchange binding') }]}
        onChange={(next) => set(next === 'queue' ? { queue: '', exchange: undefined, routingKey: undefined } : { queue: undefined, exchange: '', routingKey: '#' })} />
    </Field>
    {mode === 'queue'
      ? <Field noMargin label={t('gofi.canvas.broker-forms.queue', 'Queue')} description={t('gofi.canvas.broker-forms.queue-description', 'An existing queue. Replicas consuming it share its messages.')} invalid={!amqp.queue?.trim()} error={amqp.queue?.trim() ? undefined : t('gofi.common.required', 'Required.')}>
          <Input disabled={readOnly} value={amqp.queue ?? ''} placeholder={'orders' /* i18n-ignore: example identifier */} onChange={(e) => set({ queue: e.currentTarget.value })} />
        </Field>
      : <FieldRow>
          <Field noMargin label={t('gofi.canvas.broker-forms.exchange', 'Exchange')} description={t('gofi.canvas.broker-forms.exchange-description', 'Bound to a temporary, exclusive queue: each replica gets every message, and nothing is kept while disconnected.')} invalid={!amqp.exchange?.trim()} error={amqp.exchange?.trim() ? undefined : t('gofi.common.required', 'Required.')}>
            <Input disabled={readOnly} value={amqp.exchange ?? ''} placeholder={'events' /* i18n-ignore: example identifier */} onChange={(e) => set({ exchange: e.currentTarget.value })} />
          </Field>
          <Field noMargin label={t('gofi.canvas.broker-forms.routing-key', 'Routing key')}>
            <Input disabled={readOnly} value={amqp.routingKey ?? ''} placeholder="sensor.#" onChange={(e) => set({ routingKey: e.currentTarget.value })} />
          </Field>
        </FieldRow>}
    <Field noMargin label={t('gofi.canvas.broker-forms.prefetch', 'Prefetch')} description={t('gofi.canvas.broker-forms.prefetch-description', 'Unacknowledged deliveries in flight; default 100.')} invalid={(amqp.prefetch ?? 0) < 0 || (amqp.prefetch ?? 0) > 65535} error={t('gofi.canvas.broker-forms.prefetch-range', 'Use 0-65535.')}>
      <Input disabled={readOnly} type="number" value={amqp.prefetch ?? ''} placeholder="100" onChange={(e) => set({ prefetch: e.currentTarget.value ? Number(e.currentTarget.value) : undefined })} />
    </Field>
    <CredentialFields username={amqp.username} password={amqp.password} readOnly={readOnly} hint={t('gofi.canvas.broker-forms.credentials-override-url', 'Overrides credentials in the URL.')} onChange={(username, password) => set({ username, password })} />
    {isTLS && <TLSFields tls={amqp.tls ?? {}} readOnly={readOnly} onChange={(tls) => set({ tls: Object.values(tls).some(Boolean) ? tls : undefined })} />}
  </>
}

const deliverOptions = () => [{ value: 'all', label: t('gofi.common.all', 'All') }, { value: 'new', label: t('gofi.canvas.broker-forms.deliver-new', 'New') }, { value: 'last', label: t('gofi.canvas.broker-forms.deliver-last', 'Last') }, { value: 'last_per_subject', label: t('gofi.canvas.broker-forms.deliver-last-per-subject', 'Last per subject') }]

export function JetStreamForm({ value: js, readOnly, onChange }: FormProps<JetStreamSubscribe>) {
  const set = (next: Partial<JetStreamSubscribe>) => onChange({ ...js, ...next })
  const auth: NATSAuth = js.auth ?? {}
  const setAuth = (next: Partial<NATSAuth>) => {
    const merged = { ...auth, ...next }
    set({ auth: Object.values(merged).some(Boolean) ? merged : undefined })
  }
  return <>
    <Field noMargin label={t('gofi.canvas.broker-forms.server-url', 'Server URL')} invalid={!!urlProblem(js.url, ['nats', 'tls'])} error={urlProblem(js.url, ['nats', 'tls'])}>
      <Input disabled={readOnly} value={js.url} placeholder="nats://nats:4222" onChange={(e) => set({ url: e.currentTarget.value })} />
    </Field>
    <Field noMargin label={t('gofi.canvas.broker-forms.stream', 'Stream')} invalid={!!jetStreamNameProblem(js.stream, true)} error={jetStreamNameProblem(js.stream, true)}>
      <Input disabled={readOnly} value={js.stream} placeholder={'ORDERS' /* i18n-ignore: example identifier */} onChange={(e) => set({ stream: e.currentTarget.value })} />
    </Field>
    <FieldRow>
      <Field noMargin label={t('gofi.canvas.broker-forms.durable-consumer', 'Durable consumer (optional)')} description={t('gofi.canvas.broker-forms.durable-consumer-description', 'Defaults to gofi-<flow name>; created or updated.')} invalid={!!jetStreamNameProblem(js.consumer ?? '', false)} error={jetStreamNameProblem(js.consumer ?? '', false)}>
        <Input disabled={readOnly} value={js.consumer ?? ''} placeholder="gofi-orders" onChange={(e) => set({ consumer: e.currentTarget.value || undefined })} />
      </Field>
      <Field noMargin label={t('gofi.canvas.broker-forms.filter-subject', 'Filter subject (optional)')}>
        <Input disabled={readOnly} value={js.subject ?? ''} placeholder="orders.>" onChange={(e) => set({ subject: e.currentTarget.value || undefined })} />
      </Field>
    </FieldRow>
    <Field noMargin label={t('gofi.canvas.broker-forms.deliver-policy', 'Deliver policy')} description={t('gofi.canvas.broker-forms.deliver-policy-description', 'Fixed when the consumer is first created.')}>
      <RadioButtonGroup disabled={readOnly} value={js.deliverPolicy || 'all'} options={deliverOptions()} onChange={(deliverPolicy) => set({ deliverPolicy: deliverPolicy === 'all' ? undefined : deliverPolicy as JetStreamSubscribe['deliverPolicy'] })} />
    </Field>
    <Field noMargin label={t('gofi.canvas.broker-forms.ack-wait', 'Ack wait')} description={t('gofi.canvas.broker-forms.ack-wait-description', 'Redelivered if the pipeline run takes longer; 1s to 1h.')} invalid={!!durationProblem(js.ackWait, 1)} error={durationProblem(js.ackWait, 1)}>
      <Input disabled={readOnly} value={js.ackWait ?? ''} placeholder="30s" onChange={(e) => set({ ackWait: e.currentTarget.value || undefined })} />
    </Field>
    <CredentialFields username={auth.username} password={auth.password} readOnly={readOnly} onChange={(username, password) => setAuth({ username, password })} />
    <Field noMargin label={t('gofi.canvas.broker-forms.token', 'Token')}><Input disabled={readOnly} type="password" value={auth.token ?? ''} onChange={(e) => setAuth({ token: e.currentTarget.value || undefined })} /></Field>
    <Field noMargin label={t('gofi.canvas.broker-forms.creds-file', 'Credentials file contents (.creds)')} description={t('gofi.canvas.broker-forms.creds-file-description', 'Takes precedence over the other credentials. Same options as the NATS sink.')}>
      <TextArea disabled={readOnly} rows={3} value={auth.creds ?? ''} onChange={(e) => setAuth({ creds: e.currentTarget.value || undefined })} />
    </Field>
  </>
}

export function RedisForm({ value: redis, readOnly, onChange }: FormProps<RedisSubscribe>) {
  const set = (next: Partial<RedisSubscribe>) => onChange({ ...redis, ...next })
  const mode = redis.channels !== undefined ? 'pubsub' : 'stream'
  return <>
    <Field noMargin label={t('gofi.canvas.broker-forms.url', 'URL')} description={t('gofi.canvas.broker-forms.redis-url-description', 'redis:// or rediss://, with the database as the path.')} invalid={!!urlProblem(redis.url, ['redis', 'rediss'])} error={urlProblem(redis.url, ['redis', 'rediss'])}>
      <Input disabled={readOnly} value={redis.url} placeholder="redis://redis:6379/0" onChange={(e) => set({ url: e.currentTarget.value })} />
    </Field>
    <Field noMargin label={t('gofi.canvas.broker-forms.mode', 'Mode')}>
      <RadioButtonGroup disabled={readOnly} value={mode} options={[{ value: 'stream', label: t('gofi.canvas.broker-forms.redis-stream-mode', 'Stream (consumer group)') }, { value: 'pubsub', label: 'Pub/Sub' }]}
        onChange={(next) => onChange(next === 'stream' ? { url: redis.url, username: redis.username, password: redis.password, tls: redis.tls, stream: '' } : { url: redis.url, username: redis.username, password: redis.password, tls: redis.tls, channels: [] })} />
    </Field>
    {mode === 'stream' ? <>
      <Field noMargin label={t('gofi.canvas.broker-forms.stream-key', 'Stream key')} invalid={!redis.stream?.trim()} error={redis.stream?.trim() ? undefined : t('gofi.common.required', 'Required.')}>
        <Input disabled={readOnly} value={redis.stream ?? ''} placeholder={'events' /* i18n-ignore: example identifier */} onChange={(e) => set({ stream: e.currentTarget.value })} />
      </Field>
      <FieldRow>
        <Field noMargin label={t('gofi.canvas.broker-forms.group', 'Group (optional)')} description={t('gofi.canvas.broker-forms.group-description', 'Defaults to gofi-<flow name>; created if missing.')}>
          <Input disabled={readOnly} value={redis.group ?? ''} placeholder="gofi-events" onChange={(e) => set({ group: e.currentTarget.value || undefined })} />
        </Field>
        <Field noMargin label={t('gofi.canvas.broker-forms.consumer', 'Consumer (optional)')} description={t('gofi.canvas.broker-forms.consumer-description', 'Defaults to the runtime hostname.')}>
          <Input disabled={readOnly} value={redis.consumer ?? ''} onChange={(e) => set({ consumer: e.currentTarget.value || undefined })} />
        </Field>
      </FieldRow>
      <Field noMargin label={t('gofi.canvas.broker-forms.new-group-starts-at', 'New group starts at')}>
        <RadioButtonGroup disabled={readOnly} value={redis.start || 'latest'} options={startOptions()} onChange={(start) => set({ start: start === 'latest' ? undefined : start as 'earliest' })} />
      </Field>
      <Field noMargin label={t('gofi.canvas.broker-forms.field', 'Field (optional)')} description={t('gofi.canvas.broker-forms.field-description', 'Emit only this entry field. Leave blank for all fields as one JSON object.')}>
        <Input disabled={readOnly} value={redis.field ?? ''} placeholder={'data' /* i18n-ignore: example identifier */} onChange={(e) => set({ field: e.currentTarget.value || undefined })} />
      </Field>
    </> : <>
      <Field noMargin label={t('gofi.canvas.broker-forms.channels', 'Channels')} description={t('gofi.canvas.broker-forms.channels-description', 'Pub/Sub keeps nothing while disconnected: at-most-once.')} invalid={!redis.channels?.length} error={redis.channels?.length ? undefined : t('gofi.canvas.broker-forms.add-channel', 'Add at least one channel.')}>
        <TagsInput disabled={readOnly} tags={redis.channels ?? []} placeholder={'sensors' /* i18n-ignore: example identifier */} onChange={(channels) => set({ channels })} />
      </Field>
      <Checkbox disabled={readOnly} label={t('gofi.canvas.broker-forms.glob-patterns', 'Glob patterns')} description={t('gofi.canvas.broker-forms.glob-patterns-description', 'PSUBSCRIBE, e.g. sensors.*')} value={!!redis.pattern} onChange={(e) => set({ pattern: e.currentTarget.checked || undefined })} />
    </>}
    <CredentialFields username={redis.username} password={redis.password} readOnly={readOnly} hint={t('gofi.canvas.broker-forms.credentials-override-url', 'Overrides credentials in the URL.')} onChange={(username, password) => set({ username, password })} />
    {redis.url.startsWith('rediss:') && <TLSFields tls={redis.tls ?? {}} readOnly={readOnly} onChange={(tls) => set({ tls: Object.values(tls).some(Boolean) ? tls : undefined })} />}
  </>
}

function CredentialFields({ username, password, hint, readOnly, onChange }: { username?: string; password?: string; hint?: string; readOnly?: boolean; onChange: (username?: string, password?: string) => void }) {
  return <FieldRow>
    <Field noMargin label={t('gofi.common.username', 'Username')} description={hint}><Input disabled={readOnly} value={username ?? ''} onChange={(e) => onChange(e.currentTarget.value || undefined, password)} /></Field>
    <Field noMargin label={t('gofi.common.password', 'Password')}><Input disabled={readOnly} type="password" value={password ?? ''} onChange={(e) => onChange(username, e.currentTarget.value || undefined)} /></Field>
  </FieldRow>
}

function TLSFields({ tls, readOnly, onChange }: { tls: SourceTLS; readOnly?: boolean; onChange: (tls: SourceTLS) => void }) {
  const set = (next: Partial<SourceTLS>) => onChange({ ...tls, ...next })
  const pairProblem = !!tls.cert !== !!tls.key ? t('gofi.canvas.broker-forms.tls-pair', 'Set both certificate and key, or neither.') : undefined
  return <>
    <Field noMargin label={t('gofi.canvas.broker-forms.ca-certificate', 'CA certificate (optional, PEM)')} description={t('gofi.canvas.broker-forms.ca-certificate-description', 'Defaults to the system roots.')}>
      <TextArea disabled={readOnly} rows={2} value={tls.ca ?? ''} onChange={(e) => set({ ca: e.currentTarget.value || undefined })} />
    </Field>
    <FieldRow>
      <Field noMargin label={t('gofi.canvas.broker-forms.client-certificate', 'Client certificate (optional, PEM)')} invalid={!!pairProblem} error={pairProblem}>
        <TextArea disabled={readOnly} rows={2} value={tls.cert ?? ''} onChange={(e) => set({ cert: e.currentTarget.value || undefined })} />
      </Field>
      <Field noMargin label={t('gofi.canvas.broker-forms.client-key', 'Client key (optional, PEM)')}>
        <TextArea disabled={readOnly} rows={2} value={tls.key ?? ''} onChange={(e) => set({ key: e.currentTarget.value || undefined })} />
      </Field>
    </FieldRow>
    <Checkbox disabled={readOnly} label={t('gofi.canvas.broker-forms.skip-verification', 'Skip certificate verification')} description={t('gofi.canvas.broker-forms.skip-verification-description', 'Insecure; for testing only.')} value={!!tls.insecureSkipVerify} onChange={(e) => set({ insecureSkipVerify: e.currentTarget.checked || undefined })} />
  </>
}
