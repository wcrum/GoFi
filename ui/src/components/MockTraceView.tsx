import { Alert, Badge } from '@cnak-us/design-system'
import type { BadgeColor } from '@cnak-us/design-system'
import type { SimulateResult } from '../types'

// Presentational renderer for a SimulateResult dry-run trace. No data
// fetching — both MockPanel (editor, stepping through with Next/Prev) and
// ListenerStream (live replay, whole trace at once) feed it a result.
//
// When `activeIndex` is provided, the stage row at that index is highlighted
// (for the editor's step-through: index -1 = parse step, 0..n-1 = stages).
// When omitted, the full trace renders flat with no per-row emphasis.

interface Props {
  result: SimulateResult
  activeIndex?: number
}

function passBadge(pass: boolean): { text: string; color: BadgeColor } {
  return pass ? { text: 'pass', color: 'green' } : { text: 'drop', color: 'red' }
}

export function MockTraceView({ result, activeIndex }: Props) {
  const stepping = activeIndex !== undefined

  return (
    <div className="space-y-3 text-xs">
      {result.parseError && (
        <Alert severity="warning" title="Parse error">
          {result.parseError}
        </Alert>
      )}

      {/* Parse step: fields snapshot right after parsing, before any stage. */}
      <div
        className={`rounded border p-2 ${
          stepping && activeIndex === -1
            ? 'border-amber-400 bg-amber-400/10'
            : 'border-g-border-weak bg-g-primary'
        }`}
      >
        <div className="flex items-center gap-2 mb-1">
          <span className="font-semibold text-g-text">Parsed</span>
          <Badge text="ingress" color="gray" />
        </div>
        <FieldsDump fields={result.parsedFields} />
      </div>

      {result.stages.map((stage, i) => {
        const badge = passBadge(stage.pass)
        const active = stepping && activeIndex === i
        return (
          <div
            key={stage.id}
            className={`rounded border p-2 ${
              active ? 'border-amber-400 bg-amber-400/10' : 'border-g-border-weak bg-g-primary'
            }`}
          >
            <div className="flex items-center gap-2 mb-1">
              <span className="font-semibold text-g-text">{stage.kind}</span>
              {stage.type && <Badge text={stage.type} color="blue" />}
              <Badge text={badge.text} color={badge.color} className="ml-auto" />
            </div>
            {stage.error && (
              <div className="text-red-400 font-mono mb-1 break-all">{stage.error}</div>
            )}
            <FieldsDump fields={stage.fields} />
          </div>
        )
      })}

      {/* Final outcome. */}
      {result.dropped ? (
        <div className="rounded border border-red-900/40 bg-red-950/30 p-2">
          <div className="flex items-center gap-2">
            <Badge text="dropped" color="red" />
            {result.droppedBy && <span className="text-g-text-secondary">by {result.droppedBy}</span>}
            {result.deadLetter && <Badge text="→ dead-letter sink" color="orange" className="ml-auto" />}
          </div>
        </div>
      ) : (
        <div className="rounded border border-g-border-weak bg-g-primary p-2">
          <div className="text-g-text-secondary mb-2">Dry run — nothing was actually sent.</div>
          <div className="space-y-1">
            {(result.sinks ?? []).map((s) => (
              <div key={s.index} className="flex items-center gap-2">
                <span className="text-g-text">{s.type}</span>
                <Badge text={s.sent ? 'would send' : 'skipped'} color={s.sent ? 'green' : 'gray'} />
                {!s.sent && s.reason && (
                  <span className="text-g-text-secondary font-mono truncate" title={s.reason}>
                    {s.reason}
                  </span>
                )}
              </div>
            ))}
            {(result.sinks ?? []).length === 0 && (
              <div className="text-g-text-secondary">No sinks matched.</div>
            )}
          </div>
        </div>
      )}
    </div>
  )
}

// Matches ListenerStream's EventRow JSON-dump styling for consistency.
function FieldsDump({ fields }: { fields: Record<string, unknown> }) {
  return (
    <pre className="text-g-text-secondary whitespace-pre-wrap break-all font-mono">
      {JSON.stringify(fields, null, 2)}
    </pre>
  )
}
