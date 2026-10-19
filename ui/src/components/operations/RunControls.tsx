import { useState } from 'react'
import { Badge, Button, ConfirmModal, IconButton } from '@grafana/ui'
import { restartFlow, startFlow, stopFlow, useFlowStatus } from '../../operations-api'
import { runActions, runStateView } from '../../lib/runstate'
import { useToast } from '../Toast'
import { useGoFi } from '../../state'
import { StatusIndicator } from '../StatusIndicator'
import { t } from '../../i18n'

/** A flow's run state next to page actions (dot and label). */
export function RunStateIndicator({ name }: { name: string }) {
  const view = runStateView(useFlowStatus(name))
  return <StatusIndicator tone={view.tone} text={view.text} tooltip={view.tooltip} pulse={view.busy} />
}

/** A flow's run state as a tag on a list row. */
export function RunStateBadge({ name }: { name: string }) {
  const status = useFlowStatus(name)
  const view = runStateView(status)
  if (!status) return <Badge text="…" color="darkgrey" />
  return <Badge text={view.text} color={view.color} tooltip={view.tooltip} icon={view.busy ? 'sync' : undefined} />
}

type Action = 'start' | 'stop' | 'restart'

/** Start / Stop / Restart. Stop and Restart ask first; compact renders icon
 * buttons for table rows. */
export function RunControls({ name, compact = false }: { name: string; compact?: boolean }) {
  const status = useFlowStatus(name)
  const allowed = runActions(status)
  const addToast = useToast()
  const { reload } = useGoFi()
  const [busy, setBusy] = useState<Action | null>(null)
  const [confirm, setConfirm] = useState<Action | null>(null)

  async function run(action: Action) {
    setConfirm(null)
    setBusy(action)
    try {
      await (action === 'start' ? startFlow : action === 'stop' ? stopFlow : restartFlow)(name)
      void reload() // the stored definition's state changed
      addToast(action === 'start' ? t('gofi.operations.run-controls.started', 'Flow {{name}} started.', { name })
        : action === 'stop' ? t('gofi.operations.run-controls.stopped', 'Flow {{name}} stopped. In-flight messages were drained.', { name })
        : t('gofi.operations.run-controls.restarted', 'Flow {{name}} restarted.', { name }), 'success')
    } catch (error) {
      const message = error instanceof Error ? error.message : String(error)
      addToast(action === 'start' ? t('gofi.operations.run-controls.start-failed', 'Could not start {{name}}: {{message}}', { name, message })
        : action === 'stop' ? t('gofi.operations.run-controls.stop-failed', 'Could not stop {{name}}: {{message}}', { name, message })
        : t('gofi.operations.run-controls.restart-failed', 'Could not restart {{name}}: {{message}}', { name, message }))
    } finally {
      setBusy(null)
    }
  }

  const modal = <>
    <ConfirmModal isOpen={confirm === 'stop'} title={t('gofi.operations.run-controls.stop-title', 'Stop flow')} confirmText={t('gofi.common.stop', 'Stop')}
      body={t('gofi.operations.run-controls.stop-body', 'Stop "{{name}}"? GoFi stops accepting traffic, finishes messages already received, and removes the runtime (and its Kubernetes Service). The flow stays stopped across restarts until you start it.', { name })}
      onConfirm={() => void run('stop')} onDismiss={() => setConfirm(null)} />
    <ConfirmModal isOpen={confirm === 'restart'} title={t('gofi.operations.run-controls.restart-title', 'Restart flow')} confirmText={t('gofi.common.restart', 'Restart')} confirmVariant="primary"
      body={t('gofi.operations.run-controls.restart-body', 'Restart "{{name}}"? Its runtime drains and is recreated with the same definition. Senders reconnect to the new runtime.', { name })}
      onConfirm={() => void run('restart')} onDismiss={() => setConfirm(null)} />
  </>

  if (compact) return <>
    {allowed.start && <IconButton name={busy === 'start' ? 'spinner' : 'play'} tooltip={t('gofi.common.start', 'Start')} aria-label={t('gofi.operations.run-controls.start-flow', 'Start {{name}}', { name })} disabled={!!busy} onClick={() => void run('start')} />}
    {allowed.stop && <IconButton name={busy === 'stop' ? 'spinner' : 'square-shape'} tooltip={t('gofi.common.stop', 'Stop')} aria-label={t('gofi.operations.run-controls.stop-flow', 'Stop {{name}}', { name })} disabled={!!busy} onClick={() => setConfirm('stop')} />}
    {modal}
  </>
  return <>
    {allowed.start && <Button variant="primary" icon={busy === 'start' ? 'spinner' : 'play'} disabled={!!busy} onClick={() => void run('start')}>{busy === 'start' ? t('gofi.operations.run-controls.starting', 'Starting…') : t('gofi.common.start', 'Start')}</Button>}
    {allowed.restart && <Button variant="secondary" icon={busy === 'restart' ? 'spinner' : 'sync'} disabled={!!busy} onClick={() => setConfirm('restart')}>{busy === 'restart' ? t('gofi.operations.run-controls.restarting', 'Restarting…') : t('gofi.common.restart', 'Restart')}</Button>}
    {allowed.stop && <Button variant="secondary" icon={busy === 'stop' ? 'spinner' : 'square-shape'} disabled={!!busy} onClick={() => setConfirm('stop')}>{busy === 'stop' ? t('gofi.operations.run-controls.stopping', 'Stopping…') : t('gofi.common.stop', 'Stop')}</Button>}
    {modal}
  </>
}
