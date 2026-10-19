import { Field, RadioButtonGroup } from '@grafana/ui'
import { t } from '../../i18n'

/** What a CEL stage does when evaluation fails (missing key, wrong type,
 * cost or deadline exceeded): let the message continue, or drop it. */
export function OnErrorControl({ value, onChange, disabled }: { value?: 'pass' | 'drop'; onChange: (value: 'pass' | 'drop' | undefined) => void; disabled?: boolean }) {
  return <Field label={t('gofi.cel.on-error.label', 'On evaluation error')} description={value === 'drop' ? t('gofi.cel.on-error.drop-description', 'Errors drop the message (and send it to the dead-letter sink, if configured).') : t('gofi.cel.on-error.pass-description', 'Errors let the message continue unchanged (fail open).')} noMargin>
    <RadioButtonGroup size="sm" disabled={disabled} value={value ?? 'pass'} options={[{ value: 'pass', label: t('gofi.cel.on-error.pass', 'Pass') }, { value: 'drop', label: t('gofi.cel.on-error.drop', 'Drop') }]}
      onChange={next => onChange(next === 'drop' ? 'drop' : undefined)} />
  </Field>
}
