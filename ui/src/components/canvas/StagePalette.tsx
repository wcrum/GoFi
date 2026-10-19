import { useState, type ReactNode } from 'react'
import { css } from '@emotion/css'
import type { GrafanaTheme2, IconName } from '@grafana/data'
import { Badge, Card, ControlledCollapse, Dropdown, FilterInput, Icon, Menu, Stack, Text, ToolbarButton, useStyles2 } from '@grafana/ui'
import { STAGE_KIND_COLOR } from '../stageColors'
import type { StageSpec } from '../../types'
import { functionLabel, useLibraryFunctions, usePythonDefault } from './python-functions'
import { storedRef } from '../../lib/python-ref'
import { INLINE_STARTER } from './python-templates'
import { useExpressions } from '../cel/useExpressions'
import { KIND_ICON } from './StageNode'
import { formatNumber, t } from '../../i18n'

export interface StagePaletteEntry {
  kind: StageSpec['kind']
  label: string
  description: string
  defaults: Partial<StageSpec>
  /** Entries from the CEL library are listed in their own section. */
  group?: 'cel-library'
}

// Built-ins stay available alongside functions discovered from function libraries.
// Built at render time so titles and descriptions follow the active language.
export const builtinStages = (): StagePaletteEntry[] => [
  {
    kind: 'filter',
    label: t('gofi.canvas.stage-palette.cel-filter', 'CEL Filter'),
    description: t('gofi.canvas.stage-palette.cel-filter-description', 'Drop messages failing a CEL boolean expression'),
    defaults: { kind: 'filter', type: 'cel', expression: '' },
  },
  { kind: 'filter', label: t('gofi.canvas.stage-palette.regex-filter', 'Regex Filter'), description: t('gofi.canvas.stage-palette.regex-filter-description', 'Match raw traffic or a parsed field with RE2'), defaults: { kind: 'filter', type: 'regex', expression: '' } },
  {
    kind: 'transform',
    label: t('gofi.canvas.stage-palette.transform', 'Transform'),
    description: t('gofi.canvas.stage-palette.transform-description', 'Set, rename, or remove a field'),
    defaults: { kind: 'transform', transformOp: 'set', field: '', valueExpr: '' },
  },
  {
    kind: 'parse',
    label: t('gofi.canvas.stage-palette.parse-field', 'Parse field'),
    description: t('gofi.canvas.stage-palette.parse-field-description', 'Parse a field again (JSON, XML, text, or Python)'),
    defaults: { kind: 'parse', parse: { from: '', format: 'json' } },
  },
  {
    kind: 'rate-limit',
    label: t('gofi.canvas.stage-palette.rate-limit', 'Rate Limit'),
    description: t('gofi.canvas.stage-palette.rate-limit-description', 'Drop messages exceeding a per-second rate'),
    defaults: { kind: 'rate-limit', maxPerSecond: 10 },
  },
  {
    kind: 'dedup',
    label: t('gofi.canvas.stage-palette.dedup', 'Deduplicate'),
    description: t('gofi.canvas.stage-palette.dedup-description', 'Drop repeats of a key seen within a time window'),
    defaults: { kind: 'dedup', keyExpr: '', window: '30s' },
  },
  {
    kind: 'schema',
    label: t('gofi.canvas.stage-palette.schema', 'Schema Validator'),
    description: t('gofi.canvas.stage-palette.schema-description', 'Drop messages missing required fields'),
    defaults: { kind: 'schema', requiredFields: [] },
  },
]

// Python written directly in the flow; the controller runs it in the
// flow's managed, isolated worker.
const inlinePythonStage = (): StagePaletteEntry => ({
  kind: 'python',
  label: t('gofi.canvas.stage-palette.inline-python', 'Inline Python'),
  description: t('gofi.canvas.stage-palette.inline-python-description', 'Write a Python transformer directly in this flow'),
  defaults: { kind: 'python', python: { library: '', function: '', inlineSource: INLINE_STARTER } },
})

export type PipelineStep = 'frame' | 'decode' | 'parse'

/** What a palette card carries when dragged onto the canvas. */
export type PaletteDrop =
  | { kind: 'stage'; entry: StagePaletteEntry }
  | { kind: 'step'; step: PipelineStep }
  | { kind: 'sink' }
  | { kind: 'encode' }
  | { kind: 'dead-letter' }
