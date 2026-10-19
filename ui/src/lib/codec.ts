import type { CodecStep } from '../types'
import { pythonRefLabel } from './python-ref.ts'
import { t } from '../i18n/index.ts'

/** Display name of a decode/encode step: the built-in name or `library.function`
 * (just `function` when it uses the flow's default library, unless given). */
export function codecLabel(step: CodecStep | string): string { return codecLabelWith(step) }
export function codecLabelWith(step: CodecStep | string, defaultLibrary?: string): string {
  // Resources may use the shorthand: a bare built-in name.
  if (typeof step === 'string') return step
  if (step.type === 'python') return step.python?.function ? pythonRefLabel(step.python, defaultLibrary) : t('gofi.lib.codec.python-choose-function', 'Python (choose a function)')
  return step.type
}

/** Ordered steps as "a → b → c". */
export const codecChain = (steps: CodecStep[], defaultLibrary?: string) => steps.map((step) => codecLabelWith(step, defaultLibrary)).join(' → ')
