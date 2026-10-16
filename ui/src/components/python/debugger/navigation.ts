// Pure step-navigation logic for the record-and-replay Python debugger. The
// worker records every step up front; navigation only moves an index.

export interface VariableLike { name: string; type?: string; repr?: string; children?: VariableLike[]; changed?: boolean; truncated?: boolean }
export interface FrameLike { function: string; line: number }
export interface StepLike {
  event: string
  line: number
  depth: number
  function?: string
  stack?: FrameLike[]
  changedGlobals?: VariableLike[]
  stdoutLength?: number
}

const last = (steps: unknown[]) => Math.max(0, steps.length - 1)
const clamp = (steps: unknown[], index: number) => Math.min(Math.max(0, index), last(steps))

/** Step into: the very next recorded step. */
export function stepInto(steps: StepLike[], index: number): number {
  return clamp(steps, index + 1)
}

/** Step over: the next step at the same or a shallower depth (skips calls). */
export function stepOver(steps: StepLike[], index: number): number {
  const depth = steps[index]?.depth ?? 0
  for (let next = index + 1; next < steps.length; next++) if (steps[next].depth <= depth) return next
  return last(steps)
}

/** Step out: the next step in a caller (strictly shallower depth). */
export function stepOut(steps: StepLike[], index: number): number {
  const depth = steps[index]?.depth ?? 0
  for (let next = index + 1; next < steps.length; next++) if (steps[next].depth < depth) return next
  return last(steps)
}

/** Step back: the previous recorded step. */
export function stepBack(steps: StepLike[], index: number): number {
  return clamp(steps, index - 1)
}

/** Continue: the next step on a breakpoint line, or the end of the recording. */
export function continueToBreakpoint(steps: StepLike[], index: number, breakpoints: Iterable<number>): number {
  const lines = new Set(breakpoints)
  for (let next = index + 1; next < steps.length; next++) {
    const step = steps[next]
    if (lines.has(step.line) && (step.event === 'line' || step.event === 'call')) return next
  }
  return last(steps)
}

/** The most recent step (at or before `index`) executing in call-stack frame
 * `frame` (0 = outermost, the invoked function). */
export function frameStep(steps: StepLike[], index: number, frame: number): number {
  for (let previous = Math.min(index, last(steps)); previous >= 0; previous--) if (steps[previous].depth === frame) return previous
  return index
}

/** Library globals at a step: initial globals with every recorded change up
 * to and including the step applied; values changed at this step are marked. */
export function globalsAt<T extends VariableLike>(initial: T[], steps: (StepLike & { changedGlobals?: T[] })[], index: number): T[] {
  const merged = new Map<string, T>(initial.map(variable => [variable.name, { ...variable, changed: false }]))
  for (let position = 0; position <= Math.min(index, last(steps)) && position < steps.length; position++) {
    for (const variable of steps[position].changedGlobals ?? []) merged.set(variable.name, { ...variable, changed: position === index })
  }
  return [...merged.values()].sort((a, b) => a.name.localeCompare(b.name))
}

/** The run's stdout as printed up to this step. */
export function stdoutAt(stdout: string, step?: StepLike): string {
  if (!step || step.stdoutLength === undefined) return stdout
  return stdout.slice(0, Math.max(0, step.stdoutLength))
}

export interface FlowLike {
  framing?: { mode?: string; python?: { library?: string; function?: string } }
  parser?: { python?: { library?: string; function?: string }; decoders?: { type: string; python?: { library?: string; function?: string } }[] }
  stages?: { id: string; kind?: string; python?: { library?: string; function?: string; inlineSource?: string } }[]
  sinks?: { type?: string; python?: { library?: string; function?: string } }[]
}
export interface PythonStepOption { value: string; label: string; description: string }

/** Every Python step of a flow, in flow order, as DebugFrame step ids:
 * "framer", "decoder:<index>", "parser", a stage id, or "sink:<index>". */
export function pythonSteps(flow: FlowLike): PythonStepOption[] {
  const describe = (ref?: { library?: string; function?: string; inlineSource?: string }) => ref?.inlineSource !== undefined && ref.inlineSource !== '' ? 'inline Python' : ref?.library ? `${ref.library}.${ref.function}` : 'Python'
  const options: PythonStepOption[] = []
  if (flow.framing?.mode === 'python' && flow.framing.python) options.push({ value: 'framer', label: 'Framer', description: describe(flow.framing.python) })
  ;(flow.parser?.decoders ?? []).forEach((step, index) => { if (step.type === 'python' && step.python) options.push({ value: `decoder:${index}`, label: `Decoder ${index + 1}`, description: describe(step.python) }) })
  if (flow.parser?.python) options.push({ value: 'parser', label: 'Parser', description: describe(flow.parser.python) })
  for (const stage of flow.stages ?? []) if (stage.python) options.push({ value: stage.id, label: `Stage ${stage.id}`, description: describe(stage.python) })
  ;(flow.sinks ?? []).forEach((sink, index) => { if (sink.python) options.push({ value: `sink:${index}`, label: `Sink ${index + 1}`, description: describe(sink.python) }) })
  return options
}
