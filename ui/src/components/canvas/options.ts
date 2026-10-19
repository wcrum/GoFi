// Select-option lists shared by the canvas NodeInspector. Kept in one place
// so source and sink forms always offer the same choices. Lists with
// translatable labels are functions so they resolve in the active language at
// render time; pure identifier lists (TCP/UDP, HTTP methods) stay constants.
import { t } from '../../i18n/index.ts'

/** Combobox option shape used by the inspector's fixed choice lists. */
export interface SelectOption { value: string; label: string }

/** Flow source types (docs/flows.md). */
export const sourceTypeOptions = (): Array<SelectOption & { description: string }> => [
  { value: 'listen', label: t('gofi.canvas.options.source-listen', 'Listen'), description: t('gofi.canvas.options.source-listen-description', 'Senders connect to a GoFi TCP/UDP port') },
  { value: 'fetch', label: t('gofi.canvas.options.source-fetch', 'Fetch'), description: t('gofi.canvas.options.source-fetch-description', 'Poll an HTTP endpoint on an interval') },
  { value: 'subscribe', label: t('gofi.canvas.options.source-subscribe', 'Subscribe'), description: t('gofi.canvas.options.source-subscribe-description', 'NATS, MQTT, WebSocket, SSE, Zenoh, Kafka, AMQP, JetStream, or Redis') },
  { value: 'python', label: 'Python', description: t('gofi.canvas.options.source-python-description', 'A @gofi.fetcher or @gofi.streamer in an isolated library worker') },
]

export const NETWORK_OPTIONS: SelectOption[] = [
  { value: 'tcp', label: 'TCP' },
  { value: 'udp', label: 'UDP' },
  { value: 'quic', label: 'QUIC' },
]

export const framingOptions = (): SelectOption[] => [
  { value: 'line', label: t('gofi.canvas.options.framing-line', 'Line (\\n)') },
  { value: 'delimited', label: t('gofi.canvas.options.framing-delimited', 'Delimiter') },
  { value: 'length-prefix', label: t('gofi.canvas.options.framing-length-prefix', 'Length-prefix') },
  { value: 'raw', label: t('gofi.canvas.options.framing-raw', 'Raw (whole connection)') },
]

export const parserOptions = (): SelectOption[] => [
  { value: 'json', label: 'JSON' },
  { value: 'xml', label: 'XML' },
  { value: 'none', label: t('gofi.canvas.options.parser-none', 'None (passthrough)') },
]

export const sinkOptions = (): SelectOption[] => [
  { value: 'stdout', label: t('gofi.canvas.options.sink-stdout', 'Stdout (debug)') },
  { value: 'nats', label: 'NATS' },
  { value: 'http', label: 'HTTP' },
  { value: 'tcp', label: t('gofi.canvas.options.sink-tcp', 'TCP socket') },
  { value: 'udp', label: t('gofi.canvas.options.sink-udp', 'UDP socket') },
  { value: 'quic', label: t('gofi.canvas.options.sink-quic', 'QUIC stream (TLS over UDP)') },
  { value: 'aster', label: t('gofi.canvas.options.sink-aster', 'Aster secure relay') },
  { value: 'zenoh', label: t('gofi.canvas.options.sink-zenoh', 'Zenoh (REST router)') },
  { value: 'loki', label: t('gofi.canvas.options.sink-loki', 'Grafana Loki') },
]

export const HTTP_METHOD_OPTIONS: SelectOption[] = [
  { value: 'GET', label: 'GET' },
  { value: 'POST', label: 'POST' },
  { value: 'PUT', label: 'PUT' },
  { value: 'PATCH', label: 'PATCH' },
  { value: 'DELETE', label: 'DELETE' },
]

export const bodyModeOptions = (): SelectOption[] => [
  { value: 'wrapped', label: t('gofi.canvas.options.body-wrapped', 'Wrapped') },
  { value: 'raw', label: t('gofi.canvas.options.body-raw', 'Raw') },
]

// The empty-value option represents "no auth configured" (SinkCfg.auth === undefined).
// Selecting it clears the auth object back to undefined.
export const httpAuthTypeOptions = (): SelectOption[] => [
  { value: '', label: t('gofi.common.none', 'None') },
  { value: 'bearer', label: t('gofi.canvas.options.auth-bearer', 'Bearer Token') },
  { value: 'basic', label: t('gofi.canvas.options.auth-basic', 'Basic Auth') },
  { value: 'apiKey', label: t('gofi.canvas.options.auth-api-key', 'API Key') },
]

export const transformOpOptions = (): SelectOption[] => [
  { value: 'set', label: t('gofi.canvas.options.transform-set', 'Set') },
  { value: 'rename', label: t('gofi.canvas.options.transform-rename', 'Rename') },
  { value: 'remove', label: t('gofi.canvas.options.transform-remove', 'Remove') },
]
