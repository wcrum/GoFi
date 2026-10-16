import { Field, RadioButtonGroup } from '@grafana/ui'

/** What a CEL stage does when evaluation fails (missing key, wrong type,
 * cost or deadline exceeded): let the message continue, or drop it. */
export function OnErrorControl({ value, onChange, disabled }: { value?: 'pass' | 'drop'; onChange: (value: 'pass' | 'drop' | undefined) => void; disabled?: boolean }) {
  return <Field label="On evaluation error" description={value === 'drop' ? 'Errors drop the message (and send it to the dead-letter sink, if configured).' : 'Errors let the message continue unchanged (fail open).'} noMargin>
    <RadioButtonGroup size="sm" disabled={disabled} value={value ?? 'pass'} options={[{ value: 'pass', label: 'Pass' }, { value: 'drop', label: 'Drop' }]}
      onChange={next => onChange(next === 'drop' ? 'drop' : undefined)} />
  </Field>
}