export const PALETTE_MIME = 'application/x-gofi-palette'

const stepItems = (): Array<{ step: PipelineStep; icon: IconName; label: string; description: string }> => [
  { step: 'frame', icon: 'gf-grid', label: t('gofi.canvas.stage-palette.step-frame', 'Frame'), description: t('gofi.canvas.stage-palette.step-frame-description', 'Split the byte stream into frames (line, delimiter, length prefix). Without it, each connection or datagram is one frame.') },
  { step: 'decode', icon: 'unlock', label: t('gofi.canvas.stage-palette.step-decode', 'Decode'), description: t('gofi.canvas.stage-palette.step-decode-description', 'Apply byte decoders in order (base64, gzip, hex…). Without it, frames are not decoded.') },
  { step: 'parse', icon: 'brackets-curly', label: t('gofi.canvas.stage-palette.step-parse', 'Parse'), description: t('gofi.canvas.stage-palette.step-parse-description', 'Turn frames into fields (JSON, XML, text, or a Python parser). Without it, frames pass through as fields.raw.') },
]

interface Props {
  onAdd: (entry: StagePaletteEntry) => void
  onAddSink: () => void
  /** Adds an Encode step before the sinks, ready to be wired to one. */
  onAddEncode: () => void
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
  const defaultLibrary = usePythonDefault()
  const pythonStages: StagePaletteEntry[] = functions.filter(fn => fn.kind === 'transformer').map(fn => ({ kind: 'python', label: functionLabel(fn.library, fn.name, defaultLibrary), description: fn.description || t('gofi.canvas.stage-palette.python-transformer', 'Python transformer'), defaults: { kind: 'python', python: storedRef({ library: fn.library, function: fn.name }, defaultLibrary) } }))
  // Saved bool CEL expressions become filters linked to the library entry.
  const { expressions } = useExpressions()
  const celStages: StagePaletteEntry[] = expressions.filter(e => e.resultType === 'bool').map(e => ({ kind: 'filter', group: 'cel-library', label: t('gofi.canvas.stage-palette.cel-library-filter', 'CEL filter: {{name}}', { name: e.name }), description: e.description || e.expression, defaults: { kind: 'filter', type: 'cel', expression: '', expressionRef: { name: e.name } } }))
  const stages = builtinStages()
  const builtin = (...kinds: StageSpec['kind'][]) => stages.filter(entry => kinds.includes(entry.kind))
  return {
    groups: [
      { title: t('gofi.canvas.stage-palette.group-filter', 'Filter & validate'), entries: builtin('filter', 'schema') },
      { title: t('gofi.canvas.stage-palette.group-transform', 'Transform'), entries: builtin('transform', 'parse') },
      { title: 'Python', entries: [inlinePythonStage(), ...pythonStages] },
      { title: t('gofi.canvas.stage-palette.group-traffic', 'Traffic controls'), entries: builtin('rate-limit', 'dedup') },
      { title: t('gofi.canvas.stage-palette.group-cel-library', 'CEL library'), entries: celStages },
    ],
    registryError: error,
    pythonCount: pythonStages.length,
  }
}

const matchText = (entry: StagePaletteEntry) => `${entry.label} ${entry.description} ${entry.kind} ${entry.defaults.python?.library || entry.label} ${entry.group ? 'library cel' : ''}`
const kindBadge = (entry: StagePaletteEntry) => <Badge text={entry.kind === 'python' ? 'python' : entry.kind} color={STAGE_KIND_COLOR[entry.kind] ?? 'darkgrey'} />
const SINK_ICON: IconName = 'arrow-right'
const ENCODE_ICON: IconName = 'lock'
const DEAD_LETTER_ICON: IconName = 'exclamation-triangle'

