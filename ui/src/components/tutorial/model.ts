// The tutorial's step model: ordered ids, progress, and the page each step
// points the user at. No React and no translations here, so it is unit
// tested under node; the panel renders the words.

export const TUTORIAL_STEP_IDS = ['welcome', 'navigation', 'first-flow', 'ingress', 'monitoring', 'done'] as const
export type TutorialStepId = typeof TUTORIAL_STEP_IDS[number]
export const STEP_COUNT = TUTORIAL_STEP_IDS.length

/** Clamps to 1..STEP_COUNT; anything that is not a whole number is step 1. */
export function clampStep(step: number): number {
  if (!Number.isInteger(step)) return 1
  return Math.min(STEP_COUNT, Math.max(1, step))
}

export const stepId = (step: number): TutorialStepId => TUTORIAL_STEP_IDS[clampStep(step) - 1]
export const stepNumber = (id: TutorialStepId): number => TUTORIAL_STEP_IDS.indexOf(id) + 1
export const isFirstStep = (step: number): boolean => clampStep(step) === 1
export const isLastStep = (step: number): boolean => clampStep(step) === STEP_COUNT

/** Progress as the panel shows it: step n of total, and a percentage for the bar. */
export function progress(step: number): { step: number; total: number; percent: number } {
  const current = clampStep(step)
  return { step: current, total: STEP_COUNT, percent: Math.round((current / STEP_COUNT) * 100) }
}

/** What the panel knows about this GoFi when it picks a page for a step. */
export interface StepContext {
  /** The flow the ingress and monitoring steps talk about, once one is saved. */
  flowName?: string
}

/** The hash route a step opens beside the panel, or null to stay where the
 * user is. The ingress step opens the flow's editor (its settings hold the
 * exposure) when the flow exists, else the New flow page. */
export function stepHref(id: TutorialStepId, context: StepContext = {}): string | null {
  const flow = context.flowName ? encodeURIComponent(context.flowName) : ''
  switch (id) {
    case 'welcome': return '#/'
    case 'navigation': return null
    case 'first-flow': return '#/flows/new'
    case 'ingress': return flow ? `#/flows/${flow}/edit` : '#/flows/new'
    case 'monitoring': return flow ? `#/explore?flow=${flow}` : '#/explore'
    case 'done': return null
  }
}

/** True when `hash` already shows the page a step opens (so Next does not
 * reload the page the user is on). Query strings are ignored. */
export function onStepPage(hash: string, href: string | null): boolean {
  if (href === null) return true
  const strip = (value: string) => value.replace(/^#/, '').split('?')[0].replace(/\/+$/, '') || '/'
  return strip(hash) === strip(href)
}

/** Links the final step offers (the repository's docs folder). */
export const DOCS_BASE = 'https://github.com/wcrum/gofi/blob/main/docs'
export const docsUrl = (file: string, anchor = ''): string => `${DOCS_BASE}/${file}${anchor ? `#${anchor}` : ''}`

/** The query flag that reopens the tutorial: `?tutorial=1`, either before the
 * hash (`/?tutorial=1#/flows`) or inside it (`#/flows?tutorial=1`). */
export const TUTORIAL_QUERY = 'tutorial'
export function tutorialRequested(location: { search: string; hash: string }): boolean {
  const flag = (query: string) => new URLSearchParams(query).get(TUTORIAL_QUERY) === '1'
  return flag(location.search) || flag(location.hash.split('?')[1] ?? '')
}
