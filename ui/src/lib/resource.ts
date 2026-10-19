import { parseDocument, stringify } from 'yaml'
import type { FlowDefinition } from '../types'
import type { ConfigurationInfo } from '../api'
import { normalizeFlow, orderFlowSpec } from './flow-normalize.ts'
import { t } from '../i18n/index.ts'

/** The declarative gofi.wcrum.dev Flow resource for a definition, with the
 * spec in the current shape and its keys in pipeline order (source, framing,
 * decode, parser, stages, sinks, ...). */
export function flowResource(definition: FlowDefinition, configuration: ConfigurationInfo | null) {
  const { name, ...spec } = normalizeFlow(definition)
  return {
    apiVersion: configuration?.apiVersion || 'gofi.wcrum.dev/v1alpha1',
    kind: 'Flow',
    metadata: { name, ...(configuration?.namespace ? { namespace: configuration.namespace } : {}) },
    spec: orderFlowSpec(spec),
  }
}

/** A Flow resource parsed from YAML: the definition, or the first problem
 * with its 1-based line when the YAML itself is malformed. */
export type ParsedFlow = { definition: FlowDefinition; error?: undefined } | { definition?: undefined; error: string; line?: number }

/** Parses one gofi.wcrum.dev Flow resource (the inverse of flowResource).
 * Field-level checks happen when the definition is converted and validated. */
export function parseFlowResource(text: string): ParsedFlow {
  const document = parseDocument(text, { prettyErrors: false })
  if (document.errors.length) {
    const first = document.errors[0]
    return { error: first.message.split('\n')[0], line: text.slice(0, first.pos[0]).split('\n').length }
  }
  const resource = document.toJS() as { apiVersion?: unknown; kind?: unknown; metadata?: { name?: unknown }; spec?: unknown } | null
  if (!resource || typeof resource !== 'object' || Array.isArray(resource)) return { error: t('gofi.lib.resource.error-not-mapping', 'Expected a Flow resource (a YAML mapping)') }
  if (resource.kind !== 'Flow') return { error: t('gofi.lib.resource.error-kind', 'kind must be Flow, not {{kind}}', { kind: JSON.stringify(resource.kind ?? null) }) }
  if (typeof resource.apiVersion !== 'string' || !resource.apiVersion.startsWith('gofi.wcrum.dev/')) return { error: t('gofi.lib.resource.error-api-version', 'apiVersion must be gofi.wcrum.dev/<version>') }
  const name = resource.metadata?.name
  if (typeof name !== 'string' || !name) return { error: t('gofi.lib.resource.error-name-required', 'metadata.name is required') }
  if (!resource.spec || typeof resource.spec !== 'object' || Array.isArray(resource.spec)) return { error: t('gofi.lib.resource.error-spec-mapping', 'spec must be a mapping') }
  // Older resources keep decoders under parser; read them in the current shape.
  return { definition: normalizeFlow({ ...(resource.spec as Omit<FlowDefinition, 'name'>), name }) }
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
    case 'kubernetes': return t('gofi.lib.resource.source-kubernetes', 'Kubernetes CRDs')
    case 'file': return t('gofi.lib.resource.source-file', 'Local resource store')
    case 'memory': return t('gofi.lib.resource.source-memory', 'Runtime memory')
    default: return t('gofi.lib.resource.source-unavailable', 'Unavailable')
  }
}
