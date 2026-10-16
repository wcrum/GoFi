import { useCallback, useEffect, useState } from 'react'
import type { Dispatch, SetStateAction } from 'react'
import type { Edge } from '@xyflow/react'
import { PanelHeader, Input, Spinner, Badge } from '../primitives'
import { useToast } from '../Toast'
import { RiPlayLine, RiDeleteBinLine, RiAddLine, RiMagicLine } from 'react-icons/ri'
import type { SimulateResult, TestCase } from '../../types'
import { simulateListener } from '../../api'
import { genId } from '../../lib/id'
import { generateSampleRaw } from '../../lib/mockData'
import { flowToListener, orderedSinkNodeIds } from './convert'
import type { FlowNode, MockState } from './types'
import { MockTraceView } from '../MockTraceView'
import { TEXTAREA_CLASSNAME } from './NodeInspector'

const AUTO_PLAY_MS = 600

interface Props {
  nodes: FlowNode[]
  edges: Edge[]
  name: string
  testCases: TestCase[]
  setTestCases: Dispatch<SetStateAction<TestCase[]>>
  // readOnly (view mode) disables test-case add/rename/delete/generate since
  // there's no update endpoint to persist edits to an already-created
  // listener — building an affordance that silently discards data would be a
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

  states['ingress'] = step === 0 ? 'active' : result.parseError ? 'error' : 'pass'

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
      def = flowToListener(name, nodes, edges)
    } catch {
      // A generator only needs the stage/sink expressions, which are readable
      // even from an incomplete graph — but flowToListener throws on a broken
      // one. Fall back to scanning whatever specs the nodes carry directly.
      const ingress = nodes.find((n) => n.type === 'ingress')
      def = {
        name,
        source: { port: 0, network: 'tcp' },
        framing: { mode: 'line' },
        parser: ingress?.type === 'ingress' ? ingress.data.parser : { format: 'json' },
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
      // makes flowToListener throw — surface that inline rather than crashing.
      const def = { ...flowToListener(name, nodes, edges), testCases }
      const res = await simulateListener(def, selected.raw)
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
    <aside className="w-80 shrink-0 border-l border-g-border-weak bg-g-primary flex flex-col overflow-y-auto">
      <PanelHeader title="Test flow" onClose={onClose} />

      <div className="p-4 space-y-4">
        {/* Test case list */}
        <div>
          <div className="flex items-center justify-between mb-2">
            <span className="text-xs font-medium text-g-text-secondary uppercase tracking-wide">Test Cases</span>
            {!readOnly && (
              <button
                onClick={handleAdd}
                className="flex items-center gap-1 text-xs px-2 py-0.5 rounded border border-g-border-weak text-g-text-secondary hover:text-g-text transition-colors"
              >
                <RiAddLine size={12} /> Add
              </button>
            )}
          </div>
          {testCases.length === 0 ? (
            <p className="text-xs text-g-text-secondary">No test cases yet.</p>
          ) : (
            <div className="space-y-1">
              {testCases.map((tc) => (
                <div
                  key={tc.id}
                  onClick={() => {
                    setSelectedId(tc.id)
                    resetPlayback()
                  }}
                  className={`group flex items-center gap-2 rounded px-2 py-1.5 cursor-pointer border transition-colors ${
                    tc.id === selectedId
                      ? 'border-g-accent-main/60 bg-g-hover'
                      : 'border-g-border-weak hover:bg-g-hover'
                  }`}
                >
                  <div className="min-w-0 flex-1">
                    <div className="text-sm text-g-text truncate">{tc.name}</div>
                    <div className="text-xs text-g-text-secondary font-mono truncate">
                      {tc.raw || '(empty)'}
                    </div>
                  </div>
                  {!readOnly && (
                    <button
                      onClick={(e) => {
                        e.stopPropagation()
                        handleDelete(tc.id)
                      }}
                      className="shrink-0 text-g-text-secondary hover:text-red-400 opacity-0 group-hover:opacity-100 transition-opacity"
                      title="Delete test case"
                    >
                      <RiDeleteBinLine size={14} />
                    </button>
                  )}
                </div>
              ))}
            </div>
          )}
        </div>

        {/* Selected test case editor */}
        {selected && (
          <div className="space-y-3 border-t border-g-border-weak pt-4">
            <Input
              value={selected.name}
              disabled={readOnly}
              onChange={(e) => updateSelected({ name: e.target.value })}
              placeholder="Test case name"
            />

            <div>
              <div className="flex items-center justify-between mb-1">
                <span className="text-xs text-g-text-secondary">Raw input</span>
                {!readOnly && (
                  <button
                    onClick={handleGenerate}
                    className="flex items-center gap-1 text-xs text-g-text-secondary hover:text-g-text transition-colors"
                    title="Generate a heuristic placeholder sample from the pipeline's expressions"
                  >
                    <RiMagicLine size={12} /> Generate
                  </button>
                )}
              </div>
              <textarea
                rows={5}
                disabled={readOnly}
                className={TEXTAREA_CLASSNAME}
                value={selected.raw}
                onChange={(e) => updateSelected({ raw: e.target.value })}
                placeholder='{"type":"a-f-G"}'
              />
            </div>

            <button
              onClick={handlePlay}
              disabled={loading}
              className="w-full flex items-center justify-center gap-1.5 px-3 py-2 rounded text-sm font-medium bg-g-accent-main text-white hover:bg-g-accent-main/90 disabled:opacity-50 transition-colors"
            >
              <RiPlayLine /> {loading ? 'Running…' : 'Play'}
            </button>
          </div>
        )}

        {loading && (
          <div className="flex justify-center">
            <Spinner size="16px" />
          </div>
        )}

        {/* Playback + trace */}
        {result && (
          <div className="space-y-3 border-t border-g-border-weak pt-4">
            <div className="flex items-center gap-2">
              <button
                onClick={() => {
                  setAutoPlay(false)
                  setStep((s) => Math.max(0, s - 1))
                }}
                disabled={step <= 0}
                className="text-xs px-2 py-1 rounded border border-g-border-weak text-g-text-secondary hover:text-g-text disabled:opacity-40 transition-colors"
              >
                Prev
              </button>
              <button
                onClick={() => {
                  setAutoPlay(false)
                  setStep((s) => Math.min(totalSteps - 1, s + 1))
                }}
                disabled={step >= totalSteps - 1}
                className="text-xs px-2 py-1 rounded border border-g-border-weak text-g-text-secondary hover:text-g-text disabled:opacity-40 transition-colors"
              >
                Next
              </button>
              <button
                onClick={() => {
                  // Restart from the top if auto-playing from the end.
                  if (!autoPlay && step >= totalSteps - 1) setStep(0)
                  setAutoPlay((p) => !p)
                }}
                className="text-xs px-2 py-1 rounded border border-g-border-weak text-g-text-secondary hover:text-g-text transition-colors"
              >
                {autoPlay ? 'Pause' : 'Auto-play'}
              </button>
              <Badge text={`step ${step + 1}/${totalSteps}`} color="gray" className="ml-auto" />
            </div>

            <MockTraceView result={result} activeIndex={step - 1} />
          </div>
        )}
      </div>
    </aside>
  )
}
