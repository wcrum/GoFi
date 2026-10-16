import { useState, type ReactNode } from 'react'
import { css } from '@emotion/css'
import type { GrafanaTheme2, IconName } from '@grafana/data'
import { Badge, Card, ControlledCollapse, Dropdown, FilterInput, Icon, Menu, Stack, Text, ToolbarButton, useStyles2 } from '@grafana/ui'
import { STAGE_KIND_COLOR } from '../stageColors'
import type { StageSpec } from '../../types'
import { useLibraryFunctions } from './python-functions'
import { INLINE_STARTER } from './python-templates'
import { useExpressions } from '../cel/useExpressions'
import { KIND_ICON } from './StageNode'

export interface StagePaletteEntry {
  kind: StageSpec['kind']
  label: string
  description: string
  defaults: Partial<StageSpec>
  /** Entries from the CEL library are listed in their own section. */
  group?: 'cel-library'
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

// Python written directly in the flow; the controller runs it in the
// flow's managed, isolated worker.
const INLINE_PYTHON_STAGE: StagePaletteEntry = {
  kind: 'python',
  label: 'Inline Python',
  description: 'Write a Python transformer directly in this flow',
  defaults: { kind: 'python', python: { library: '', function: '', inlineSource: INLINE_STARTER } },
}

export type PipelineStep = 'frame' | 'decode' | 'parse'

/** What a palette card carries when dragged onto the canvas. */
export type PaletteDrop =
  | { kind: 'stage'; entry: StagePaletteEntry }
  | { kind: 'step'; step: PipelineStep }
  | { kind: 'sink' }
  | { kind: 'dead-letter' }
export const PALETTE_MIME = 'application/x-gofi-palette'

const STEP_ITEMS: Array<{ step: PipelineStep; icon: IconName; label: string; description: string }> = [
  { step: 'frame', icon: 'gf-grid', label: 'Frame', description: 'Split the byte stream into frames (line, delimiter, length prefix). Without it, each connection or datagram is one frame.' },
  { step: 'decode', icon: 'unlock', label: 'Decode', description: 'Apply byte decoders in order (base64, gzip, hex…). Without it, frames are not decoded.' },
  { step: 'parse', icon: 'brackets-curly', label: 'Parse', description: 'Turn frames into fields (JSON, XML, text, or a Python parser). Without it, frames pass through as fields.raw.' },
]

interface Props {
  onAdd: (entry: StagePaletteEntry) => void
  onAddSink: () => void
  onAddDeadLetter: () => void
  hasDeadLetter: boolean
  /** Optional pipeline steps the flow does not have yet. */
  missingSteps?: PipelineStep[]
  onAddStep?: (step: PipelineStep) => void
}

interface PaletteGroup { title: string; entries: StagePaletteEntry[] }

/** Every addable stage, grouped. Shared by the side palette and the toolbar
 * "Add stage" dropdown so both always offer the same choices. */
function usePaletteGroups(): { groups: PaletteGroup[]; registryError: string | null; pythonCount: number } {
  const { functions, error } = useLibraryFunctions()
  const pythonStages: StagePaletteEntry[] = functions.filter(fn => fn.kind === 'transformer').map(fn => ({ kind: 'python', label: `${fn.library}.${fn.name}`, description: fn.description || 'Python transformer', defaults: { kind: 'python', python: { library: fn.library, function: fn.name } } }))
  // Saved bool CEL expressions become filters linked to the library entry.
  const { expressions } = useExpressions()
  const celStages: StagePaletteEntry[] = expressions.filter(e => e.resultType === 'bool').map(e => ({ kind: 'filter', group: 'cel-library', label: `CEL filter: ${e.name}`, description: e.description || e.expression, defaults: { kind: 'filter', type: 'cel', expression: '', expressionRef: { name: e.name } } }))
  const builtin = (...kinds: StageSpec['kind'][]) => BUILTIN_STAGES.filter(entry => kinds.includes(entry.kind))
  return {
    groups: [
      { title: 'Filter & validate', entries: builtin('filter', 'schema') },
      { title: 'Transform', entries: builtin('transform') },
      { title: 'Python', entries: [INLINE_PYTHON_STAGE, ...pythonStages] },
      { title: 'Traffic controls', entries: builtin('rate-limit', 'dedup') },
      { title: 'CEL library', entries: celStages },
    ],
    registryError: error,
    pythonCount: pythonStages.length,
  }
}

const matchText = (entry: StagePaletteEntry) => `${entry.label} ${entry.description} ${entry.kind} ${entry.defaults.python?.library || ''} ${entry.group ? 'library cel' : ''}`
const kindBadge = (entry: StagePaletteEntry) => <Badge text={entry.kind === 'python' ? 'python' : entry.kind} color={STAGE_KIND_COLOR[entry.kind] ?? 'darkgrey'} />
const SINK_ICON: IconName = 'arrow-right'
const DEAD_LETTER_ICON: IconName = 'exclamation-triangle'

export function StagePalette({ onAdd, onAddSink, onAddDeadLetter, hasDeadLetter, missingSteps = [], onAddStep }: Props) {
  const styles = useStyles2(getStyles)
  const { groups, registryError, pythonCount } = usePaletteGroups()
  const [query, setQuery] = useState('')
  const search = query.trim().toLowerCase()
  const matches = (text: string) => !search || text.toLowerCase().includes(search)
  const visible = groups.map(group => ({ ...group, entries: group.entries.filter(entry => matches(matchText(entry))) })).filter(group => group.entries.length)
  const showSink = matches('Add sink destination forward HTTP NATS TCP UDP Aster Zenoh Loki stdout container application')
  const showDeadLetter = !hasDeadLetter && matches('Dead letter sink rejected dropped messages destination')
  // Every card can be clicked (appends at the end of the chain) or dragged onto
  // the canvas: dropped on a connection it is inserted there.
  const item = (key: string, icon: IconName, heading: string, description: string, onClick: () => void, drop: PaletteDrop, tags?: ReactNode) =>
    <div key={key} draggable className={styles.draggable} title="Click to add, or drag onto a connection"
      onDragStart={(event) => { event.dataTransfer.setData(PALETTE_MIME, JSON.stringify(drop)); event.dataTransfer.effectAllowed = 'copy' }}>
      <Card isCompact noMargin onClick={onClick}>
        <Card.Heading>{heading}</Card.Heading>
        <Card.Figure><Icon name={icon} className={styles.icon} /></Card.Figure>
        <Card.Description>{description}</Card.Description>
        {tags && <Card.Tags>{tags}</Card.Tags>}
      </Card>
    </div>
  const steps = STEP_ITEMS.filter((step) => missingSteps.includes(step.step) && matches(`${step.label} ${step.description} pipeline`))
  return <aside className={styles.palette} aria-label="Add a stage">
    <div className={styles.search}>
      <Text element="h3" variant="h6">Add a stage</Text>
      <FilterInput aria-label="Search stages and Python functions" value={query} onChange={setQuery} placeholder="Search stages and functions" />
    </div>
    <div className={styles.groups}>
      {steps.length > 0 && onAddStep && <ControlledCollapse label={`Pipeline steps (${steps.length})`} isOpen className={styles.collapse}>
        <Stack direction="column" gap={1}>{steps.map((step) => item(`step:${step.step}`, step.icon, step.label, step.description, () => onAddStep(step.step), { kind: 'step', step: step.step }))}</Stack>
      </ControlledCollapse>}
      {visible.map(group => <ControlledCollapse key={group.title} label={`${group.title} (${group.entries.length})`} isOpen className={styles.collapse}>
        <Stack direction="column" gap={1}>{group.entries.map(entry => item(`${group.title}:${entry.label}`, KIND_ICON[entry.kind], entry.label, entry.description, () => onAdd(entry), { kind: 'stage', entry }, kindBadge(entry)))}</Stack>
      </ControlledCollapse>)}
      {(showSink || showDeadLetter) && <ControlledCollapse label="Destinations" isOpen className={styles.collapse}>
        <Stack direction="column" gap={1}>
          {showSink && item('sink', SINK_ICON, 'Add sink', 'Forward passing traffic to an application.', onAddSink, { kind: 'sink' })}
          {showDeadLetter && item('dead-letter', DEAD_LETTER_ICON, 'Dead letter sink', 'Route rejected messages for inspection.', onAddDeadLetter, { kind: 'dead-letter' })}
        </Stack>
      </ControlledCollapse>}
      {!visible.length && !showSink && !showDeadLetter && <Text variant="bodySmall" color="secondary">No stages or functions match “{query}”.</Text>}
      {registryError ? <Text variant="bodySmall" color="warning">Python function registry unavailable.</Text>
        : !pythonCount && <Text variant="bodySmall" color="secondary">Launch a Python library to add its transformer functions here.</Text>}
    </div>
  </aside>
}

/** "Add stage" dropdown (Grafana Dropdown + Menu.Group pattern) with the side
 * palette's entries, shown only on narrow screens where the palette is hidden. */
export function AddStageMenu({ onAdd, onAddSink, onAddDeadLetter, hasDeadLetter }: Props) {
  const { groups } = usePaletteGroups()
  const [open, setOpen] = useState(false)
  const menu = <Menu>
    {groups.filter(group => group.entries.length).map(group => <Menu.Group key={group.title} label={group.title}>
      {group.entries.map(entry => <Menu.Item key={entry.label} label={entry.label} description={entry.description} icon={KIND_ICON[entry.kind]} onClick={() => onAdd(entry)} />)}
    </Menu.Group>)}
    <Menu.Divider />
    <Menu.Group label="Destinations">
      <Menu.Item label="Add sink" description="Forward passing traffic to an application" icon={SINK_ICON} onClick={onAddSink} />
      <Menu.Item label="Dead letter sink" description={hasDeadLetter ? 'This flow already has one' : 'Route rejected messages for inspection'} icon={DEAD_LETTER_ICON} disabled={hasDeadLetter} onClick={onAddDeadLetter} />
    </Menu.Group>
  </Menu>
  return <Dropdown overlay={menu} placement="bottom-start" onVisibleChange={setOpen}>
    <ToolbarButton icon="plus" isOpen={open} variant="primary">Add stage</ToolbarButton>
  </Dropdown>
}

const getStyles = (theme: GrafanaTheme2) => ({
  draggable: css({ cursor: 'grab', '&:active': { cursor: 'grabbing' } }),
  palette: css({ width: '100%', flexShrink: 0, display: 'flex', flexDirection: 'column', minHeight: 0, background: theme.colors.background.primary, borderRight: `1px solid ${theme.colors.border.weak}`, [theme.breakpoints.down('md')]: { display: 'none' } }),
  search: css({ display: 'flex', flexDirection: 'column', gap: theme.spacing(1), padding: theme.spacing(1.5), borderBottom: `1px solid ${theme.colors.border.weak}` }),
  groups: css({ flex: 1, minHeight: 0, overflowY: 'auto', padding: theme.spacing(1) }),
  collapse: css({ marginBottom: theme.spacing(1) }),
  icon: css({ color: theme.colors.primary.text }),
})
