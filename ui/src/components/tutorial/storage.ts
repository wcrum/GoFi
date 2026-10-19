// Where the tutorial remembers itself: one localStorage key holding the
// status and the step to resume at. Storage is optional everywhere (private
// windows, blocked site data): without it the tutorial opens on every load
// and forgets its step, which is the only honest fallback.

export const TUTORIAL_STORAGE_KEY = 'gofi.tutorial.v1'

/** `active`: open (or was open when the page reloaded); `dismissed`: "Skip
 * for now" or closed; `done`: finished once. */
export type TutorialStatus = 'active' | 'dismissed' | 'done'
export interface TutorialState { status: TutorialStatus; step: number }

export interface StorageLike {
  getItem(key: string): string | null
  setItem(key: string, value: string): void
  removeItem(key: string): void
}

const STATUSES: readonly TutorialStatus[] = ['active', 'dismissed', 'done']

function defaultStorage(): StorageLike | null {
  try { return typeof localStorage === 'undefined' ? null : localStorage } catch { return null }
}

/** The stored state, or null on a first visit (or unreadable storage). */
export function readTutorialState(storage: StorageLike | null = defaultStorage()): TutorialState | null {
  try {
    const raw = storage?.getItem(TUTORIAL_STORAGE_KEY)
    if (!raw) return null
    const parsed = JSON.parse(raw) as Partial<TutorialState> | null
    if (!parsed || typeof parsed !== 'object' || !STATUSES.includes(parsed.status as TutorialStatus)) return null
    const step = Number(parsed.step)
    return { status: parsed.status as TutorialStatus, step: Number.isInteger(step) && step >= 1 ? step : 1 }
  } catch { return null }
}

export function writeTutorialState(state: TutorialState, storage: StorageLike | null = defaultStorage()): void {
  try { storage?.setItem(TUTORIAL_STORAGE_KEY, JSON.stringify(state)) } catch { /* storage unavailable: the tutorial forgets its place */ }
}

/** Forgets the tutorial, so the next load treats the browser as a first visit. */
export function clearTutorialState(storage: StorageLike | null = defaultStorage()): void {
  try { storage?.removeItem(TUTORIAL_STORAGE_KEY) } catch { /* storage unavailable */ }
}

/** Opens by itself on a first visit (no state) and after a reload while it
 * was open; never after it was skipped or finished. */
export function shouldAutoOpen(state: TutorialState | null): boolean {
  return state === null || state.status === 'active'
}

/** The step to open at: where the user left off, or the start once finished. */
export function resumeStep(state: TutorialState | null): number {
  return state && state.status !== 'done' ? state.step : 1
}
