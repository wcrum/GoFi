import type { Node } from '@xyflow/react'
import type { Source, Framing, ParserCfg, SinkCfg, StageSpec } from '../../types'

// Transient mock-playback highlight state, independent of `stats`. Set while
// stepping a test case through the pipeline (see MockPanel): 'active' = the
// step currently on this node; 'pass'/'drop'/'error' = the terminal state for
// a node playback has already moved past. Cleared to undefined outside playback.
export type MockState = 'active' | 'pass' | 'drop' | 'error'

export type IngressNodeData = {
  source: Source
  framing: Framing
  parser: ParserCfg
  stats?: { total: number; lastFields?: Record<string, unknown>; connected: boolean }
  mockState?: MockState
}

export type StageNodeData = {
  spec: StageSpec
  stats?: { droppedCount: number; passedCount: number }
  mockState?: MockState
}

// `isDeadLetter` is set only on the single, fixed-id 'deadletter' node — it
// has no edges and represents `ListenerDefinition.deadLetterSink`, a floating
// config node rather than part of the traffic-flow chain. SinkNode renders it
// with a distinct visual treatment when this flag is true.
export type SinkNodeData = { sink: SinkCfg; isDeadLetter?: boolean; mockState?: MockState }

export type IngressNode = Node<IngressNodeData, 'ingress'>
export type StageNode = Node<StageNodeData, 'stage'>
export type SinkNode = Node<SinkNodeData, 'sink'>
export type FlowNode = IngressNode | StageNode | SinkNode
