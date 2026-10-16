import { useEffect, useRef, useState } from 'react'
import { getMode, resolveTargets, replayTraffic, type DiscoveredEndpoint, type ReplayDelivery } from '../../api'
import type { SinkCfg } from '../../types'
import { FormField, Input, Select, Button } from '../primitives'
import { KeyValueEditor } from './KeyValueEditor'

export function SinkTargetTools({ sink, readOnly, onChange }: { sink: SinkCfg; readOnly?: boolean; onChange: (sink: SinkCfg) => void }) {
  const [kubernetes, setKubernetes] = useState(false)
  const [endpoints, setEndpoints] = useState<DiscoveredEndpoint[] | null>(null)
  const [deliveries, setDeliveries] = useState<ReplayDelivery[] | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [resolving, setResolving] = useState(false)
  const [sending, setSending] = useState(false)
  const [raw, setRaw] = useState('{"message":"Hello from GoFi"}\n')
  useEffect(() => { getMode().then(mode => setKubernetes(mode.driverKind === 'kubernetes')).catch(() => {}) }, [])
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
  return <div className="space-y-3 mb-4">
    {kubernetes && <FormField label="Destination selection"><Select disabled={readOnly} value={sink.kubernetes ? 'selector' : 'url'} options={[{ value: 'url', label: 'Direct address' }, { value: 'selector', label: 'Kubernetes pods by label' }]} onChange={value => onChange({ ...sink, url: value === 'selector' ? undefined : sink.url, kubernetes: value === 'selector' ? { selector: {}, port: 9000 } : undefined })} /></FormField>}
    {sink.kubernetes && <div className="space-y-3 rounded border border-g-border-weak p-3">
      <FormField label="Namespace (GoFi namespace only)"><Input disabled={readOnly} value={sink.kubernetes.namespace ?? ''} placeholder="GoFi namespace" onChange={event => onChange({ ...sink, kubernetes: { ...sink.kubernetes!, namespace: event.target.value || undefined } })} /></FormField>
      <FormField label="Pod labels"><KeyValueEditor value={sink.kubernetes.selector} disabled={readOnly} onChange={selector => onChange({ ...sink, kubernetes: { ...sink.kubernetes!, selector } })} /></FormField>
      <FormField label="Destination port"><Input type="number" min={1} max={65535} disabled={readOnly} value={sink.kubernetes.port || ''} onChange={event => onChange({ ...sink, kubernetes: { ...sink.kubernetes!, port: Number(event.target.value) } })} /></FormField>
      <p className="text-[11px] text-g-text-secondary">Matching ready pods receive a copy of passing traffic. Targets refresh as pods change.</p>
      <Button onClick={preview} disabled={resolving} className="rounded border border-g-border-medium px-3 py-1.5 text-xs hover:bg-g-hover disabled:opacity-50">{resolving ? 'Resolving…' : 'Preview matching pods'}</Button>
      {endpoints && <div className="text-[11px] text-g-text-secondary space-y-1"><p>{endpoints.length} matching target{endpoints.length === 1 ? '' : 's'}</p>{endpoints.map(endpoint => <p key={`${endpoint.namespace}/${endpoint.name}/${endpoint.host}`} className="font-mono break-all">{endpoint.name} · {endpoint.host}:{sink.kubernetes!.port}</p>)}</div>}
    </div>}
    {<details className="rounded border border-g-border-weak p-3"><summary className="cursor-pointer text-xs text-g-text">Send a sample to destinations</summary><p className="text-[11px] text-g-text-secondary mt-3">Sends real {network.toUpperCase()} traffic directly to {sink.kubernetes ? 'every matching ready pod' : 'the configured address'}. Include any framing delimiter in the payload.</p><textarea aria-label="Sample traffic payload" value={raw} onChange={event => setRaw(event.target.value)} rows={4} className="mt-3 w-full rounded border border-g-border-medium bg-g-canvas p-2 text-xs font-mono" /><Button onClick={send} disabled={sending || (!sink.kubernetes && !sink.url) || !raw} className="mt-2 rounded border border-g-accent-main/60 px-3 py-1.5 text-xs text-g-accent-main hover:bg-g-hover disabled:opacity-50">{sending ? 'Sending…' : sink.kubernetes ? 'Send sample to matching pods' : 'Send sample'}</Button>{deliveries && <div role="status" className="mt-3 space-y-2 text-[11px]">{!deliveries.length && <p>No destinations matched.</p>}{deliveries.map((delivery,index) => <div key={index} className={delivery.sent ? 'text-emerald-300' : 'text-red-300'}><p className="font-mono break-all">{delivery.endpoint?.name || delivery.endpoint?.host || 'Destination'}: {delivery.sent ? 'Socket write succeeded' : 'Send failed'}</p>{delivery.error && <p>{delivery.error}</p>}</div>)}<p className="text-g-text-secondary">A successful socket write does not confirm application processing.</p></div>}</details>}
    {error && <p role="alert" className="text-xs text-red-300 break-words">{error}</p>}
  </div>
}
