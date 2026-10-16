import { useLocation } from 'react-router-dom-v5-compat'

// Hash routes keep every page linkable without server-side route handling;
// the Go server only serves index.html and static assets. react-router's
// HashRouter (the same package @grafana/ui links bind to) owns the location.
export type FlowTab = 'flow' | 'traffic' | 'connectivity' | 'resource' | 'edit'
export type ClusterTab = 'overview' | 'networking'
export type Route =
  | { page: 'home' }
  | { page: 'flows' }
  | { page: 'flow-new' }
  | { page: 'flow'; name: string; tab: FlowTab }
  | { page: 'explore'; flow: string }
  | { page: 'libraries' }
  | { page: 'expressions'; name: string }
  | { page: 'endpoints' }
  | { page: 'configuration' }
  | { page: 'cluster'; tab: ClusterTab }
  | { page: 'not-found'; path: string }

const FLOW_TABS: FlowTab[] = ['flow', 'traffic', 'connectivity', 'resource', 'edit']

export function parseRoute(hash: string): Route {
  const [rawPath, rawQuery = ''] = hash.replace(/^#/, '').split('?')
  const parts = rawPath.split('/').filter(Boolean).map(decodeURIComponent)
  const query = new URLSearchParams(rawQuery)
  if (!parts.length) return { page: 'home' }
  switch (parts[0]) {
    case 'flows':
      if (parts.length === 1) return { page: 'flows' }
      if (parts[1] === 'new' && parts.length === 2) return { page: 'flow-new' }
      if (parts.length === 2) return { page: 'flow', name: parts[1], tab: 'flow' }
      if (parts.length === 3 && FLOW_TABS.includes(parts[2] as FlowTab)) return { page: 'flow', name: parts[1], tab: parts[2] as FlowTab }
      break
    case 'explore':
      if (parts.length === 1) return { page: 'explore', flow: query.get('flow') ?? '' }
      break
    case 'libraries':
      if (parts.length === 1) return { page: 'libraries' }
      break
    case 'cel':
      if (parts.length === 1) return { page: 'expressions', name: query.get('name') ?? '' }
      break
    case 'connections':
      if (parts[1] === 'endpoints' && parts.length === 2) return { page: 'endpoints' }
      break
    case 'cluster':
      if (parts.length === 1) return { page: 'cluster', tab: 'overview' }
      if (parts.length === 2 && parts[1] === 'networking') return { page: 'cluster', tab: 'networking' }
      break
    case 'admin':
      if (parts[1] === 'configuration' && parts.length === 2) return { page: 'configuration' }
      break
  }
  return { page: 'not-found', path: rawPath }
}

/** Builds an href for a route; flow names are path-encoded. */
export const paths = {
  home: () => '#/',
  flows: () => '#/flows',
  newFlow: () => '#/flows/new',
  flow: (name: string, tab: FlowTab = 'flow') => `#/flows/${encodeURIComponent(name)}${tab === 'flow' ? '' : `/${tab}`}`,
  explore: (flow = '') => `#/explore${flow ? `?flow=${encodeURIComponent(flow)}` : ''}`,
  libraries: () => '#/libraries',
  expressions: (name = '') => `#/cel${name ? `?name=${encodeURIComponent(name)}` : ''}`,
  endpoints: () => '#/connections/endpoints',
  configuration: () => '#/admin/configuration',
  cluster: (tab: ClusterTab = 'overview') => tab === 'overview' ? '#/cluster' : `#/cluster/${tab}`,
}

export function navigate(href: string, options: { replace?: boolean } = {}) {
  if (options.replace) window.location.replace(href)
  else window.location.hash = href.replace(/^#/, '')
}

/** Router path for @grafana/ui TextLink/Link, which resolve through react-router. */
export const to = (href: string) => href.replace(/^#/, '')

export function useRoute(): Route {
  const location = useLocation()
  return parseRoute(`#${location.pathname}${location.search}`)
}
