import type { FlowDefinition } from '../types'

// A one-shot draft handed to the New flow page. Stored per tab; the page
// peeks on mount and clears it after.
const KEY = 'gofi.newFlowSeed'

export function setFlowSeed(definition: FlowDefinition) {
  try { sessionStorage.setItem(KEY, JSON.stringify(definition)) } catch { /* storage unavailable */ }
}

export function peekFlowSeed(): FlowDefinition | undefined {
  try {
    const value = sessionStorage.getItem(KEY)
    return value ? JSON.parse(value) as FlowDefinition : undefined
  } catch { return undefined }
}

export function clearFlowSeed() {
  try { sessionStorage.removeItem(KEY) } catch { /* storage unavailable */ }
}

