// Worker phase → status indicator. The supervisor reports Starting, Ready, or
// Failed; Starting right after Ready is a restart.

export type WorkerTone = 'success' | 'warning' | 'error' | 'info' | 'neutral'
export interface WorkerState { tone: WorkerTone; text: string; pulse: boolean; tooltip?: string; transitioning: boolean }

export function workerState(status: { phase?: string; error?: string } | undefined, previousPhase?: string): WorkerState {
  switch (status?.phase) {
    case 'Ready':
    case 'Running':
      return { tone: 'success', text: 'Running', pulse: true, transitioning: false, tooltip: 'The worker is running and accepts invocations.' }
    case 'Starting':
    case 'Pending':
      return previousPhase === 'Ready' || previousPhase === 'Running'
        ? { tone: 'info', text: 'Restarting worker…', pulse: true, transitioning: true, tooltip: 'The worker is being replaced (definition change or failed health checks).' }
        : { tone: 'info', text: 'Starting worker…', pulse: true, transitioning: true }
    case 'Failed':
      return { tone: 'error', text: 'Failed', pulse: false, transitioning: false, tooltip: status.error || 'The worker failed to start.' }
    case undefined:
      return { tone: 'neutral', text: 'Stopped', pulse: false, transitioning: false }
    default:
      return { tone: 'neutral', text: status?.phase ?? 'Unknown', pulse: false, transitioning: false, tooltip: status?.error || undefined }
  }
}
