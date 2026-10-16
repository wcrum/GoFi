import { useCallback, useEffect, useState } from 'react'
import type { Dispatch, SetStateAction } from 'react'
import type { Edge } from '@xyflow/react'
import { css } from '@emotion/css'
import type { GrafanaTheme2 } from '@grafana/data'
import { Badge, Button, ButtonGroup, Card, Field, IconButton, Input, Stack, Text, TextArea, ToolbarButton, useStyles2 } from '@grafana/ui'
import { useToast } from '../Toast'
import type { SimulateResult, TestCase } from '../../types'
import { simulateFlow } from '../../api'
import { genId } from '../../lib/id'
import { generateSampleRaw } from '../../lib/mockData'
import { graphToDefinition, orderedSinkNodeIds } from './convert'
import type { FlowNode, MockState } from './types'
import { MockTraceView } from '../MockTraceView'

const AUTO_PLAY_MS = 600

interface Props {
  nodes: FlowNode[]
  edges: Edge[]
  name: string
  testCases: TestCase[]
  setTestCases: Dispatch<SetStateAction<TestCase[]>>
  // readOnly (view mode) disables test-case add/rename/delete/generate since
  // there's no update endpoint to persist edits to an already-created
  // flow — building an affordance that silently discards data would be a
  // trap. Existing test cases stay visible and playable; running a case
  // mutates nothing, so Play works in both modes.
  readOnly: boolean
  // Drives the canvas node highlighting: applies the given mockState to each
  // id in the map and clears it on every other node (see FlowCanvas.setMockStates).
  setMockStates: (map: Record<string, MockState | undefined>) => void
  onClose: () => void
}

/** Map a playback step to the canvas node-highlight map. Steps are: 0 =
 * ingress/parse, k in 1..stages.length = stage k-1, stages.length+1 = final
 * sink-dispatch. A stage playback has moved past shows its terminal
 * pass/drop/error color; the active step shows amber; the final step lights
 * the sink node(s), or the dead-letter node when the message was dropped. */
function statesForStep(
  result: SimulateResult,
  step: number,
  nodes: FlowNode[],
  edges: Edge[],
): Record<string, MockState | undefined> {
  const states: Record<string, MockState | undefined> = {}

  // Step 0 is the fixed prefix: Source, Frame, and Decode succeed once a frame
  // exists; a decode or parse failure is reported on the Parse step.
  for (const id of ['ingress', 'frame', 'decode']) states[id] = step === 0 ? 'active' : 'pass'
  states['parse'] = step === 0 ? 'active' : result.parseError ? 'error' : 'pass'

  ;(result.stages ?? []).forEach((stage, i) => {
    const pos = i + 1
    if (step === pos) states[stage.id] = 'active'
    else if (step > pos) states[stage.id] = stage.pass ? 'pass' : stage.error ? 'error' : 'drop'
    // step < pos: not reached yet — leave unset (cleared on canvas).
  })

  const finalStep = (result.stages ?? []).length + 1
  if (step >= finalStep) {
    if (result.dropped) {
      // Dropped messages only reach a sink if a dead-letter sink is
      // configured. SinkNode gives this a distinct "receives this drop"
      // label rather than a plain pass, since it's not really "passing".
      if (result.deadLetter) states['deadletter'] = 'pass'
    } else {
      // Map each SinkOutcome to its exact canvas node id via the same
      // fan-out edge order the backend used to build SimulateResult.sinks
      // (see convert.ts's orderedSinkNodeIds), so a sink whose `when` didn't
      // match shows as a distinct drop rather than a uniform "sent" glow.
      try {
        const sinkIds = orderedSinkNodeIds(nodes, edges)
        for (const outcome of result.sinks ?? []) {
          const id = sinkIds[outcome.index]
          if (id) states[id] = outcome.sent ? 'pass' : 'drop'
        }
      } catch {
        // Graph changed since Play ran (e.g. a sink node was deleted) —
        // nothing sensible to highlight; leave sinks unhighlighted rather
        // than guess.
      }
    }
  }

  return states
}

