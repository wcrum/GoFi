import { useCallback, useMemo, useRef } from 'react'
import type { LogStreamState } from '../../sandbox-api'

/** Resolves once a log stream is following (or cannot), so a run's earliest
 * lines are not missed. Gives up after `timeoutMs` and runs anyway. Pass
 * `onState` to the WorkerLogsPanel; await `ready()` before issuing the run. */
export function useLogReadiness(timeoutMs = 1500) {
  const state = useRef<LogStreamState>('connecting')
  const waiters = useRef<(() => void)[]>([])
  const onState = useCallback((next: LogStreamState) => {
    state.current = next
    if (next !== 'connecting' && next !== 'reconnecting') { waiters.current.forEach(resolve => resolve()); waiters.current = [] }
  }, [])
  const ready = useCallback((reuse: boolean) => new Promise<void>(resolve => {
    if (reuse && state.current === 'live') { resolve(); return }
    state.current = 'connecting'
    waiters.current.push(resolve)
    setTimeout(resolve, timeoutMs)
  }), [timeoutMs])
  return useMemo(() => ({ onState, ready }), [onState, ready])
}
