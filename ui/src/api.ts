import type { ListenerDefinition } from './types'

const BASE = '/api'

export async function listListeners(): Promise<ListenerDefinition[]> {
  const r = await fetch(`${BASE}/listeners`)
  if (!r.ok) throw new Error(`list failed: ${r.status}`)
  return r.json()
}

export async function createListener(def: ListenerDefinition): Promise<ListenerDefinition> {
  const r = await fetch(`${BASE}/listeners`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(def),
  })
  if (!r.ok) {
    const err = await r.json().catch(() => ({ error: r.statusText }))
    throw new Error(err.error ?? r.statusText)
  }
  return r.json()
}

export async function deleteListener(name: string): Promise<void> {
  const r = await fetch(`${BASE}/listeners/${encodeURIComponent(name)}`, { method: 'DELETE' })
  if (!r.ok && r.status !== 404) throw new Error(`delete failed: ${r.status}`)
}

export function streamListener(
  name: string,
  onMessage: (ev: import('./types').TapEvent) => void,
  onError?: (e: Event) => void,
): () => void {
  const es = new EventSource(`${BASE}/listeners/${encodeURIComponent(name)}/stream`)
  es.onmessage = (e) => {
    try {
      onMessage(JSON.parse(e.data))
    } catch {
      // ignore malformed
    }
  }
  if (onError) es.onerror = onError
  return () => es.close()
}