export function MockPanel({ nodes, edges, name, testCases, setTestCases, readOnly, setMockStates, onClose }: Props) {
  const styles = useStyles2(getStyles)
  const addToast = useToast()
  const [selectedId, setSelectedId] = useState<string | null>(testCases[0]?.id ?? null)
  const [result, setResult] = useState<SimulateResult | null>(null)
  const [step, setStep] = useState(0)
  const [loading, setLoading] = useState(false)
  const [autoPlay, setAutoPlay] = useState(false)

  const selected = testCases.find((t) => t.id === selectedId) ?? null
  const totalSteps = result ? (result.stages ?? []).length + 2 : 0 // parse + stages + final

  const clearHighlights = useCallback(() => setMockStates({}), [setMockStates])

  // Clear canvas highlights when the panel unmounts.
  useEffect(() => clearHighlights, [clearHighlights])

  // Push the current step's highlights to the canvas whenever it changes.
  useEffect(() => {
    if (result) setMockStates(statesForStep(result, step, nodes, edges))
    // nodes/edges intentionally excluded: highlighting keys off ids, which
    // are stable across the drags/edits that churn the nodes/edges arrays.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [result, step])

  // Auto-play: advance one step per tick, stopping at the last step.
  useEffect(() => {
    if (!autoPlay || !result) return
    if (step >= totalSteps - 1) {
      setAutoPlay(false)
      return
    }
    const id = window.setTimeout(() => setStep((s) => s + 1), AUTO_PLAY_MS)
    return () => window.clearTimeout(id)
  }, [autoPlay, step, totalSteps, result])

  function updateSelected(patch: Partial<TestCase>) {
    if (!selected) return
    setTestCases((prev) => prev.map((t) => (t.id === selected.id ? { ...t, ...patch } : t)))
  }

  function handleAdd() {
    const tc: TestCase = { id: genId(), name: `Test ${testCases.length + 1}`, raw: '' }
    setTestCases((prev) => [...prev, tc])
    setSelectedId(tc.id)
    resetPlayback()
  }

  function handleDelete(id: string) {
    setTestCases((prev) => prev.filter((t) => t.id !== id))
    if (selectedId === id) {
      setSelectedId(null)
      resetPlayback()
    }
  }

  function handleGenerate() {
    if (!selected) return
    let def
    try {
      def = graphToDefinition(name, nodes, edges)
    } catch {
      // A generator only needs the stage/sink expressions, which are readable
      // even from an incomplete graph — but graphToDefinition throws on a broken
      // one. Fall back to scanning whatever specs the nodes carry directly.
      const parse = nodes.find((n) => n.type === 'parse')
      def = {
        name,
        source: { listen: { port: 0, network: 'tcp' } },
        framing: { mode: 'line' },
        parser: parse?.type === 'parse' ? parse.data.parser : { format: 'json' },
        stages: nodes.flatMap((n) => (n.type === 'stage' ? [n.data.spec] : [])),
        sinks: nodes.flatMap((n) => (n.type === 'sink' && n.id !== 'deadletter' ? [n.data.sink] : [])),
      }
    }
    updateSelected({ raw: generateSampleRaw(def) })
  }

  function resetPlayback() {
    setResult(null)
    setStep(0)
    setAutoPlay(false)
    clearHighlights()
  }

  async function handlePlay() {
    if (!selected) return
    setAutoPlay(false)
    setLoading(true)
    try {
      // Build against the *current* graph. An incomplete draft (e.g. no sink)
      // makes graphToDefinition throw — surface that inline rather than crashing.
      const def = { ...graphToDefinition(name, nodes, edges), testCases }
      const res = await simulateFlow(def, selected.raw)
      setResult(res)
      setStep(0)
    } catch (e) {
      addToast(String(e))
      setResult(null)
    } finally {
      setLoading(false)
    }
  }

  return (
    <aside className={styles.panel} aria-label="Test flow">
      <div className={styles.header}>
        <Text element="h2" variant="h5">Test flow</Text>
        <IconButton name="times" tooltip="Close test flow" onClick={onClose} />
      </div>

      <div className={styles.body}>
        {/* Test case list */}
        <Stack direction="column" gap={1}>
          <Stack justifyContent="space-between" alignItems="center">
            <Text variant="bodySmall" weight="medium" color="secondary">Test cases</Text>
            {!readOnly && <Button size="sm" variant="secondary" icon="plus" onClick={handleAdd}>Add</Button>}
          </Stack>
          {testCases.length === 0 ? (
            <Text variant="bodySmall" color="secondary">No test cases yet.</Text>
          ) : testCases.map((tc) => (
            <Card key={tc.id} isCompact noMargin isSelected={tc.id === selectedId} onClick={() => { setSelectedId(tc.id); resetPlayback() }}>
              <Card.Heading>{tc.name}</Card.Heading>
              <Card.Description className={styles.raw}>{tc.raw || '(empty)'}</Card.Description>
              {!readOnly && <Card.SecondaryActions>
                <IconButton name="trash-alt" tooltip="Delete test case" onClick={(e) => { e.stopPropagation(); handleDelete(tc.id) }} />
              </Card.SecondaryActions>}
            </Card>
          ))}
        </Stack>

        {/* Selected test case editor */}
        {selected && (
          <div className={styles.section}>
            <Stack direction="column" gap={2}>
              <Field label="Name" noMargin>
                <Input value={selected.name} disabled={readOnly} onChange={(e) => updateSelected({ name: e.currentTarget.value })} placeholder="Test case name" />
              </Field>
              <Field label={<Stack justifyContent="space-between" alignItems="center">
                <span>Raw input</span>
                {!readOnly && <Button size="sm" variant="secondary" fill="text" icon="ai-sparkle" onClick={handleGenerate} tooltip="Generate a heuristic placeholder sample from the pipeline's expressions">Generate</Button>}
              </Stack>} noMargin>
                <TextArea rows={5} disabled={readOnly} className={styles.mono} value={selected.raw} onChange={(e) => updateSelected({ raw: e.currentTarget.value })} placeholder='{"type":"a-f-G"}' />
              </Field>
              <Button variant="primary" icon={loading ? 'spinner' : 'play'} onClick={handlePlay} disabled={loading} fullWidth>
                {loading ? 'Running...' : 'Play'}
              </Button>
            </Stack>
          </div>
        )}

        {/* Playback + trace */}
        {result && (
          <div className={styles.section}>
            <Stack direction="column" gap={2}>
              <Stack alignItems="center" justifyContent="space-between">
                <ButtonGroup>
                  <ToolbarButton icon="angle-left" tooltip="Previous step" disabled={step <= 0} onClick={() => { setAutoPlay(false); setStep((s) => Math.max(0, s - 1)) }} />
                  <ToolbarButton icon="angle-right" tooltip="Next step" disabled={step >= totalSteps - 1} onClick={() => { setAutoPlay(false); setStep((s) => Math.min(totalSteps - 1, s + 1)) }} />
                  <ToolbarButton icon={autoPlay ? 'pause' : 'play'} onClick={() => {
                    // Restart from the top if auto-playing from the end.
                    if (!autoPlay && step >= totalSteps - 1) setStep(0)
                    setAutoPlay((p) => !p)
                  }}>{autoPlay ? 'Pause' : 'Auto-play'}</ToolbarButton>
                </ButtonGroup>
                <Badge text={`step ${step + 1}/${totalSteps}`} color="darkgrey" />
              </Stack>
              <MockTraceView result={result} activeIndex={step - 1} />
            </Stack>
          </div>
        )}
      </div>
    </aside>
  )
}

const getStyles = (theme: GrafanaTheme2) => ({
  panel: css({ width: '100%', flexShrink: 0, display: 'flex', flexDirection: 'column', minHeight: 0, background: theme.colors.background.primary, borderLeft: `1px solid ${theme.colors.border.weak}`, [theme.breakpoints.down('md')]: { width: '100%', position: 'absolute', inset: 0, zIndex: 5 } }),
  header: css({ display: 'flex', alignItems: 'center', justifyContent: 'space-between', padding: theme.spacing(1, 1, 1, 2), borderBottom: `1px solid ${theme.colors.border.weak}`, h2: { margin: 0 } }),
  body: css({ flex: 1, minHeight: 0, overflowY: 'auto', padding: theme.spacing(2), display: 'flex', flexDirection: 'column', gap: theme.spacing(2) }),
  section: css({ borderTop: `1px solid ${theme.colors.border.weak}`, paddingTop: theme.spacing(2) }),
  raw: css({ fontFamily: theme.typography.fontFamilyMonospace, fontSize: theme.typography.bodySmall.fontSize, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }),
  mono: css({ fontFamily: theme.typography.fontFamilyMonospace, fontSize: theme.typography.bodySmall.fontSize }),
})
