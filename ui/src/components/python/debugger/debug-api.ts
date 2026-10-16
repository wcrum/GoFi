import { createClient, ConnectError, Code } from '@connectrpc/connect'
import { createConnectTransport } from '@connectrpc/connect-web'
import { toJson, type JsonObject } from '@bufbuild/protobuf'
import { GoFiService, SimulateResultSchema } from '../../../gen/gofi/v1/gofi_pb'
import { SandboxService, type DebugRecording as ProtoRecording, type DebugVariable as ProtoVariable, type RunFunctionResponse } from '../../../gen/gofi/v1/sandbox_pb'
import type { PythonFunctionKind, PythonRunResult } from '../../../sandbox-api'
import type { SimulateResult } from '../../../types'

export interface DebugVariable { name: string; type: string; repr: string; children: DebugVariable[]; changed: boolean; truncated: boolean }
export interface DebugWatch { expression: string; repr: string; error: string }
export interface DebugStep {
  index: number; event: 'call' | 'line' | 'return' | 'exception' | string; line: number; function: string; depth: number
  stack: { function: string; line: number }[]
  locals: DebugVariable[]; changedGlobals: DebugVariable[]; watches: DebugWatch[]
  stdoutLength: number; returnValue?: DebugVariable; exception: string
}
export interface DebugRecording {
  steps: DebugStep[]; result?: PythonRunResult; initialGlobals: DebugVariable[]
  truncated: boolean; truncationReason: string; entryLine: number; runId: string
}

const transport = createConnectTransport({ baseUrl: window.location.origin })
const sandbox = createClient(SandboxService, transport)
const gofi = createClient(GoFiService, transport)
const encoder = new TextEncoder()
const decoder = new TextDecoder('utf-8', { fatal: true })

/** True when this controller does not implement the debugger RPCs yet. */
export const debuggerUnavailable = (error: unknown) => ConnectError.from(error).code === Code.Unimplemented
export const debugErrorMessage = (error: unknown) => debuggerUnavailable(error) ? 'This GoFi controller cannot record debug sessions yet.' : ConnectError.from(error).rawMessage || String(error)

const variable = (value: ProtoVariable): DebugVariable => ({ name: value.name, type: value.type, repr: value.repr, children: value.children.map(variable), changed: value.changed, truncated: value.truncated })
function result(value: RunFunctionResponse): PythonRunResult {
  const output: PythonRunResult = { fields: (value.fields ?? {}) as Record<string, unknown>, drop: value.drop, stdout: value.stdout, stderr: value.stderr, durationUs: Number(value.durationUs), entryLine: value.entryLine, executedLines: [...value.executedLines], runId: value.runId }
  if (value.raw !== undefined) { try { output.rawText = decoder.decode(value.raw) } catch { output.rawBase64 = btoa(String.fromCharCode(...value.raw)) } }
  if (value.error) output.error = value.error
  if (value.errorLocation && value.errorLocation.line > 0) output.errorLocation = { line: value.errorLocation.line, column: value.errorLocation.column, code: value.errorLocation.code, message: value.errorLocation.message }
  return output
}
export function recordingFromProto(value: ProtoRecording): DebugRecording {
  return {
    steps: value.steps.map(step => ({
      index: step.index, event: step.event, line: step.line, function: step.function, depth: step.depth,
      stack: step.stack.map(frame => ({ function: frame.function, line: frame.line })),
      locals: step.locals.map(variable), changedGlobals: step.changedGlobals.map(variable),
      watches: step.watches.map(watch => ({ expression: watch.expression, repr: watch.repr, error: watch.error })),
      stdoutLength: step.stdoutLength, returnValue: step.returnValue ? variable(step.returnValue) : undefined, exception: step.exception,
    })),
    result: value.result ? result(value.result) : undefined,
    initialGlobals: value.initialGlobals.map(variable),
    truncated: value.truncated, truncationReason: value.truncationReason, entryLine: value.entryLine, runId: value.runId,
  }
}

export interface DebugInput { kind: PythonFunctionKind; name: string; raw: string; fields: Record<string, unknown>; meta: Record<string, unknown>; config?: Record<string, unknown> }
/** Records one traced execution: a saved library's function, or unsaved source. */
export async function debugFunction(target: { library?: string; source?: string; timeoutSeconds?: number }, input: DebugInput, options: { watches: string[]; runId: string; maxSteps?: number }): Promise<DebugRecording> {
  return recordingFromProto(await sandbox.debugFunction({
    library: target.library ?? '', source: target.library ? '' : (target.source ?? ''), name: input.name, kind: input.kind,
    raw: encoder.encode(input.raw), fields: input.fields as JsonObject, meta: input.meta as JsonObject, config: input.config as JsonObject | undefined,
    watches: options.watches, maxSteps: options.maxSteps ?? 0, runId: options.runId, timeoutSeconds: target.timeoutSeconds ?? 0,
  }))
}

export interface FrameDebug {
  simulation?: SimulateResult
  step: string
  function?: { library: string; function: string; inlineSource?: string }
  inputFields: Record<string, unknown>; inputMeta: Record<string, string>; inputRaw: string
  recording?: DebugRecording
  notReached: string
}
/** Simulates a captured frame through a configured flow up to a Python
 * step and records that step. Nothing is sent to any destination. */
export async function debugFrame(flowName: string, raw: string, step: string, watches: string[]): Promise<FrameDebug> {
  const response = await gofi.debugFrame({ flowName, raw: encoder.encode(raw), step, watches, maxSteps: 0 })
  let inputRaw = ''
  try { inputRaw = decoder.decode(response.inputRaw) } catch { inputRaw = `base64:${btoa(String.fromCharCode(...response.inputRaw))}` }
  const simulation = response.simulation ? toJson(SimulateResultSchema, response.simulation, { alwaysEmitImplicit: true }) as unknown as SimulateResult : undefined
  return {
    simulation: simulation ? { ...simulation, parsedFields: simulation.parsedFields ?? {}, stages: (simulation.stages ?? []).map(stage => ({ ...stage, fields: stage.fields ?? {} })) } : undefined,
    step: response.step,
    function: response.function ? { library: response.function.library, function: response.function.function, inlineSource: response.function.inlineSource || undefined } : undefined,
    inputFields: (response.inputFields ?? {}) as Record<string, unknown>, inputMeta: { ...response.inputMeta }, inputRaw,
    recording: response.recording ? recordingFromProto(response.recording) : undefined,
    notReached: response.notReached,
  }
}
