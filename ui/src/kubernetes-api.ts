import { useEffect, useState } from 'react'
import { createClient, ConnectError, Code } from '@connectrpc/connect'
import { createConnectTransport } from '@connectrpc/connect-web'
import { toJson } from '@bufbuild/protobuf'
import { KubernetesService, DescribeClusterResponseSchema, ListClusterNetworkingResponseSchema, ListUDSPackagesResponseSchema, AnalyzeConnectivityResponseSchema, PlanUDSPackageResponseSchema } from './gen/gofi/v1/kubernetes_pb'
import { routeHealthFromJson, type RouteHealth } from './api'
import { flowToProto } from './rpc-model'
import type { FlowDefinition } from './types'

const client = createClient(KubernetesService, createConnectTransport({ baseUrl: window.location.origin }))

export interface ClusterCapability { id: string; name: string; present: boolean; version?: string; detail?: string; evidence?: string[] }
export interface GatewayListener { name: string; protocol: string; port: number; hostname?: string; allowedRoutes?: string; attachedRoutes?: number; managedByGofi?: boolean }
export interface GatewayInfo { namespace: string; name: string; className: string; controller?: string; addresses?: string[]; programmed?: boolean; listeners?: GatewayListener[]; implementation?: string; waypoint?: boolean; routeKinds?: string[] }
export interface AddressPool { namespace: string; name: string; addresses?: string[]; autoAssign?: boolean }
export interface LoadBalancerInfo { namespace: string; name: string; className?: string; addresses?: string[]; ports?: string[] }
export interface ClusterInfo {
  kubernetesVersion?: string; namespace: string; clusterWide?: boolean
  capabilities: ClusterCapability[]; gatewayClasses?: { name: string; controller: string; accepted?: boolean; implementation?: string }[]
  gateways: GatewayInfo[]; addressPools: AddressPool[]; loadBalancerClasses: string[]; loadBalancers: LoadBalancerInfo[]
  nodePortMin?: number; nodePortMax?: number; warnings: string[]; manageGatewayListeners?: boolean; manageUdsAllows?: boolean; namespaces: string[]
}
export interface NetworkingObject { kind: string; apiVersion: string; namespace: string; name: string; engine: string; summary: string; details: string[]; yaml: string; affectedFlows: string[] }
export interface ClusterNetworking { objects: NetworkingObject[]; namespaces: string[]; warnings: string[] }

const withDefaults = (info: ClusterInfo): ClusterInfo => ({ ...info, capabilities: info.capabilities ?? [], gateways: info.gateways ?? [], addressPools: info.addressPools ?? [], loadBalancerClasses: info.loadBalancerClasses ?? [], loadBalancers: info.loadBalancers ?? [], warnings: info.warnings ?? [], namespaces: info.namespaces ?? [] })

export async function describeCluster(): Promise<ClusterInfo> {
  return withDefaults(toJson(DescribeClusterResponseSchema, await client.describeCluster({})) as unknown as ClusterInfo)
}
export async function listClusterNetworking(namespace = ''): Promise<ClusterNetworking> {
  const json = toJson(ListClusterNetworkingResponseSchema, await client.listClusterNetworking({ namespace })) as unknown as Partial<ClusterNetworking>
  return { objects: (json.objects ?? []).map(o => ({ ...o, details: o.details ?? [], affectedFlows: o.affectedFlows ?? [] })), namespaces: json.namespaces ?? [], warnings: json.warnings ?? [] }
}
/** True when the controller is not in Kubernetes mode. */
export function notKubernetes(error: unknown): boolean {
  const code = ConnectError.from(error).code
  return code === Code.FailedPrecondition || code === Code.Unimplemented
}

// One shared, briefly cached describe call for inspectors and pickers.
let cached: { at: number; promise: Promise<ClusterInfo> } | undefined
export function useClusterInfo(enabled = true): { info?: ClusterInfo; error?: string } {
  const [state, setState] = useState<{ info?: ClusterInfo; error?: string }>({})
  useEffect(() => {
    if (!enabled) return
    if (!cached || Date.now() - cached.at > 15000) cached = { at: Date.now(), promise: describeCluster() }
    let live = true
    cached.promise.then(info => { if (live) setState({ info }) }).catch(error => { if (live) setState({ error: notKubernetes(error) ? undefined : String(error) }) })
    return () => { live = false }
  }, [enabled])
  return state
}

