import { useEffect, useRef, useState } from 'react'
import { Button, IconButton, Input, Stack, Text } from '@grafana/ui'
import { t } from '../../i18n'

interface KeyValueEditorProps {
  value: Record<string, string>
  onChange: (value: Record<string, string>) => void
  keyPlaceholder?: string
  valuePlaceholder?: string
  disabled?: boolean
}

interface Entry {
  id: string
  key: string
  value: string
}

let idCounter = 0
function makeId(): string {
  idCounter += 1
  return `kv-${idCounter}`
}

function entriesFromRecord(record: Record<string, string>): Entry[] {
  return Object.entries(record).map(([key, value]) => ({ id: makeId(), key, value }))
}

function recordFromEntries(entries: Entry[]): Record<string, string> {
  const record: Record<string, string> = {}
  for (const entry of entries) {
    const key = entry.key.trim()
    if (key === '') continue
    record[key] = entry.value
  }
  return record
}

function recordsEqual(a: Record<string, string>, b: Record<string, string>): boolean {
  const aKeys = Object.keys(a)
  const bKeys = Object.keys(b)
  if (aKeys.length !== bKeys.length) return false
  return aKeys.every((k) => a[k] === b[k])
}

// Small hand-rolled key/value list editor (headers, etc). Keeps its own list of
// entries with stable local ids so typing an empty or temporarily-duplicate key
// doesn't get clobbered by re-derivation from the (unordered, key-deduped)
// Record<string, string> passed in via props.
export function KeyValueEditor({ value, onChange, keyPlaceholder, valuePlaceholder, disabled }: KeyValueEditorProps) {
  const [entries, setEntries] = useState<Entry[]>(() => entriesFromRecord(value))
  const mounted = useRef(false)

  useEffect(() => {
    if (!mounted.current) {
      mounted.current = true
      return
    }
    // Resync from external changes (e.g. a different sink node was selected),
    // but skip it if the incoming value already matches what our local state
    // would produce, so we don't stomp on in-progress edits caused by our own
    // onChange call.
    if (!recordsEqual(recordFromEntries(entries), value)) {
      setEntries(entriesFromRecord(value))
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [value])

  const commit = (next: Entry[]) => {
    setEntries(next)
    onChange(recordFromEntries(next))
  }

  return (
    <Stack direction="column" gap={1}>
      {entries.map((entry) => (
        <Stack key={entry.id} gap={1} alignItems="center">
          <Input
            disabled={disabled}
            value={entry.key}
            aria-label={t('gofi.canvas.key-value-editor.key', 'Key')}
            onChange={(e) => commit(entries.map((en) => (en.id === entry.id ? { ...en, key: e.currentTarget.value } : en)))}
            placeholder={keyPlaceholder ?? t('gofi.canvas.key-value-editor.key', 'Key')}
          />
          <Input
            disabled={disabled}
            value={entry.value}
            aria-label={t('gofi.canvas.key-value-editor.value', 'Value')}
            onChange={(e) => commit(entries.map((en) => (en.id === entry.id ? { ...en, value: e.currentTarget.value } : en)))}
            placeholder={valuePlaceholder ?? t('gofi.canvas.key-value-editor.value', 'Value')}
          />
          {!disabled && <IconButton name="times" tooltip={t('gofi.canvas.key-value-editor.remove-entry', 'Remove entry')} onClick={() => commit(entries.filter((en) => en.id !== entry.id))} />}
        </Stack>
      ))}
      {entries.length === 0 && disabled && <Text color="disabled" variant="bodySmall">{t('gofi.common.none', 'None')}</Text>}
      {!disabled && (
        <div>
          <Button type="button" variant="secondary" size="sm" icon="plus" onClick={() => commit([...entries, { id: makeId(), key: '', value: '' }])}>
            {t('gofi.common.add', 'Add')}
          </Button>
        </div>
      )}
    </Stack>
  )
}
