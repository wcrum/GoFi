import { Handle, Position, type NodeProps } from '@xyflow/react'
import { Badge } from '@grafana/ui'
import { FRAMING_COLOR, PARSER_COLOR } from '../stageColors'
import type { DecodeNode as DecodeNodeType, FrameNode as FrameNodeType, ParseNode as ParseNodeType } from './types'
import { NodeBadges, NodeCard, NodeDetail } from './NodeCard'
import { codecChain, codecLabel } from '../../lib/codec'

/** Frame: how the byte stream (or each datagram) becomes discrete frames. */
export function FrameNode({ data, selected }: NodeProps<FrameNodeType>) {
  const { framing, mockState } = data
  const detail = framing.mode === 'delimited' ? `delimiter ${JSON.stringify(framing.delimiter ?? '')}`
    : framing.mode === 'length-prefix' ? `${framing.prefixLen ?? 4}-byte length prefix`
    : framing.mode === 'python' ? (framing.python?.function ? `Python · ${framing.python.library}.${framing.python.function}` : 'Python framer (choose a function)')
    : framing.mode === 'line' ? 'one frame per line'
    : 'whole connection or datagram is one frame'
  return <NodeCard icon="gf-grid" title="Frame" selected={selected} mockState={mockState}>
    <Handle type="target" position={Position.Left} />
    <NodeBadges><Badge text={framing.mode} color={FRAMING_COLOR[framing.mode] ?? 'darkgrey'} /></NodeBadges>
    <NodeDetail>{detail}</NodeDetail>
    <Handle type="source" position={Position.Right} />
  </NodeCard>
}

/** Decode: ordered byte transformations applied before parsing. */
export function DecodeNode({ data, selected }: NodeProps<DecodeNodeType>) {
  const decoders = data.decoders
  return <NodeCard icon="unlock" title="Decode" selected={selected} mockState={data.mockState}>
    <Handle type="target" position={Position.Left} />
    {decoders.length
      ? <NodeBadges>{decoders.map((decoder, index) => <Badge key={`${codecLabel(decoder)}-${index}`} text={codecLabel(decoder)} color={decoder.type === 'python' ? 'blue' : 'purple'} icon={decoder.type === 'python' ? 'brackets-curly' : undefined} />)}</NodeBadges>
      : <NodeDetail>No decoding: frames pass through</NodeDetail>}
    {decoders.length > 1 && <NodeDetail>{codecChain(decoders)}</NodeDetail>}
    <Handle type="source" position={Position.Right} />
  </NodeCard>
}

/** Parse: how a decoded frame becomes structured fields. */
export function ParseNode({ data, selected }: NodeProps<ParseNodeType>) {
  const { parser, mockState } = data
  const python = parser.format === 'python' && parser.python
  return <NodeCard icon="brackets-curly" title="Parse" selected={selected} mockState={mockState}>
    <Handle type="target" position={Position.Left} />
    <NodeBadges><Badge text={python ? 'python' : parser.format || 'none'} color={python ? 'blue' : PARSER_COLOR[parser.format] ?? 'darkgrey'} /></NodeBadges>
    {python ? <NodeDetail title={`${python.library}.${python.function}`}>{python.library}.{python.function}</NodeDetail> : null}
    <NodeDetail>{parser.errorPolicy === 'drop' ? 'errors drop the frame' : 'errors continue'}</NodeDetail>
    <Handle type="source" position={Position.Right} />
  </NodeCard>
}
