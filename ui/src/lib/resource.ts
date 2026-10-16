import { stringify } from 'yaml'
import type { FlowDefinition } from '../types'
import type { ConfigurationInfo } from '../api'

/** The declarative gofi.wcrum.dev Flow resource for a definition. */
export function flowResource(definition: FlowDefinition, configuration: ConfigurationInfo | null) {
  const { name, ...spec } = definition
  return {
    apiVersion: configuration?.apiVersion || 'gofi.wcrum.dev/v1alpha1',
    kind: 'Flow',
    metadata: { name, ...(configuration?.namespace ? { namespace: configuration.namespace } : {}) },
    spec,
  }
}

/** Kubernetes-style YAML; several resources become one multi-document stream
 * that `kubectl apply -f` accepts directly. */
export function toYAML(resources: unknown | unknown[]): string {
  const documents = Array.isArray(resources) ? resources : [resources]
  return documents.map(resource => stringify(resource, { lineWidth: 0, aliasDuplicateObjects: false })).join('---\n')
}

export function downloadYAML(filename: string, resources: unknown | unknown[]) {
  const url = URL.createObjectURL(new Blob([toYAML(resources)], { type: 'application/yaml' }))
  const link = document.createElement('a')
  link.href = url; link.download = filename; link.click(); URL.revokeObjectURL(url)
}

/** Editor height that fits the manifest without empty space below it. */
export const manifestHeight = (text: string) => `min(${text.split('\n').length * 19 + 16}px, 70vh)`

export function configurationSourceLabel(configuration: ConfigurationInfo | null): string {
  switch (configuration?.source) {
    case 'kubernetes': return 'Kubernetes CRDs'
    case 'file': return 'Local resource store'
    case 'memory': return 'Runtime memory'
    default: return 'Unavailable'
  }
}
