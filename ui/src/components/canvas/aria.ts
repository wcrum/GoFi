// Localized accessible names for React Flow's own UI (the control panel
// buttons and the keyboard hints it attaches to nodes and edges), which
// otherwise stay in English whatever the portal language. Pass
// `ariaLabelConfig={flowAriaLabels()}` to every <ReactFlow>, memoised per
// mount (I18nProvider remounts the tree on a locale change).

import type { ReactFlowProps } from '@xyflow/react'
import { t } from '../../i18n/index.ts'

export type FlowAriaLabels = NonNullable<ReactFlowProps['ariaLabelConfig']>

export const flowAriaLabels = (): FlowAriaLabels => ({
  'controls.ariaLabel': t('gofi.canvas.aria.controls', 'Canvas controls'),
  'controls.zoomIn.ariaLabel': t('gofi.canvas.aria.zoom-in', 'Zoom in'),
  'controls.zoomOut.ariaLabel': t('gofi.canvas.aria.zoom-out', 'Zoom out'),
  'controls.fitView.ariaLabel': t('gofi.canvas.aria.fit-view', 'Fit view'),
  'controls.interactive.ariaLabel': t('gofi.canvas.aria.toggle-interactivity', 'Toggle interactivity'),
  'node.a11yDescription.default': t('gofi.canvas.aria.node-description', 'Press Enter or Space to select a node. Press Delete to remove it and Escape to cancel.'),
  'node.a11yDescription.keyboardDisabled': t('gofi.canvas.aria.node-description-keyboard', 'Press Enter or Space to select a node, then use the arrow keys to move it. Press Delete to remove it and Escape to cancel.'),
  'edge.a11yDescription.default': t('gofi.canvas.aria.edge-description', 'Press Enter or Space to select a connection, then Delete to remove it or Escape to cancel.'),
})

/** aria-label of the connection between two nodes (React Flow's default is
 * the English "Edge from … to …"). */
export const edgeAriaLabel = (source: string, target: string): string =>
  t('gofi.canvas.aria.edge', 'Connection from {{source}} to {{target}}', { source, target })
