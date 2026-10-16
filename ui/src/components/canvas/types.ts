import type { Node } from '@xyflow/react'
import type { CodecStep, FlowSource, Framing, ParserCfg, SinkCfg, StageSpec } from '../../types'

// Transient mock-playback highlight state, independent of `stats`. Set while
// stepping a test case through the pipeline (see MockPanel): 'active' = the
// step currently on this node; 'pass'/'drop'/'error' = the terminal state for
// a node playback has already moved past. Cleared to undefined outside playback.
export type MockState = 'active' | 'pass' | 'drop' | 'error'

// The fixed pipeline prefix is one node per concern: Source (how bytes
// arrive) -> Frame (how a byte stream becomes frames) -> Decode (byte
// transformations such as base64/gzip) -> Parse (how a frame becomes fields).
// Each holds its own slice of the FlowDefinition; convert.ts merges them.
export type IngressNodeData = {
  source: FlowSource
  stats?: { total: number; lastFields?: Record<string, unknown>; connected: boolean }
  mockState?: MockState
}
export type FrameNodeData = { framing: Framing; mockState?: MockState }
export type DecodeNodeData = { decoders: CodecStep[]; maxDecodedBytes?: number; mockState?: MockState }
export type ParseNodeData = { parser: Omit<ParserCfg, 'decoders' | 'maxDecodedBytes'>; mockState?: MockState }

/** Fixed node ids of the pipeline prefix, in execution order. */
export const PIPELINE_PREFIX = ['ingress', 'frame', 'decode', 'parse'] as const

export type StageNodeData = {
  spec: StageSpec
  stats?: { droppedCount: number; passedCount: number }
  mockState?: MockState
}

// `isDeadLetter` is set only on the single, fixed-id 'deadletter' node — it
// has no edges and represents `FlowDefinition.deadLetterSink`, a floating
// config node rather than part of the traffic-flow chain. SinkNode renders it
// with a distinct visual treatment when this flag is true.
export type SinkNodeData = { sink: SinkCfg; isDeadLetter?: boolean; mockState?: MockState }

export type IngressNode = Node<IngressNodeData, 'ingress'>
export type FrameNode = Node<FrameNodeData, 'frame'>
export type DecodeNode = Node<DecodeNodeData, 'decode'>
export type ParseNode = Node<ParseNodeData, 'parse'>
export type StageNode = Node<StageNodeData, 'stage'>
export type SinkNode = Node<SinkNodeData, 'sink'>
export type FlowNode = IngressNode | FrameNode | DecodeNode | ParseNode | StageNode | SinkNode
