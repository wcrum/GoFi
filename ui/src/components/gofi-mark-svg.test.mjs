import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { GOFI_FAVICON_TILE_OPACITY, GOFI_MARK_PATH, gofiFaviconDataUrl, gofiMarkSvg } from './gofi-mark-svg.ts'

test('gofiMarkSvg colors tile and glyph', () => {
  const svg = gofiMarkSvg('#ff7700')
  assert.match(svg, /^<svg xmlns="http:\/\/www.w3.org\/2000\/svg" viewBox="0 0 32 32">/)
  assert.match(svg, /<rect [^>]*fill="#ff7700" fill-opacity="0.14"\/>/)
  assert.match(svg, /<g transform="translate\(4 4\)" fill="#ff7700">/)
  assert.equal(svg.match(/<circle /g).length, 4)
  assert.ok(svg.includes(GOFI_MARK_PATH))
})

test('artwork matches public/gofi-logo.svg', () => {
  const logo = readFileSync(new URL('../../public/gofi-logo.svg', import.meta.url), 'utf8')
  const strip = s => s.replace(/\s+/g, '').replace(/fill-opacity="[^"]*"/, '')
  assert.equal(strip(gofiMarkSvg('#2ec1a6')), strip(logo))
})

test('gofiMarkSvg rejects non-color input', () => {
  assert.throws(() => gofiMarkSvg('red"/><script>'))
  assert.throws(() => gofiMarkSvg('url(x)'))
  assert.doesNotThrow(() => gofiMarkSvg('rgb(1, 2, 3)'))
})

test('gofiFaviconDataUrl is an encoded svg data URL with the stronger tile', () => {
  const url = gofiFaviconDataUrl('#3d71d9')
  assert.ok(url.startsWith('data:image/svg+xml,'))
  const body = url.slice('data:image/svg+xml,'.length)
  assert.ok(!/[#<>"\s]/.test(body))
  const svg = decodeURIComponent(body)
  assert.ok(svg.includes('fill="#3d71d9"'))
  assert.ok(svg.includes(`fill-opacity="${GOFI_FAVICON_TILE_OPACITY}"`))
})
