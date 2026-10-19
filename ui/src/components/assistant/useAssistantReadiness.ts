import { useEffect, useState } from 'react'
import { getAssistantConfiguration } from '../../assistant-api'

/** Fired (on window) after the Administration page saves or tests the
 * assistant so the header control re-checks readiness. */
export const ASSISTANT_CHANGED_EVENT = 'gofi:assistant-changed'
export const notifyAssistantChanged = () => window.dispatchEvent(new Event(ASSISTANT_CHANGED_EVENT))

/** Fired (on window) by pages that hand the user over to GoFiBot, e.g. the
 * new-function wizard: `detail.message` is sent as the user's message once
 * the panel is open. The same string as NewFunctionPage.ASSISTANT_OPEN_EVENT
 * (not imported, so the chrome does not pull that page in). */
export const ASSISTANT_OPEN_EVENT = 'gofi:assistant-open'
export interface AssistantOpenDetail { message?: string }

export interface AssistantReadiness { ready: boolean; message: string }

/** Whether a working model endpoint is connected: the controller's cached
 * probe says enabled, keyed, and answering. Fetched once on mount and again
 * on ASSISTANT_CHANGED_EVENT; any failure (no assistant, controller down)
 * reads as not ready. */
export function useAssistantReadiness(): AssistantReadiness {
  const [state, setState] = useState<AssistantReadiness>({ ready: false, message: '' })
  useEffect(() => {
    let cancelled = false
    const check = () => {
      getAssistantConfiguration()
        .then(result => { if (!cancelled) setState({ ready: result.ready, message: result.readyMessage }) })
        .catch(() => { if (!cancelled) setState({ ready: false, message: '' }) })
    }
    check()
    // Re-check now and then so a key saved from another tab, or an endpoint
    // that came back, shows the control without a reload.
    const timer = setInterval(check, 60_000)
    window.addEventListener(ASSISTANT_CHANGED_EVENT, check)
    return () => { cancelled = true; clearInterval(timer); window.removeEventListener(ASSISTANT_CHANGED_EVENT, check) }
  }, [])
  return state
}
