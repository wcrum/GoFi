#!/usr/bin/env node
// i18n layout sweep (playwright-core, no test runner): every route renders in
// every locale (en-US, uk-UA, zh-Hant and the pseudo-locale, which pads every
// string by ~35%) at desktop (1440×900) and phone (390×844) widths without
// console or page errors, without horizontal page overflow, and without text
// that is hard-clipped (overflow hidden with neither an ellipsis nor a line
// clamp). Ellipsis truncations are counted as warnings, since some are by
// design (long values with a title tooltip). Writes testenv/reports/ui-i18n.json.
//
//   node ui/e2e/i18n-sweep.mjs                               # against :5180
//   GOFI_UI_URL=http://127.0.0.1:5180 node ui/e2e/i18n-sweep.mjs
//   GOFI_I18N_LOCALES=uk-UA,pseudo GOFI_I18N_WIDTHS=390 node ui/e2e/i18n-sweep.mjs
//
// playwright-core resolves from ui/node_modules (GOFI_PW_MODULES overrides);
// install Chromium once with `npx playwright-core install chromium`.
import { createRequire } from 'node:module'
import { mkdir, writeFile } from 'node:fs/promises'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const here = path.dirname(fileURLToPath(import.meta.url))
const uiDir = path.resolve(here, '..')
const repo = path.resolve(uiDir, '..')
const pwModules = process.env.GOFI_PW_MODULES ?? path.join(uiDir, 'node_modules')
const { chromium } = createRequire(path.join(pwModules, 'package.json'))('playwright-core')

const uiUrl = process.env.GOFI_UI_URL ?? 'http://127.0.0.1:5180'
const reportDir = process.env.GOFI_REPORT_DIR ?? path.join(repo, 'testenv', 'reports')
const shotDir = path.join(reportDir, 'ui-i18n')
await mkdir(shotDir, { recursive: true })

const locales = (process.env.GOFI_I18N_LOCALES ?? 'en-US,uk-UA,zh-Hant,pseudo').split(',').map(s => s.trim()).filter(Boolean)
const widths = (process.env.GOFI_I18N_WIDTHS ?? '1440,390').split(',').map(Number)
const ignoredConsole = [/ResizeObserver loop/, /favicon/, /Download the React DevTools/, /\[vite\]/, /Monaco/, /source map/i, /status of 4\d\d/]

// Routes: the static pages plus, when the backend has a flow, that flow's tabs
// and editor (through the UI's API proxy; the flow list is empty otherwise).
async function routes() {
  const list = [['home', '#/'], ['flows', '#/flows'], ['new-flow', '#/flows/new'], ['explore', '#/explore'], ['functions', '#/functions'], ['cel', '#/cel'], ['endpoints', '#/connections/endpoints'], ['cluster', '#/cluster'], ['cluster-networking', '#/cluster/networking'], ['not-found', '#/nope']]
  try {
    const res = await fetch(`${uiUrl}/gofi.v1.GoFiService/ListFlows`, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: '{}' })
    const { flows = [] } = await res.json()
    // Prefer a flow with a Python stage: its node titles are the longest.
    const flow = flows.find(f => (f.stages ?? []).some(s => s.kind === 'python'))?.name ?? flows[0]?.name
    if (flow) {
      const enc = encodeURIComponent(flow)
      list.push(['home-selected', `#/?flow=${enc}`], ['flow', `#/flows/${enc}`], ['flow-traffic', `#/flows/${enc}/traffic`], ['flow-problems', `#/flows/${enc}/problems`], ['flow-resource', `#/flows/${enc}/resource`], ['flow-resource-rev', `#/flows/${enc}/resource?rev=1`], ['flow-edit', `#/flows/${enc}/edit`])
    }
  } catch { /* no backend: static pages only */ }
  return list
}

