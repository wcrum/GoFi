import { Alert, Combobox, ControlledCollapse, Field, Input, MultiCombobox, RadioButtonGroup, Stack, Text } from '@grafana/ui'
import type { ComboboxOption } from '@grafana/ui'
import type { DeployModeInfo } from '../../api'
import { parseSelector, useClusterInfo, useUDSPackages, type ClusterInfo, type GatewayInfo } from '../../kubernetes-api'
import { NODE_PORT_RANGE, type ListenSource, type ServiceExposure, type ServiceType } from '../../types'
import { KeyValueEditor } from './KeyValueEditor'

const ADD_LISTENER = '__gofi_add_listener__'

const has = (info: ClusterInfo | undefined, id: string) => !!info?.capabilities.find(capability => capability.id === id)?.present

function exposureOptions(info: ClusterInfo | undefined): Array<ComboboxOption<string>> {
  const lb = has(info, 'load-balancer')
  const gateways = has(info, 'gateway-api')
  return [
    { value: '', label: 'Internal', description: 'Pod port only, reachable where network policy allows' },
    { value: 'ClusterIP', label: 'ClusterIP', description: 'A stable in-cluster Service address' },
    { value: 'NodePort', label: 'NodePort', description: `A port on every node (${info?.nodePortMin ?? NODE_PORT_RANGE.min}–${info?.nodePortMax ?? NODE_PORT_RANGE.max})` },
    { value: 'LoadBalancer', label: 'LoadBalancer', description: info && !lb ? 'No load balancer detected in this cluster' : 'An external address on any port, such as 60084' },
    { value: 'Gateway', label: 'Gateway route', description: info && !gateways ? 'Gateway API is not installed' : 'A TCPRoute/UDPRoute on a Gateway listener (Envoy Gateway, Istio)' },
  ]
}

/** How a listen source is reached in Kubernetes: Service type, load balancer
 * or Gateway choice, and advanced Service settings. Options come from the
 * cluster's discovered capabilities. */
