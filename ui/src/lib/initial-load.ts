/** The state machine behind useInitialLoad, free of React so node tests can
 * drive it with fake clocks. A page gathers everything its first paint needs
 * in one loader; the state is `loading` until that settles (and for at least
 * `minMs`, so a fast response does not flash the indicator), then `ready` or
 * `error`. A result for a key that is no longer current is dropped. */
export type InitialLoadStatus = 'loading' | 'ready' | 'error'
export interface InitialLoadState<T> { status: InitialLoadStatus; data?: T; error?: string }

export interface InitialLoaderOptions<T> {
  /** Shortest time the loading state lasts (default 150 ms). */
  minMs?: number
  now?: () => number
  wait?: (ms: number) => Promise<void>
  onChange: (state: InitialLoadState<T>, key: string) => void
}

export const DEFAULT_MIN_MS = 150

export const errorText = (error: unknown): string => error instanceof Error ? error.message : String(error)

export function createInitialLoader<T>(loader: (key: string) => Promise<T>, options: InitialLoaderOptions<T>) {
  const { minMs = DEFAULT_MIN_MS, now = Date.now, wait = ms => new Promise<void>(resolve => setTimeout(resolve, ms)), onChange } = options
  let state: InitialLoadState<T> = { status: 'loading' }
  let key: string | undefined
  let sequence = 0
  let disposed = false
  const set = (next: InitialLoadState<T>, forKey: string) => { state = next; if (!disposed) onChange(state, forKey) }

  async function run(nextKey: string, showLoading: boolean): Promise<void> {
    const ticket = ++sequence
    key = nextKey
    if (showLoading) set({ status: 'loading' }, nextKey)
    const started = now()
    let result: { ok: true; data: T } | { ok: false; error: string }
    try { result = { ok: true, data: await loader(nextKey) } }
    catch (error) { result = { ok: false, error: errorText(error) } }
    if (ticket !== sequence) return // a newer load (or dispose) superseded this one
    if (showLoading) {
      const remaining = minMs - (now() - started)
      if (remaining > 0) { await wait(remaining); if (ticket !== sequence) return }
    }
    set(result.ok ? { status: 'ready', data: result.data } : { status: 'error', error: result.error }, nextKey)
  }

  return {
    get: () => state,
    /** Loads `key`, showing the loading state; a result for an earlier key arriving later is dropped. */
    load: (nextKey: string) => run(nextKey, true),
    /** Re-runs the loader for the current key. A ready page keeps its data (no indicator) until the fresh data arrives; an errored one shows the loading state again. */
    reload: () => key === undefined ? Promise.resolve() : run(key, state.status !== 'ready'),
    dispose: () => { disposed = true; sequence++ },
  }
}
