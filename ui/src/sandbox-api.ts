import { createClient } from '@connectrpc/connect'
import { createConnectTransport } from '@connectrpc/connect-web'
import { fromJson, toJson, type JsonValue, type JsonObject } from '@bufbuild/protobuf'
import { SandboxService, SandboxDefinitionSchema, SandboxStatusSchema } from './gen/gofi/v1/sandbox_pb'
export interface SandboxDefinition { name: string; sourceCode: string; timeoutSeconds?: number; memoryMib?: number }
export interface SandboxStatus { name: string; phase: string; error?: string }
export type PythonFunctionKind = 'parser' | 'transformer' | 'sink' | 'listener'
export interface LibraryFunction { library: string; name: string; kind: PythonFunctionKind; description: string }
const client = createClient(SandboxService, createConnectTransport({ baseUrl: window.location.origin }))
export async function listSandboxes(): Promise<{ sandboxes: SandboxDefinition[]; statuses: SandboxStatus[] }> {
  const response = await client.listSandboxes({})
  return { sandboxes: response.sandboxes.map(value => toJson(SandboxDefinitionSchema, value) as unknown as SandboxDefinition), statuses: response.statuses.map(value => toJson(SandboxStatusSchema, value) as unknown as SandboxStatus) }
}
export async function applySandbox(sandbox: SandboxDefinition): Promise<{ sandbox: SandboxDefinition; status?: SandboxStatus }> {
  const response = await client.applySandbox({ sandbox: fromJson(SandboxDefinitionSchema, sandbox as unknown as JsonValue) })
  if (!response.sandbox) throw new Error('GoFi returned no Python library')
  return { sandbox: toJson(SandboxDefinitionSchema, response.sandbox) as unknown as SandboxDefinition, status: response.status ? toJson(SandboxStatusSchema, response.status) as unknown as SandboxStatus : undefined }
}
export async function deleteSandbox(name: string): Promise<void> { await client.deleteSandbox({ name }) }
export async function listLibraryFunctions(): Promise<LibraryFunction[]> {
  return (await client.listFunctions({})).functions.map(({library,name,kind,description}) => ({library,name,kind:kind as PythonFunctionKind,description}))
}
export interface SourceDiagnostic { line: number; column: number; code: string; message: string }
export interface SourceValidation { valid: boolean; diagnostics: SourceDiagnostic[] }
export interface PythonRunResult { fields: Record<string, unknown>; rawBase64?: string; drop: boolean }
export async function formatPythonSource(source: string): Promise<string> { return (await client.formatSource({ source })).source }
export async function validatePythonSource(source: string): Promise<SourceValidation> {
  const result = await client.validateSource({ source })
  return { valid: result.valid, diagnostics: result.diagnostics.map(({line,column,code,message}) => ({line,column,code,message})) }
}
export async function runPythonFunction({library,name,kind,raw,fields,meta}: {library:string;name:string;kind:PythonFunctionKind;raw:string;fields:Record<string,unknown>;meta:Record<string,unknown>}): Promise<PythonRunResult> {
  const result = await client.runFunction({ library, name, kind, raw: new TextEncoder().encode(raw), fields: fields as JsonObject, meta: meta as JsonObject })
  const output: PythonRunResult = { fields: result.fields ?? {}, drop: result.drop }
  if (result.raw !== undefined) { let binary=''; for (const byte of result.raw) binary+=String.fromCharCode(byte); output.rawBase64=btoa(binary) }
  return output
}