export interface UDSPackageEndpoint { kind: 'expose' | 'allow'; description?: string; selector: Record<string, string>; port: number; protocol: string; service?: string; host?: string; remote?: string }
export interface UDSPackageInfo { namespace: string; name: string; phase?: string; endpoints: UDSPackageEndpoint[]; workloads: string[] }
export async function listUDSPackages(): Promise<{ packages: UDSPackageInfo[]; namespace: string; warnings: string[] }> {
  const json = toJson(ListUDSPackagesResponseSchema, await client.listUDSPackages({})) as unknown as { packages?: UDSPackageInfo[]; namespace: string; warnings?: string[] }
  return { namespace: json.namespace, warnings: json.warnings ?? [], packages: (json.packages ?? []).map(p => ({ ...p, workloads: p.workloads ?? [], endpoints: (p.endpoints ?? []).map(e => ({ ...e, selector: e.selector ?? {}, port: e.port ?? 0 })) })) }
}

export interface PolicyFinding { engine: string; kind?: string; name?: string; namespace?: string; effect: string; detail: string }
/** action "apply-gofi-package": GoFi can apply it (review with planUdsPackage);
 * otherwise copy-only, with note saying who owns the change. */
export interface SuggestedFix { engine: string; title: string; yaml: string; action?: string; note?: string }
export interface ConnectivityPath { id: string; direction: 'ingress' | 'egress'; from: string; to: string; port: number; protocol: string; verdict: 'allowed' | 'blocked' | 'unknown'; summary: string; findings: PolicyFinding[]; fixes: SuggestedFix[]; observed?: RouteHealth; diagnosis?: Diagnosis }
/** The verdict joined with what the running flow observed (see kubernetes.proto). */
export type Diagnosis = 'not-allowed' | 'unverified' | 'unreachable' | 'mismatch'
export interface Connectivity { namespace: string; paths: ConnectivityPath[]; engines: { name: string; present: boolean; note?: string }[]; limits: string[] }
/** Verifies every path a (possibly unsaved) flow needs against UDS Packages and Pepr. */
export async function analyzeConnectivity(flow: FlowDefinition): Promise<Connectivity> {
  const json = toJson(AnalyzeConnectivityResponseSchema, await client.analyzeConnectivity({ flow: flowToProto(flow) })) as unknown as Partial<Connectivity>
  return { namespace: json.namespace ?? '', engines: json.engines ?? [], limits: json.limits ?? [],
    paths: (json.paths ?? []).map(p => ({ ...p, port: p.port ?? 0, findings: p.findings ?? [], fixes: p.fixes ?? [], observed: p.observed && routeHealthFromJson(p.observed) })) }
}

/** GoFi's own Package, live and as GoFi would apply it for the saved flows. */
export interface PackagePlan { namespace: string; name: string; managed: boolean; liveYaml: string; desiredYaml: string; inSync: boolean; admission?: { accepted: boolean; error?: string }; digest: string; error?: string }
const planFromJson = (json: Partial<PackagePlan>): PackagePlan => ({ namespace: '', name: '', managed: false, liveYaml: '', desiredYaml: '', inSync: false, digest: '', ...json })
export async function planUdsPackage(): Promise<PackagePlan> {
  return planFromJson(toJson(PlanUDSPackageResponseSchema, await client.planUDSPackage({})) as unknown as Partial<PackagePlan>)
}
/** Applies the reviewed plan; refused when it changed since (digest). */
export async function applyUdsPackage(digest: string): Promise<PackagePlan> {
  const { plan } = await client.applyUDSPackage({ digest })
  return plan ? planFromJson(toJson(PlanUDSPackageResponseSchema, plan) as unknown as Partial<PackagePlan>) : await planUdsPackage()
}

// Shared, briefly cached Package inventory for pickers.
let packagesCache: { at: number; promise: ReturnType<typeof listUDSPackages> } | undefined
export function useUDSPackages(enabled = true): { packages: UDSPackageInfo[]; namespace?: string } {
  const [state, setState] = useState<{ packages: UDSPackageInfo[]; namespace?: string }>({ packages: [] })
  useEffect(() => {
    if (!enabled) return
    if (!packagesCache || Date.now() - packagesCache.at > 15000) packagesCache = { at: Date.now(), promise: listUDSPackages() }
    let live = true
    packagesCache.promise.then(result => { if (live) setState({ packages: result.packages, namespace: result.namespace }) }).catch(() => {})
    return () => { live = false }
  }, [enabled])
  return state
}

/** Parses "a=b,c=d" (labelString on the server) into a selector. */
export function parseSelector(text: string): Record<string, string> {
  const out: Record<string, string> = {}
  for (const part of text.split(',')) {
    const [key, value] = part.split('=')
    if (key && value !== undefined) out[key.trim()] = value.trim()
  }
  return out
}
