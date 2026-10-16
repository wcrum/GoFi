import { css } from '@emotion/css'
import type { GrafanaTheme2 } from '@grafana/data'
import { Combobox, useStyles2 } from '@grafana/ui'
import { LogsExplorer } from '../components/logs/LogsExplorer'
import { navigate, paths } from '../router'
import { Page } from '../shell/Page'
import { useGoFi } from '../state'

export default function ExplorePage({ flow }: { flow: string }) {
  const styles = useStyles2(getStyles)
  const { flows } = useGoFi()
  const options = [{ value: '', label: 'All flows' }, ...flows.map(item => ({ value: item.name, label: item.name }))]
  // A flow named in the URL stays selectable before the inventory loads.
  if (flow && !flows.some(item => item.name === flow)) options.push({ value: flow, label: flow })
  return <Page title="Explore" icon="compass"
    subTitle="Inspect traffic observed by GoFi flows as it arrives. This is a bounded, best-effort live view, not durable history."
    actions={<Combobox aria-label="Flow" prefixIcon="exchange-alt" width={32} value={flow} options={options} onChange={option => navigate(paths.explore(option.value))} />}>
    <div className={styles.fill}><LogsExplorer key={flow} flow={flow} /></div>
  </Page>
}

const getStyles = (_theme: GrafanaTheme2) => ({
  fill: css({ height: '100%', minHeight: 0 }),
})
