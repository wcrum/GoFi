import { fromJson, toJson, type JsonValue } from '@bufbuild/protobuf'
import { ListenerSchema, SimulateResultSchema, TrafficEventSchema, type Listener, type SimulateResult as ProtoSimulateResult, type TrafficEvent } from './gen/gofi/v1/gofi_pb.ts'
import type { ListenerDefinition, SimulateResult, TapEvent } from './types'

export function listenerToProto(listener: ListenerDefinition): Listener {
  return fromJson(ListenerSchema, listener as unknown as JsonValue)
}
export function listenerFromProto(listener: Listener): ListenerDefinition {
  const json = toJson(ListenerSchema, listener) as unknown as ListenerDefinition
  return { ...json, source: json.source ?? { network: 'tcp', port: 0 }, framing: json.framing ?? { mode: 'line' }, parser: json.parser ?? { format: '' }, stages: json.stages ?? [], sinks: json.sinks ?? [] }
}
export function trafficFromProto(event: TrafficEvent): TapEvent {
  const json = toJson(TrafficEventSchema, event, { alwaysEmitImplicit: true }) as unknown as TapEvent
  return { ...json, fields: json.fields ?? {}, meta: json.meta ?? {} }
}
export function simulationFromProto(result: ProtoSimulateResult): SimulateResult {
  const json = toJson(SimulateResultSchema, result, { alwaysEmitImplicit: true }) as unknown as SimulateResult
  return { ...json, parsedFields: json.parsedFields ?? {}, stages: (json.stages ?? []).map((stage) => ({ ...stage, fields: stage.fields ?? {} })) }
}
