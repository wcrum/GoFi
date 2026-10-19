import { css } from '@emotion/css'
import type { GrafanaTheme2 } from '@grafana/data'
import { Combobox, Field, IconButton, Text, useStyles2 } from '@grafana/ui'
import type { CodecStep } from '../../types'
import { codecChain, codecLabelWith } from '../../lib/codec'
import { refLibrary, storedRef } from '../../lib/python-ref'
import { functionLabel, useLibraryFunctions, usePythonDefault } from './python-functions'
import { t } from '../../i18n'

type CodecRole = 'decoder' | 'encoder'

// Built-in codecs (spec.BuiltinCodecs), by role. bzip2 decodes only.
// Resolved at render so the descriptions follow the active language.
const builtinDescriptions = (role: CodecRole): Record<string, string> => role === 'decoder' ? {
  base64: t('gofi.canvas.codec-list-editor.decode-base64', 'Standard Base64 (RFC 4648) to bytes'), base64url: t('gofi.canvas.codec-list-editor.decode-base64url', 'URL-safe Base64 to bytes'), base32: t('gofi.canvas.codec-list-editor.decode-base32', 'Base32 (RFC 4648) to bytes'), hex: t('gofi.canvas.codec-list-editor.decode-hex', 'Hexadecimal text to bytes'), url: t('gofi.canvas.codec-list-editor.decode-url', 'URL percent-decoding'),
  gzip: t('gofi.canvas.codec-list-editor.decode-gzip', 'Decompress gzip'), zlib: t('gofi.canvas.codec-list-editor.decode-zlib', 'Decompress zlib (deflate with a zlib header)'), deflate: t('gofi.canvas.codec-list-editor.decode-deflate', 'Decompress raw deflate'), zstd: t('gofi.canvas.codec-list-editor.decode-zstd', 'Decompress Zstandard'),
  snappy: t('gofi.canvas.codec-list-editor.decode-snappy', 'Decompress Snappy (block format, as in Prometheus remote write)'), lz4: t('gofi.canvas.codec-list-editor.decode-lz4', 'Decompress LZ4 (frame format)'), brotli: t('gofi.canvas.codec-list-editor.decode-brotli', 'Decompress Brotli'), bzip2: t('gofi.canvas.codec-list-editor.decode-bzip2', 'Decompress bzip2'),
} : {
  base64: t('gofi.canvas.codec-list-editor.encode-base64', 'Bytes to standard Base64 (RFC 4648)'), base64url: t('gofi.canvas.codec-list-editor.encode-base64url', 'Bytes to URL-safe Base64'), base32: t('gofi.canvas.codec-list-editor.encode-base32', 'Bytes to Base32 (RFC 4648)'), hex: t('gofi.canvas.codec-list-editor.encode-hex', 'Bytes to hexadecimal text'), url: t('gofi.canvas.codec-list-editor.encode-url', 'URL percent-encoding'),
  gzip: t('gofi.canvas.codec-list-editor.encode-gzip', 'Compress with gzip'), zlib: t('gofi.canvas.codec-list-editor.encode-zlib', 'Compress with zlib (deflate with a zlib header)'), deflate: t('gofi.canvas.codec-list-editor.encode-deflate', 'Compress with raw deflate'), zstd: t('gofi.canvas.codec-list-editor.encode-zstd', 'Compress with Zstandard'),
  snappy: t('gofi.canvas.codec-list-editor.encode-snappy', 'Compress with Snappy (block format)'), lz4: t('gofi.canvas.codec-list-editor.encode-lz4', 'Compress with LZ4 (frame format)'), brotli: t('gofi.canvas.codec-list-editor.encode-brotli', 'Compress with Brotli'),
}

