import { useEffect, useRef, useState } from 'react'
import { getMode, resolveTargets, replayTraffic, type DiscoveredEndpoint, type ReplayDelivery } from '../../api'
import type { SinkCfg } from '../../types'
import { css } from '@emotion/css'
import type { GrafanaTheme2 } from '@grafana/data'
import { Alert, Box, Button, Combobox, ControlledCollapse, Field, Input, RadioButtonGroup, Stack, Text, TextArea, useStyles2 } from '@grafana/ui'
import { KeyValueEditor } from './KeyValueEditor'
import { useClusterInfo, useUDSPackages } from '../../kubernetes-api'

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
  const transport = sink.type === 'udp' ? 'UDP' : 'TCP'
  // Workloads other Packages admit on this transport: a destination is "connecting to a Package".
  const packageTargets = uds.packages.filter(pkg => pkg.namespace !== uds.namespace).flatMap(pkg => pkg.endpoints
    .filter(endpoint => endpoint.port > 0 && Object.keys(endpoint.selector).length > 0 && (endpoint.kind === 'expose' || endpoint.protocol === transport))
    .map(endpoint => ({ pkg, endpoint, value: `${pkg.namespace}/${pkg.name}/${endpoint.kind}/${JSON.stringify(endpoint.selector)}/${endpoint.port}` })))
  const targetKey = JSON.stringify([sink.kubernetes, sink.url, sink.type])
  const latestTarget = useRef(targetKey)
  latestTarget.current = targetKey
  useEffect(() => { setEndpoints(null); setDeliveries(null); setError(null) }, [targetKey])
  const network = sink.type === 'udp' ? 'udp' : 'tcp'
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
  return <Box marginBottom={2}><Stack direction="column" gap={2}>
    {kubernetes && <Field label="Destination selection" noMargin><Combobox disabled={readOnly} value={sink.kubernetes ? 'selector' : 'url'} options={[{ value: 'url', label: 'Direct address' }, { value: 'selector', label: 'Kubernetes pods by label' }]} onChange={option => onChange({ ...sink, url: option.value === 'selector' ? undefined : sink.url, kubernetes: option.value === 'selector' ? { selector: {}, port: 9000 } : undefined })} /></Field>}
    {sink.kubernetes && <Box borderStyle="solid" borderColor="weak" borderRadius="default" padding={1.5}><Stack direction="column" gap={2}>
      {packageTargets.length > 0 && <Field label="Pick from a UDS Package" description={`Workloads other Packages expose or admit on ${transport}. Fills namespace, labels, and port; the Connectivity tab verifies the Package admits GoFi.`} noMargin>
        <Combobox disabled={readOnly} value={null} placeholder="Choose a Package workload"
          options={packageTargets.map(({ pkg, endpoint, value }) => ({ value, label: `${pkg.namespace}/${pkg.name} · ${Object.entries(endpoint.selector).map(([k, v]) => `${k}=${v}`).join(',')} :${endpoint.port}/${endpoint.protocol}`,
            description: endpoint.kind === 'expose' ? `Exposed as ${endpoint.host}` : `Admits ${endpoint.remote}${endpoint.description ? ` · ${endpoint.description}` : ''}` }))}
          onChange={option => { const match = packageTargets.find(target => target.value === option?.value); if (match) onChange({ ...sink, kubernetes: { ...sink.kubernetes!, namespace: match.pkg.namespace === cluster.info?.namespace ? undefined : match.pkg.namespace, selector: match.endpoint.selector, port: match.endpoint.port } }) }} />
      </Field>}
      <Field label="Namespace" description={cluster.info?.clusterWide ? `Pods in other namespaces are reached through a GoFi-managed EndpointSlice in ${cluster.info.namespace}.` : 'GoFi namespace only: cluster discovery is disabled (chart discovery.clusterWide).'} noMargin>
        <Combobox disabled={readOnly || !cluster.info?.clusterWide} value={sink.kubernetes.namespace || cluster.info?.namespace || ''} createCustomValue
          options={(cluster.info?.namespaces ?? []).map(ns => ({ value: ns, label: ns, description: ns === cluster.info?.namespace ? 'GoFi namespace' : undefined }))}
          onChange={option => onChange({ ...sink, kubernetes: { ...sink.kubernetes!, namespace: option.value === cluster.info?.namespace ? undefined : option.value || undefined } })} />
      </Field>
      <Field label="Pod labels" noMargin><KeyValueEditor value={sink.kubernetes.selector} disabled={readOnly} onChange={selector => onChange({ ...sink, kubernetes: { ...sink.kubernetes!, selector } })} /></Field>
      <Field label="Delivery" description={(sink.kubernetes.delivery ?? 'all') === 'all' ? 'Every matching ready Pod receives each message (replay to all).' : 'Each message goes to one matching Pod, load-balanced.'} noMargin>
        <RadioButtonGroup disabled={readOnly} value={sink.kubernetes.delivery ?? 'all'} options={[{ value: 'all', label: 'Every Pod' }, { value: 'one', label: 'One Pod' }]} onChange={value => onChange({ ...sink, kubernetes: { ...sink.kubernetes!, delivery: value === 'all' ? undefined : 'one' } })} />
      </Field>
      {sink.kubernetes.namespace && sink.kubernetes.namespace !== cluster.info?.namespace && <Text variant="bodySmall" color="secondary">
        {cluster.info?.manageUdsAllows ? 'GoFi adds the egress allow to its UDS Package. ' : ''}The target namespace must also allow ingress from {cluster.info?.namespace ?? 'GoFi'} on this port (for UDS, an Ingress allow with remoteNamespace {cluster.info?.namespace ?? 'gofi'}).
      </Text>}
      <Field label="Destination port" description="Targets refresh as Pods change." noMargin><Input type="number" min={1} max={65535} disabled={readOnly} value={sink.kubernetes.port || ''} onChange={event => onChange({ ...sink, kubernetes: { ...sink.kubernetes!, port: Number(event.currentTarget.value) } })} /></Field>
      <div><Button variant="secondary" size="sm" icon={resolving ? 'spinner' : 'search'} onClick={preview} disabled={resolving}>{resolving ? 'Resolving...' : 'Preview matching pods'}</Button></div>
      {endpoints && <Stack direction="column" gap={0.5}>
        <Text variant="bodySmall" color="secondary">{endpoints.length} matching target{endpoints.length === 1 ? '' : 's'}</Text>
        {endpoints.map(endpoint => <Text key={`${endpoint.namespace}/${endpoint.name}/${endpoint.host}`} variant="code">{endpoint.name} · {endpoint.host}:{sink.kubernetes!.port}</Text>)}
      </Stack>}
    </Stack></Box>}
    <ControlledCollapse label="Send a sample to destinations" isOpen={false}>
      <Stack direction="column" gap={1.5}>
        <Text variant="bodySmall" color="secondary">Sends real {network.toUpperCase()} traffic directly to {sink.kubernetes ? 'every matching ready pod' : 'the configured address'}. Include any framing delimiter in the payload.</Text>
        <TextArea aria-label="Sample traffic payload" value={raw} onChange={event => setRaw(event.currentTarget.value)} rows={4} className={styles.mono} />
        <div><Button variant="primary" size="sm" icon={sending ? 'spinner' : 'arrow-right'} onClick={send} disabled={sending || (!sink.kubernetes && !sink.url) || !raw}>{sending ? 'Sending...' : sink.kubernetes ? 'Send sample to matching pods' : 'Send sample'}</Button></div>
        {deliveries && <div role="status"><Stack direction="column" gap={1}>
          {!deliveries.length && <Text variant="bodySmall">No destinations matched.</Text>}
          {deliveries.map((delivery, index) => <Text key={index} variant="bodySmall" color={delivery.sent ? 'success' : 'error'}>
            <span className={styles.mono}>{delivery.endpoint?.name || delivery.endpoint?.host || 'Destination'}</span>: {delivery.sent ? 'Socket write succeeded' : `Send failed${delivery.error ? ` — ${delivery.error}` : ''}`}
          </Text>)}
          <Text variant="bodySmall" color="secondary">A successful socket write does not confirm application processing.</Text>
        </Stack></div>}
      </Stack>
    </ControlledCollapse>
    {error && <Alert severity="error" title="Destination check failed">{error}</Alert>}
  </Stack></Box>
}

const getStyles = (theme: GrafanaTheme2) => ({
  mono: css({ fontFamily: theme.typography.fontFamilyMonospace, fontSize: theme.typography.bodySmall.fontSize, wordBreak: 'break-all' }),
})
