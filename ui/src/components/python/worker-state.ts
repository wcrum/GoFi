// Worker phase → status indicator. The supervisor reports Starting, Ready, or
// Failed; Starting right after Ready is a restart.
import { t } from '../../i18n/index.ts'

export type WorkerTone = 'success' | 'warning' | 'error' | 'info' | 'neutral'
export interface WorkerState { tone: WorkerTone; text: string; pulse: boolean; tooltip?: string; transitioning: boolean }

export function workerState(status: { phase?: string; error?: string } | undefined, previousPhase?: string): WorkerState {
  switch (status?.phase) {
    case 'Ready':
    case 'Running':
      return { tone: 'success', text: t('gofi.python.worker-state.running', 'Running'), pulse: true, transitioning: false, tooltip: t('gofi.python.worker-state.running-tooltip', 'The worker is running and accepts invocations.') }
    case 'Starting':
    case 'Pending':
      return previousPhase === 'Ready' || previousPhase === 'Running'
        ? { tone: 'info', text: t('gofi.python.worker-state.restarting', 'Restarting worker…'), pulse: true, transitioning: true, tooltip: t('gofi.python.worker-state.restarting-tooltip', 'The worker is being replaced (definition change or failed health checks).') }
        : { tone: 'info', text: t('gofi.python.worker-state.starting', 'Starting worker…'), pulse: true, transitioning: true }
    case 'Failed':
      return { tone: 'error', text: t('gofi.python.worker-state.failed', 'Failed'), pulse: false, transitioning: false, tooltip: status.error || t('gofi.python.worker-state.failed-tooltip', 'The worker failed to start.') }
    case undefined:
      return { tone: 'neutral', text: t('gofi.python.worker-state.stopped', 'Stopped'), pulse: false, transitioning: false }
    default:
      return { tone: 'neutral', text: status?.phase ?? t('gofi.python.worker-state.unknown', 'Unknown'), pulse: false, transitioning: false, tooltip: status?.error || undefined }
  }
}
