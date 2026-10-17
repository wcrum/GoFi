import { createClient, ConnectError, Code } from '@connectrpc/connect'
import { createConnectTransport } from '@connectrpc/connect-web'
import { fromJson, toJson, type JsonValue, type JsonObject } from '@bufbuild/protobuf'
import { timestampDate } from '@bufbuild/protobuf/wkt'
import { jsonValue } from './rpc-model'
import {
  SandboxService, SandboxDefinitionSchema, SandboxStatusSchema, LibraryFunctionSchema,
  DescribePythonEnvironmentResponseSchema, type RunFunctionResponse,
} from './gen/gofi/v1/sandbox_pb'

export type PythonFunctionKind = 'framer' | 'decoder' | 'parser' | 'transformer' | 'encoder' | 'sink'
export interface EgressRule { cidr?: string; port?: number; protocol?: string; namespace?: string; podSelector?: Record<string, string>; description?: string }
/** A saved sample input. `raw` is UTF-8 text in the UI and bytes on the wire. */
export interface FunctionTest {
  name: string; function: string; kind: PythonFunctionKind
  raw?: string; fields?: Record<string, unknown>; meta?: Record<string, unknown>; config?: Record<string, unknown>
  expectFields?: Record<string, unknown>; expectDrop?: boolean
}
export interface SandboxDefinition {
  name: string; sourceCode: string; timeoutSeconds?: number; memoryMib?: number
  tests?: FunctionTest[]
  /** Empty for user libraries; "flow:<flow name>" for controller-managed inline libraries. */
  managedBy?: string
  egress?: EgressRule[]
}
export interface SandboxStatus { name: string; phase: string; error?: string }
export interface FunctionParam { name: string; type?: string; default?: unknown; required?: boolean; description?: string }
/** managedBy is "flow:<name>" for a library GoFi generates from that flow's inline Python. */
export interface LibraryFunction { library: string; name: string; kind: PythonFunctionKind; description: string; params: FunctionParam[]; managedBy?: string }
export interface SourceDiagnostic { line: number; column: number; code: string; message: string }
export interface SourceValidation { valid: boolean; diagnostics: SourceDiagnostic[] }
export interface PythonRunResult {
  fields: Record<string, unknown>
  rawBase64?: string
  /** Raw output decoded as UTF-8 when it is valid text. */
  rawText?: string
  drop: boolean
  stdout: string
  stderr: string
  durationUs: number
  /** User-code exception; the call itself succeeded. */
  error?: string
  errorLocation?: SourceDiagnostic
  /** Line of the `def` that received the input (0 when unknown). */
  entryLine: number
  /** With traceLines: source lines executed for this input, first-hit order. */
  executedLines: number[]
  /** Correlates this run with WorkerLogEntry.invocationId. */
  runId: string
}
/** One line of live worker output. */
export interface WorkerLogEntry {
  /** RFC 3339 timestamp. */
  time?: string
  stream: 'stdout' | 'stderr' | 'system' | string
  line: string
  invocationId: string
  function: string
  kind: string
  level: string
}
export interface PythonEnvironment {
  pythonVersion: string
  packages: { name: string; version: string; summary?: string }[]
  decorators: { name: string; signature: string; description?: string }[]
  networkPolicy: string
  maxTimeoutSeconds: number
  maxMemoryMib: number
}

const client = createClient(SandboxService, createConnectTransport({ baseUrl: window.location.origin }))
const encoder = new TextEncoder()
const decoder = new TextDecoder('utf-8', { fatal: true })
const toBase64 = (bytes: Uint8Array) => { let binary = ''; for (const byte of bytes) binary += String.fromCharCode(byte); return btoa(binary) }
const fromBase64 = (value: string) => Uint8Array.from(atob(value), character => character.charCodeAt(0))
const textFromBase64 = (value?: string) => { if (!value) return ''; try { return decoder.decode(fromBase64(value)) } catch { return '' } }

/** True when the server does not implement an RPC yet (older controller). */
export function isUnimplemented(error: unknown): boolean { return ConnectError.from(error).code === Code.Unimplemented }

