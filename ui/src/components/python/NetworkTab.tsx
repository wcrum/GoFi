import { useState } from 'react'
import { css } from '@emotion/css'
import type { GrafanaTheme2 } from '@grafana/data'
import { Alert, Button, Card, Combobox, Field, IconButton, Input, RadioButtonGroup, Stack, useStyles2 } from '@grafana/ui'
import { EmptyState } from '../EmptyState'
import type { EgressRule } from '../../sandbox-api'
import { t, Trans } from '../../i18n'

/** Outbound destinations a library worker may reach. Empty = no egress. */
export function NetworkTab({ rules, onChange, readOnly }: { rules: EgressRule[]; onChange: (rules: EgressRule[]) => void; readOnly?: boolean }) {
  const update = (index: number, rule: EgressRule) => onChange(rules.map((item, position) => position === index ? rule : item))
  const add = () => onChange([...rules, { cidr: '', port: 4222, protocol: 'tcp', description: '' }])
  return <Stack direction="column" gap={2}>
    <Alert severity={rules.length ? 'warning' : 'info'} title={rules.length ? t('gofi.python.network.rule-count', '{{count}} outbound rules', { count: rules.length }) : t('gofi.python.network.no-outbound', 'No outbound network')} bottomSpacing={0}>
      {rules.length
        ? <Trans i18nKey="gofi.python.network.rules-note">Kubernetes enforces each rule exactly with a NetworkPolicy (CIDR, or namespace + pod selector) and allows cluster DNS automatically. <b>Docker cannot filter by destination:</b> any rule attaches the worker to an egress-capable network, so it can reach every destination the host can.</Trans>
        : t('gofi.python.network.isolated-note', "Workers are isolated: functions compute and print, and GoFi's built-in sinks (NATS, HTTP, TCP/UDP, Loki, Zenoh, Aster) deliver results. Add a rule only when Python code must open its own connection.")}
    </Alert>
    {rules.map((rule, index) => <EgressRuleCard key={index} rule={rule} readOnly={readOnly} onChange={next => update(index, next)} onDelete={() => onChange(rules.filter((_, position) => position !== index))} />)}
    {!rules.length && <EmptyState variant="completed" message={t('gofi.python.network.empty', 'This library has no outbound network access')} hideImage />}
    {!readOnly && <div><Button icon="plus" variant="secondary" onClick={add}>{t('gofi.python.network.add-rule', 'Add egress rule')}</Button></div>}
    {!readOnly && rules.length > 0 && <Alert severity="info" title={t('gofi.python.network.applies-title', 'Applies on Save and launch')} bottomSpacing={0}>{t('gofi.python.network.applies-body', 'Egress rules take effect when the worker is relaunched.')}</Alert>}
  </Stack>
}

function EgressRuleCard({ rule, onChange, onDelete, readOnly }: { rule: EgressRule; onChange: (rule: EgressRule) => void; onDelete: () => void; readOnly?: boolean }) {
  const styles = useStyles2(getStyles)
  // Empty strings are omitted by protobuf JSON, so the chosen mode is local state.
  const [target, setTarget] = useState<'cidr' | 'cluster'>(rule.namespace || Object.keys(rule.podSelector ?? {}).length ? 'cluster' : 'cidr')
  const selector = Object.entries(rule.podSelector ?? {}).map(([key, value]) => `${key}=${value}`).join(', ')
  const anyPort = t('gofi.python.network.any-port', 'any')
  const heading = rule.description || (target === 'cidr' ? `${rule.cidr || t('gofi.python.network.cidr', 'CIDR')}:${rule.port || anyPort}` : `${rule.namespace || t('gofi.python.network.namespace-placeholder', 'namespace')}:${rule.port || anyPort}`)
  return <Card noMargin isCompact>
    <Card.Heading>{heading}</Card.Heading>
    <Card.Description>
      <div className={styles.grid}>
        <Field label={t('gofi.python.network.destination', 'Destination')} noMargin>
          <RadioButtonGroup size="sm" disabled={readOnly} value={target} options={[{ value: 'cidr', label: t('gofi.python.network.target-cidr', 'IP / CIDR') }, { value: 'cluster', label: t('gofi.python.network.target-cluster', 'In-cluster pods') }]}
            onChange={(value: 'cidr' | 'cluster') => { setTarget(value); onChange(value === 'cidr' ? { port: rule.port, protocol: rule.protocol, description: rule.description, cidr: '' } : { port: rule.port, protocol: rule.protocol, description: rule.description, namespace: '', podSelector: {} }) }} />
        </Field>
        {target === 'cidr' ? <Field label={t('gofi.python.network.cidr', 'CIDR')} description={t('gofi.python.network.cidr-description', 'e.g. 10.0.12.5/32')} noMargin><Input value={rule.cidr ?? ''} disabled={readOnly} placeholder="10.0.0.0/24" onChange={event => onChange({ ...rule, cidr: event.currentTarget.value })} /></Field> : <>
          <Field label={t('gofi.common.namespace', 'Namespace')} noMargin><Input value={rule.namespace ?? ''} disabled={readOnly} placeholder={'nats' /* i18n-ignore: example identifier */} onChange={event => onChange({ ...rule, namespace: event.currentTarget.value })} /></Field>
          <Field label={t('gofi.python.network.pod-selector', 'Pod selector')} description={t('gofi.python.network.pod-selector-description', 'key=value, comma separated')} noMargin><Input value={selector} disabled={readOnly} placeholder="app=nats" onChange={event => onChange({ ...rule, podSelector: Object.fromEntries(event.currentTarget.value.split(',').map(part => part.trim()).filter(Boolean).map(part => { const [key, ...value] = part.split('='); return [key.trim(), value.join('=').trim()] })) })} /></Field>
        </>}
        <Field label={t('gofi.common.port', 'Port')} noMargin><Input type="number" min={1} max={65535} value={rule.port ?? ''} disabled={readOnly} onChange={event => onChange({ ...rule, port: event.currentTarget.value ? Number(event.currentTarget.value) : undefined })} /></Field>
        <Field label={t('gofi.common.protocol', 'Protocol')} noMargin><Combobox value={rule.protocol || 'tcp'} disabled={readOnly} options={[{ value: 'tcp', label: 'TCP' }, { value: 'udp', label: 'UDP' }]} onChange={option => option && onChange({ ...rule, protocol: option.value })} /></Field>
        <Field label={t('gofi.common.description', 'Description')} noMargin><Input value={rule.description ?? ''} disabled={readOnly} placeholder={t('gofi.python.network.description-placeholder', 'NATS cluster')} onChange={event => onChange({ ...rule, description: event.currentTarget.value })} /></Field>
      </div>
    </Card.Description>
    {!readOnly && <Card.SecondaryActions><IconButton name="trash-alt" tooltip={t('gofi.python.network.remove-rule', 'Remove rule')} onClick={onDelete} /></Card.SecondaryActions>}
  </Card>
}

const getStyles = (theme: GrafanaTheme2) => ({
  grid: css({ display: 'grid', gap: theme.spacing(1.5), gridTemplateColumns: 'repeat(auto-fit, minmax(180px, 1fr))', marginTop: theme.spacing(1) }),
})
