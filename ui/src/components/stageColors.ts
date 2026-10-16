// Shared color maps for pipeline-stage badges, used by the flow table and
// the flow-canvas node components so there is one source of truth for stage
// coloring. Values are Grafana Badge colors.
import type { BadgeColor } from '@grafana/ui'

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
// StageNode/StagePalette so each of the 5 stage kinds reads as visually
// distinct at a glance.
export const STAGE_KIND_COLOR: Record<string, StageColor> = {
  filter: 'orange',
  transform: 'blue',
  'rate-limit': 'purple',
  dedup: 'brand',
  schema: 'green',
  python: 'purple',
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

export const MOCK_STATE_LABEL: Record<string, string> = {
  active: 'running',
  pass: 'pass',
  drop: 'drop',
  error: 'error',
}
