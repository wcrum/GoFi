// Readable view of a flow-managed library. GoFi generates the library from a
// flow's inline stages as one `_gofi._inline("<stage id>", "<source>")` call
// per stage, with both arguments JSON string literals (sandbox/inline.go). The
// view shows each stage's actual code under a comment header instead.
import { t } from '../../i18n/index.ts'

export interface InlineStage { id: string; source: string }
export interface ManagedView {
  text: string
  stages: InlineStage[]
  /** 1-based line in `text` where each stage's code starts. */
  offsets: Record<string, number>
}

const CALL = /^_gofi\._inline\(("(?:[^"\\]|\\.)*"),\s*("(?:[^"\\]|\\.)*")\)\s*$/

/** Inline stages of a generated library, or null when it isn't in that form. */
export function parseManagedSource(source: string): InlineStage[] | null {
  const stages: InlineStage[] = []
  for (const raw of source.split('\n')) {
    const line = raw.trim()
    if (!line || line.startsWith('#') || line === 'import gofi as _gofi') continue
    const match = CALL.exec(line)
    if (!match) return null
    try {
      const id = JSON.parse(match[1]) as unknown
      const code = JSON.parse(match[2]) as unknown
      if (typeof id !== 'string' || typeof code !== 'string') return null
      stages.push({ id, source: code })
    } catch {
      return null
    }
  }
  return stages.length ? stages : null
}

/** Read-only text: a header per stage, then that stage's code verbatim. */
export function managedView(flow: string, stages: InlineStage[]): ManagedView {
  const lines = [
    t('gofi.python.managed-source.header-title', '# Inline Python stages of flow "{{flow}}" (read-only).', { flow }),
    t('gofi.python.managed-source.header-note', '# GoFi generates and runs this library from the flow; edit the stages in the flow.'),
  ]
  const offsets: Record<string, number> = {}
  for (const stage of stages) {
    lines.push('', t('gofi.python.managed-source.stage-header', '# ── Stage "{{id}}" · inline transformer {{rule}}', { id: stage.id, rule: '─'.repeat(Math.max(4, 40 - stage.id.length)) }))
    offsets[stage.id] = lines.length + 1
    lines.push(...stage.source.replace(/\n+$/, '').split('\n'))
  }
  return { text: lines.join('\n') + '\n', stages, offsets }
}

/** Maps a stage-relative line (as workers report for inline stages) into the
 * view; returns the line unchanged when the stage is unknown. */
export function viewLine(view: ManagedView | null, stage: string | undefined, line: number | undefined): number | undefined {
  if (!line || !view || !stage || view.offsets[stage] === undefined) return line
  return view.offsets[stage] + line - 1
}

/** 1-based line of `def transform` within each stage, in view coordinates. */
export function stageEntryLines(view: ManagedView): Record<string, number> {
  const entries: Record<string, number> = {}
  for (const stage of view.stages) {
    const index = stage.source.split('\n').findIndex(line => /^\s*(async\s+)?def\s+transform\s*\(/.test(line))
    entries[stage.id] = view.offsets[stage.id] + Math.max(0, index)
  }
  return entries
}
