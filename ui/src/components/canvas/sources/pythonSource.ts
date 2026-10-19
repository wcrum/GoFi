// Pure helpers for the Python source form (source.python). Client-side
// mirrors of spec/python_source.go; the server remains authoritative and
// also checks the function's kind against the library's registry on save.
import type { PythonSource } from '../../../types.ts'
import { t } from '../../../i18n/index.ts'

/** The registry fields these helpers read (sandbox-api LibraryFunction). */
export interface SourceFunctionLike { library: string; name: string; kind: string; description?: string; every?: string; managedBy?: string }

export const SOURCE_FUNCTION_KINDS = ['fetcher', 'streamer'] as const
export const DEFAULT_PYTHON_SOURCE: PythonSource = { library: '', function: '' }

/** Fetchers and streamers of saved user libraries (never a flow's inline library). */
export function sourceFunctions<T extends SourceFunctionLike>(functions: T[]): T[] {
  return functions.filter(fn => !fn.managedBy && (SOURCE_FUNCTION_KINDS as readonly string[]).includes(fn.kind))
}

/** Libraries that register at least one fetcher or streamer, plus the current one. */
export function sourceLibraries(functions: SourceFunctionLike[], current?: string): string[] {
  const names = new Set(sourceFunctions(functions).map(fn => fn.library))
  if (current) names.add(current)
  return [...names].sort()
}

/** Function options for one library; a saved choice that is missing (worker
 * not ready, renamed, wrong kind) stays selectable and is marked. */
export function sourceFunctionOptions(functions: SourceFunctionLike[], library: string, current?: string) {
  const options = sourceFunctions(functions).filter(fn => fn.library === library)
    .map(fn => ({ value: fn.name, label: fn.name, description: [fn.kind, fn.kind === 'fetcher' && fn.every ? t('gofi.canvas.python-source.every-interval', 'every {{every}}', { every: fn.every }) : '', fn.description ? fn.description.split('\n')[0] : ''].filter(Boolean).join(' · ') }))
  if (current && !options.some(option => option.value === current)) options.push({ value: current, label: t('gofi.canvas.python-source.function-unavailable', '{{name}} (unavailable)', { name: current }), description: t('gofi.canvas.python-source.not-registered', 'Not registered as a fetcher or streamer by a ready worker') })
  return options
}

/** The registered source function a reference points at, if any. */
export function sourceFunctionOf<T extends SourceFunctionLike>(functions: T[], source: PythonSource): T | undefined {
  return sourceFunctions(functions).find(fn => fn.library === source.library && fn.name === source.function)
}

const UNIT_SECONDS: Record<string, number> = { ns: 1e-9, us: 1e-6, 'µs': 1e-6, ms: 1e-3, s: 1, m: 60, h: 3600 }

/** Seconds in a Go duration ("10s", "1m30s"), or undefined when invalid. */
export function durationSeconds(value: string): number | undefined {
  const text = value.trim()
  if (!text) return undefined
  const pattern = /(\d+(?:\.\d*)?|\.\d+)(ns|us|µs|ms|s|m|h)/y
  let total = 0
  let position = 0
  while (position < text.length) {
    pattern.lastIndex = position
    const match = pattern.exec(text)
    if (!match) return undefined
    total += Number(match[1]) * UNIT_SECONDS[match[2]]
    position = pattern.lastIndex
  }
  return total
}

/** Mirrors spec.PythonSource.Validate for `every` (empty uses the decorator's). */
export function everyProblem(every?: string): string | undefined {
  if (!every) return undefined
  const seconds = durationSeconds(every)
  if (seconds === undefined) return t('gofi.canvas.python-source.every-format', 'Use a duration such as 10s, 1m, or 1m30s.')
  return seconds < 1 ? t('gofi.canvas.python-source.every-min', 'At least 1s.') : undefined
}

/** Parses the config editor's text: a JSON object, or empty for none. */
export function parseConfigText(text: string): { config?: Record<string, unknown>; error?: string } {
  if (!text.trim()) return {}
  try {
    const value: unknown = JSON.parse(text)
    if (!value || typeof value !== 'object' || Array.isArray(value)) return { error: t('gofi.canvas.python-source.config-object', 'Config must be a JSON object, e.g. {"limit": 10}.') }
    const config = value as Record<string, unknown>
    return Object.keys(config).length ? { config } : {}
  } catch (reason) {
    return { error: t('gofi.canvas.python-source.config-invalid-json', 'Invalid JSON: {{reason}}', { reason: reason instanceof Error ? reason.message : String(reason) }) }
  }
}

/** Why a Python source cannot be saved. `kind` lets callers pick problems
 * without inspecting the (localized) message. */
export type PythonSourceProblemKind = 'missing-library' | 'missing-function' | 'every' | 'wrong-kind'
export interface PythonSourceProblem { kind: PythonSourceProblemKind; message: string }

/** Problems a save would hit, in form order. `functions` undefined skips the
 * registry check (the registry has not loaded). */
export function pythonSourceProblems(source: PythonSource, functions?: SourceFunctionLike[]): PythonSourceProblem[] {
  const problems: PythonSourceProblem[] = []
  if (!source.library.trim()) problems.push({ kind: 'missing-library', message: t('gofi.canvas.python-source.choose-library', 'Choose a library.') })
  if (!source.function.trim()) problems.push({ kind: 'missing-function', message: t('gofi.canvas.python-source.choose-function', 'Choose a fetcher or streamer.') })
  const every = everyProblem(source.every)
  if (every) problems.push({ kind: 'every', message: t('gofi.canvas.python-source.every-problem', 'Every: {{problem}}', { problem: every }) })
  if (functions && source.library && source.function) {
    const wrongKind = functions.find(fn => fn.library === source.library && fn.name === source.function && !(SOURCE_FUNCTION_KINDS as readonly string[]).includes(fn.kind))
    if (wrongKind && !sourceFunctionOf(functions, source)) problems.push({ kind: 'wrong-kind', message: t('gofi.canvas.python-source.wrong-kind', '{{library}}.{{function}} is a {{kind}}; a source needs @gofi.fetcher or @gofi.streamer.', { library: source.library, function: source.function, kind: wrongKind.kind }) })
  }
  return problems
}