export function ExposureSettings({ listen, onChange, readOnly, deployment }: { listen: ListenSource; onChange: (listen: ListenSource) => void; readOnly?: boolean; deployment: DeployModeInfo | null }) {
  const { info, error } = useClusterInfo()
  const service = listen.service
  const exposure = service?.type ?? ''
  const uds = useUDSPackages(has(info, 'uds-core'))
  // A UDS Package's workloads as senders: each choice becomes an allowFrom peer.
  const senderOptions = uds.packages.filter(pkg => pkg.namespace !== uds.namespace).flatMap(pkg => (pkg.workloads.length ? pkg.workloads : ['']).map(workload => ({
    value: `${pkg.namespace}|${pkg.name}|${workload}`, label: `${pkg.namespace}/${pkg.name}${workload ? ` · ${workload}` : ' · any workload'}`, description: pkg.phase ? `Package ${pkg.phase}` : undefined })))
  const senderValues = (service?.allowFrom ?? []).map(peer => `${peer.namespace}|${peer.package ?? ''}|${Object.entries(peer.selector ?? {}).map(([k, v]) => `${k}=${v}`).join(',')}`)
  const network = (listen.network || 'tcp').toUpperCase()
  const set = (next: Partial<ServiceExposure> | undefined) => onChange({ ...listen, service: next === undefined ? undefined : { ...service!, ...next } })
  const range = { min: deployment?.nodePortMin || info?.nodePortMin || NODE_PORT_RANGE.min, max: deployment?.nodePortMax || info?.nodePortMax || NODE_PORT_RANGE.max }
  const nodePort = service?.nodePort
  const nodePortProblem = nodePort && (nodePort < range.min || nodePort > range.max) ? `This cluster accepts NodePorts ${range.min}–${range.max}. For another port, use LoadBalancer or a Gateway route.` : undefined

  const gateways = (info?.gateways ?? []).filter(gw => !gw.waypoint)
  const routeKind = network === 'UDP' ? 'UDPRoute' : 'TCPRoute'
  const supports = (gw: GatewayInfo) => !gw.routeKinds?.length || gw.routeKinds.includes(routeKind)
  const selectedGateway = service?.gateway ? gateways.find(gw => gw.namespace === service.gateway!.namespace && gw.name === service.gateway!.name) : undefined
  const listeners = (selectedGateway?.listeners ?? []).filter(l => l.protocol.toUpperCase() === network && !l.managedByGofi)
  const gatewayPort = service?.gateway?.port || listen.port
  const senderAddress = exposure === 'Gateway' && selectedGateway
    ? `${selectedGateway.addresses?.[0] ?? '<gateway address>'}:${service?.gateway?.listener ? (listeners.find(l => l.name === service.gateway!.listener)?.port ?? '?') : gatewayPort}`
    : undefined

  return <>
    <Field label="Exposure" description="How senders reach this flow.">
      <Combobox disabled={readOnly} value={exposure} options={exposureOptions(info)}
        onChange={option => {
          const type = option.value as ServiceType | ''
          if (!type) return set(undefined)
          onChange({ ...listen, service: { type, ...(type === 'NodePort' ? { nodePort: service?.nodePort } : {}), ...(type === 'Gateway' ? { gateway: service?.gateway } : {}), annotations: service?.annotations } })
        }} />
    </Field>

    {exposure === 'NodePort' && <Field label="NodePort" description={`External port on every node, ${range.min}–${range.max}. Leave blank to have Kubernetes assign one.`} invalid={!!nodePortProblem} error={nodePortProblem}>
      <Input disabled={readOnly} type="number" min={range.min} max={range.max} value={nodePort ?? ''} placeholder="Assigned automatically" onChange={event => set({ nodePort: Number(event.currentTarget.value) || undefined })} />
    </Field>}

    {exposure === 'LoadBalancer' && <>
      <Field label="Load balancer" description={info?.loadBalancerClasses.length ? 'Leave on the default unless several load balancer implementations run in this cluster.' : 'The cluster default load balancer serves this Service.'}>
        <Combobox disabled={readOnly} value={service?.loadBalancer?.class ?? ''} createCustomValue
          options={[{ value: '', label: 'Cluster default' }, ...(info?.loadBalancerClasses ?? []).map(value => ({ value, label: value }))]}
          onChange={option => set({ loadBalancer: { ...service?.loadBalancer, class: option.value || undefined } })} />
      </Field>
      {(info?.addressPools.length ?? 0) > 0 && <Field label="Address pool" description="MetalLB pool the address is taken from.">
        <Combobox disabled={readOnly} value={service?.loadBalancer?.addressPool ?? ''}
          options={[{ value: '', label: 'Any pool that auto-assigns' }, ...info!.addressPools.map(pool => ({ value: pool.name, label: pool.name, description: (pool.addresses ?? []).join(', ') + (pool.autoAssign === false ? ' · manual only' : '') }))]}
          onChange={option => set({ loadBalancer: { ...service?.loadBalancer, addressPool: option.value || undefined } })} />
      </Field>}
    </>}

    {exposure === 'ClusterIP' && has(info, 'uds-core') && <Field label="Allowed senders" description={service?.allowFrom?.length ? 'Only these UDS Package workloads may send; GoFi allows exactly them, and the Connectivity tab checks each sender\'s Package allows the egress.' : 'Any namespace may send. Choose UDS Package workloads to allow only them.'}>
      <MultiCombobox disabled={readOnly} options={senderOptions} value={senderValues} placeholder="Any namespace"
        onChange={selected => set({ allowFrom: selected.length ? selected.map(option => { const [namespace, pkg, workload] = option.value.split('|'); return { namespace, package: pkg || undefined, selector: workload ? parseSelector(workload) : undefined } }) : undefined })} />
    </Field>}

    {exposure === 'Gateway' && <>
      <Field label="Gateway" description={gateways.length ? `Gateways that can carry a ${routeKind}.` : 'No Gateways found. Create one with Envoy Gateway or Istio first.'}>
        <Combobox disabled={readOnly} value={service?.gateway ? `${service.gateway.namespace}/${service.gateway.name}` : null}
          options={gateways.map(gw => ({ value: `${gw.namespace}/${gw.name}`, label: `${gw.namespace}/${gw.name}`,
            description: `${gw.implementation || gw.className}${gw.addresses?.length ? ` · ${gw.addresses.join(', ')}` : ''}${supports(gw) ? '' : ` · no ${routeKind} support`}${gw.programmed === false ? ' · not programmed' : ''}` }))}
          onChange={option => { const [namespace, name] = option.value.split('/'); set({ gateway: { namespace, name } }) }} />
      </Field>
      {selectedGateway && !supports(selectedGateway) && <Alert severity="warning" title={`${selectedGateway.implementation || selectedGateway.className} does not route ${network}`} bottomSpacing={0}>Choose an Envoy Gateway Gateway for {routeKind}s.</Alert>}
      {selectedGateway && <Field label="Listener" description={info?.manageGatewayListeners ? 'Use an existing listener, or let GoFi add one for this flow.' : 'GoFi may not add listeners here (chart gateway.manageListeners); pick an existing one.'}>
        <Combobox disabled={readOnly} value={service?.gateway?.listener || (info?.manageGatewayListeners ? ADD_LISTENER : null)}
          options={[
            ...(info?.manageGatewayListeners ? [{ value: ADD_LISTENER, label: 'Add a listener for this flow', description: `GoFi adds gofi-<flow> (${network}) to ${selectedGateway.namespace}/${selectedGateway.name}` }] : []),
            ...listeners.map(l => ({ value: l.name, label: `${l.name} · ${l.protocol} ${l.port}`, description: `Routes from ${l.allowedRoutes || 'Same'} · ${l.attachedRoutes ?? 0} attached` })),
          ]}
          onChange={option => set({ gateway: { ...service!.gateway!, listener: option.value === ADD_LISTENER ? undefined : option.value, port: option.value === ADD_LISTENER ? service?.gateway?.port : undefined } })} />
      </Field>}
      {selectedGateway && !service?.gateway?.listener && info?.manageGatewayListeners && <Field label="Gateway port" description="Port the new listener opens on the Gateway address.">
        <Input disabled={readOnly} type="number" min={1} max={65535} value={service?.gateway?.port ?? ''} placeholder={String(listen.port || 9000)} onChange={event => set({ gateway: { ...service!.gateway!, port: Number(event.currentTarget.value) || undefined } })} />
      </Field>}
      {selectedGateway?.listeners?.find(l => l.name === service?.gateway?.listener)?.allowedRoutes === 'Same' && selectedGateway.namespace !== info?.namespace &&
        <Alert severity="warning" title="This listener only accepts routes from its own namespace" bottomSpacing={0}>Set allowedRoutes to include namespace {info?.namespace}, or let GoFi add a listener.</Alert>}
      {senderAddress && <Text variant="bodySmall" color="secondary">Senders use {senderAddress}.</Text>}
    </>}

    {(exposure === 'NodePort' || exposure === 'LoadBalancer' || exposure === 'ClusterIP') && <ControlledCollapse label="Advanced" isOpen={!!(service?.externalTrafficPolicy || service?.loadBalancer?.ip || Object.keys(service?.annotations ?? {}).length)}>
      <Stack direction="column" gap={1}>
        {exposure === 'LoadBalancer' && <Field label="Requested address" description="A specific IP from the pool (MetalLB metallb.io/loadBalancerIPs).">
          <Input disabled={readOnly} value={service?.loadBalancer?.ip ?? ''} placeholder="Assigned automatically" onChange={event => set({ loadBalancer: { ...service?.loadBalancer, ip: event.currentTarget.value || undefined } })} />
        </Field>}
        {exposure !== 'ClusterIP' && <Field label="External traffic policy" description="Local keeps the sender's source IP (useful for UDP), but only nodes running the flow Pod accept traffic.">
          <RadioButtonGroup disabled={readOnly} value={service?.externalTrafficPolicy ?? 'Cluster'} options={[{ value: 'Cluster', label: 'Cluster' }, { value: 'Local', label: 'Local' }]} onChange={value => set({ externalTrafficPolicy: value === 'Cluster' ? undefined : value as 'Local' })} />
        </Field>}
        <Field label="Service annotations" description="For load balancer or platform integrations (for example kube-vip or cloud provider settings).">
          <KeyValueEditor value={service?.annotations ?? {}} disabled={readOnly} onChange={annotations => set({ annotations: Object.keys(annotations).length ? annotations : undefined })} />
        </Field>
      </Stack>
    </ControlledCollapse>}

    {exposure && has(info, 'uds-core') && <Text variant="bodySmall" color="secondary">
      {info?.manageUdsAllows ? 'UDS Core: GoFi adds the network allow for this exposure to its gofi-flows Package.' : 'UDS Core blocks this ingress unless a Package allows it. Enable uds.manageAllows in the chart, or add an allow for this port.'}
    </Text>}
    {error && <Text variant="bodySmall" color="error">Cluster discovery failed: {error}</Text>}
  </>
}
