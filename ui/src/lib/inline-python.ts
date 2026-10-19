import { t } from '../i18n/index.ts'

/** One block of inline Python authored in a flow, and where it is used. */
export interface InlineBlock { label: string; source: string }

/** Flow keys whose children hold inline Python. */
const STEP_KEYS = ['framing', 'parser', 'decoders', 'stages', 'sinks', 'encoders', 'deadLetterSink']
/** Keys whose items are numbered (or, for stages, named by id). */
const LIST_KEYS = ['stages', 'sinks', 'decoders', 'encoders']

/** Every `inlineSource` in a flow definition, in document order, labelled by
 * its step (e.g. "Stage inl", "Destination 1 › Encoder 1"). */
export function inlineBlocks(flow: unknown): InlineBlock[] {
  const blocks: InlineBlock[] = []
  const walk = (value: unknown, trail: string[]) => {
    if (Array.isArray(value)) { value.forEach((item, index) => walk(item, [...trail, String(index)])); return }
    if (!value || typeof value !== 'object') return
    const record = value as Record<string, unknown>
    if (typeof record.inlineSource === 'string' && record.inlineSource.trim()) blocks.push({ label: describe(trail), source: record.inlineSource })
    for (const [key, child] of Object.entries(record)) {
      // A stage is named by its id rather than its index.
      const name = key === 'stages' && Array.isArray(child) ? null : key
      if (name === null) (child as unknown[]).forEach((stage, index) => walk(stage, [...trail, 'stages', String((stage as { id?: string })?.id ?? index + 1)]))
      else walk(child, [...trail, key])
    }
  }
  walk(flow, [])
  return blocks
}

/** The label of one step, numbered (1-based) or named when it is a list item. */
function stepLabel(step: string, item?: string): string {
  const index = item !== undefined && /^\d+$/.test(item) && step !== 'stages' ? Number(item) + 1 : item
  switch (step) {
    case 'framing': return t('gofi.lib.inline-python.step-frame', 'Frame')
    case 'parser': return t('gofi.lib.inline-python.step-parse', 'Parse')
    case 'deadLetterSink': return t('gofi.lib.inline-python.step-dead-letter', 'Dead letter')
    case 'decoders': return index === undefined ? t('gofi.lib.inline-python.step-decoder', 'Decoder') : t('gofi.lib.inline-python.step-decoder-n', 'Decoder {{index}}', { index })
    case 'encoders': return index === undefined ? t('gofi.lib.inline-python.step-encoder', 'Encoder') : t('gofi.lib.inline-python.step-encoder-n', 'Encoder {{index}}', { index })
    case 'sinks': return index === undefined ? t('gofi.lib.inline-python.step-destination', 'Destination') : t('gofi.lib.inline-python.step-destination-n', 'Destination {{index}}', { index })
    case 'stages': return index === undefined ? t('gofi.lib.inline-python.step-stage', 'Stage') : t('gofi.lib.inline-python.step-stage-id', 'Stage {{id}}', { id: index })
    default: return step
  }
}

function describe(trail: string[]): string {
  const parts: string[] = []
  for (let i = 0; i < trail.length; i++) {
    if (!STEP_KEYS.includes(trail[i])) continue
    const next = trail[i + 1]
    if (LIST_KEYS.includes(trail[i]) && next !== undefined) { parts.push(stepLabel(trail[i], next)); i++ }
    else parts.push(stepLabel(trail[i]))
  }
  return parts.join(' › ') || t('gofi.lib.inline-python.inline-python', 'Inline Python')
}
