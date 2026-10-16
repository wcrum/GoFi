import { useCallback, useEffect, useState } from 'react'
import { Alert, Badge, Button, Combobox, Drawer, Field, PanelChrome, Stack, Text } from '@grafana/ui'
import { listSandboxes } from '../../../sandbox-api'
import { MockTraceView } from '../../MockTraceView'
import { debugErrorMessage, debugFrame, type FrameDebug } from './debug-api'
import { pythonSteps, type FlowLike } from './navigation'
import { PythonDebugger } from './PythonDebugger'
import { PageLoading } from '../../PageLoading'

/** Debugs one captured live frame: simulates it through the flow up to a
 * Python step and records that step with the exact input it received. */
export function FrameDebugDrawer({ flowName, flow, raw, onClose }: { flowName: string; flow: FlowLike; raw: string; onClose: () => void }) {
  const options = pythonSteps(flow)
  const [step, setStep] = useState(options[0]?.value ?? '')
  const [watches, setWatches] = useState<string[]>([])
  const [session, setSession] = useState<FrameDebug | null>(null)
  const [source, setSource] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')

  const record = useCallback(async (selected: string, watchList: string[]) => {
    setBusy(true); setError('')
    try {
      const response = await debugFrame(flowName, raw, selected, watchList)
      setSession(response)
      // Line numbers refer to the library the step runs in (inline stages run
      // in the flow's managed library), so show that library's source.
      const library = response.function?.library
      if (library) {
        const { sandboxes } = await listSandboxes()
        setSource(sandboxes.find(item => item.name === library)?.sourceCode ?? response.function?.inlineSource ?? '')
      } else setSource(response.function?.inlineSource ?? '')
    } catch (reason) { setError(debugErrorMessage(reason)); setSession(null) }
    finally { setBusy(false) }
  }, [flowName, raw])
  useEffect(() => { if (step) void record(step, []) }, []) // eslint-disable-line react-hooks/exhaustive-deps

  const fn = session?.function
  return <Drawer title={`Debug captured frame · ${flowName}`} subtitle={<Stack gap={1} alignItems="center">
    <Badge color="blue" icon="info-circle" text="Simulated — nothing was sent" />
    <Text color="secondary" variant="bodySmall">The frame runs through this flow's current definition up to the selected Python step.</Text>
  </Stack>} size="lg" onClose={onClose}>
    <Stack direction="column" gap={2}>
      <Stack gap={1} alignItems="flex-end" wrap="wrap">
        <Field label="Python step" noMargin>
          <Combobox width={44} value={step} options={options} onChange={option => { if (!option) return; setStep(option.value); void record(option.value, watches) }} />
        </Field>
        <Button icon={busy ? 'spinner' : 'bug'} disabled={busy || !step} onClick={() => void record(step, watches)}>{busy ? 'Recording...' : 'Debug step'}</Button>
        {fn && <Text color="secondary" variant="bodySmall">{fn.library}.{fn.function}</Text>}
      </Stack>
      <PanelChrome title="Captured frame" collapsible collapsed>
        <pre style={{ margin: 0, whiteSpace: 'pre-wrap', wordBreak: 'break-word' }}>{raw || '(empty frame)'}</pre>
      </PanelChrome>
      {error && <Alert severity="error" title="Debug session failed" bottomSpacing={0}>{error}</Alert>}
      {busy && !session && <PageLoading text="Simulating and recording..." />}
      {session?.notReached && <Alert severity="warning" title="The frame never reached this step" bottomSpacing={0}>{session.notReached}</Alert>}
      {session?.simulation && <PanelChrome title="Simulation up to this step" collapsible>
        <MockTraceView result={session.simulation} />
      </PanelChrome>}
      {session?.recording && <PythonDebugger recording={session.recording} source={source} breakpointKey={fn?.library || flowName}
        input={{ raw: session.inputRaw, fields: session.inputFields, meta: session.inputMeta }}
        watches={watches} onWatchesChange={setWatches} onRerecord={() => void record(step, watches)} rerecording={busy} title={`Debugger for ${flowName} ${step}`} />}
    </Stack>
  </Drawer>
}
