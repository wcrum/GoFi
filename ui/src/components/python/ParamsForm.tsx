import { useEffect, useState } from 'react'
import { css } from '@emotion/css'
import type { GrafanaTheme2 } from '@grafana/data'
import { Field, Input, Switch, Text, TextArea, useStyles2 } from '@grafana/ui'
import type { FunctionParam } from '../../sandbox-api'
import { coerceParam } from '../canvas/python-templates'

/** Typed inputs for a function's keyword parameters (per-node configuration).
 * Values are emitted only when they parse for the declared type. */
export function ParamsForm({ params, value, onChange, disabled }: { params: FunctionParam[]; value: Record<string, unknown>; onChange: (config: Record<string, unknown>) => void; disabled?: boolean }) {
  const styles = useStyles2(getStyles)
  if (!params.length) return <Text color="secondary" variant="bodySmall">This function declares no configurable parameters. Add keyword-only parameters (after <Text variant="code">*</Text>) to make it configurable per node.</Text>
  return <div className={styles.grid}>
    {params.map(param => <ParamInput key={param.name} param={param} value={value[param.name]} disabled={disabled}
      onChange={next => { const config = { ...value }; if (next === undefined) delete config[param.name]; else config[param.name] = next; onChange(config) }} />)}
  </div>
}

function ParamInput({ param, value, onChange, disabled }: { param: FunctionParam; value: unknown; onChange: (value: unknown) => void; disabled?: boolean }) {
  const structured = param.type === 'list' || param.type === 'dict' || param.type === 'any'
  const display = (current: unknown) => current === undefined ? '' : structured ? JSON.stringify(current) : String(current)
  const [text, setText] = useState(display(value ?? param.default))
  const [error, setError] = useState('')
  useEffect(() => { setText(display(value ?? param.default)); setError('') }, [value, param.default]) // eslint-disable-line react-hooks/exhaustive-deps
  const label = `${param.name}${param.required ? ' *' : ''}`
  const description = [param.type && param.type !== 'any' ? param.type : '', param.default !== undefined && param.default !== null ? `default ${JSON.stringify(param.default)}` : '', param.description ?? ''].filter(Boolean).join(' · ')
  if (param.type === 'bool') return <Field label={label} description={description} noMargin>
    <Switch value={Boolean(value ?? param.default)} disabled={disabled} onChange={event => onChange(event.currentTarget.checked)} />
  </Field>
  const commit = (next: string) => {
    setText(next)
    if (next === '' && !param.required) { setError(''); onChange(undefined); return }
    try { onChange(coerceParam(param.type, next)); setError('') } catch (reason) { setError(reason instanceof Error ? reason.message : String(reason)) }
  }
  return <Field label={label} description={description} invalid={!!error} error={error} noMargin>
    {structured ? <TextArea rows={2} value={text} disabled={disabled} aria-label={param.name} onChange={event => commit(event.currentTarget.value)} />
      : <Input value={text} disabled={disabled} aria-label={param.name} type={param.type === 'int' || param.type === 'float' ? 'number' : 'text'} step={param.type === 'float' ? 'any' : undefined} onChange={event => commit(event.currentTarget.value)} />}
  </Field>
}

const getStyles = (theme: GrafanaTheme2) => ({
  grid: css({ display: 'grid', gap: theme.spacing(2), gridTemplateColumns: 'repeat(auto-fill, minmax(200px, 1fr))' }),
})
