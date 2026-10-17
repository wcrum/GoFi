// Single source of the GoFi mark artwork (same as public/gofi-logo.svg, the
// static teal fallback favicon). Used by <GoFiMark> and by ThemeProvider to
// build an accent-colored favicon.

export const GOFI_MARK_VIEWBOX = '0 0 32 32'
/** Glyph group offset inside the 32x32 tile. */
export const GOFI_MARK_GLYPH_TRANSFORM = 'translate(4 4)'
/** The four stream nodes (cx, cy) of radius GOFI_MARK_DOT_RADIUS. */
export const GOFI_MARK_DOTS: ReadonlyArray<readonly [number, number]> = [[20, 12], [4, 12], [12, 20], [12, 4]]
export const GOFI_MARK_DOT_RADIUS = 2
export const GOFI_MARK_PATH = 'm13.943 8.619 4.404-4.392 1.413 1.416-4.405 4.392zM8.32 9.68l.31.32 1.42-1.41-4.02-4.04h-.01l-.31-.32-1.42 1.41 4.02 4.05zM15.41 13.94 14 15.35l3.99 4.01.35.35 1.42-1.41-3.99-4.01zM8.59 13.95l-4.03 4.01-.32.33 1.41 1.41 4.03-4.02.33-.32z'
/** Tile tint opacity in-app. */
export const GOFI_MARK_TILE_OPACITY = 0.14
/** Stronger tint for the favicon: at 16px the faint in-app tile vanishes on
 * both light and dark tab strips, so give the glyph a visible backing. */
export const GOFI_FAVICON_TILE_OPACITY = 0.24

/** Matches the hex/rgb(a)/hsl(a) colors the theme produces; anything else is
 * rejected so a cached value can't inject markup into the SVG. */
const SAFE_COLOR = /^(#[0-9a-f]{3,8}|(rgb|hsl)a?\([0-9.,%\s/]+\))$/i

/** Standalone SVG document for the mark in `color`. */
export function gofiMarkSvg(color: string, tileOpacity = GOFI_MARK_TILE_OPACITY): string {
  if (!SAFE_COLOR.test(color)) throw new Error(`gofiMarkSvg: unsupported color ${JSON.stringify(color)}`)
  const opacity = Math.min(1, Math.max(0, tileOpacity))
  const dots = GOFI_MARK_DOTS.map(([cx, cy]) => `<circle cx="${cx}" cy="${cy}" r="${GOFI_MARK_DOT_RADIUS}"/>`).join('')
  return `<svg xmlns="http://www.w3.org/2000/svg" viewBox="${GOFI_MARK_VIEWBOX}">`
    + `<rect width="32" height="32" rx="6" fill="${color}" fill-opacity="${opacity}"/>`
    + `<g transform="${GOFI_MARK_GLYPH_TRANSFORM}" fill="${color}">${dots}<path d="${GOFI_MARK_PATH}"/></g></svg>`
}

/** `data:` URL of the favicon for `color` (percent-encoded, so '#' etc. are safe). */
export function gofiFaviconDataUrl(color: string): string {
  return `data:image/svg+xml,${encodeURIComponent(gofiMarkSvg(color, GOFI_FAVICON_TILE_OPACITY))}`
}
