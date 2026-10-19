import { Handle, Position, type NodeProps } from '@xyflow/react'
import { Badge } from '@grafana/ui'
import { FRAMING_COLOR, PARSER_COLOR } from '../stageColors'
import type { Encoding } from '../../types'
import type { DecodeNode as DecodeNodeType, EncodeNode as EncodeNodeType, FrameNode as FrameNodeType, ParseNode as ParseNodeType } from './types'
import { NodeBadges, NodeCard, NodeDetail } from './NodeCard'
import { codecChain, codecLabelWith } from '../../lib/codec'
import { pythonRefLabel } from '../../lib/python-ref'
import { usePythonDefault } from './python-functions'
import { formatNumber, t } from '../../i18n'

/** Frame: how the byte stream (or each datagram) becomes discrete frames. */
export function FrameNode({ data, selected }: NodeProps<FrameNodeType>) {
  const { framing, mockState } = data
  const defaultLibrary = usePythonDefault()
  const detail = framing.mode === 'delimited' ? t('gofi.canvas.pipeline-nodes.frame-delimiter', 'delimiter {{delimiter}}', { delimiter: JSON.stringify(framing.delimiter ?? '') })
    : framing.mode === 'length-prefix' ? t('gofi.canvas.pipeline-nodes.frame-length-prefix', '{{bytes}}-byte length prefix', { bytes: framing.prefixLen ?? 4 })
    : framing.mode === 'python' ? (framing.python?.function ? t('gofi.canvas.pipeline-nodes.frame-python', 'Python · {{function}}', { function: pythonRefLabel(framing.python, defaultLibrary) }) : t('gofi.canvas.pipeline-nodes.frame-python-choose', 'Python framer (choose a function)'))
    : framing.mode === 'line' ? t('gofi.canvas.pipeline-nodes.frame-line', 'one frame per line')
    : t('gofi.canvas.pipeline-nodes.frame-raw', 'whole connection or datagram is one frame')
  return <NodeCard icon="gf-grid" title={t('gofi.canvas.pipeline-nodes.frame-title', 'Frame')} selected={selected} mockState={mockState}>
    <Handle type="target" position={Position.Left} />
    <NodeBadges><Badge text={framing.mode} color={FRAMING_COLOR[framing.mode] ?? 'darkgrey'} /></NodeBadges>
    <NodeDetail>{detail}</NodeDetail>
    <Handle type="source" position={Position.Right} />
  </NodeCard>
}

/** Decode: ordered byte transformations applied before parsing. */
export function DecodeNode({ data, selected }: NodeProps<DecodeNodeType>) {
  const decoders = data.decoders
  const defaultLibrary = usePythonDefault()
  const codecLabel = (step: (typeof decoders)[number]) => codecLabelWith(step, defaultLibrary)
  return <NodeCard icon="unlock" title={t('gofi.canvas.pipeline-nodes.decode-title', 'Decode')} selected={selected} mockState={data.mockState}>
    <Handle type="target" position={Position.Left} />
    {decoders.length
      ? <NodeBadges>{decoders.map((decoder, index) => <Badge key={`${codecLabel(decoder)}-${index}`} text={codecLabel(decoder)} color={decoder.type === 'python' ? 'blue' : 'purple'} icon={decoder.type === 'python' ? 'brackets-curly' : undefined} />)}</NodeBadges>
      : <NodeDetail>{t('gofi.canvas.pipeline-nodes.decode-none', 'No decoding: frames pass through')}</NodeDetail>}
    {decoders.length > 1 && <NodeDetail>{codecChain(decoders, defaultLibrary)}</NodeDetail>}
    <Handle type="source" position={Position.Right} />
  </NodeCard>
}

/** Parse: how a decoded frame becomes structured fields. */
export function ParseNode({ data, selected }: NodeProps<ParseNodeType>) {
  const { parser, mockState } = data
  const python = parser.format === 'python' && parser.python
  const pythonLabel = python ? pythonRefLabel(python, usePythonDefault()) : ''
  return <NodeCard icon="brackets-curly" title={t('gofi.canvas.pipeline-nodes.parse-title', 'Parse')} selected={selected} mockState={mockState}>
    <Handle type="target" position={Position.Left} />
    <NodeBadges><Badge text={python ? 'python' : parser.format || 'none'} color={python ? 'blue' : PARSER_COLOR[parser.format] ?? 'darkgrey'} /></NodeBadges>
    {python ? <NodeDetail title={pythonLabel}>{pythonLabel}</NodeDetail> : null}
    <NodeDetail>{parser.errorPolicy === 'drop' ? t('gofi.canvas.pipeline-nodes.parse-errors-drop', 'errors drop the frame') : t('gofi.canvas.pipeline-nodes.parse-errors-continue', 'errors continue')}</NodeDetail>
    <Handle type="source" position={Position.Right} />
  </NodeCard>
}

/** Encoding format names as the spec spells them; an unset format is "wrapped". */
export const ENCODING_FORMAT_COLOR: Record<string, 'green' | 'blue' | 'orange'> = { wrapped: 'green', fields: 'blue', raw: 'orange' }
export const encodingFormat = (encoding: Encoding): NonNullable<Encoding['format']> => encoding.format || 'wrapped'

/** "wrapped → gzip → base64": the format, then the encoders in order. */
export const encodingChain = (encoding: Encoding, defaultLibrary?: string): string =>
  [encodingFormat(encoding), ...(encoding.encoders ?? []).map((step) => codecLabelWith(step, defaultLibrary))].join(' → ')

/** Encode: how a message becomes the bytes its sinks send. Sits between the
 * last stage and the sinks it feeds; the dead-letter sink may hang off one too. */
export function EncodeNode({ data, selected }: NodeProps<EncodeNodeType>) {
  const { encoding, mockState } = data
  const defaultLibrary = usePythonDefault()
  const encoders = encoding.encoders ?? []
  const format = encodingFormat(encoding)
  const sinks = data.sinkCount ?? 0
  return <NodeCard icon="lock" title={t('gofi.canvas.pipeline-nodes.encode-title', 'Encode')} selected={selected} mockState={mockState}
    badge={{ text: format, color: ENCODING_FORMAT_COLOR[format] ?? 'darkgrey' }}>
    <Handle type="target" position={Position.Left} />
    {encoders.length
      ? <NodeBadges>{encoders.map((encoder, index) => <Badge key={`${codecLabelWith(encoder, defaultLibrary)}-${index}`} text={codecLabelWith(encoder, defaultLibrary)} color={encoder.type === 'python' ? 'blue' : 'purple'} icon={encoder.type === 'python' ? 'brackets-curly' : undefined} />)}</NodeBadges>
      : <NodeDetail>{t('gofi.canvas.pipeline-nodes.encode-no-encoders', 'No encoders: the formatted payload is sent as is')}</NodeDetail>}
    {encoders.length > 1 && <NodeDetail title={encodingChain(encoding, defaultLibrary)}>{encodingChain(encoding, defaultLibrary)}</NodeDetail>}
    <NodeDetail>{sinks
      ? t('gofi.canvas.pipeline-nodes.encode-feeds', 'feeds {{n}} sinks', { count: sinks, n: formatNumber(sinks), defaultValue_one: 'feeds {{n}} sink' })
      : t('gofi.canvas.pipeline-nodes.encode-unconnected', 'Not connected to a sink: ignored when saving')}</NodeDetail>
    <Handle type="source" position={Position.Right} />
  </NodeCard>
}
