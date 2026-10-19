import type { StageSpec } from '../types'
import { codecLabelWith } from './codec.ts'
import { pythonRefLabel } from './python-ref.ts'
import { t } from '../i18n/index.ts'

/** One-line summary of a parse stage, in pipeline order: input, decoders,
 * format, target. e.g. `fields["message"] → base64 → json → payload`. */
export function parseStageSummary(spec: Pick<StageSpec, 'parse'>, defaultLibrary?: string): string {
  const parse = spec.parse
  if (!parse) return t('gofi.lib.parse-stage.choose', 'Choose what to parse')
  const from = parse.from?.trim() && parse.from.trim() !== 'raw' ? parse.from.trim() : 'raw'
  const format = parse.format === 'python' ? (parse.python?.function ? pythonRefLabel(parse.python, defaultLibrary) : 'python') : parse.format || 'json'
  const steps = [from, ...(parse.decoders ?? []).map((step) => codecLabelWith(step, defaultLibrary)), format]
  if (parse.into?.trim()) steps.push(parse.into.trim())
  return steps.join(' → ')
}
