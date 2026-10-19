/** Pure presentation of flow run state and bulletins (no RPC imports, so
 * node tests can load it). */
import { t } from '../i18n/index.ts'

export type RunTone = 'success' | 'warning' | 'error' | 'info' | 'neutral'
type BadgeColor = 'green' | 'red' | 'orange' | 'blue' | 'darkgrey'

export interface RunStateLike { desired: 'running' | 'stopped'; phase: string; restarts: number; message?: string; runtimeStatus?: string }
export interface RunStateView { text: string; tone: RunTone; color: BadgeColor; tooltip?: string; busy: boolean }

const TONE_COLOR: Record<RunTone, BadgeColor> = { success: 'green', error: 'red', warning: 'orange', info: 'blue', neutral: 'darkgrey' }

/** Phase label, tone, and whether the phase is transitional. Translated at
 * call time so a language switch is picked up. */
function phaseView(phase: string): [string, RunTone, boolean] {
  switch (phase) {
    case 'running': return [t('gofi.lib.runstate.phase-running', 'Running'), 'success', false]
    case 'starting': return [t('gofi.lib.runstate.phase-starting', 'Starting'), 'info', true]
    case 'stopping': return [t('gofi.lib.runstate.phase-stopping', 'Stopping'), 'warning', true]
    case 'stopped': return [t('gofi.lib.runstate.phase-stopped', 'Stopped'), 'neutral', false]
    case 'crashed': return [t('gofi.lib.runstate.phase-crashed', 'Crashed'), 'error', true]
    case 'failed': return [t('gofi.lib.runstate.phase-failed', 'Failed'), 'error', false]
    case 'invalid': return [t('gofi.lib.runstate.phase-invalid', 'Invalid'), 'error', false]
    default: return [t('gofi.lib.runstate.phase-unknown', 'Unknown'), 'neutral', false]
  }
}

/** Running / Stopped / Starting / Stopping / Crashed (with restarts) / Failed / Invalid. */
export function runStateView(status: RunStateLike | undefined): RunStateView {
  if (!status) return { text: t('gofi.lib.runstate.phase-unknown', 'Unknown'), tone: 'neutral', color: 'darkgrey', busy: false }
  const [phase, tone, busy] = phaseView(status.phase)
  const withRestarts = status.phase !== 'stopped' && status.restarts > 0
  const text = withRestarts ? t('gofi.lib.runstate.phase-with-restarts', '{{phase}} · {{count}} restarts', { phase, count: status.restarts }) : phase
  const tooltip = [status.message, status.runtimeStatus && t('gofi.lib.runstate.runtime-status', 'Runtime: {{status}}', { status: status.runtimeStatus })].filter(Boolean).join(' · ') || undefined
  return { text, tone, color: TONE_COLOR[tone], tooltip, busy }
}

/** Which run actions make sense now. */
export function runActions(status: RunStateLike | undefined): { start: boolean; stop: boolean; restart: boolean } {
  if (!status) return { start: false, stop: false, restart: false }
  const invalid = status.phase === 'invalid'
  return { start: !invalid && status.desired === 'stopped', stop: !invalid && status.desired === 'running', restart: !invalid && status.desired === 'running' }
}

/** Human component names: "Destination 2", "Stage enrich", "Parser". */
export function componentLabel(component: string): string {
  const sink = /^sink:(\d+)$/.exec(component)
  if (sink) return t('gofi.lib.runstate.component-destination', 'Destination {{index}}', { index: Number(sink[1]) + 1 })
  if (component.startsWith('stage:')) return t('gofi.lib.runstate.component-stage', 'Stage {{id}}', { id: component.slice(6) })
  switch (component) {
    case 'source': return t('gofi.lib.runstate.component-source', 'Source')
    case 'framer': return t('gofi.lib.runstate.component-framer', 'Framer')
    case 'parser': return t('gofi.lib.runstate.component-parser', 'Parser')
    case 'deadLetter': return t('gofi.lib.runstate.component-dead-letter', 'Dead letter')
    case 'runtime': return t('gofi.lib.runstate.component-runtime', 'Runtime')
    case 'tap': return t('gofi.lib.runstate.component-tap', 'Live traffic')
    case 'stages': return t('gofi.lib.runstate.component-stages', 'Stages')
    default: return component
  }
}
