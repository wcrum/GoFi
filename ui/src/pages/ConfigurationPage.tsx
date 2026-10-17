import { useState } from 'react'
import { css } from '@emotion/css'
import type { GrafanaTheme2 } from '@grafana/data'
import '../lib/monaco'
import { Alert, Button, Card, CodeEditor, Combobox, Field, Stack, Text, TextLink, useStyles2 } from '@grafana/ui'
import { EmptyState } from '../components/EmptyState'
import { configurationSourceLabel, downloadYAML, flowResource, manifestHeight, toYAML } from '../lib/resource'
import { paths, to } from '../router'
import { Page } from '../shell/Page'
import { useGoFi } from '../state'

export default function ConfigurationPage() {
  const styles = useStyles2(getStyles)
  const { flows, configuration } = useGoFi()
  const [selected, setSelected] = useState('')
  const definition = flows.find(flow => flow.name === selected) ?? flows[0]
  const durable = configuration?.source === 'kubernetes' || configuration?.source === 'file'
  const exportAll = () => downloadYAML('gofi-flows.yaml', flows.map(flow => flowResource(flow, configuration)))
  const manifest = definition ? toYAML(flowResource(definition, configuration)) : ''
  return <Page title="Configuration" icon="cog" subTitle="Keep flow definitions in version control and apply GoFi resources through your deployment pipeline."
    actions={<Button icon="download-alt" variant="secondary" disabled={!flows.length} onClick={exportAll}>Export resources</Button>}>
    <Stack direction="column" gap={2}>
      <Card noMargin>
        <Card.Heading>Configuration source</Card.Heading>
        <Card.Meta>{[configurationSourceLabel(configuration), configuration?.apiVersion || 'gofi.wcrum.dev/v1alpha1', ...(configuration?.namespace ? [`namespace ${configuration.namespace}`] : [])]}</Card.Meta>
        <Card.Description>
          {configuration?.source === 'kubernetes' ? 'The control plane reconciles gofi.wcrum.dev Flow resources into running flows. Commit exported resources and use GitOps to enforce your application configuration.'
            : configuration?.source === 'file' ? 'Flow resources persist across restarts in the configured file. Keep that file in version control and update it declaratively to reconcile your running flows.'
            : 'This control plane has no durable resource store configured.'}
          {configuration?.path && <span className={styles.path}><Text variant="code">{configuration.path}</Text></span>}
        </Card.Description>
      </Card>
      {!durable && configuration && <Alert severity="warning" title="Not durable">Flow changes are lost when the process restarts.</Alert>}
      {flows.length ? <>
        <Field label="Flow" noMargin className={styles.picker}>
          <Combobox value={definition?.name ?? ''} options={flows.map(flow => ({ value: flow.name, label: flow.name }))} onChange={option => setSelected(option.value)} />
        </Field>
        <div className={styles.editor}><CodeEditor language="yaml" readOnly showLineNumbers showMiniMap={false} height={manifestHeight(manifest)} width="100%" value={manifest} monacoOptions={{ scrollBeyondLastLine: false }} /></div>
        {configuration?.source === 'kubernetes' && <Text color="secondary" variant="bodySmall">Apply the export with <Text variant="code">kubectl apply -f gofi-flows.yaml</Text>. The file is a multi-document YAML stream with one Flow per document. Review credentials before committing exported definitions.</Text>}
        {configuration?.source === 'file' && <Text color="secondary" variant="bodySmall">Save the exported Flow resources to the configured resource file to reconcile them in binary or container mode.</Text>}
      </> : <EmptyState variant="call-to-action" message="No flows to export"><TextLink href={to(paths.newFlow())} inline={false}>Create a flow</TextLink> to preview its declarative resource.</EmptyState>}
    </Stack>
  </Page>
}
const getStyles = (theme: GrafanaTheme2) => ({
  path: css({ display: 'block', marginTop: theme.spacing(1), wordBreak: 'break-all' }),
  picker: css({ maxWidth: 360 }),
  editor: css({ border: `1px solid ${theme.colors.border.weak}`, borderRadius: theme.shape.radius.default, overflow: 'hidden' }),
})