function definitionToJson(sandbox: SandboxDefinition): JsonValue {
  const json = { ...sandbox, tests: (sandbox.tests ?? []).map(test => ({ ...test, raw: test.raw ? toBase64(encoder.encode(test.raw)) : undefined })) }
  // fromJson rejects explicit undefined (e.g. bytes `raw`), so drop those keys.
  return JSON.parse(JSON.stringify(json)) as JsonValue
}
function definitionFromJson(json: JsonValue): SandboxDefinition {
  const value = json as unknown as SandboxDefinition & { tests?: (FunctionTest & { raw?: string })[] }
  return { ...value, tests: (value.tests ?? []).map(test => ({ ...test, raw: textFromBase64(test.raw) })) }
}

export async function listSandboxes(): Promise<{ sandboxes: SandboxDefinition[]; statuses: SandboxStatus[] }> {
  const response = await client.listSandboxes({})
  return { sandboxes: response.sandboxes.map(value => definitionFromJson(toJson(SandboxDefinitionSchema, value))), statuses: response.statuses.map(value => toJson(SandboxStatusSchema, value) as unknown as SandboxStatus) }
}
export async function applySandbox(sandbox: SandboxDefinition): Promise<{ sandbox: SandboxDefinition; status?: SandboxStatus }> {
  const response = await client.applySandbox({ sandbox: fromJson(SandboxDefinitionSchema, jsonValue(definitionToJson(sandbox))) })
  if (!response.sandbox) throw new Error('GoFi returned no Python library')
  return { sandbox: definitionFromJson(toJson(SandboxDefinitionSchema, response.sandbox)), status: response.status ? toJson(SandboxStatusSchema, response.status) as unknown as SandboxStatus : undefined }
}
export async function deleteSandbox(name: string): Promise<void> { await client.deleteSandbox({ name }) }
export async function listLibraryFunctions(): Promise<LibraryFunction[]> {
  return (await client.listFunctions({})).functions.map(fn => {
    const json = toJson(LibraryFunctionSchema, fn) as unknown as Omit<LibraryFunction, 'params'> & { params?: FunctionParam[] }
    return { ...json, kind: fn.kind as PythonFunctionKind, description: fn.description, params: json.params ?? [] }
  })
}
export async function formatPythonSource(source: string): Promise<string> { return (await client.formatSource({ source })).source }
export async function validatePythonSource(source: string): Promise<SourceValidation> {
  const result = await client.validateSource({ source })
  return { valid: result.valid, diagnostics: result.diagnostics.map(({ line, column, code, message }) => ({ line, column, code, message })) }
}

function runResult(result: RunFunctionResponse): PythonRunResult {
  const output: PythonRunResult = { fields: (result.fields ?? {}) as Record<string, unknown>, drop: result.drop, stdout: result.stdout, stderr: result.stderr, durationUs: Number(result.durationUs), entryLine: result.entryLine, executedLines: [...result.executedLines], runId: result.runId }
  if (result.raw !== undefined) {
    output.rawBase64 = toBase64(result.raw)
    try { output.rawText = decoder.decode(result.raw) } catch { /* binary output */ }
  }
  if (result.error) output.error = result.error
  if (result.errorLocation && result.errorLocation.line > 0) output.errorLocation = { line: result.errorLocation.line, column: result.errorLocation.column, code: result.errorLocation.code, message: result.errorLocation.message }
  return output
}
interface RunInput {
  kind: PythonFunctionKind; name: string; raw: string; fields: Record<string, unknown>; meta: Record<string, unknown>; config?: Record<string, unknown>
  /** Client-chosen id; live log lines of this run carry it as invocationId. */
  runId?: string
  /** Report the source lines executed for this input (slower). */
  traceLines?: boolean
}
export async function runPythonFunction({ library, name, kind, raw, fields, meta, config, runId, traceLines }: RunInput & { library: string }): Promise<PythonRunResult> {
  return runResult(await client.runFunction({ library, name, kind, raw: encoder.encode(raw), fields: fields as JsonObject, meta: meta as JsonObject, config: config as JsonObject | undefined, runId: runId ?? '', traceLines: traceLines ?? false }))
}
/** Runs unsaved source in a short-lived isolated worker; nothing is saved. */
export async function runPythonSource({ source, name, kind, raw, fields, meta, config, timeoutSeconds, memoryMib, runId, traceLines }: RunInput & { source: string; timeoutSeconds?: number; memoryMib?: number }): Promise<PythonRunResult> {
  return runResult(await client.runSource({ source, name, kind, raw: encoder.encode(raw), fields: fields as JsonObject, meta: meta as JsonObject, config: config as JsonObject | undefined, timeoutSeconds: timeoutSeconds ?? 0, memoryMib: memoryMib ?? 0, runId: runId ?? '', traceLines: traceLines ?? false }))
}

