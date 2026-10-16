import { isUnimplemented, runPythonFunction, runPythonSource, type PythonFunctionKind, type PythonRunResult } from '../../sandbox-api'

export interface RunTarget {
  /** Current editor source. */
  source: string
  /** Saved library name, when the source matches what is saved and launched. */
  savedLibrary?: string
  timeoutSeconds?: number
  memoryMib?: number
}
export interface RunInput {
  kind: PythonFunctionKind; name: string; raw: string; fields: Record<string, unknown>; meta: Record<string, unknown>; config?: Record<string, unknown>
  /** Correlates the run with live worker log lines. */
  runId?: string
  traceLines?: boolean
}
export interface RunOutcome { result: PythonRunResult; mode: 'unsaved' | 'saved' }

/** Saved, launched libraries run in their worker; anything else runs as
 * unsaved source in a short-lived isolated worker. Neither runs here. */
export async function runPython(target: RunTarget, input: RunInput): Promise<RunOutcome> {
  if (target.savedLibrary) return { result: await runPythonFunction({ ...input, library: target.savedLibrary }), mode: 'saved' }
  try {
    return { result: await runPythonSource({ ...input, source: target.source, timeoutSeconds: target.timeoutSeconds, memoryMib: target.memoryMib }), mode: 'unsaved' }
  } catch (error) {
    if (isUnimplemented(error)) throw new Error('This GoFi controller cannot run unsaved source yet. Save and launch the library, then run it.')
    throw error
  }
}

/** Parses a JSON object input, naming the field in errors. */
export function objectInput(value: string, label: string): Record<string, unknown> {
  if (!value.trim()) return {}
  const parsed: unknown = JSON.parse(value)
  if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) throw new Error(`${label} must be a JSON object`)
  return parsed as Record<string, unknown>
}
