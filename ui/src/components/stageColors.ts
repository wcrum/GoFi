// Shared color maps for pipeline-stage badges, used by the flow table and
// the flow-canvas node components so there is one source of truth for stage
// coloring. Values are Grafana Badge colors.
import type { BadgeColor } from '@grafana/ui'
import { t } from '../i18n/index.ts'

export type StageColor = BadgeColor

export const FRAMING_COLOR: Record<string, StageColor> = {
  line: 'blue',
  delimited: 'green',
  'length-prefix': 'purple',
  raw: 'orange',
}

export const PARSER_COLOR: Record<string, StageColor> = {
  xml: 'blue',
  json: 'green',
  text: 'orange',
  python: 'purple',
  none: 'darkgrey',
}

export const SINK_COLOR: Record<string, StageColor> = {
  nats: 'green',
  http: 'blue',
  stdout: 'darkgrey',
  tcp: 'purple',
  aster: 'green',
  zenoh: 'blue',
  loki: 'orange',
  udp: 'orange',
}

// Per-stage-kind color, keyed by StageSpec['kind'], used by the flow-canvas
// StageNode/StagePalette. Traffic controls (rate-limit, dedup) share a color,
// as they share a palette group. 'brand' is Grafana's gradient, reserved for
// branding, so stages never use it.
export const STAGE_KIND_COLOR: Record<string, StageColor> = {
  filter: 'orange',
  transform: 'blue',
  'rate-limit': 'purple',
  dedup: 'purple',
  schema: 'green',
  python: 'purple',
  parse: 'blue',
}

// Outline color for the transient mock-playback highlight on canvas nodes
// (see MockState in canvas/types.ts), as a Grafana theme color key. All three
// node components share it: warning for the active step, success/error for
// passed/dropped nodes. Distinct from the primary-colored selection outline.
export const MOCK_STATE_OUTLINE: Record<string, 'warning' | 'success' | 'error'> = {
  active: 'warning',
  pass: 'success',
  drop: 'error',
  error: 'error',
}

// Badge color/label pair for the same MockState, used by Ingress/Stage/Sink
// node bodies to render a small textual status alongside the outline above
// (the outline alone is easy to miss and does not say *why*).
export const MOCK_STATE_BADGE_COLOR: Record<string, StageColor> = {
  active: 'orange',
  pass: 'green',
  drop: 'red',
  error: 'orange',
}

/** Localized badge text for a MockState; unknown states return the raw key.
 * A function (not a module constant) so labels resolve in the active locale. */
export function mockStateLabel(state: string): string {
  switch (state) {
    case 'active': return t('gofi.components.stage-colors.mock-running', 'running')
    case 'pass': return t('gofi.components.stage-colors.mock-pass', 'pass')
    case 'drop': return t('gofi.components.stage-colors.mock-drop', 'drop')
    case 'error': return t('gofi.components.stage-colors.mock-error', 'error')
    default: return state
  }
}
