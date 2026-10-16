import { useEffect, useState } from 'react'
import FlowCanvas from '../components/canvas/FlowCanvas'
import { clearFlowSeed, peekFlowSeed } from '../lib/flow-seed'
import { navigate, paths } from '../router'
import { Page } from '../shell/Page'
import { useGoFi } from '../state'
import type { FlowDefinition } from '../types'

export default function NewFlowPage() {
  const { reload, upsertLocal } = useGoFi()
  // A draft seeded elsewhere (e.g. "Create flow from hook"), used once.
  const [seed] = useState(peekFlowSeed)
  useEffect(clearFlowSeed, [])
  const saved = (definition: FlowDefinition) => { upsertLocal(definition); navigate(paths.flow(definition.name)); void reload() }
  return <Page title="New flow" icon="exchange-alt" subTitle="Receive TCP or UDP traffic, decode frames, filter with expressions, and route to your applications." layout="canvas">
    <FlowCanvas mode="create" initial={seed} onSaved={saved} onClose={() => navigate(paths.flows())} closeLabel="Cancel" />
  </Page>
}
