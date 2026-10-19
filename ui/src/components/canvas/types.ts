import type { Node } from '@xyflow/react'
import type { CodecStep, Encoding, FlowSource, Framing, ParserCfg, SinkCfg, StageSpec } from '../../types'

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
  /** `pending`: the tap stream has not reported yet; the section renders placeholders at its final size. */
  stats?: { total: number; lastFields?: Record<string, unknown>; connected: boolean; pending?: boolean }
  mockState?: MockState
}
export type FrameNodeData = { framing: Framing; mockState?: MockState }
// Decode edits FlowDefinition.decode; Parse edits FlowDefinition.parser.
export type DecodeNodeData = { decoders: CodecStep[]; maxDecodedBytes?: number; mockState?: MockState }
export type ParseNodeData = { parser: Omit<ParserCfg, 'decoders' | 'maxDecodedBytes'>; mockState?: MockState }

/** Fixed node ids of the pipeline prefix, in execution order. */
export const PIPELINE_PREFIX = ['ingress', 'frame', 'decode', 'parse'] as const

export type StageNodeData = {
  spec: StageSpec
  stats?: { droppedCount: number; passedCount: number; pending?: boolean }
  mockState?: MockState
}

// Encode: how a message becomes the bytes its sinks send (a format, then
// encoders in order). One Encode node feeds any number of sinks; on the wire
// it is still each connected sink's `encoding` (convert.ts derives the nodes
// from identical per-sink encodings and writes them back on save), so the
// spec, controller, and CRD are unchanged.
export type EncodeNodeData = {
  encoding: Encoding
  /** How many sinks connect from this node (derived from the edges by FlowCanvas; display only). */
  sinkCount?: number
  mockState?: MockState
}

// `isDeadLetter` is set only on the single, fixed-id 'deadletter' node. It
// represents `FlowDefinition.deadLetterSink`: not part of the traffic-flow
// chain (nothing in the chain connects to it), though an Encode node may feed
// it. SinkNode renders it with a distinct visual treatment when this flag is
// true. A sink's `encoding` is never kept here: it lives on the Encode node
// the sink is connected from.
export type SinkNodeData = { sink: SinkCfg; isDeadLetter?: boolean; mockState?: MockState }

export type IngressNode = Node<IngressNodeData, 'ingress'>
export type FrameNode = Node<FrameNodeData, 'frame'>
export type DecodeNode = Node<DecodeNodeData, 'decode'>
export type ParseNode = Node<ParseNodeData, 'parse'>
export type StageNode = Node<StageNodeData, 'stage'>
export type EncodeNode = Node<EncodeNodeData, 'encode'>
export type SinkNode = Node<SinkNodeData, 'sink'>
export type FlowNode = IngressNode | FrameNode | DecodeNode | ParseNode | StageNode | EncodeNode | SinkNode
