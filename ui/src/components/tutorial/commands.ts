// How to reach a flow from outside, per driver, and the shell command that
// sends it one test frame. Pure: the panel supplies translated placeholders
// (such as "<node address>") where the real value is not known.

import type { ListenSource, ServiceType } from '../../types'

/** The driver kinds the controller reports (`getMode().driverKind`). */
export type DriverKind = 'kubernetes' | 'docker' | 'inprocess'

/** How senders reach the port: a Kubernetes Service, a port Docker
 * publishes on its host, or a port the binary binds on its own host. */
export type IngressKind = 'kubernetes' | 'docker' | 'host'

export function ingressKind(driverKind: string | undefined): IngressKind {
  return driverKind === 'kubernetes' ? 'kubernetes' : driverKind === 'docker' ? 'docker' : 'host'
}

/** The address senders use when GoFi binds or publishes the port on the
 * machine the browser is talking to; Kubernetes has no single answer. */
export function defaultHost(driverKind: string | undefined): string | undefined {
  return ingressKind(driverKind) === 'kubernetes' ? undefined : '127.0.0.1'
}

/** One JSON line the tutorial's example flow accepts. */
export const SAMPLE_FRAME = '{"level":"info","message":"hello from GoFi"}'

/** Stand-ins for values only the operator knows. */
export interface IngressPlaceholders {
  nodeAddress: string
  nodePort: string
  loadBalancerAddress: string
  gatewayAddress: string
  clusterService: string
}

export type IngressVia = 'host' | 'docker' | 'node-port' | 'load-balancer' | 'gateway' | 'cluster-ip'
export interface ResolvedIngress { host: string; port: number | string; via: IngressVia; exposure?: ServiceType }

export interface IngressTarget {
  driverKind: string | undefined
  listen: Pick<ListenSource, 'network' | 'port' | 'service'>
  /** A known address (the Docker host, a node, a load balancer), when the caller has one. */
  host?: string
  /** The NodePort Kubernetes assigned, when the caller looked it up. */
  assignedNodePort?: number
}

/** Where a test frame goes for this flow in this environment. */
export function resolveIngress(target: IngressTarget, placeholders: IngressPlaceholders): ResolvedIngress {
  const { listen } = target
  const kind = ingressKind(target.driverKind)
  if (kind !== 'kubernetes') return { host: target.host || defaultHost(target.driverKind) || '127.0.0.1', port: listen.port, via: kind }
  const exposure = listen.service?.type
  switch (exposure) {
    case 'NodePort': {
      const nodePort = listen.service?.nodePort || target.assignedNodePort
      return { host: target.host || placeholders.nodeAddress, port: nodePort || placeholders.nodePort, via: 'node-port', exposure }
    }
    case 'LoadBalancer':
      return { host: target.host || listen.service?.loadBalancer?.ip || placeholders.loadBalancerAddress, port: listen.service?.port || listen.port, via: 'load-balancer', exposure }
    case 'Gateway':
      return { host: target.host || placeholders.gatewayAddress, port: listen.service?.gateway?.port || listen.port, via: 'gateway', exposure }
    default:
      return { host: target.host || placeholders.clusterService, port: listen.service?.port || listen.port, via: 'cluster-ip', exposure: exposure ?? 'ClusterIP' }
  }
}

/** Single-quotes a string for a POSIX shell. */
export function shellQuote(value: string): string {
  return `'${value.replace(/'/g, `'\\''`)}'`
}

/** The command that sends one framed line to the flow with netcat, or null
 * for a network netcat cannot speak (QUIC). UDP needs `-u`; `-w1` makes nc
 * return after sending instead of waiting for a reply that never comes. */
export function testCommand(network: string, host: string, port: number | string, frame = SAMPLE_FRAME): string | null {
  if (network === 'quic') return null
  const transport = network === 'udp' ? 'nc -u -w1' : 'nc'
  return `printf '%s\\n' ${shellQuote(frame)} | ${transport} ${host} ${port}`
}

/** `kubectl port-forward` to a flow's Pod: the one way to reach a ClusterIP
 * flow from a workstation without changing its exposure. */
export function portForwardCommand(flowName: string, port: number, namespace?: string): string {
  const ns = namespace ? ` -n ${shellQuote(namespace)}` : ''
  return `kubectl${ns} port-forward -l gofi.io/listener=${shellQuote(flowName)} ${port}:${port}`
}
