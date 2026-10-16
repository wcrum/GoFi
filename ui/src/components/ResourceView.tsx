import { useState } from 'react'
import { RiDownloadLine, RiCodeSSlashLine } from 'react-icons/ri'
import { Badge } from './primitives'
import type { ListenerDefinition } from '../types'
import type { ConfigurationInfo } from '../api'

function resource(definition: ListenerDefinition, configuration: ConfigurationInfo | null) {
  const { name, ...spec } = definition
  return {
    apiVersion: configuration?.apiVersion ?? 'gofi.wcrum.dev/v1alpha1',
    kind: configuration?.kind ?? 'Listener',
    metadata: { name, ...(configuration?.namespace ? { namespace: configuration.namespace } : {}) },
    spec,
  }
}
export function ResourceView({ listeners, configuration }: { listeners: ListenerDefinition[]; configuration: ConfigurationInfo | null }) {
  const [selected, setSelected] = useState('')
  const definition = listeners.find((listener) => listener.name === selected) ?? listeners[0]
  const manifest = definition ? JSON.stringify(resource(definition, configuration), null, 2) : ''
  function download() {
    const data = listeners.map((listener) => resource(listener, configuration))
    const list = { apiVersion: 'v1', kind: 'List', items: data }
    const url = URL.createObjectURL(new Blob([JSON.stringify(list, null, 2) + '\n'], { type: 'application/json' }))
    const link = document.createElement('a')
    link.href = url; link.download = 'gofi-listeners.json'; link.click(); URL.revokeObjectURL(url)
  }
  return <div className="p-6 w-full min-w-0">
    <div className="flex items-start justify-between gap-4"><div><p className="text-xs uppercase tracking-widest text-g-accent-main">Declare your intent</p><h3 className="text-2xl font-semibold mt-2">Application configuration</h3><p className="text-sm text-g-text-secondary mt-3 max-w-2xl leading-relaxed">Keep listener definitions in version control and apply GoFi resources through your deployment pipeline. The resource defines the ingress, decoding, filters, and destinations together.</p></div><button disabled={!listeners.length} onClick={download} className="inline-flex shrink-0 items-center gap-2 border border-g-border-medium rounded px-3 py-2 text-xs hover:bg-g-hover disabled:opacity-40"><RiDownloadLine />Export resources</button></div>
    <div className="mt-6 rounded border border-g-border-weak bg-g-primary p-4 flex gap-3"><RiCodeSSlashLine className="text-g-accent-main shrink-0 mt-0.5" /><div><div className="flex gap-3 items-center text-sm font-medium">Configuration source <Badge text={configuration?.source === 'kubernetes' ? 'Kubernetes CRDs' : configuration?.source === 'file' ? 'Local resource store' : configuration?.source === 'memory' ? 'Runtime memory' : 'Unavailable'} color={configuration?.source === 'kubernetes' || configuration?.source === 'file' ? 'green' : 'gray'} /></div><p className="mt-2 text-xs text-g-text-secondary leading-relaxed">{configuration?.source === 'kubernetes' ? 'The control plane reconciles gofi.wcrum.dev Listener resources into running flows. Commit exported resources and use GitOps to enforce your application configuration.' : configuration?.source === 'file' ? 'Listener resources persist across restarts in the configured file. Keep that file in version control and update it declaratively to reconcile your running flows.' : 'This control plane has no durable resource store configured.'}</p>{configuration?.path && <p className="mt-2 text-xs font-mono text-g-text break-all">{configuration.path}</p>}</div></div>
    <div className="mt-5 rounded border border-g-border-weak overflow-hidden"><div className="bg-g-primary px-4 py-3 flex items-center gap-3"><label className="text-xs text-g-text-secondary" htmlFor="resource-listener">Listener</label><select id="resource-listener" className="text-xs bg-g-secondary border border-g-border-medium rounded px-2 py-1" value={definition?.name ?? ''} onChange={(event) => setSelected(event.target.value)} disabled={!listeners.length}>{listeners.map((listener) => <option key={listener.name}>{listener.name}</option>)}</select><span className="ml-auto text-[10px] text-g-text-secondary">gofi.wcrum.dev/v1alpha1</span></div><pre className="p-5 text-xs text-sky-200 overflow-auto max-h-[54vh] leading-relaxed">{manifest || 'Create a listener to preview its declarative resource.'}</pre></div>
    {listeners.length > 0 && configuration?.source === 'file' && <p className="text-xs text-g-text-secondary mt-4">Save the exported Listener resources to the configured resource file to reconcile them in binary or container mode.</p>}
    {listeners.length > 0 && configuration?.source === 'kubernetes' && <p className="text-xs text-g-text-secondary mt-4">Apply the export with <code className="text-g-text">kubectl apply -f gofi-listeners.json</code>. The JSON file is a Kubernetes List containing all listeners. Review credentials before committing exported definitions.</p>}
  </div>
}
