import { Card, Badge } from '@cnak-us/design-system'
import {
  RiFilterLine,
  RiEditLine,
  RiSpeedLine,
  RiFingerprintLine,
  RiShieldCheckLine,
  RiSendPlaneLine,
  RiErrorWarningLine,
} from 'react-icons/ri'
import { STAGE_KIND_COLOR } from '../stageColors'
import type { StageSpec } from '../../types'

export interface StagePaletteEntry {
  kind: StageSpec['kind']
  label: string
  description: string
  defaults: Partial<StageSpec>
}

// Structured as a plain array so it's easy to swap for a
// `GET /api/stage-types` call once the backend registry grows further.
export const BUILTIN_STAGES: StagePaletteEntry[] = [
  {
    kind: 'filter',
    label: 'CEL Filter',
    description: 'Drop messages failing a CEL boolean expression',
    defaults: { kind: 'filter', type: 'cel', expression: '' },
  },
  {
    kind: 'transform',
    label: 'Transform',
    description: 'Set, rename, or remove a field',
    defaults: { kind: 'transform', transformOp: 'set', field: '', valueExpr: '' },
  },
  {
    kind: 'rate-limit',
    label: 'Rate Limit',
    description: 'Drop messages exceeding a per-second rate',
    defaults: { kind: 'rate-limit', maxPerSecond: 10 },
  },
  {
    kind: 'dedup',
    label: 'Deduplicate',
    description: 'Drop repeats of a key seen within a time window',
    defaults: { kind: 'dedup', keyExpr: '', window: '30s' },
  },
  {
    kind: 'schema',
    label: 'Schema Validator',
    description: 'Drop messages missing required fields',
    defaults: { kind: 'schema', requiredFields: [] },
  },
]

const KIND_ICON: Record<StageSpec['kind'], typeof RiFilterLine> = {
  filter: RiFilterLine,
  transform: RiEditLine,
  'rate-limit': RiSpeedLine,
  dedup: RiFingerprintLine,
  schema: RiShieldCheckLine,
}

interface Props {
  onAdd: (entry: StagePaletteEntry) => void
  onAddSink: () => void
  onAddDeadLetter: () => void
  hasDeadLetter: boolean
}

export function StagePalette({ onAdd, onAddSink, onAddDeadLetter, hasDeadLetter }: Props) {
  return (
    <aside className="w-56 shrink-0 border-r border-g-border-weak p-3 space-y-2 overflow-y-auto">
      <div className="text-xs font-medium text-g-text-secondary uppercase tracking-wide mb-1">
        Stages
      </div>
      {BUILTIN_STAGES.map((entry) => {
        const Icon = KIND_ICON[entry.kind]
        return (
          <Card
            key={entry.kind}
            padding="sm"
            onClick={() => onAdd(entry)}
            className="cursor-pointer hover:bg-g-hover hover:border-g-accent-main/50 transition-colors"
            title={entry.description}
          >
            <div className="flex items-center gap-2">
              <Icon className="text-g-accent-main shrink-0" />
              <div className="min-w-0 flex-1">
                <div className="text-sm text-g-text font-medium truncate">{entry.label}</div>
                <div className="text-xs text-g-text-secondary truncate">{entry.description}</div>
              </div>
              <Badge text={entry.kind} color={STAGE_KIND_COLOR[entry.kind] ?? 'gray'} className="shrink-0" />
            </div>
          </Card>
        )
      })}

      <div className="text-xs font-medium text-g-text-secondary uppercase tracking-wide mb-1 pt-3">
        Sinks
      </div>
      <Card
        padding="sm"
        onClick={onAddSink}
        className="cursor-pointer hover:bg-g-hover hover:border-g-accent-main/50 transition-colors"
        title="Add another sink target — fans out passing messages to it in addition to existing sinks"
      >
        <div className="flex items-center gap-2">
          <RiSendPlaneLine className="text-g-accent-main shrink-0" />
          <div className="min-w-0">
            <div className="text-sm text-g-text font-medium truncate">+ Add Sink</div>
            <div className="text-xs text-g-text-secondary truncate">Fan out to another destination</div>
          </div>
        </div>
      </Card>

      {!hasDeadLetter && (
        <Card
          padding="sm"
          onClick={onAddDeadLetter}
          className="cursor-pointer hover:bg-g-hover hover:border-orange-400/50 transition-colors"
          title="Add a dead-letter sink for dropped messages"
        >
          <div className="flex items-center gap-2">
            <RiErrorWarningLine className="text-orange-400 shrink-0" />
            <div className="min-w-0">
              <div className="text-sm text-g-text font-medium truncate">+ Dead Letter Sink</div>
              <div className="text-xs text-g-text-secondary truncate">Where dropped messages go</div>
            </div>
          </div>
        </Card>
      )}
    </aside>
  )
}
