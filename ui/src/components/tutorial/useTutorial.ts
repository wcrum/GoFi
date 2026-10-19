import { useCallback, useEffect, useRef, useState } from 'react'
import { navigate } from '../../router'
import { STEP_COUNT, clampStep, onStepPage, stepHref, stepId, tutorialRequested, type StepContext } from './model'
import { readTutorialState, resumeStep, shouldAutoOpen, writeTutorialState } from './storage'

export interface TutorialController {
  open: boolean
  step: number
  /** Opens at the remembered step (step 1 after it was finished). Never
   * navigates: the first step explains itself inside the panel. */
  start: () => void
  /** Shows a step and opens the page it talks about, unless already there. */
  goTo: (step: number, context?: StepContext) => void
  /** "Skip for now" and the close button: remembers the step for later. */
  skip: () => void
  /** The last step's Done. */
  finish: () => void
}

/** The tutorial's open state and step, persisted under gofi.tutorial.v1.
 * Opens by itself on a first visit, after a reload while it was open, and
 * when the URL carries ?tutorial=1. */
export function useTutorial(): TutorialController {
  const [open, setOpen] = useState(false)
  const [step, setStep] = useState(1)
  const stepRef = useRef(step)
  stepRef.current = step

  const openAt = useCallback((at: number) => {
    const target = clampStep(at)
    setStep(target); setOpen(true)
    writeTutorialState({ status: 'active', step: target })
  }, [])

  useEffect(() => {
    const state = readTutorialState()
    if (tutorialRequested(window.location) || shouldAutoOpen(state)) openAt(resumeStep(state))
  }, [openAt])

  const start = useCallback(() => openAt(resumeStep(readTutorialState())), [openAt])
  const goTo = useCallback((next: number, context: StepContext = {}) => {
    const target = clampStep(next)
    setStep(target)
    writeTutorialState({ status: 'active', step: target })
    const href = stepHref(stepId(target), context)
    if (href && !onStepPage(window.location.hash, href)) navigate(href)
  }, [])
  const skip = useCallback(() => { setOpen(false); writeTutorialState({ status: 'dismissed', step: stepRef.current }) }, [])
  const finish = useCallback(() => { setOpen(false); writeTutorialState({ status: 'done', step: STEP_COUNT }) }, [])

  return { open, step, start, goTo, skip, finish }
}
