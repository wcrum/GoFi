import { useState } from 'react'
import { Badge, Input } from '../primitives'
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
import { useLibraryFunctions } from './python-functions'

export interface StagePaletteEntry {
  kind: StageSpec['kind']
  label: string
  description: string
  defaults: Partial<StageSpec>
}

// Built-ins stay available alongside functions discovered from Python libraries.
export const BUILTIN_STAGES: StagePaletteEntry[] = [
  {
    kind: 'filter',
    label: 'CEL Filter',
    description: 'Drop messages failing a CEL boolean expression',
    defaults: { kind: 'filter', type: 'cel', expression: '' },
  },
  { kind: 'filter', label: 'Regex Filter', description: 'Match raw traffic or a parsed field with RE2', defaults: { kind: 'filter', type: 'regex', expression: '' } },
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
  python: RiEditLine,
}

interface Props {
  onAdd: (entry: StagePaletteEntry) => void
  onAddSink: () => void
  onAddDeadLetter: () => void
  hasDeadLetter: boolean
}

export function StagePalette({ onAdd, onAddSink, onAddDeadLetter, hasDeadLetter }: Props) {
  const { functions, error } = useLibraryFunctions()
  const [query, setQuery] = useState('')
  const search = query.trim().toLowerCase()
  const matches = (text: string) => !search || text.toLowerCase().includes(search)
  const pythonStages: StagePaletteEntry[] = functions.filter(fn => fn.kind === 'transformer').map(fn => ({ kind: 'python', label: `${fn.library}.${fn.name}`, description: fn.description || 'Python transformer', defaults: { kind: 'python', python: { library: fn.library, function: fn.name } } }))
  const entries = [...BUILTIN_STAGES, ...pythonStages].filter(entry => matches(`${entry.label} ${entry.description} ${entry.kind} ${entry.defaults.python?.library || ''}`))
  const groups = [
    { title: 'Filter & validate', kinds: ['filter', 'schema'] },
    { title: 'Transform', kinds: ['transform', 'python'] },
    { title: 'Traffic controls', kinds: ['rate-limit', 'dedup'] },
  ]
  const showSink = matches('Add sink destination forward HTTP NATS TCP UDP Aster Zenoh Loki stdout container application')
  const showDeadLetter = !hasDeadLetter && matches('Dead letter sink rejected dropped messages destination')
  const card = (entry: StagePaletteEntry) => {
    const Icon = KIND_ICON[entry.kind]
    return <button key={entry.label} onClick={() => onAdd(entry)} className="w-full text-left rounded border border-g-border-weak bg-g-primary p-3 hover:bg-g-hover hover:border-g-accent-main/50 transition-colors" title={entry.description}>
      <div className="flex items-center gap-2"><Icon className="text-g-accent-main shrink-0" /><span className="min-w-0 flex-1 text-xs text-g-text font-medium break-words">{entry.label}</span><Badge text={entry.kind === 'python' ? 'Python' : entry.kind} color={STAGE_KIND_COLOR[entry.kind] ?? 'gray'} className="shrink-0" /></div>
      <p className="mt-1.5 text-[11px] text-g-text-secondary leading-relaxed">{entry.description}</p>
    </button>
  }
  return <aside className="w-64 shrink-0 border-r border-g-border-weak flex flex-col min-h-0 bg-g-primary">
    <div className="p-3 border-b border-g-border-weak"><h3 className="text-xs font-medium mb-2">Add a stage</h3><Input aria-label="Search stages and Python functions" value={query} onChange={event => setQuery(event.target.value)} placeholder="Search stages and functions…" className="!text-xs !px-2 !py-1.5" /></div>
    <div className="p-3 space-y-5 overflow-y-auto">
      {groups.map(group => {
        const items = entries.filter(entry => group.kinds.includes(entry.kind))
        return items.length ? <section key={group.title}><h4 className="text-[10px] font-semibold uppercase tracking-wider text-g-text-secondary mb-2">{group.title}</h4><div className="space-y-2">{items.map(card)}</div></section> : null
      })}
      {(showSink || showDeadLetter) && <section><h4 className="text-[10px] font-semibold uppercase tracking-wider text-g-text-secondary mb-2">Destinations</h4><div className="space-y-2">{showSink && <button onClick={onAddSink} className="w-full text-left rounded border border-g-border-weak p-3 hover:bg-g-hover hover:border-g-accent-main/50"><span className="flex items-center gap-2 text-xs font-medium"><RiSendPlaneLine className="text-g-accent-main" />Add sink</span><p className="mt-1.5 text-[11px] text-g-text-secondary">Forward passing traffic to an application.</p></button>}{showDeadLetter && <button onClick={onAddDeadLetter} className="w-full text-left rounded border border-g-border-weak p-3 hover:bg-g-hover hover:border-orange-400/50"><span className="flex items-center gap-2 text-xs font-medium"><RiErrorWarningLine className="text-orange-400" />Dead letter sink</span><p className="mt-1.5 text-[11px] text-g-text-secondary">Route rejected messages for inspection.</p></button>}</div></section>}
      {!entries.length && !showSink && !showDeadLetter && <p role="status" className="text-xs text-g-text-secondary">No stages or functions match “{query}”.</p>}
      {error ? <p className="text-[11px] text-amber-300">Python function registry unavailable.</p> : !pythonStages.length && <p className="text-[11px] text-g-text-secondary">Launch a Python library to add its transformer functions here.</p>}
    </div>
  </aside>
}
