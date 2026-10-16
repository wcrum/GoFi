import { useCallback, useEffect, useRef, useState } from 'react'
import { Alert, Badge, Drawer, Stack, Text } from '@grafana/ui'
import { newRunId } from '../../canvas/python-templates'
import { useLogReadiness } from '../useLogReadiness'
import type { RunTarget } from '../run'
import { WorkerLogsPanel, type LogTarget } from '../WorkerLogsPanel'
import { debugErrorMessage, debugFunction, type DebugInput, type DebugRecording } from './debug-api'
import { PythonDebugger } from './PythonDebugger'
import { PageLoading } from '../../PageLoading'

/** Records one traced execution of a function (saved worker, or unsaved
 * source in a short-lived worker) and opens the step debugger on it. */
export function DebugDrawer({ target, input, onClose }: { target: RunTarget; input: DebugInput; onClose: () => void }) {
  const [recording, setRecording] = useState<DebugRecording | null>(null)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const [watches, setWatches] = useState<string[]>([])
  const [logs, setLogs] = useState<{ target: LogTarget; runId: string } | null>(null)
  const readiness = useLogReadiness()
  const watchesRef = useRef(watches)
  watchesRef.current = watches
  const breakpointKey = target.savedLibrary ?? 'unsaved'

  const record = useCallback(async () => {
    setBusy(true); setError('')
    try {
      const runId = newRunId()
      setLogs({ target: target.savedLibrary ? { library: target.savedLibrary } : { runId }, runId })
      await readiness.ready(!!target.savedLibrary)
      setRecording(await debugFunction({ library: target.savedLibrary, source: target.source, timeoutSeconds: target.timeoutSeconds }, input, { watches: watchesRef.current, runId }))
    } catch (reason) { setError(debugErrorMessage(reason)) }
    finally { setBusy(false) }
  }, [target, input, readiness])
  useEffect(() => { void record() }, []) // eslint-disable-line react-hooks/exhaustive-deps

  return <Drawer title={`Debug ${input.name}`} subtitle={<Stack gap={1} alignItems="center">
    <Badge color="purple" icon="bug" text={input.kind} />
    <Badge color={target.savedLibrary ? 'green' : 'darkgrey'} text={target.savedLibrary ? `Saved worker · ${target.savedLibrary}` : 'Unsaved · scratch worker'} />
    <Text color="secondary" variant="bodySmall">Recorded in the isolated worker, then replayed here. Nothing is sent.</Text>
  </Stack>} size="lg" onClose={onClose}>
    <Stack direction="column" gap={2}>
      {error && <Alert severity="error" title="Debug session failed" bottomSpacing={0}>{error}</Alert>}
      {!recording && busy && <PageLoading text="Recording execution..." />}
      {recording && <PythonDebugger recording={recording} source={target.source} breakpointKey={breakpointKey} input={input} watches={watches} onWatchesChange={setWatches} onRerecord={() => void record()} rerecording={busy} title={`Debugger for ${input.name}`} />}
      {logs && <WorkerLogsPanel compact height={120} title="Worker logs · debug run" target={logs.target} highlightInvocation={logs.runId} onState={readiness.onState} />}
    </Stack>
  </Drawer>
}
