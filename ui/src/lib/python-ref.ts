// Python function references may omit `library` when the flow sets a default
// library (FlowDefinition.python.library). These helpers resolve and display
// such references consistently.
import type { PythonFunctionRef } from '../types'

/** The library a ref runs in: its own, else the flow default. */
export const refLibrary = (ref: Pick<PythonFunctionRef, 'library'> | undefined, defaultLibrary?: string) => ref?.library || defaultLibrary || ''

/** "library.function"; a ref using the flow default shows just "function"
 * unless the default is known. */
export function pythonRefLabel(ref: Pick<PythonFunctionRef, 'library' | 'function'>, defaultLibrary?: string): string {
  const library = refLibrary(ref, defaultLibrary)
  return library ? `${library}.${ref.function}` : ref.function
}

/** The ref as stored: the library is omitted when it is the flow default. */
export function storedRef<T extends PythonFunctionRef>(ref: T, defaultLibrary?: string): T {
  if (!defaultLibrary || ref.library !== defaultLibrary || ref.inlineSource !== undefined) return ref
  const { library: _library, ...rest } = ref
  void _library
  return rest as T
}

const isRefMissingLibrary = (value: Record<string, unknown>) =>
  typeof value.function === 'string' && value.function !== '' && !value.library && value.inlineSource === undefined

/** How many Python function refs in `value` rely on the flow default library. */
export function countDefaultRefs(value: unknown): number {
  if (Array.isArray(value)) return value.reduce((n: number, item) => n + countDefaultRefs(item), 0)
  if (!value || typeof value !== 'object') return 0
  const record = value as Record<string, unknown>
  return (isRefMissingLibrary(record) ? 1 : 0) + Object.values(record).reduce((n: number, child) => n + countDefaultRefs(child), 0)
}

/** `value` with every ref that relies on the default given `library`
 * explicitly (used when the flow default is removed). Unchanged parts keep
 * their identity. */
export function fillDefaultLibrary<T>(value: T, library: string): T {
  if (Array.isArray(value)) {
    const next = value.map((item) => fillDefaultLibrary(item, library))
    return (next.some((item, i) => item !== value[i]) ? next : value) as T
  }
  if (!value || typeof value !== 'object') return value
  const record = value as Record<string, unknown>
  let changed = false
  const next: Record<string, unknown> = {}
  for (const [key, child] of Object.entries(record)) {
    next[key] = fillDefaultLibrary(child, library)
    if (next[key] !== child) changed = true
  }
  if (isRefMissingLibrary(record)) { next.library = library; changed = true }
  return (changed ? next : value) as T
}
