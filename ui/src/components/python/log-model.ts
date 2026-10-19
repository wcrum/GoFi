// Runner-focused view of worker log lines: lifecycle events, hidden runtime
// boilerplate, and invocation lines grouped into run blocks. Pure functions
// (no React) so they are unit tested with node --test.
import { t } from '../../i18n/index.ts'

export interface LogLine {
  time?: string
  stream: string
  line: string
  invocationId: string
  function: string
  kind?: string
  level?: string
}

export type LifecycleTone = 'starting' | 'ready' | 'stopped' | 'error' | 'info'
export type RunOutcome = 'running' | 'ok' | 'dropped' | 'error'
export type LineSeverity = 'output' | 'stderr' | 'warn' | 'error' | 'system'

export interface RunLine { time?: string; text: string; severity: LineSeverity }
export type LogItem =
  | { type: 'lifecycle'; key: string; time?: string; tone: LifecycleTone; text: string; functions?: string[]; /** A `starting` row that a later lifecycle event superseded. */ settled?: boolean }
  | { type: 'runtime'; key: string; time?: string; text: string }
  | { type: 'line'; key: string; time?: string; text: string; severity: LineSeverity }
  | { type: 'run'; key: string; id: string; function: string; kind: string; start?: string; durationUs?: number; outcome: RunOutcome; error?: string; errorLine?: number; lines: RunLine[]; debugSteps?: number }

/** Uvicorn/ASGI server chatter: infrastructure, not user output. */
const RUNTIME = [
  /^Started server process/, /^Waiting for application (startup|shutdown)/, /^Application (startup|shutdown) complete/,
  /^Uvicorn running on/, /^Shutting down/, /^Finished server process/,
]
export const isRuntimeLine = (entry: LogLine) => !entry.invocationId && entry.stream === 'system' && RUNTIME.some(pattern => pattern.test(entry.line))

function lifecycle(entry: LogLine): { tone: LifecycleTone; text: string; functions?: string[] } {
  const line = entry.line
  const ready = /^worker ready: (.*)$/.exec(line)
  if (ready) return { tone: 'ready', text: t('gofi.python.log-model.worker-ready', 'Worker ready'), functions: ready[1].split(',').map(item => item.trim()).filter(Boolean) }
  if (/^worker starting/.test(line)) {
    // The worker appends "(source loads per invocation)" when it runs lazily.
    const lazy = /^worker starting \(source loads per invocation\)/.test(line)
    return { tone: 'starting', text: lazy ? t('gofi.python.log-model.worker-starting-lazy', 'Worker starting (source loads per invocation)') : t('gofi.python.log-model.worker-starting', 'Worker starting') }
  }
  if (/^worker stopped/.test(line)) return { tone: 'stopped', text: t('gofi.python.log-model.worker-stopped', 'Worker stopped — reattaching when it runs again') }
  if (/^library failed to load/.test(line) || entry.level === 'error') return { tone: 'error', text: line.charAt(0).toUpperCase() + line.slice(1) }
  if (entry.level === 'warn') return { tone: 'stopped', text: line.charAt(0).toUpperCase() + line.slice(1) }
  return { tone: 'info', text: line.charAt(0).toUpperCase() + line.slice(1) }
}

const STARTED = /^(?:invocation|debug recording) started: (\S+) (.+)$/
const FINISHED = /^invocation finished in (\d+) us( \(dropped\))?/
const FAILED = /^invocation failed after (\d+) us(?: at line (\d+))?: (.*)$/s
const KILLED = /^(?:invocation|debug run) killed: (.*)$|^invocation process exited without a result(.*)$/
const DEBUG_DONE = /^debug recording finished: (.*)$/

/** Groups lines into lifecycle rows, runtime rows (omitted unless
 * `showRuntime`), standalone output, and one block per invocation id in
 * order of first appearance. */
