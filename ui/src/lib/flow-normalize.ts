// One place that turns any FlowDefinition the UI reads (API, YAML, revision
// history) into the current shape, so every view sees only that shape:
//  - decoding lives in top-level `decode`, not `parser.decoders` /
//    `parser.maxDecodedBytes` (the legacy location the backend still accepts).
import type { FlowDefinition } from '../types'

/** The flow in the current shape. Legacy parser decoders move to `decode`
 * (an existing `decode` wins); an empty `decode` is dropped. Pure: the input
 * is not modified. */
export function normalizeFlow<T extends Partial<FlowDefinition>>(flow: T): T {
  if (!flow || typeof flow !== 'object') return flow
  const { decoders: legacyDecoders, maxDecodedBytes: legacyMax, ...parser } = flow.parser ?? ({} as NonNullable<T['parser']>)
  const decoders = flow.decode?.decoders?.length ? flow.decode.decoders : legacyDecoders ?? []
  const maxDecodedBytes = flow.decode?.maxDecodedBytes || legacyMax || undefined
  const { decode: _decode, ...rest } = flow
  void _decode
  const out = { ...rest } as T
  if (flow.parser) (out as Partial<FlowDefinition>).parser = parser as FlowDefinition['parser']
  if (decoders.length || maxDecodedBytes) (out as Partial<FlowDefinition>).decode = { decoders, ...(maxDecodedBytes ? { maxDecodedBytes } : {}) }
  return out
}

/** Pipeline order of a Flow spec's keys, as the resource YAML writes them. */
export const FLOW_SPEC_KEY_ORDER = ['source', 'framing', 'decode', 'parser', 'stages', 'sinks', 'deadLetterSink', 'testCases', 'python', 'state'] as const

/** Identity of a flow for change detection, independent of key order: the
 * graph builds `name` first and `decode` before `parser`, while a resource
 * read back from YAML has them elsewhere. Both sides go through this. */
export const flowKey = (flow: Partial<FlowDefinition>): string => JSON.stringify(orderFlowSpec(normalizeFlow(flow) as Record<string, unknown>))

/** The spec with keys in pipeline order (unknown keys keep their place after). */
export function orderFlowSpec<T extends Record<string, unknown>>(spec: T): T {
  const ordered: Record<string, unknown> = {}
  for (const key of FLOW_SPEC_KEY_ORDER) if (key in spec && spec[key] !== undefined) ordered[key] = spec[key]
  for (const [key, value] of Object.entries(spec)) if (!(key in ordered) && value !== undefined) ordered[key] = value
  return ordered as T
}
