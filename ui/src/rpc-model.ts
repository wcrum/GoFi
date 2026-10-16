import { fromJson, toJson, type JsonValue } from '@bufbuild/protobuf'
import { FlowSchema, SimulateResultSchema, TrafficEventSchema, type Flow, type SimulateResult as ProtoSimulateResult, type TrafficEvent } from './gen/gofi/v1/gofi_pb.ts'
import type { FlowDefinition, SimulateResult, TapEvent } from './types'

export function flowToProto(flow: FlowDefinition): Flow {
  return fromJson(FlowSchema, jsonValue(flow))
}
export function flowFromProto(flow: Flow): FlowDefinition {
  const json = toJson(FlowSchema, flow) as unknown as FlowDefinition
  return { ...json, source: json.source ?? { listen: { network: 'tcp', port: 0 } }, framing: json.framing ?? { mode: 'line' }, parser: json.parser ?? { format: '' }, stages: json.stages ?? [], sinks: json.sinks ?? [] }
}
export function trafficFromProto(event: TrafficEvent): TapEvent {
  const json = toJson(TrafficEventSchema, event, { alwaysEmitImplicit: true }) as unknown as TapEvent
  return { ...json, fields: json.fields ?? {}, meta: json.meta ?? {} }
}
export function simulationFromProto(result: ProtoSimulateResult): SimulateResult {
  const json = toJson(SimulateResultSchema, result, { alwaysEmitImplicit: true }) as unknown as SimulateResult
  return { ...json, parsedFields: json.parsedFields ?? {}, stages: (json.stages ?? []).map((stage) => ({ ...stage, fields: stage.fields ?? {} })) }
}

/** A plain JSON value from a UI object: drops `undefined` fields (which the
 * protobuf JSON decoder rejects) exactly as a real JSON document would. */
export function jsonValue(value: unknown): JsonValue {
  return JSON.parse(JSON.stringify(value ?? null)) as JsonValue
}