export function StagePalette({ onAdd, onAddSink, onAddEncode, onAddDeadLetter, hasDeadLetter, missingSteps = [], onAddStep }: Props) {
  const styles = useStyles2(getStyles)
  const { groups, registryError, pythonCount } = usePaletteGroups()
  const [query, setQuery] = useState('')
  const search = query.trim().toLowerCase()
  const matches = (text: string) => !search || text.toLowerCase().includes(search)
  const visible = groups.map(group => ({ ...group, entries: group.entries.filter(entry => matches(matchText(entry))) })).filter(group => group.entries.length)
  const showSink = matches(t('gofi.canvas.stage-palette.sink-search-terms', 'Add sink destination forward HTTP NATS TCP UDP Aster Zenoh Loki stdout container application'))
  const showEncode = matches(t('gofi.canvas.stage-palette.encode-search-terms', 'Encode encoding format wrapped fields raw encoder gzip base64 compress payload before sink'))
  const showDeadLetter = !hasDeadLetter && matches(t('gofi.canvas.stage-palette.dead-letter-search-terms', 'Dead letter sink rejected dropped messages destination'))
  // Every card can be clicked (appends at the end of the chain) or dragged onto
  // the canvas: dropped on a connection it is inserted there.
  const item = (key: string, icon: IconName, heading: string, description: string, onClick: () => void, drop: PaletteDrop, tags?: ReactNode) =>
    <div key={key} draggable className={styles.draggable} title={t('gofi.canvas.stage-palette.card-title', 'Click to add, or drag onto a connection')}
      onDragStart={(event) => { event.dataTransfer.setData(PALETTE_MIME, JSON.stringify(drop)); event.dataTransfer.effectAllowed = 'copy' }}>
      <Card isCompact noMargin onClick={onClick}>
        <Card.Heading>{heading}</Card.Heading>
        <Card.Figure><Icon name={icon} className={styles.icon} /></Card.Figure>
        <Card.Description>{description}</Card.Description>
        {tags && <Card.Tags>{tags}</Card.Tags>}
      </Card>
    </div>
  const steps = stepItems().filter((step) => missingSteps.includes(step.step) && matches(`${step.label} ${step.description} ${t('gofi.canvas.stage-palette.step-search-term', 'pipeline')}`))
  return <aside className={styles.palette} aria-label={t('gofi.canvas.stage-palette.title', 'Add a stage')}>
    <div className={styles.search}>
      <Text element="h3" variant="h6">{t('gofi.canvas.stage-palette.title', 'Add a stage')}</Text>
      <FilterInput aria-label={t('gofi.canvas.stage-palette.search-aria', 'Search stages and Python functions')} value={query} onChange={setQuery} placeholder={t('gofi.canvas.stage-palette.search-placeholder', 'Search stages and functions')} />
    </div>
    <div className={styles.groups}>
      {steps.length > 0 && onAddStep && <ControlledCollapse label={t('gofi.canvas.stage-palette.pipeline-steps', 'Pipeline steps ({{n}})', { n: formatNumber(steps.length) })} isOpen className={styles.collapse}>
        <Stack direction="column" gap={1}>{steps.map((step) => item(`step:${step.step}`, step.icon, step.label, step.description, () => onAddStep(step.step), { kind: 'step', step: step.step }))}</Stack>
      </ControlledCollapse>}
      {visible.map(group => <ControlledCollapse key={group.title} label={t('gofi.canvas.stage-palette.group-with-count', '{{title}} ({{n}})', { title: group.title, n: formatNumber(group.entries.length) })} isOpen className={styles.collapse}>
        <Stack direction="column" gap={1}>{group.entries.map(entry => item(`${group.title}:${entry.label}`, KIND_ICON[entry.kind], entry.label, entry.description, () => onAdd(entry), { kind: 'stage', entry }, kindBadge(entry)))}</Stack>
      </ControlledCollapse>)}
      {(showSink || showEncode || showDeadLetter) && <ControlledCollapse label={t('gofi.canvas.stage-palette.destinations', 'Destinations')} isOpen className={styles.collapse}>
        <Stack direction="column" gap={1}>
          {showSink && item('sink', SINK_ICON, t('gofi.canvas.stage-palette.add-sink', 'Add sink'), t('gofi.canvas.stage-palette.add-sink-card-description', 'Forward passing traffic to an application.'), onAddSink, { kind: 'sink' })}
          {showEncode && item('encode', ENCODE_ICON, t('gofi.canvas.stage-palette.encode', 'Encode'), t('gofi.canvas.stage-palette.encode-card-description', 'Format and encode messages before a sink sends them. Drop it on a sink\'s connection, or wire sinks to it.'), onAddEncode, { kind: 'encode' })}
          {showDeadLetter && item('dead-letter', DEAD_LETTER_ICON, t('gofi.canvas.stage-palette.dead-letter-sink', 'Dead letter sink'), t('gofi.canvas.stage-palette.dead-letter-card-description', 'Route rejected messages for inspection.'), onAddDeadLetter, { kind: 'dead-letter' })}
        </Stack>
      </ControlledCollapse>}
      {!visible.length && !showSink && !showEncode && !showDeadLetter && <Text variant="bodySmall" color="secondary">{t('gofi.canvas.stage-palette.no-matches', 'No stages or functions match “{{query}}”.', { query })}</Text>}
      {registryError ? <Text variant="bodySmall" color="warning">{t('gofi.canvas.registry-unavailable', 'Python function registry unavailable.')}</Text>
        : !pythonCount && <Text variant="bodySmall" color="secondary">{t('gofi.canvas.stage-palette.launch-library-hint', 'Launch a Python library to add its transformer functions here.')}</Text>}
    </div>
  </aside>
}

