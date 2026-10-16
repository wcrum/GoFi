import { createClient } from '@connectrpc/connect'
import { createConnectTransport } from '@connectrpc/connect-web'
import { fromJson, toJson, type JsonObject, type JsonValue } from '@bufbuild/protobuf'
import { CelExpressionSchema, DescribeEnvironmentResponseSchema, ExpressionService, type EvaluateExpressionResponse } from './gen/gofi/v1/expression_pb'
import { ValueSchema } from '@bufbuild/protobuf/wkt'
import { jsonValue } from './rpc-model'

const client = createClient(ExpressionService, createConnectTransport({ baseUrl: window.location.origin }))

export type ResultType = 'bool' | 'any'
export interface CelExpression {
  name: string
  description?: string
  expression: string
  tags?: string[]
  resultType?: ResultType
  /** Server-managed; increases when the expression text changes. */
  generation?: number
  usedBy?: string[]
  pinnedRevisions?: { generation: number; expression: string }[]
}
export interface ExpressionDiagnostic { line: number; column: number; message: string }
export interface CelEnvironment {
  variables: { name: string; type: string; description?: string }[]
  functions: { name: string; signatures?: string[]; description?: string }[]
  costLimit: number
  maxExpressionBytes: number
}
export interface EvaluationResult {
  value?: unknown
  error?: string
  diagnostics: ExpressionDiagnostic[]
  cost: number
  durationUs: number
  boundFields: Record<string, unknown>
  boundMeta: Record<string, string>
}

// int64 values arrive as strings in protobuf JSON; the UI uses numbers.
function fromProto(json: Record<string, unknown>): CelExpression {
  const value = json as unknown as CelExpression & { generation?: string | number; pinnedRevisions?: { generation: string | number; expression: string }[] }
  return { ...value, generation: Number(value.generation ?? 0), pinnedRevisions: (value.pinnedRevisions ?? []).map(r => ({ generation: Number(r.generation), expression: r.expression })) }
}

export async function listExpressions(): Promise<CelExpression[]> {
  const { expressions } = await client.listExpressions({})
  return expressions.map(e => fromProto(toJson(CelExpressionSchema, e) as Record<string, unknown>))
}
export async function applyExpression(expression: CelExpression): Promise<{ expression: CelExpression; reloadedFlows: string[] }> {
  const { name, description, expression: text, tags, resultType } = expression
  const response = await client.applyExpression({ expression: fromJson(CelExpressionSchema, jsonValue({ name, description, expression: text, tags, resultType })) })
  return { expression: fromProto(toJson(CelExpressionSchema, response.expression!) as Record<string, unknown>), reloadedFlows: response.reloadedFlows }
}
export async function deleteExpression(name: string): Promise<void> {
  await client.deleteExpression({ name })
}
export async function checkExpression(expression: string, resultType: ResultType): Promise<{ valid: boolean; diagnostics: ExpressionDiagnostic[]; outputType: string }> {
  const response = await client.checkExpression({ expression, resultType })
  return { valid: response.valid, outputType: response.outputType, diagnostics: response.diagnostics.map(d => ({ line: d.line, column: d.column, message: d.message })) }
}
export async function evaluateExpression(request: { expression: string; resultType: ResultType; raw?: string; fields?: Record<string, unknown>; flow?: string }): Promise<EvaluationResult> {
  const response: EvaluateExpressionResponse = await client.evaluateExpression({
    expression: request.expression,
    resultType: request.resultType,
    raw: new TextEncoder().encode(request.raw ?? ''),
    fields: request.fields as JsonObject | undefined,
    flow: request.flow ?? '',
  })
  return {
    value: response.value ? toJson(ValueSchema, response.value) : undefined,
    error: response.error || undefined,
    diagnostics: response.diagnostics.map(d => ({ line: d.line, column: d.column, message: d.message })),
    cost: Number(response.cost),
    durationUs: Number(response.durationUs),
    boundFields: (response.boundFields ?? {}) as Record<string, unknown>,
    boundMeta: response.boundMeta,
  }
}
let environment: Promise<CelEnvironment> | null = null
/** The shared CEL environment; fetched once per page load. */
export function describeEnvironment(): Promise<CelEnvironment> {
  environment ??= client.describeEnvironment({}).then(response => {
    const json = toJson(DescribeEnvironmentResponseSchema, response, { alwaysEmitImplicit: true }) as unknown as CelEnvironment & { costLimit: string | number }
    return { ...json, costLimit: Number(json.costLimit) }
  }).catch(error => { environment = null; throw error })
  return environment
}
