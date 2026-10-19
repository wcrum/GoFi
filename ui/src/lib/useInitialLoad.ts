import { useCallback, useEffect, useRef, useState } from 'react'
import { createInitialLoader, DEFAULT_MIN_MS, type InitialLoadState, type InitialLoadStatus } from './initial-load'

export type { InitialLoadState, InitialLoadStatus }
export interface InitialLoad<T> extends InitialLoadState<T> { reload: () => Promise<void> }

/** Gathers a page's first-paint data behind one loading state, so the page
 * renders whole (header, counters, active tab) instead of assembling itself
 * as each request lands. `loader` runs whenever `key` changes (for example the
 * flow name); a result for a stale key is dropped. The loading state lasts at
 * least `minMs` to avoid a flicker on fast responses. `reload` refetches in
 * place: a ready page keeps showing its data until the new data arrives. */
export function useInitialLoad<T>(key: string, loader: (key: string) => Promise<T>, minMs = DEFAULT_MIN_MS): InitialLoad<T> {
  const loaderRef = useRef(loader)
  loaderRef.current = loader
  const [state, setState] = useState<InitialLoadState<T> & { key?: string }>({ status: 'loading' })
  const controller = useRef<ReturnType<typeof createInitialLoader<T>>>(null)
  useEffect(() => {
    const current = controller.current ?? createInitialLoader<T>(k => loaderRef.current(k), { minMs, onChange: (next, forKey) => setState({ ...next, key: forKey }) })
    controller.current = current
    void current.load(key)
    // The controller lives as long as the hook; a key change loads in place
    // and the sequence guard drops the previous key's result.
  }, [key, minMs])
  useEffect(() => () => { controller.current?.dispose(); controller.current = null }, [])
  const reload = useCallback(() => controller.current?.reload() ?? Promise.resolve(), [])
  // Until the loader for the current key reports, the previous key's data is not this page's.
  const current: InitialLoadState<T> = state.key === key ? state : { status: 'loading' }
  return { status: current.status, data: current.data, error: current.error, reload }
}
