import { css } from '@emotion/css'
import type { GrafanaTheme2 } from '@grafana/data'

/** Outline for @grafana/ui `Card`s placed on the page canvas. Grafana's Card
 * paints `background.secondary` with no border, and in every light theme that
 * surface is within a few steps of the canvas (Grafana Light: #f4f5f5 on
 * #fbfbfb; GitHub / VS Code / Solarized Light: identical), so the card reads as
 * loose text. The weak border is what Grafana's refreshed Card adds; in dark
 * themes it only outlines a surface that is already distinct. Pass it as the
 * Card's `className`. */
export const cardSurface = (theme: GrafanaTheme2) => css({
  border: `1px solid ${theme.colors.border.weak}`,
})
