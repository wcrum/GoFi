import { useEffect, useState } from 'react'
import FlowCanvas from '../components/canvas/FlowCanvas'
import { FLOW_SEED_EVENT, clearFlowSeed, peekFlowSeed } from '../lib/flow-seed'
import { navigate, paths } from '../router'
import { Page } from '../shell/Page'
import { useGoFi } from '../state'
import type { FlowDefinition } from '../types'
import { t } from '../i18n'

export default function NewFlowPage() {
  const { reload, upsertLocal } = useGoFi()
  // A draft seeded elsewhere (e.g. "Create flow from hook" or the tutorial's
  // "Load example"), used once. A seed announced while this page is open
  // replaces the canvas (keyed by generation) with the new draft.
  const [seed, setSeed] = useState(peekFlowSeed)
  const [generation, setGeneration] = useState(0)
  useEffect(() => {
    clearFlowSeed()
    const onSeed = () => {
      const next = peekFlowSeed()
      if (!next) return
      clearFlowSeed()
      setSeed(next)
      setGeneration(value => value + 1)
    }
    window.addEventListener(FLOW_SEED_EVENT, onSeed)
    return () => window.removeEventListener(FLOW_SEED_EVENT, onSeed)
  }, [])
  const saved = (definition: FlowDefinition) => { upsertLocal(definition); navigate(paths.flow(definition.name)); void reload() }
  return <Page title={t('gofi.pages.new-flow.title', 'New flow')} icon="exchange-alt" subTitle={t('gofi.pages.new-flow.subtitle', 'Receive TCP or UDP traffic, decode frames, filter with expressions, and route to your applications.')} layout="canvas">
    <FlowCanvas key={generation} mode="create" initial={seed} onSaved={saved} onClose={() => navigate(paths.flows())} closeLabel={t('gofi.common.cancel', 'Cancel')} />
  </Page>
}
