import { useState } from 'react'
import { PanelHeader, Alert, Input, Select } from '@cnak-us/design-system'
import type { ListenerDefinition } from '../types'
import { createListener } from '../api'

interface Props {
  onClose: () => void
  onCreated: () => void
}

const FRAMING_OPTIONS = [
  { value: 'line', label: 'Line (\\n)' },
  { value: 'delimited', label: 'Delimiter' },
  { value: 'length-prefix', label: 'Length-prefix' },
  { value: 'raw', label: 'Raw (whole connection)' },
]

const PARSER_OPTIONS = [
  { value: 'json', label: 'JSON' },
  { value: 'xml', label: 'XML' },
  { value: 'none', label: 'None (passthrough)' },
]

const SINK_OPTIONS = [
  { value: 'stdout', label: 'Stdout (debug)' },
  { value: 'nats', label: 'NATS' },
  { value: 'http', label: 'HTTP POST' },
]

export function CreateListenerModal({ onClose, onCreated }: Props) {
  const [name, setName] = useState('')
  const [port, setPort] = useState('')
  const [network, setNetwork] = useState('tcp')
  const [framingMode, setFramingMode] = useState('line')
  const [delimiter, setDelimiter] = useState('')
  const [parserFormat, setParserFormat] = useState('json')
  const [filterExpr, setFilterExpr] = useState('')
  const [sinkType, setSinkType] = useState('stdout')
  const [sinkURL, setSinkURL] = useState('')
  const [sinkSubject, setSinkSubject] = useState('')
  const [error, setError] = useState<string | null>(null)
  const [saving, setSaving] = useState(false)

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault()
    setError(null)
    setSaving(true)
    try {
      const def: ListenerDefinition = {
        name,
        source: { port: parseInt(port, 10), network },
        framing: {
          mode: framingMode,
          ...(framingMode === 'delimited' && delimiter ? { delimiter } : {}),
        },
        parser: { format: parserFormat },
        ...(filterExpr.trim() ? { filter: { expression: filterExpr.trim() } } : {}),
        sink: {
          type: sinkType,
          ...(sinkURL ? { url: sinkURL } : {}),
          ...(sinkSubject ? { subject: sinkSubject } : {}),
        },
      }
      await createListener(def)
      onCreated()
    } catch (e) {
      setError(String(e))
    } finally {
      setSaving(false)
    }
  }

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/50">
      <div className="bg-g-primary border border-g-border-weak rounded-lg shadow-2xl w-full max-w-lg">
        <PanelHeader title="New Listener" onClose={onClose} />
        <form onSubmit={handleSubmit} className="p-5 space-y-4">
          {error && <Alert severity="error">{error}</Alert>}

          <Row label="Name">
            <Input value={name} onChange={(e) => setName(e.target.value)} placeholder="my-listener" required />
          </Row>

          <div className="grid grid-cols-2 gap-3">
            <Row label="Port">
              <Input type="number" value={port} onChange={(e) => setPort(e.target.value)} placeholder="9000" required />
            </Row>
            <Row label="Network">
              <Select
                value={network}
                onChange={setNetwork}
                options={[{ value: 'tcp', label: 'TCP' }, { value: 'udp', label: 'UDP' }]}
              />
            </Row>
          </div>

          <Row label="Framing">
            <Select
              value={framingMode}
              onChange={setFramingMode}
              options={FRAMING_OPTIONS}
            />
          </Row>

          {framingMode === 'delimited' && (
            <Row label="Delimiter">
              <Input value={delimiter} onChange={(e) => setDelimiter(e.target.value)} placeholder="</event>" />
            </Row>
          )}

          <Row label="Parser">
            <Select
              value={parserFormat}
              onChange={setParserFormat}
              options={PARSER_OPTIONS}
            />
          </Row>

          <Row label="CEL Filter (optional)">
            <Input
              value={filterExpr}
              onChange={(e) => setFilterExpr(e.target.value)}
              placeholder='fields["type"] == "a-f-G"'
            />
          </Row>

          <Row label="Sink">
            <Select
              value={sinkType}
              onChange={setSinkType}
              options={SINK_OPTIONS}
            />
          </Row>

          {(sinkType === 'nats' || sinkType === 'http') && (
            <Row label="URL">
              <Input
                value={sinkURL}
                onChange={(e) => setSinkURL(e.target.value)}
                placeholder={sinkType === 'nats' ? 'nats://localhost:4222' : 'http://localhost:8080'}
              />
            </Row>
          )}

          {sinkType === 'nats' && (
            <Row label="Subject">
              <Input
                value={sinkSubject}
                onChange={(e) => setSinkSubject(e.target.value)}
                placeholder="ingress.events"
              />
            </Row>
          )}

          <div className="flex justify-end gap-2 pt-2">
            <button
              type="button"
              onClick={onClose}
              className="px-4 py-2 rounded-md text-sm text-g-text-secondary border border-g-border-weak hover:bg-g-hover transition-colors"
            >
              Cancel
            </button>
            <button
              type="submit"
              disabled={saving}
              className="px-4 py-2 rounded-md text-sm font-medium bg-g-accent-main text-white hover:bg-g-accent-main/90 disabled:opacity-50 transition-colors"
            >
              {saving ? 'Creating…' : 'Create'}
            </button>
          </div>
        </form>
      </div>
    </div>
  )
}

function Row({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="space-y-1">
      <label className="text-xs font-medium text-g-text-secondary uppercase tracking-wide">{label}</label>
      {children}
    </div>
  )
}