export function buildLogItems(entries: LogLine[], showRuntime: boolean): LogItem[] {
  const items: LogItem[] = []
  const runs = new Map<string, Extract<LogItem, { type: 'run' }>>()
  entries.forEach((entry, index) => {
    const key = `${index}`
    if (!entry.invocationId) {
      if (isRuntimeLine(entry)) { if (showRuntime) items.push({ type: 'runtime', key, time: entry.time, text: entry.line }); return }
      if (entry.stream === 'system') { items.push({ type: 'lifecycle', key, time: entry.time, ...lifecycle(entry) }); return }
      items.push({ type: 'line', key, time: entry.time, text: entry.line, severity: entry.stream === 'stderr' ? 'stderr' : 'output' })
      return
    }
    let run = runs.get(entry.invocationId)
    if (!run) {
      run = { type: 'run', key: `run:${entry.invocationId}`, id: entry.invocationId, function: entry.function, kind: entry.kind ?? '', start: entry.time, outcome: 'running', lines: [] }
      runs.set(entry.invocationId, run)
      items.push(run)
    }
    if (!run.function && entry.function) run.function = entry.function
    if (!run.kind && entry.kind) run.kind = entry.kind
    if (entry.stream !== 'system') { run.lines.push({ time: entry.time, text: entry.line, severity: entry.stream === 'stderr' ? 'stderr' : 'output' }); return }
    const started = STARTED.exec(entry.line)
    if (started) { run.start = entry.time ?? run.start; run.kind = run.kind || started[1]; run.function = run.function || started[2]; return }
    const finished = FINISHED.exec(entry.line)
    if (finished) { run.durationUs = Number(finished[1]); run.outcome = finished[2] ? 'dropped' : 'ok'; return }
    const failed = FAILED.exec(entry.line)
    if (failed) { run.durationUs = Number(failed[1]); run.outcome = 'error'; run.errorLine = failed[2] ? Number(failed[2]) : undefined; run.error = failed[3]; return }
    const killed = KILLED.exec(entry.line)
    if (killed) { run.outcome = 'error'; run.error = entry.line.charAt(0).toUpperCase() + entry.line.slice(1); return }
    // Debugger bookkeeping is shown on the run header, not as program output.
    const debugDone = DEBUG_DONE.exec(entry.line)
    if (debugDone) { if (run.outcome === 'running') run.outcome = 'ok'; run.debugSteps = Number(/(\d+)/.exec(debugDone[1])?.[1] ?? 0); return }
    // Other worker-internal lines inside a run are runtime details.
    if (entry.level === 'error' || entry.level === 'warn') { run.lines.push({ time: entry.time, text: entry.line, severity: entry.level }); return }
    if (showRuntime) run.lines.push({ time: entry.time, text: entry.line, severity: 'system' })
  })
  // A worker that reported ready (or stopped, or failed) is no longer
  // starting: the earlier row keeps its place but stops animating.
  let lastStarting: Extract<LogItem, { type: 'lifecycle' }> | undefined
  for (const item of items) {
    if (item.type !== 'lifecycle') continue
    if (item.tone === 'starting') lastStarting = item
    else if (lastStarting) { lastStarting.settled = true; lastStarting = undefined }
  }
  // The worker's own startup banner (uvicorn) is flushed right after "ready"
  // with the same timestamp; it belongs before the readiness it announces.
  for (let i = 0; i < items.length; i++) {
    const item = items[i]
    if (item.type !== 'lifecycle' || item.tone !== 'ready') continue
    let end = i + 1
    while (end < items.length) { const next = items[end]; if (next.type !== 'runtime' || (next.time ?? '') > (item.time ?? '')) break; end++ }
    if (end > i + 1) { items.splice(i, 0, ...items.splice(i + 1, end - i - 1)); i = end - 1 }
  }
  return items
}

/** Text filter over items; "only" keeps a single run's block. */
export function filterLogItems(items: LogItem[], query: string, onlyRun?: string): LogItem[] {
  const needle = query.trim().toLowerCase()
  return items.filter(item => {
    if (onlyRun) return item.type === 'run' && item.id === onlyRun && matches(item, needle)
    return matches(item, needle)
  })
}

function matches(item: LogItem, needle: string): boolean {
  if (!needle) return true
  const texts = item.type === 'run'
    ? [item.function, item.kind, item.id, item.error ?? '', ...item.lines.map(line => line.text)]
    : [item.text, ...(item.type === 'lifecycle' ? item.functions ?? [] : [])]
  return texts.some(text => text.toLowerCase().includes(needle))
}

/** "1.2 ms", "350 µs", "2.41 s". */
export function formatDuration(us?: number): string {
  if (us === undefined || !Number.isFinite(us)) return ''
  if (us < 1000) return t('gofi.python.log-model.duration-microseconds', '{{value}} µs', { value: us })
  if (us < 1_000_000) return t('gofi.python.log-model.duration-milliseconds', '{{value}} ms', { value: (us / 1000).toFixed(us < 10_000 ? 1 : 0) })
  return t('gofi.python.log-model.duration-seconds', '{{value}} s', { value: (us / 1_000_000).toFixed(2) })
}

/** Elapsed seconds for an in-progress run, as "1.4s". */
export function formatElapsed(ms: number): string {
  return t('gofi.python.log-model.elapsed-seconds', '{{value}}s', { value: (Math.max(0, ms) / 1000).toFixed(1) })
}
