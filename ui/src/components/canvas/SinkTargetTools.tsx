import { useEffect, useRef, useState } from 'react'
import { getMode, resolveTargets, replayTraffic, type DiscoveredEndpoint, type ReplayDelivery } from '../../api'
import type { SinkCfg } from '../../types'
import { css } from '@emotion/css'
import type { GrafanaTheme2 } from '@grafana/data'
import { Alert, Box, Button, Combobox, ControlledCollapse, Field, Input, RadioButtonGroup, Stack, Text, TextArea, useStyles2 } from '@grafana/ui'
import { KeyValueEditor } from './KeyValueEditor'
import { useClusterInfo, useUDSPackages } from '../../kubernetes-api'
import { formatNumber, t } from '../../i18n'

export function SinkTargetTools({ sink, readOnly, onChange }: { sink: SinkCfg; readOnly?: boolean; onChange: (sink: SinkCfg) => void }) {
  const styles = useStyles2(getStyles)
  const [kubernetes, setKubernetes] = useState(false)
  const [endpoints, setEndpoints] = useState<DiscoveredEndpoint[] | null>(null)
  const [deliveries, setDeliveries] = useState<ReplayDelivery[] | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [resolving, setResolving] = useState(false)
  const [sending, setSending] = useState(false)
  const [raw, setRaw] = useState('{"message":"Hello from GoFi"}\n')
  useEffect(() => { getMode().then(mode => setKubernetes(mode.driverKind === 'kubernetes')).catch(() => {}) }, [])
  const cluster = useClusterInfo(kubernetes)
  const uds = useUDSPackages(kubernetes)
  const transport = sink.type === 'udp' || sink.type === 'quic' ? 'UDP' : 'TCP' // QUIC runs over UDP
  // Workloads other Packages admit on this transport: a destination is "connecting to a Package".
  const packageTargets = uds.packages.filter(pkg => pkg.namespace !== uds.namespace).flatMap(pkg => pkg.endpoints
    .filter(endpoint => endpoint.port > 0 && Object.keys(endpoint.selector).length > 0 && (endpoint.kind === 'expose' || endpoint.protocol === transport))
    .map(endpoint => ({ pkg, endpoint, value: `${pkg.namespace}/${pkg.name}/${endpoint.kind}/${JSON.stringify(endpoint.selector)}/${endpoint.port}` })))
  const targetKey = JSON.stringify([sink.kubernetes, sink.url, sink.type])
  const latestTarget = useRef(targetKey)
  latestTarget.current = targetKey
  useEffect(() => { setEndpoints(null); setDeliveries(null); setError(null) }, [targetKey])
  const network = sink.type === 'quic' ? 'quic' : sink.type === 'udp' ? 'udp' : 'tcp'
  async function preview() {
    if (!sink.kubernetes) return
    setResolving(true); setError(null)
    const requested = targetKey
    try { const result = await resolveTargets(sink.kubernetes, network); if (latestTarget.current === requested) setEndpoints(result) }
    catch (reason) { if (latestTarget.current === requested) setError(String(reason)) }
    finally { setResolving(false) }
  }
  async function send() {
    setSending(true); setError(null); setDeliveries(null)
    const requested = targetKey
    try { const result = await replayTraffic({ network, target: sink.kubernetes, url: sink.kubernetes ? undefined : sink.url, raw: new TextEncoder().encode(raw) }); if (latestTarget.current === requested) setDeliveries(result) }
    catch (reason) { if (latestTarget.current === requested) setError(String(reason)) }
    finally { setSending(false) }
  }
  // No margin of its own: the inspector lays its fields out in one gapped column.
  return <Stack direction="column" gap={2}>
    {kubernetes && <Field label={t('gofi.canvas.sink-target-tools.destination-selection', 'Destination selection')} noMargin><Combobox disabled={readOnly} value={sink.kubernetes ? 'selector' : 'url'} options={[{ value: 'url', label: t('gofi.canvas.sink-target-tools.direct-address', 'Direct address') }, { value: 'selector', label: t('gofi.canvas.sink-target-tools.pods-by-label', 'Kubernetes pods by label') }]} onChange={option => onChange({ ...sink, url: option.value === 'selector' ? undefined : sink.url, kubernetes: option.value === 'selector' ? { selector: {}, port: 9000 } : undefined })} /></Field>}
    {sink.kubernetes && <Box borderStyle="solid" borderColor="weak" borderRadius="default" padding={1.5}><Stack direction="column" gap={2}>
      {packageTargets.length > 0 && <Field label={t('gofi.canvas.sink-target-tools.pick-package', 'Pick from a network policy Package')} description={t('gofi.canvas.sink-target-tools.pick-package-description', 'Workloads other Packages expose or admit on {{transport}}. Fills namespace, labels, and port; the Connectivity tab verifies the Package admits GoFi.', { transport })} noMargin>
        <Combobox disabled={readOnly} value={null} placeholder={t('gofi.canvas.sink-target-tools.choose-package-workload', 'Choose a Package workload')}
          options={packageTargets.map(({ pkg, endpoint, value }) => ({ value, label: `${pkg.namespace}/${pkg.name} · ${Object.entries(endpoint.selector).map(([k, v]) => `${k}=${v}`).join(',')} :${endpoint.port}/${endpoint.protocol}`,
            description: endpoint.kind === 'expose' ? t('gofi.canvas.sink-target-tools.exposed-as', 'Exposed as {{host}}', { host: endpoint.host }) : endpoint.description ? t('gofi.canvas.sink-target-tools.admits-with-description', 'Admits {{remote}} · {{description}}', { remote: endpoint.remote, description: endpoint.description }) : t('gofi.canvas.sink-target-tools.admits', 'Admits {{remote}}', { remote: endpoint.remote }) }))}
          onChange={option => { const match = packageTargets.find(target => target.value === option?.value); if (match) onChange({ ...sink, kubernetes: { ...sink.kubernetes!, namespace: match.pkg.namespace === cluster.info?.namespace ? undefined : match.pkg.namespace, selector: match.endpoint.selector, port: match.endpoint.port } }) }} />
      </Field>}
      <Field label={t('gofi.common.namespace', 'Namespace')} description={cluster.info?.clusterWide ? t('gofi.canvas.sink-target-tools.namespace-cluster-wide', 'Pods in other namespaces are reached through a GoFi-managed EndpointSlice in {{namespace}}.', { namespace: cluster.info.namespace }) : t('gofi.canvas.sink-target-tools.namespace-local-only', 'GoFi namespace only: cluster discovery is disabled (chart discovery.clusterWide).')} noMargin>
        <Combobox disabled={readOnly || !cluster.info?.clusterWide} value={sink.kubernetes.namespace || cluster.info?.namespace || ''} createCustomValue
          options={(cluster.info?.namespaces ?? []).map(ns => ({ value: ns, label: ns, description: ns === cluster.info?.namespace ? t('gofi.canvas.sink-target-tools.gofi-namespace', 'GoFi namespace') : undefined }))}
          onChange={option => onChange({ ...sink, kubernetes: { ...sink.kubernetes!, namespace: option.value === cluster.info?.namespace ? undefined : option.value || undefined } })} />
      </Field>
      <Field label={t('gofi.canvas.sink-target-tools.pod-labels', 'Pod labels')} noMargin><KeyValueEditor value={sink.kubernetes.selector} disabled={readOnly} onChange={selector => onChange({ ...sink, kubernetes: { ...sink.kubernetes!, selector } })} /></Field>
      <Field label={t('gofi.canvas.sink-target-tools.delivery', 'Delivery')} description={(sink.kubernetes.delivery ?? 'all') === 'all' ? t('gofi.canvas.sink-target-tools.delivery-all-description', 'Every matching ready Pod receives each message (replay to all).') : t('gofi.canvas.sink-target-tools.delivery-one-description', 'Each message goes to one matching Pod, load-balanced.')} noMargin>
        <RadioButtonGroup disabled={readOnly} value={sink.kubernetes.delivery ?? 'all'} options={[{ value: 'all', label: t('gofi.canvas.sink-target-tools.delivery-all', 'Every Pod') }, { value: 'one', label: t('gofi.canvas.sink-target-tools.delivery-one', 'One Pod') }]} onChange={value => onChange({ ...sink, kubernetes: { ...sink.kubernetes!, delivery: value === 'all' ? undefined : 'one' } })} />
      </Field>
      {sink.kubernetes.namespace && sink.kubernetes.namespace !== cluster.info?.namespace && <Text variant="bodySmall" color="secondary">
        {cluster.info?.manageUdsAllows ? `${t('gofi.canvas.sink-target-tools.egress-allow-added', 'GoFi adds the egress allow to its network policy Package.')} ` : ''}{t('gofi.canvas.sink-target-tools.ingress-allow-needed', 'The target namespace must also allow ingress from {{namespace}} on this port (an Ingress allow with remoteNamespace {{remoteNamespace}} in a policy-managed cluster).', { namespace: cluster.info?.namespace ?? 'GoFi', remoteNamespace: cluster.info?.namespace ?? 'gofi' })}
      </Text>}
      <Field label={t('gofi.canvas.sink-target-tools.destination-port', 'Destination port')} description={t('gofi.canvas.sink-target-tools.destination-port-description', 'Targets refresh as Pods change.')} noMargin><Input type="number" min={1} max={65535} disabled={readOnly} value={sink.kubernetes.port || ''} onChange={event => onChange({ ...sink, kubernetes: { ...sink.kubernetes!, port: Number(event.currentTarget.value) } })} /></Field>
      <div><Button variant="secondary" size="sm" icon={resolving ? 'spinner' : 'search'} onClick={preview} disabled={resolving}>{resolving ? t('gofi.canvas.sink-target-tools.resolving', 'Resolving...') : t('gofi.canvas.sink-target-tools.preview-pods', 'Preview matching pods')}</Button></div>
      {endpoints && <Stack direction="column" gap={0.5}>
        <Text variant="bodySmall" color="secondary">{t('gofi.canvas.sink-target-tools.matching-targets', '{{n}} matching targets', { count: endpoints.length, n: formatNumber(endpoints.length), defaultValue_one: '{{n}} matching target' })}</Text>
        {endpoints.map(endpoint => <Text key={`${endpoint.namespace}/${endpoint.name}/${endpoint.host}`} variant="code">{endpoint.name} · {endpoint.host}:{sink.kubernetes!.port}</Text>)}
      </Stack>}
    </Stack></Box>}
    <ControlledCollapse label={t('gofi.canvas.sink-target-tools.send-sample-title', 'Send a sample to destinations')} isOpen={false} className={styles.collapse}>
      <Stack direction="column" gap={1.5}>
        <Text variant="bodySmall" color="secondary">{t('gofi.canvas.sink-target-tools.send-sample-description', 'Sends real {{network}} traffic directly to {{target}}. Include any framing delimiter in the payload.', { network: network.toUpperCase(), target: sink.kubernetes ? t('gofi.canvas.sink-target-tools.target-every-pod', 'every matching ready pod') : t('gofi.canvas.sink-target-tools.target-configured-address', 'the configured address') })}</Text>
        <TextArea aria-label={t('gofi.canvas.sink-target-tools.sample-payload', 'Sample traffic payload')} value={raw} onChange={event => setRaw(event.currentTarget.value)} rows={4} className={styles.mono} />
        <div><Button variant="primary" size="sm" icon={sending ? 'spinner' : 'arrow-right'} onClick={send} disabled={sending || (!sink.kubernetes && !sink.url) || !raw}>{sending ? t('gofi.canvas.sink-target-tools.sending', 'Sending...') : sink.kubernetes ? t('gofi.canvas.sink-target-tools.send-sample-pods', 'Send sample to matching pods') : t('gofi.canvas.sink-target-tools.send-sample', 'Send sample')}</Button></div>
        {deliveries && <div role="status"><Stack direction="column" gap={1}>
          {!deliveries.length && <Text variant="bodySmall">{t('gofi.canvas.sink-target-tools.no-destinations-matched', 'No destinations matched.')}</Text>}
          {deliveries.map((delivery, index) => <Text key={index} variant="bodySmall" color={delivery.sent ? 'success' : 'error'}>
            <span className={styles.mono}>{delivery.endpoint?.name || delivery.endpoint?.host || t('gofi.canvas.sink-target-tools.destination', 'Destination')}</span>: {delivery.sent ? t('gofi.canvas.sink-target-tools.socket-write-succeeded', 'Socket write succeeded') : delivery.error ? t('gofi.canvas.sink-target-tools.send-failed-with-error', 'Send failed — {{error}}', { error: delivery.error }) : t('gofi.canvas.sink-target-tools.send-failed', 'Send failed')}
          </Text>)}
          <Text variant="bodySmall" color="secondary">{t('gofi.canvas.sink-target-tools.socket-write-caveat', 'A successful socket write does not confirm application processing.')}</Text>
        </Stack></div>}
      </Stack>
    </ControlledCollapse>
    {error && <Alert severity="error" title={t('gofi.canvas.sink-target-tools.check-failed', 'Destination check failed')} bottomSpacing={0}>{error}</Alert>}
  </Stack>
}

const getStyles = (theme: GrafanaTheme2) => ({
  mono: css({ fontFamily: theme.typography.fontFamilyMonospace, fontSize: theme.typography.bodySmall.fontSize, wordBreak: 'break-all' }),
  collapse: css({ marginBottom: 0 }),
})