// In-page probe: horizontal overflow of the document, text clipped by an
// overflow:hidden ancestor-less box, and ellipsis truncations (warnings).
function probe() {
  const out = { hscroll: [], clipped: [], truncated: [] }
  const visible = el => { const r = el.getBoundingClientRect(); return r.width > 0 && r.height > 0 && r.bottom > 0 && r.right > 0 && r.top < innerHeight && r.left < innerWidth }
  const describe = el => `${el.tagName.toLowerCase()}${el.getAttribute('role') ? `[${el.getAttribute('role')}]` : ''}: "${(el.textContent || '').trim().slice(0, 60)}"`
  const root = document.documentElement
  if (root.scrollWidth > root.clientWidth + 1) out.hscroll.push({ el: 'html', scrollWidth: root.scrollWidth, clientWidth: root.clientWidth })
  if (document.body.scrollWidth > innerWidth + 1) out.hscroll.push({ el: 'body', scrollWidth: document.body.scrollWidth, clientWidth: innerWidth })
  for (const el of document.querySelectorAll('body *')) {
    if (el.children.length > 0 || !(el.textContent || '').trim() || !visible(el)) continue
    if (el.closest('.monaco-editor, svg, .react-flow__edge, pre, code, [class*=sparkline]')) continue
    const cs = getComputedStyle(el)
    const hidden = cs.overflowX === 'hidden' || cs.overflow === 'hidden' || cs.overflowX === 'clip'
    if (!hidden || el.scrollWidth <= el.clientWidth + 1) continue
    const entry = { el: describe(el), clientWidth: el.clientWidth, scrollWidth: el.scrollWidth }
    if (cs.textOverflow === 'ellipsis' || cs.webkitLineClamp !== 'none') out.truncated.push(entry)
    else out.clipped.push(entry)
  }
  return out
}

const browser = await chromium.launch()
const results = []
let failed = 0
const pages = await routes()
for (const locale of locales) {
  for (const width of widths) {
    const height = width < 600 ? 844 : 900
    const context = await browser.newContext({ viewport: { width, height }, isMobile: width < 600, hasTouch: width < 600 })
    await context.addInitScript(([tag, pseudo]) => {
      try {
        localStorage.setItem('gofi.locale', tag)
        if (pseudo) localStorage.setItem('gofi.i18n.pseudo', '1'); else localStorage.removeItem('gofi.i18n.pseudo')
      } catch { /* storage unavailable */ }
    }, [locale === 'pseudo' ? 'en-US' : locale, locale === 'pseudo'])
    const page = await context.newPage()
    const errors = []
    page.on('console', msg => { if (msg.type() === 'error' && !ignoredConsole.some(re => re.test(msg.text()))) errors.push(`console: ${msg.text().slice(0, 200)}`) })
    page.on('pageerror', err => errors.push(`pageerror: ${err.message}`))
    for (const [name, hash] of pages) {
      errors.length = 0
      const label = `${name} ${locale} ${width}px`
      try {
        await page.goto(`${uiUrl}/${hash}`, { waitUntil: 'load' })
        // Lazy pages compile on first visit in the dev server; wait for text.
        const deadline = Date.now() + 30000
        let text = ''
        while (Date.now() < deadline && text.trim().length < 20) { text = await page.evaluate(() => document.body.innerText).catch(() => ''); await page.waitForTimeout(250) }
        await page.waitForTimeout(600)
        if (/Something went wrong|Unexpected Application Error|Minified React error/i.test(text)) throw new Error('error boundary rendered')
        const found = await page.evaluate(probe)
        const lang = await page.evaluate(() => document.documentElement.lang)
        const wantLang = locale === 'pseudo' ? 'en-US' : locale
        const problems = [...errors]
        if (lang !== wantLang) problems.push(`<html lang> is "${lang}", want "${wantLang}"`)
        for (const h of found.hscroll) problems.push(`horizontal page overflow: ${h.el} ${h.scrollWidth}>${h.clientWidth}`)
        for (const c of found.clipped) problems.push(`clipped text: ${c.el} (${c.scrollWidth}>${c.clientWidth})`)
        const ok = problems.length === 0
        if (!ok) await page.screenshot({ path: path.join(shotDir, `${name}-${locale}-${width}.png`) })
        results.push({ name: label, ok, problems, truncated: found.truncated })
        if (!ok) failed++
        console.log(`${ok ? 'PASS' : 'FAIL'} ${label}${found.truncated.length ? ` (${found.truncated.length} ellipsis truncation${found.truncated.length === 1 ? '' : 's'})` : ''}${ok ? '' : `: ${problems.slice(0, 4).join(' | ')}`}`)
      } catch (error) {
        failed++
        results.push({ name: label, ok: false, problems: [String(error?.message ?? error)] })
        console.log(`FAIL ${label}: ${String(error?.message ?? error).split('\n')[0]}`)
      }
    }
    await context.close()
  }
}
await browser.close()
const summary = { name: 'ui-i18n', deployment: 'ui', startedAt: new Date(), uiUrl, locales, widths, passed: failed === 0, failed, results }
await writeFile(path.join(reportDir, 'ui-i18n.json'), JSON.stringify(summary, null, 2))
console.log(`${results.length - failed}/${results.length} passed; report ${path.join(reportDir, 'ui-i18n.json')}`)
process.exit(failed ? 1 : 0)
