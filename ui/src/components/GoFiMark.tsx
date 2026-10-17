import { useTheme2 } from '@grafana/ui'
import { GOFI_MARK_DOT_RADIUS, GOFI_MARK_DOTS, GOFI_MARK_GLYPH_TRANSFORM, GOFI_MARK_PATH, GOFI_MARK_TILE_OPACITY, GOFI_MARK_VIEWBOX } from './gofi-mark-svg'

export interface GoFiMarkProps {
  /** Rendered width and height in pixels. */
  size?: number
  /** Glyph color; defaults to the selected accent (theme.colors.primary.main). */
  color?: string
  className?: string
  /** Accessible name. Without one the mark is decorative (aria-hidden). */
  title?: string
}

/** GoFi's mark: the stream glyph on a tinted tile, in the accent color. The
 * artwork lives in gofi-mark-svg.ts (shared with the accent favicon; same as
 * public/gofi-logo.svg, the static teal fallback). */
export function GoFiMark({ size = 26, color, className, title }: GoFiMarkProps) {
  const theme = useTheme2()
  const fill = color ?? theme.colors.primary.main
  return <svg width={size} height={size} viewBox={GOFI_MARK_VIEWBOX} className={className} role={title ? 'img' : undefined} aria-hidden={title ? undefined : true}>
    {title && <title>{title}</title>}
    <rect width="32" height="32" rx="6" fill={fill} fillOpacity={GOFI_MARK_TILE_OPACITY} />
    <g transform={GOFI_MARK_GLYPH_TRANSFORM} fill={fill}>
      {GOFI_MARK_DOTS.map(([cx, cy]) => <circle key={`${cx},${cy}`} cx={cx} cy={cy} r={GOFI_MARK_DOT_RADIUS} />)}
      <path d={GOFI_MARK_PATH} />
    </g>
  </svg>
}
