// Select-option lists shared by the canvas NodeInspector (and formerly by
// the now-deleted CreateListenerModal). Kept in one place so ingress/sink
// forms always offer the same choices.
import type { SelectOption } from '../primitives'

export const NETWORK_OPTIONS: SelectOption[] = [
  { value: 'tcp', label: 'TCP' },
  { value: 'udp', label: 'UDP' },
]

export const FRAMING_OPTIONS: SelectOption[] = [
  { value: 'line', label: 'Line (\\n)' },
  { value: 'delimited', label: 'Delimiter' },
  { value: 'length-prefix', label: 'Length-prefix' },
  { value: 'raw', label: 'Raw (whole connection)' },
]

export const PARSER_OPTIONS: SelectOption[] = [
  { value: 'json', label: 'JSON' },
  { value: 'xml', label: 'XML' },
  { value: 'none', label: 'None (passthrough)' },
]

export const SINK_OPTIONS: SelectOption[] = [
  { value: 'stdout', label: 'Stdout (debug)' },
  { value: 'nats', label: 'NATS' },
  { value: 'http', label: 'HTTP' },
  { value: 'tcp', label: 'TCP socket' },
  { value: 'udp', label: 'UDP socket' },
  { value: 'aster', label: 'Aster secure relay' },
  { value: 'zenoh', label: 'Zenoh REST router' },
  { value: 'loki', label: 'Grafana Loki' },
]

export const HTTP_METHOD_OPTIONS: SelectOption[] = [
  { value: 'GET', label: 'GET' },
  { value: 'POST', label: 'POST' },
  { value: 'PUT', label: 'PUT' },
  { value: 'PATCH', label: 'PATCH' },
  { value: 'DELETE', label: 'DELETE' },
]

export const BODY_MODE_OPTIONS: SelectOption[] = [
  { value: 'wrapped', label: 'Wrapped' },
  { value: 'raw', label: 'Raw' },
]

// The empty-value option represents "no auth configured" (SinkCfg.auth === undefined).
// Selecting it clears the auth object back to undefined.
export const HTTP_AUTH_TYPE_OPTIONS: SelectOption[] = [
  { value: '', label: 'None' },
  { value: 'bearer', label: 'Bearer Token' },
  { value: 'basic', label: 'Basic Auth' },
  { value: 'apiKey', label: 'API Key' },
]

export const TRANSFORM_OP_OPTIONS: SelectOption[] = [
  { value: 'set', label: 'Set' },
  { value: 'rename', label: 'Rename' },
  { value: 'remove', label: 'Remove' },
]
