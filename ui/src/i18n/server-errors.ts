// UI-side hook for translating server (Connect) errors.
//
// Today the Go side returns plain English messages. The plan (docs/i18n.md,
// "Server errors") is for it to attach an error detail carrying a stable
// `code` plus `params`; the UI then renders `gofi.server.<code>` with those
// params. Until then this maps the Connect status code and the handful of
// raw messages the controller sends most often, and otherwise shows the raw
// message after a translated status label.

import { t } from './runtime.ts'

/** Connect status names (ConnectError.code as `Code[code]`), lower-case. */
const STATUS_TEXT: Record<string, () => string> = {
  canceled: () => t('gofi.server.status.canceled', 'The request was cancelled.'),
  unknown: () => t('gofi.server.status.unknown', 'Something went wrong on the server.'),
  invalid_argument: () => t('gofi.server.status.invalid-argument', 'The request was rejected as invalid.'),
  deadline_exceeded: () => t('gofi.server.status.deadline-exceeded', 'The server took too long to answer.'),
  not_found: () => t('gofi.server.status.not-found', 'Not found.'),
  already_exists: () => t('gofi.server.status.already-exists', 'It already exists.'),
  permission_denied: () => t('gofi.server.status.permission-denied', 'You are not allowed to do that.'),
  resource_exhausted: () => t('gofi.server.status.resource-exhausted', 'A limit was reached.'),
  failed_precondition: () => t('gofi.server.status.failed-precondition', 'The current state does not allow that.'),
  aborted: () => t('gofi.server.status.aborted', 'The operation was aborted.'),
  unimplemented: () => t('gofi.server.status.unimplemented', 'This GoFi controller does not support that yet.'),
  internal: () => t('gofi.server.status.internal', 'Internal server error.'),
  unavailable: () => t('gofi.server.status.unavailable', 'The GoFi controller is unreachable.'),
  unauthenticated: () => t('gofi.server.status.unauthenticated', 'Sign in is required.'),
}

/** Raw messages the controller sends today (api/*.go), keyed by exact text. */
const KNOWN_MESSAGES: Record<string, () => string> = {
  'private tool credential required': () => t('gofi.server.message.private-tool-credential-required', 'A credential is required to use this private tool.'),
  'library credential required': () => t('gofi.server.message.library-credential-required', 'A credential is required to use this library.'),
  'function not registered': () => t('gofi.server.message.function-not-registered', 'That function is not registered.'),
  'source cannot be parsed': () => t('gofi.server.message.source-cannot-be-parsed', 'The source cannot be parsed.'),
}

/** Structured codes the server will send once errors carry details. Add a
 * key here when the Go side starts emitting the code. */
const SERVER_CODES: Record<string, (params: Record<string, unknown>) => string> = {
  flow_not_found: params => t('gofi.server.error.flow-not-found', 'Flow "{{name}}" was not found.', params),
  flow_already_exists: params => t('gofi.server.error.flow-already-exists', 'A flow named "{{name}}" already exists.', params),
  flow_invalid: params => t('gofi.server.error.flow-invalid', 'Flow "{{name}}" is invalid: {{reason}}', params),
}

export interface ServerErrorInput {
  /** A GoFi error code from the error details (`flow_not_found`), or a Connect status name (`not_found`, `NotFound`) or number. */
  code?: string | number | null
  /** Parameters from the error details; `message` may carry the raw server text. */
  params?: Record<string, unknown> | null
}

const CONNECT_CODE_NAMES = ['', 'canceled', 'unknown', 'invalid_argument', 'deadline_exceeded', 'not_found', 'already_exists',
  'permission_denied', 'resource_exhausted', 'failed_precondition', 'aborted', 'out_of_range', 'unimplemented', 'internal', 'unavailable',
  'data_loss', 'unauthenticated']

function normalizeCode(code: string | number): string {
  if (typeof code === 'number') return CONNECT_CODE_NAMES[code] ?? 'unknown'
  return code.replace(/([a-z0-9])([A-Z])/g, '$1_$2').toLowerCase()
}

/** A translated, user-facing text for a server error. Resolution order:
 * structured GoFi code → known raw message → Connect status label plus the
 * raw message. The raw message (params.message) is kept visible so nothing
 * is hidden while most errors are still English-only. */
export function translateServerError(code: string | number | null | undefined, params: Record<string, unknown> | null = null): string {
  const values = params ?? {}
  const raw = typeof values.message === 'string' ? values.message.trim() : ''
  const normalized = code === null || code === undefined ? '' : normalizeCode(code)
  if (normalized && SERVER_CODES[normalized]) return SERVER_CODES[normalized](values)
  if (raw && KNOWN_MESSAGES[raw]) return KNOWN_MESSAGES[raw]()
  const status = normalized ? STATUS_TEXT[normalized]?.() : undefined
  if (status && raw && raw.toLowerCase() !== normalized.replace(/_/g, ' ')) return `${status} ${raw}`
  return status ?? raw ?? STATUS_TEXT.unknown()
}
