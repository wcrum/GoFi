import type { CodecStep } from '../types'

/** Display name of a decode/encode step: the built-in name or `library.function`. */
export function codecLabel(step: CodecStep): string {
  if (step.type === 'python') return step.python?.function ? `${step.python.library}.${step.python.function}` : 'Python (choose a function)'
  return step.type
}

/** Ordered steps as "a → b → c". */
export const codecChain = (steps: CodecStep[]) => steps.map(codecLabel).join(' → ')
