import { useState } from 'react'
import { css } from '@emotion/css'
import type { GrafanaTheme2 } from '@grafana/data'
import { Alert, Button, Card, Combobox, EmptyState, Field, IconButton, Input, RadioButtonGroup, Stack, useStyles2 } from '@grafana/ui'
import type { EgressRule } from '../../sandbox-api'

/** Outbound destinations a library worker may reach. Empty = no egress. */
export function NetworkTab({ rules, onChange, readOnly }: { rules: EgressRule[]; onChange: (rules: EgressRule[]) => void; readOnly?: boolean }) {
  const update = (index: number, rule: EgressRule) => onChange(rules.map((item, position) => position === index ? rule : item))
  const add = () => onChange([...rules, { cidr: '', port: 4222, protocol: 'tcp', description: '' }])
  return <Stack direction="column" gap={2}>
    <Alert severity={rules.length ? 'warning' : 'info'} title={rules.length ? `${rules.length} outbound rule${rules.length === 1 ? '' : 's'}` : 'No outbound network'} bottomSpacing={0}>
      {rules.length
        ? <>Kubernetes enforces each rule exactly with a NetworkPolicy (CIDR, or namespace + pod selector) and allows cluster DNS automatically. <b>Docker cannot filter by destination:</b> any rule attaches the worker to an egress-capable network, so it can reach every destination the host can.</>
        : <>Workers are isolated: functions compute and print, and GoFi's built-in sinks (NATS, HTTP, TCP/UDP, Loki, Zenoh, Aster) deliver results. Add a rule only when Python code must open its own connection.</>}
    </Alert>
    {rules.map((rule, index) => <EgressRuleCard key={index} rule={rule} readOnly={readOnly} onChange={next => update(index, next)} onDelete={() => onChange(rules.filter((_, position) => position !== index))} />)}
    {!rules.length && <EmptyState variant="completed" message="This library has no outbound network access" hideImage />}
    {!readOnly && <div><Button icon="plus" variant="secondary" onClick={add}>Add egress rule</Button></div>}
    {!readOnly && rules.length > 0 && <Alert severity="info" title="Applies on Save and launch" bottomSpacing={0}>Egress rules take effect when the worker is relaunched.</Alert>}
  </Stack>
}

function EgressRuleCard({ rule, onChange, onDelete, readOnly }: { rule: EgressRule; onChange: (rule: EgressRule) => void; onDelete: () => void; readOnly?: boolean }) {
  const styles = useStyles2(getStyles)
  // Empty strings are omitted by protobuf JSON, so the chosen mode is local state.
  const [target, setTarget] = useState<'cidr' | 'cluster'>(rule.namespace || Object.keys(rule.podSelector ?? {}).length ? 'cluster' : 'cidr')
  const selector = Object.entries(rule.podSelector ?? {}).map(([key, value]) => `${key}=${value}`).join(', ')
  return <Card noMargin isCompact>
    <Card.Heading>{rule.description || (target === 'cidr' ? `${rule.cidr || 'CIDR'}:${rule.port || 'any'}` : `${rule.namespace || 'namespace'}:${rule.port || 'any'}`)}</Card.Heading>
    <Card.Description>
      <div className={styles.grid}>
        <Field label="Destination" noMargin>
          <RadioButtonGroup size="sm" disabled={readOnly} value={target} options={[{ value: 'cidr', label: 'IP / CIDR' }, { value: 'cluster', label: 'In-cluster pods' }]}
            onChange={(value: 'cidr' | 'cluster') => { setTarget(value); onChange(value === 'cidr' ? { port: rule.port, protocol: rule.protocol, description: rule.description, cidr: '' } : { port: rule.port, protocol: rule.protocol, description: rule.description, namespace: '', podSelector: {} }) }} />
        </Field>
        {target === 'cidr' ? <Field label="CIDR" description="e.g. 10.0.12.5/32" noMargin><Input value={rule.cidr ?? ''} disabled={readOnly} placeholder="10.0.0.0/24" onChange={event => onChange({ ...rule, cidr: event.currentTarget.value })} /></Field> : <>
          <Field label="Namespace" noMargin><Input value={rule.namespace ?? ''} disabled={readOnly} placeholder="nats" onChange={event => onChange({ ...rule, namespace: event.currentTarget.value })} /></Field>
          <Field label="Pod selector" description="key=value, comma separated" noMargin><Input value={selector} disabled={readOnly} placeholder="app=nats" onChange={event => onChange({ ...rule, podSelector: Object.fromEntries(event.currentTarget.value.split(',').map(part => part.trim()).filter(Boolean).map(part => { const [key, ...value] = part.split('='); return [key.trim(), value.join('=').trim()] })) })} /></Field>
        </>}
        <Field label="Port" noMargin><Input type="number" min={1} max={65535} value={rule.port ?? ''} disabled={readOnly} onChange={event => onChange({ ...rule, port: event.currentTarget.value ? Number(event.currentTarget.value) : undefined })} /></Field>
        <Field label="Protocol" noMargin><Combobox value={rule.protocol || 'tcp'} disabled={readOnly} options={[{ value: 'tcp', label: 'TCP' }, { value: 'udp', label: 'UDP' }]} onChange={option => option && onChange({ ...rule, protocol: option.value })} /></Field>
        <Field label="Description" noMargin><Input value={rule.description ?? ''} disabled={readOnly} placeholder="NATS cluster" onChange={event => onChange({ ...rule, description: event.currentTarget.value })} /></Field>
      </div>
    </Card.Description>
    {!readOnly && <Card.SecondaryActions><IconButton name="trash-alt" tooltip="Remove rule" onClick={onDelete} /></Card.SecondaryActions>}
  </Card>
}

const getStyles = (theme: GrafanaTheme2) => ({
  grid: css({ display: 'grid', gap: theme.spacing(1.5), gridTemplateColumns: 'repeat(auto-fit, minmax(180px, 1fr))', marginTop: theme.spacing(1) }),
})