/** "Add stage" dropdown (Grafana Dropdown + Menu.Group pattern) with the side
 * palette's entries, shown only on narrow screens where the palette is hidden. */
export function AddStageMenu({ onAdd, onAddSink, onAddEncode, onAddDeadLetter, hasDeadLetter }: Props) {
  const { groups } = usePaletteGroups()
  const [open, setOpen] = useState(false)
  const menu = <Menu>
    {groups.filter(group => group.entries.length).map(group => <Menu.Group key={group.title} label={group.title}>
      {group.entries.map(entry => <Menu.Item key={entry.label} label={entry.label} description={entry.description} icon={KIND_ICON[entry.kind]} onClick={() => onAdd(entry)} />)}
    </Menu.Group>)}
    <Menu.Divider />
    <Menu.Group label={t('gofi.canvas.stage-palette.destinations', 'Destinations')}>
      <Menu.Item label={t('gofi.canvas.stage-palette.add-sink', 'Add sink')} description={t('gofi.canvas.stage-palette.add-sink-menu-description', 'Forward passing traffic to an application')} icon={SINK_ICON} onClick={onAddSink} />
      <Menu.Item label={t('gofi.canvas.stage-palette.encode', 'Encode')} description={t('gofi.canvas.stage-palette.encode-menu-description', 'Format and encode messages before a sink sends them')} icon={ENCODE_ICON} onClick={onAddEncode} />
      <Menu.Item label={t('gofi.canvas.stage-palette.dead-letter-sink', 'Dead letter sink')} description={hasDeadLetter ? t('gofi.canvas.stage-palette.dead-letter-exists', 'This flow already has one') : t('gofi.canvas.stage-palette.dead-letter-menu-description', 'Route rejected messages for inspection')} icon={DEAD_LETTER_ICON} disabled={hasDeadLetter} onClick={onAddDeadLetter} />
    </Menu.Group>
  </Menu>
  return <Dropdown overlay={menu} placement="bottom-start" onVisibleChange={setOpen}>
    <ToolbarButton icon="plus" isOpen={open} variant="primary">{t('gofi.canvas.stage-palette.add-stage', 'Add stage')}</ToolbarButton>
  </Dropdown>
}

const getStyles = (theme: GrafanaTheme2) => ({
  draggable: css({ cursor: 'grab', '&:active': { cursor: 'grabbing' } }),
  palette: css({ width: '100%', flexShrink: 0, display: 'flex', flexDirection: 'column', minHeight: 0, background: theme.colors.background.primary, borderRight: `1px solid ${theme.colors.border.weak}`, [theme.breakpoints.down('md')]: { display: 'none' } }),
  // Side padding equal to the groups' padding plus the collapse's border and
  // body padding, so the title and search box start where the cards start.
  search: css({ display: 'flex', flexDirection: 'column', gap: theme.spacing(1), padding: theme.spacing(1.5, 2), borderBottom: `1px solid ${theme.colors.border.weak}` }),
  groups: css({ flex: 1, minHeight: 0, overflowY: 'auto', padding: theme.spacing(1) }),
  collapse: css({ marginBottom: theme.spacing(1) }),
  icon: css({ color: theme.colors.primary.text }),
})
