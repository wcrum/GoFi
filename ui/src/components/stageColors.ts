// Shared color maps for pipeline-stage badges, used by both the list-view
// ListenerCard and the flow-canvas node components so there is one source
// of truth for stage coloring.
import type { BadgeColor } from '@cnak-us/design-system'

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
  none: 'gray',
}

export const SINK_COLOR: Record<string, StageColor> = {
  nats: 'green',
  http: 'blue',
  stdout: 'gray',
}

// Per-stage-kind color, keyed by StageSpec['kind'], used by the flow-canvas
// StageNode/StagePalette so each of the 5 stage kinds reads as visually
// distinct at a glance.
export const STAGE_KIND_COLOR: Record<string, StageColor> = {
  filter: 'yellow',
  transform: 'blue',
  'rate-limit': 'purple',
  dedup: 'orange',
  schema: 'green',
}

// Ring/border classes for the transient mock-playback highlight on canvas
// nodes (see MockState in canvas/types.ts). Kept here so all three node
// components (Ingress/Stage/Sink) share one visual language: amber pulsing for
// the active step, green/red/orange for a node playback has passed/dropped/
// errored on. Chosen to stay legible layered under the selection ring
// (`ring-2 ring-g-accent-main`), which uses a different color.
export const MOCK_STATE_RING: Record<string, string> = {
  active: 'ring-2 ring-amber-400 animate-pulse',
  pass: 'ring-2 ring-green-500',
  drop: 'ring-2 ring-red-500',
  error: 'ring-2 ring-orange-500',
}

// Badge color/label pair for the same MockState, used by Ingress/Stage/Sink
// node bodies to render a small textual status alongside the ring above (the
// ring alone is easy to miss at a glance / doesn't say *why*).
export const MOCK_STATE_BADGE_COLOR: Record<string, StageColor> = {
  active: 'yellow',
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
