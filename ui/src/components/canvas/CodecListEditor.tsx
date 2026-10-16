import { css } from '@emotion/css'
import type { GrafanaTheme2 } from '@grafana/data'
import { Combobox, Field, IconButton, Text, useStyles2 } from '@grafana/ui'
import type { CodecStep } from '../../types'
import { codecChain, codecLabel } from '../../lib/codec'
import { useLibraryFunctions } from './python-functions'

const BUILTIN_DESCRIPTIONS: Record<'decoder' | 'encoder', Record<string, string>> = {
  decoder: {
    base64: 'Standard Base64 (RFC 4648) to bytes', base64url: 'URL-safe Base64 to bytes', hex: 'Hexadecimal text to bytes',
    gzip: 'Decompress gzip', zlib: 'Decompress zlib/deflate', url: 'URL percent-decoding',
  },
  encoder: {
    base64: 'Bytes to standard Base64 (RFC 4648)', base64url: 'Bytes to URL-safe Base64', hex: 'Bytes to hexadecimal text',
    gzip: 'Compress with gzip', zlib: 'Compress with zlib/deflate', url: 'URL percent-encoding',
  },
}

/** Ordered decode or encode steps: built-in codecs and Python functions of the
 * matching kind (@gofi.decoder / @gofi.encoder), reorderable and removable. */
export function CodecListEditor({ role, steps, onChange, readOnly, emptyText }: { role: 'decoder' | 'encoder'; steps: CodecStep[]; onChange: (steps: CodecStep[]) => void; readOnly?: boolean; emptyText: string }) {
  const styles = useStyles2(getStyles)
  const { functions } = useLibraryFunctions()
  const python = functions.filter((fn) => fn.kind === role)
  const descriptions = BUILTIN_DESCRIPTIONS[role]
  const options = [
    ...Object.entries(descriptions).map(([value, description]) => ({ value, label: value, description, group: 'Built-in' })),
    ...python.map((fn) => ({ value: `python:${fn.library}:${fn.name}`, label: `${fn.library}.${fn.name}`, description: fn.description || `Python ${role}`, group: 'Python' })),
  ]
  const add = (value: string) => {
    if (!value.startsWith('python:')) return onChange([...steps, { type: value }])
    const [, library, fn] = value.split(':')
    onChange([...steps, { type: 'python', python: { library, function: fn } }])
  }
  const move = (from: number, to: number) => { const next = [...steps]; const [item] = next.splice(from, 1); next.splice(to, 0, item); onChange(next) }
  const describe = (step: CodecStep) => step.type === 'python' ? (python.find((fn) => fn.library === step.python?.library && fn.name === step.python?.function)?.description || `Python ${role}`) : descriptions[step.type] ?? `Unknown ${role}`
  return <>
    <Field label={`${role === 'decoder' ? 'Decoders' : 'Encoders'}, in order`} description={steps.length > 1 ? codecChain(steps) : undefined}>
      <div className={styles.list}>
        {steps.length === 0 && <Text variant="bodySmall" color="secondary">{emptyText}</Text>}
        {steps.map((step, index) => <div key={`${codecLabel(step)}-${index}`} className={styles.row}>
          <span className={styles.index}>{index + 1}</span>
          <span className={styles.name}>{codecLabel(step)}</span>
          <Text variant="bodySmall" color="secondary" truncate>{describe(step)}</Text>
          {!readOnly && <span className={styles.actions}>
            <IconButton name="arrow-up" aria-label={`Move ${codecLabel(step)} up`} disabled={index === 0} onClick={() => move(index, index - 1)} />
            <IconButton name="arrow-down" aria-label={`Move ${codecLabel(step)} down`} disabled={index === steps.length - 1} onClick={() => move(index, index + 1)} />
            <IconButton name="trash-alt" aria-label={`Remove ${codecLabel(step)}`} onClick={() => onChange(steps.filter((_, i) => i !== index))} />
          </span>}
        </div>)}
      </div>
    </Field>
    {!readOnly && <Field label={`Add ${role}`} description={python.length ? undefined : `Python ${role}s appear here once a library registering @gofi.${role} is launched.`}>
      <Combobox key={steps.map(codecLabel).join(',')} placeholder={`Choose ${role === 'encoder' ? 'an' : 'a'} ${role}`} value={null} options={options} onChange={(option) => option && add(option.value)} />
    </Field>}
  </>
}

const getStyles = (theme: GrafanaTheme2) => ({
  list: css({ display: 'flex', flexDirection: 'column', gap: theme.spacing(0.5), width: '100%' }),
  row: css({ display: 'grid', gridTemplateColumns: 'auto auto minmax(0, 1fr) auto', alignItems: 'center', gap: theme.spacing(1), minHeight: theme.spacing(theme.components.height.md), padding: theme.spacing(0, 1), border: `1px solid ${theme.colors.border.weak}`, borderRadius: theme.shape.radius.default, background: theme.colors.background.secondary }),
  index: css({ color: theme.colors.text.secondary, fontVariantNumeric: 'tabular-nums', minWidth: theme.spacing(2) }),
  name: css({ fontFamily: theme.typography.fontFamilyMonospace, fontWeight: theme.typography.fontWeightMedium }),
  actions: css({ display: 'flex', gap: theme.spacing(0.5) }),
})