export type LogStreamState = 'connecting' | 'live' | 'reconnecting' | 'ended' | 'unavailable'
/** Follows a worker's live output: a saved library (`library`) or the
 * short-lived worker of one unsaved run (`runId`). A library stream reconnects
 * with backoff; a run stream ends when its worker exits. Returns a stop
 * function that aborts the request and any pending retry. */
export function watchWorkerLogs(target: { library?: string; runId?: string; tailLines?: number }, onEntries: (entries: WorkerLogEntry[]) => void, onState: (state: LogStreamState, detail?: string) => void): () => void {
  const abort = new AbortController()
  let retryTimer: ReturnType<typeof setTimeout> | undefined
  let finishRetry: (() => void) | undefined
  const follow = async () => {
    let delay = 1000
    while (!abort.signal.aborted) {
      onState('connecting')
      try {
        // Batch lines per animation frame so bursts render once.
        let batch: WorkerLogEntry[] = []
        let frame: number | undefined
        const flush = () => { frame = undefined; if (batch.length) { onEntries(batch); batch = [] } }
        for await (const entry of client.watchWorkerLogs({ library: target.library ?? '', runId: target.runId ?? '', tailLines: target.tailLines ?? 100 }, { signal: abort.signal, onHeader: () => { delay = 1000; onState('live') } })) {
          if (abort.signal.aborted) return
          batch.push({ time: entry.time ? timestampDate(entry.time).toISOString() : undefined, stream: entry.stream, line: entry.line, invocationId: entry.invocationId, function: entry.function, kind: entry.kind, level: entry.level })
          if (frame === undefined) frame = requestAnimationFrame(flush)
        }
        if (frame !== undefined) cancelAnimationFrame(frame)
        flush()
        if (abort.signal.aborted) return
        // A run's worker exited: its stream is complete.
        if (target.runId) { onState('ended'); return }
      } catch (error) {
        if (abort.signal.aborted) return
        const code = ConnectError.from(error).code
        if (code === Code.Unimplemented) { onState('unavailable', 'This GoFi controller cannot stream worker logs yet.'); return }
        if (code === Code.PermissionDenied || code === Code.Unauthenticated) { onState('unavailable', ConnectError.from(error).rawMessage); return }
        if (target.runId && code === Code.NotFound) { onState('ended', ConnectError.from(error).rawMessage); return }
        onState('reconnecting', ConnectError.from(error).rawMessage)
      }
      await new Promise<void>(resolve => { finishRetry = resolve; retryTimer = setTimeout(resolve, delay) })
      finishRetry = undefined; retryTimer = undefined
      delay = Math.min(delay * 2, 10000)
    }
  }
  void follow()
  return () => { abort.abort(); if (retryTimer) clearTimeout(retryTimer); finishRetry?.() }
}
export async function describePythonEnvironment(): Promise<PythonEnvironment> {
  const json = toJson(DescribePythonEnvironmentResponseSchema, await client.describeEnvironment({}), { alwaysEmitImplicit: true }) as unknown as PythonEnvironment
  return { ...json, packages: json.packages ?? [], decorators: json.decorators ?? [] }
}