// The role is part of the sentence in English, so each phrase has one string per role.
const roleText = (role: CodecRole) => role === 'decoder' ? {
  listLabel: t('gofi.canvas.codec-list-editor.decoders-in-order', 'Decoders, in order'),
  python: t('gofi.canvas.codec-list-editor.python-decoder', 'Python decoder'),
  unknown: t('gofi.canvas.codec-list-editor.unknown-decoder', 'Unknown decoder'),
  add: t('gofi.canvas.codec-list-editor.add-decoder', 'Add decoder'),
  pythonHint: t('gofi.canvas.codec-list-editor.python-decoders-hint', 'Python decoders appear here once a library registering @gofi.decoder is launched.'),
  choose: t('gofi.canvas.codec-list-editor.choose-decoder', 'Choose a decoder'),
} : {
  listLabel: t('gofi.canvas.codec-list-editor.encoders-in-order', 'Encoders, in order'),
  python: t('gofi.canvas.codec-list-editor.python-encoder', 'Python encoder'),
  unknown: t('gofi.canvas.codec-list-editor.unknown-encoder', 'Unknown encoder'),
  add: t('gofi.canvas.codec-list-editor.add-encoder', 'Add encoder'),
  pythonHint: t('gofi.canvas.codec-list-editor.python-encoders-hint', 'Python encoders appear here once a library registering @gofi.encoder is launched.'),
  choose: t('gofi.canvas.codec-list-editor.choose-encoder', 'Choose an encoder'),
}

/** Ordered decode or encode steps: built-in codecs and Python functions of the
 * matching kind (@gofi.decoder / @gofi.encoder), reorderable and removable. */
export function CodecListEditor({ role, steps, onChange, readOnly, emptyText }: { role: CodecRole; steps: CodecStep[]; onChange: (steps: CodecStep[]) => void; readOnly?: boolean; emptyText: string }) {
  const styles = useStyles2(getStyles)
  const { functions } = useLibraryFunctions()
  const defaultLibrary = usePythonDefault()
  const codecLabel = (step: CodecStep) => codecLabelWith(step, defaultLibrary)
  const python = functions.filter((fn) => fn.kind === role)
  const descriptions = builtinDescriptions(role)
  const text = roleText(role)
  const options = [
    ...Object.entries(descriptions).map(([value, description]) => ({ value, label: value, description, group: t('gofi.canvas.codec-list-editor.group-builtin', 'Built-in') })),
    ...python.map((fn) => ({ value: `python:${fn.library}:${fn.name}`, label: functionLabel(fn.library, fn.name, defaultLibrary), description: fn.description || text.python, group: 'Python' })),
  ]
  const add = (value: string) => {
    if (!value.startsWith('python:')) return onChange([...steps, { type: value }])
    const [, library, fn] = value.split(':')
    onChange([...steps, { type: 'python', python: storedRef({ library, function: fn }, defaultLibrary) }])
  }
  const move = (from: number, to: number) => { const next = [...steps]; const [item] = next.splice(from, 1); next.splice(to, 0, item); onChange(next) }
  const describe = (step: CodecStep) => step.type === 'python' ? (python.find((fn) => fn.library === refLibrary(step.python, defaultLibrary) && fn.name === step.python?.function)?.description || text.python) : descriptions[step.type] ?? text.unknown
  return <>
    <Field label={text.listLabel} description={steps.length > 1 ? codecChain(steps, defaultLibrary) : undefined}>
      <div className={styles.list}>
        {steps.length === 0 && <Text variant="bodySmall" color="secondary">{emptyText}</Text>}
        {steps.map((step, index) => <div key={`${codecLabel(step)}-${index}`} className={styles.row}>
          <span className={styles.index}>{index + 1}</span>
          <span className={styles.name}>{codecLabel(step)}</span>
          <Text variant="bodySmall" color="secondary" truncate>{describe(step)}</Text>
          {!readOnly && <span className={styles.actions}>
            <IconButton name="arrow-up" aria-label={t('gofi.canvas.codec-list-editor.move-up', 'Move {{name}} up', { name: codecLabel(step) })} disabled={index === 0} onClick={() => move(index, index - 1)} />
            <IconButton name="arrow-down" aria-label={t('gofi.canvas.codec-list-editor.move-down', 'Move {{name}} down', { name: codecLabel(step) })} disabled={index === steps.length - 1} onClick={() => move(index, index + 1)} />
            <IconButton name="trash-alt" aria-label={t('gofi.canvas.codec-list-editor.remove', 'Remove {{name}}', { name: codecLabel(step) })} onClick={() => onChange(steps.filter((_, i) => i !== index))} />
          </span>}
        </div>)}
      </div>
    </Field>
    {!readOnly && <Field label={text.add} description={python.length ? undefined : text.pythonHint}>
      <Combobox key={steps.map(codecLabel).join(',')} placeholder={text.choose} value={null} options={options} onChange={(option) => option && add(option.value)} />
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
