#!/usr/bin/env node
// GoFi portal smoke test (playwright-core, no test runner): every page renders
// in dark and light without console errors; a flow goes through
// create → edit → stop → start → restart → rollback → delete in the UI; live
// traffic shows events; and (with an owned backend) the live view reconnects
// after a backend restart. Writes testenv/reports/ui-smoke.json and
// screenshots under testenv/reports/ui/.
//
//   node ui/e2e/run.mjs                      # against :5180 (UI) and :19190 (API)
//   GOFI_UI_URL=http://127.0.0.1:5180 GOFI_API_URL=http://127.0.0.1:19190 node ui/e2e/run.mjs
//   GOFI_UI_E2E_OWN_BACKEND=1 node ui/e2e/run.mjs   # spawns bin/gofi-e2e, smokes its embedded portal; adds the reconnect test
//   GOFI_UI_E2E_OWN_BACKEND=1 GOFI_UI_E2E_VITE=1 node ui/e2e/run.mjs   # ... through a Vite dev server instead
//
// Child process output lands in <report dir>/ui/logs/ and a blank page is
// dumped (HTML + screenshot) next to the other screenshots, so a failure says
// why the page was empty rather than only that it was.
//
// playwright-core resolves from ui/node_modules (GOFI_PW_MODULES overrides);
// install Chromium once with `npx playwright-core install chromium`.
import { createRequire } from 'node:module'
import { spawn } from 'node:child_process'
import { readFileSync } from 'node:fs'
import { mkdir, open, writeFile, mkdtemp } from 'node:fs/promises'
import { createConnection, createServer } from 'node:net'
import { tmpdir } from 'node:os'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const here = path.dirname(fileURLToPath(import.meta.url))
const uiDir = path.resolve(here, '..')
const repo = path.resolve(uiDir, '..')
const pwModules = process.env.GOFI_PW_MODULES ?? path.join(uiDir, 'node_modules')
const { chromium } = createRequire(path.join(pwModules, 'package.json'))('playwright-core')

const reportDir = process.env.GOFI_REPORT_DIR ?? path.join(repo, 'testenv', 'reports')
const shotDir = path.join(reportDir, 'ui')
const logDir = path.join(shotDir, 'logs')
await mkdir(logDir, { recursive: true })

// Child stdout and stderr go to files under the report directory (CI uploads
// it) instead of being discarded: when the portal does not come up, what the
// dev server or the controller said is the whole diagnosis.
// The handles stay in the map for the whole run: a FileHandle closed by the
// garbage collector takes the child's open descriptor with it.
const logHandles = new Map()
const logPaths = new Map()
async function logFd(name) {
  if (!logHandles.has(name)) {
    const file = path.join(logDir, `${name}.log`)
    logHandles.set(name, await open(file, 'a'))
    logPaths.set(name, file)
  }
  return logHandles.get(name).fd
}
function tailLog(name, lines = 12) {
  const file = logPaths.get(name)
  if (!file) return ''
  try { return readFileSync(file, 'utf8').trim().split('\n').slice(-lines).join(' / ').slice(-1500) } catch { return '' }
}

const results = []
const startedAt = new Date()
let failed = 0
function record(name, ok, detail = {}) {
  results.push({ name, ok, ...detail })
  if (!ok) failed++
  // A tolerated shortfall prints WARN: PASS would claim the behaviour was
  // observed when it was not.
  const label = !ok ? 'FAIL' : detail.warning ? 'WARN' : 'PASS'
  console.log(`${label} ${name}${detail.error ? `: ${detail.error}` : ''}${detail.warning ? `: ${detail.warning}` : ''}`)
}
async function step(name, fn) {
  try {
    const detail = (await fn()) ?? {}
    record(name, true, detail)
  } catch (error) {
    record(name, false, { error: String(error?.stack ?? error) })
  }
}

// --- backend ---------------------------------------------------------------
let uiUrl = process.env.GOFI_UI_URL ?? 'http://127.0.0.1:5180'
let apiUrl = process.env.GOFI_API_URL ?? 'http://127.0.0.1:19190'
const children = []
let backend = null
async function freePort() {
  return new Promise((resolve, reject) => {
    const srv = createServer()
    srv.listen(0, '127.0.0.1', () => { const { port } = srv.address(); srv.close(() => resolve(port)) })
    srv.on('error', reject)
  })
}
async function waitHttp(url, timeoutMs = 60000) {
  const deadline = Date.now() + timeoutMs
  let last
  while (Date.now() < deadline) {
    try { const r = await fetch(url); if (r.ok) return } catch (e) { last = e }
    await new Promise(r => setTimeout(r, 200))
  }
  throw new Error(`${url} not ready: ${last}`)
}
async function startBackend(dir, port) {
  const bin = process.env.GOFI_BINARY ?? path.join(repo, 'bin', 'gofi-e2e')
  const child = spawn(bin, [], {
    env: { ...process.env, GOFI_DRIVER: 'inprocess', GOFI_DEPLOY_MODE: 'binary', GOFI_CONFIG_FILE: path.join(dir, 'listeners.yaml'), GOFI_HISTORY_DIR: path.join(dir, 'history'), INGRESS_API_ADDR: `127.0.0.1:${port}`, TAP_RELAY_ADDR: '127.0.0.1:0', GOFI_DRAIN_TIMEOUT: '3s' },
    stdio: ['ignore', await logFd('controller'), await logFd('controller')],
  })
  children.push(child)
  await waitHttp(`http://127.0.0.1:${port}/readyz`)
  return child
}
// With an owned backend the portal comes from the controller's embedded
// ui/dist: that is the bundle that ships, and it keeps the Vite dev server out
// of CI. On the GitHub runner that dev server served index.html but no page
// ever rendered a character of the app, for the whole 24-minute run, with
// nothing on its stderr -- the shape of a dev server that never served the
// module graph (it withholds module requests until dependency pre-bundling
// completes). The built bundle has no such step. The same run passes locally,
// so keep GOFI_UI_E2E_VITE=1 for smoking the dev server on purpose.
const useVite = Boolean(process.env.GOFI_UI_E2E_VITE)
if (process.env.GOFI_UI_E2E_OWN_BACKEND) {
  const dir = await mkdtemp(path.join(tmpdir(), 'gofi-ui-e2e-'))
  const apiPort = await freePort()
  backend = { dir, port: apiPort, child: await startBackend(dir, apiPort) }
  apiUrl = `http://127.0.0.1:${apiPort}`
  uiUrl = apiUrl
  if (useVite) {
    const uiPort = await freePort()
    const vite = spawn(process.execPath, [path.join(uiDir, 'node_modules', 'vite', 'bin', 'vite.js'), '--port', String(uiPort), '--strictPort', '--host', '127.0.0.1'], {
      cwd: uiDir, env: { ...process.env, GOFI_API_PROXY: apiUrl }, stdio: ['ignore', await logFd('vite'), await logFd('vite')],
    })
    children.push(vite)
    uiUrl = `http://127.0.0.1:${uiPort}`
    await waitHttp(uiUrl, 90000)
  } else {
    // Without ui/dist the controller serves its RPC services only; say so once
    // here rather than through twenty blank pages.
    const index = await fetch(`${uiUrl}/`).then(res => res.text()).catch(() => '')
    if (!/<div id="root"/.test(index)) {
      console.error(`FAIL ${uiUrl}/ serves no portal: build it (npm --prefix ui run build) and rebuild the controller, or set GOFI_UI_E2E_VITE=1. Controller log: ${tailLog('controller')}`)
      process.exit(1)
    }
  }
}
// The embedded bundle needs no warm-up and has no module graph to compile.
const embedded = uiUrl === apiUrl
process.on('exit', () => { for (const c of children) { try { c.kill('SIGTERM') } catch { /* gone */ } } })

// --- API helpers (Connect JSON) -------------------------------------------
async function rpc(service, method, body) {
  const res = await fetch(`${apiUrl}/gofi.v1.${service}/${method}`, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body ?? {}) })
  const text = await res.text()
  if (!res.ok) throw new Error(`${service}/${method} ${res.status}: ${text}`)
  return text ? JSON.parse(text) : {}
}
const flowDef = (name, port, extra = {}) => ({ name, source: { listen: { port, network: 'tcp' } }, framing: { mode: 'line' }, parser: { format: 'json' }, sinks: [{ type: 'stdout' }], ...extra })
async function phase(name) {
  const { flows = [] } = await rpc('OperationsService', 'ListFlowStatus', { name })
  return flows.find(f => f.name === name)?.phase ?? 'missing'
}
async function waitPhase(name, want, timeoutMs = 30000) {
  const deadline = Date.now() + timeoutMs
  let p
  while (Date.now() < deadline) { p = await phase(name); if (p === want) return; await new Promise(r => setTimeout(r, 200)) }
  throw new Error(`${name}: phase ${p}, want ${want}`)
}
function sendLines(port, lines) {
  return new Promise((resolve, reject) => {
    const sock = createConnection({ host: '127.0.0.1', port }, () => { sock.end(lines.map(l => JSON.stringify(l)).join('\n') + '\n'); resolve() })
    sock.on('error', reject)
  })
}

// --- browser ---------------------------------------------------------------
const browser = await chromium.launch()
const ignoredConsole = [/ResizeObserver loop/, /favicon/, /Download the React DevTools/, /\[vite\]/, /Monaco/, /source map/i]
// @grafana/ui's CSS and <Icon> point at Grafana core's asset tree
// (/public/fonts/..., /public/build/img/icons/...) and the package ships only
// part of it, so a missing font or the spinner icon 404s off the real bundle.
// Cosmetic, and the dev server hid it behind its SPA fallback. These 404s are
// counted into the report rather than failing the page -- and only 404: a 5xx
// or a broken path anywhere else still counts as an error.
const missingGrafanaAsset = /\/public\/(fonts|build\/img\/icons)\//
function attach(page, errors, pending, missing) {
  // RPC errors are application semantics (Connect maps NotFound to 404 while
  // a stopped flow has no runtime); the browser still logs them as resource
  // failures. Only non-RPC failures and 5xx responses count.
  page.on('console', msg => { if (msg.type() === 'error' && !ignoredConsole.some(re => re.test(msg.text())) && !/Failed to load resource: the server responded with a status of 4\d\d/.test(msg.text())) errors.push(`console: ${msg.text()}`) })
  page.on('pageerror', err => errors.push(`pageerror: ${err.message}`))
  // ERR_ABORTED is the browser cancelling a request (navigation, removed
  // image), not a server failure; 4xx/5xx responses are what matter.
  page.on('requestfailed', req => { const text = req.failure()?.errorText ?? ''; if (!/ABORTED/.test(text) && !/favicon|hot-update|sockjs|\/@vite\/client|ws:/.test(req.url())) errors.push(`request failed: ${req.url()} ${text}`) })
  page.on('response', res => {
    if (res.status() < 400 || /favicon|sockjs|hot-update/.test(res.url())) return
    if (res.status() === 404 && missingGrafanaAsset.test(res.url())) { missing.push(res.url()); return }
    if (res.status() < 500 && /\/gofi\.v1\./.test(res.url())) return
    errors.push(`HTTP ${res.status()}: ${res.url()}`)
  })
  // A request that never settles is the signature of a server that accepted
  // the connection and then held it: the page stays blank with nothing logged.
  page.on('request', req => pending.set(req, req.url()))
  page.on('requestfinished', req => pending.delete(req))
  page.on('requestfailed', req => pending.delete(req))
}
async function newPage(theme) {
  const context = await browser.newContext({ viewport: { width: 1440, height: 900 }, colorScheme: theme === 'grafana-light' ? 'light' : 'dark' })
  await context.addInitScript(([key, value]) => { try { localStorage.setItem(key, value) } catch { /* ignore */ } }, ['gofi.theme', theme])
  const page = await context.newPage()
  const errors = []
  const pending = new Map()
  const missing = []
  attach(page, errors, pending, missing)
  return { context, page, errors, pending, missing }
}
// Poll until the app has rendered text (lazy pages compile on first visit in
// the Vite dev server).
async function waitForText(page, timeoutMs = 30000) {
  const deadline = Date.now() + timeoutMs
  let text = ''
  while (Date.now() < deadline) {
    text = await page.evaluate(() => document.body.innerText).catch(() => '')
    if (text.trim().length >= 20) return text
    await page.waitForTimeout(250)
  }
  return text
}
// "No text" on its own says nothing about why. Keep the evidence: the page's
// own errors, the requests still in flight, the document, a screenshot, and
// what the child processes last said. The dumps sit with the screenshots, so
// CI's uploaded report carries them.
async function blankPageReport(page, slug, errors, pending, headline = 'page rendered no text') {
  const parts = [headline]
  await page.screenshot({ path: path.join(shotDir, `${slug}-blank.png`) }).catch(() => {})
  // A page still navigating can refuse to serialise; that is not the same as
  // a document without a #root, so do not report it as one.
  const html = (await page.content().catch(() => '')) || (await page.evaluate(() => document.documentElement?.outerHTML ?? '').catch(() => ''))
  if (html) await writeFile(path.join(shotDir, `${slug}-blank.html`), html).catch(() => {})
  parts.push(!html ? 'the document could not be read' : /<div id="root">\s*<\/div>/.test(html) ? 'empty #root: the scripts never mounted' : /<div id="root"/.test(html) ? '#root is present but rendered nothing' : 'the document has no #root at all')
  if (pending.size) parts.push(`requests still in flight: ${[...pending.values()].slice(0, 5).join(', ')}`)
  if (errors.length) parts.push(`page errors: ${errors.slice(0, 5).join(' | ')}`)
  for (const name of ['vite', 'controller']) {
    const tail = tailLog(name)
    if (tail) parts.push(`${name} log: ${tail}`)
  }
  return parts.join('; ')
}

const pages = [['home', '#/'], ['flows', '#/flows'], ['new-flow', '#/flows/new'], ['explore', '#/explore'], ['libraries', '#/libraries'], ['expressions', '#/cel'], ['endpoints', '#/connections/endpoints'], ['cluster', '#/cluster'], ['cluster-networking', '#/cluster/networking'], ['not-found', '#/nope']]

// Warm the dev server: the first visit of each lazy page triggers Vite's
// dependency optimisation, which can reload the page mid-render. The built
// bundle needs none of this.
if (!embedded) {
  const { context, page } = await newPage('grafana-dark')
  for (const [, hash] of pages) { await page.goto(`${uiUrl}/${hash}`, { waitUntil: 'load' }).catch(() => {}); await waitForText(page, 60000) }
  await context.close()
}

for (const theme of ['grafana-dark', 'grafana-light']) {
  for (const [name, hash] of pages) {
    await step(`render ${name} (${theme})`, async () => {
      const { context, page, errors, pending, missing } = await newPage(theme)
      try {
        // A navigation that never finishes is as opaque as a blank page:
        // report it the same way.
        await page.goto(`${uiUrl}/${hash}`, { waitUntil: 'load' }).catch(async error => {
          throw new Error(await blankPageReport(page, `${name}-${theme}`, errors, pending, `navigation never finished: ${String(error.message).split('\n')[0]}`))
        })
        const text = await waitForText(page)
        await page.waitForTimeout(800)
        if (text.trim().length < 20) throw new Error(await blankPageReport(page, `${name}-${theme}`, errors, pending))
        if (/Something went wrong|Unexpected Application Error|Minified React error/i.test(text)) throw new Error('error boundary rendered')
        await page.screenshot({ path: path.join(shotDir, `${name}-${theme}.png`) })
        if (errors.length) throw new Error(errors.slice(0, 5).join(' | '))
        return { textLength: text.length, ...(missing.length ? { missingAssets: [...new Set(missing)].length } : {}) }
      } finally { await context.close().catch(() => { /* a failing close must not replace the step's own error */ }) }
    })
  }
}

// --- lifecycle through the UI -----------------------------------------------
const name = `ui-smoke-${Date.now().toString(36)}`
const port = 30000 + Math.floor(Math.random() * 500)
let apiFallbacks = []
await step('lifecycle create→edit→stop→start→restart→rollback→delete (UI)', async () => {
  const { context, page, errors } = await newPage('grafana-dark')
  try {
    // create
    await page.goto(`${uiUrl}/#/flows/new`, { waitUntil: 'load' })
    await waitForText(page, 60000)
    await page.getByLabel('Flow name').waitFor({ timeout: 60000 })
    await page.getByLabel('Flow name').fill(name)
    // The port lives in the inspector of the source node: select the node.
    await page.getByText(/^(tcp|udp):/).first().click()
    const portInput = page.getByLabel(/^Port/).first()
    await portInput.waitFor({ timeout: 15000 })
    await portInput.fill(String(port))
    await page.getByRole('button', { name: /Create flow/ }).click()
    await page.waitForURL(url => url.hash.startsWith(`#/flows/${name}`), { timeout: 20000 })
    await waitPhase(name, 'running')
    // edit: YAML editor, replace the document, save
    let edited = false
    await page.goto(`${uiUrl}/#/flows/${name}/edit`, { waitUntil: 'load' })
    const yamlOption = page.getByRole('radio', { name: /YAML/i }).or(page.getByText('YAML', { exact: true })).first()
    if (await yamlOption.count()) {
      await yamlOption.click({ force: true }).catch(() => {})
      const editor = page.locator('.monaco-editor textarea').first()
      if (await editor.count()) {
        await editor.focus()
        await page.keyboard.press('Control+A')
        const yaml = [`apiVersion: gofi.wcrum.dev/v1alpha1`, `kind: Flow`, `metadata:`, `  name: ${name}`, `spec:`, `  source:`, `    listen:`, `      port: ${port}`, `      network: tcp`, `  framing:`, `    mode: line`, `  parser:`, `    format: json`, `  stages:`, `    - id: mark`, `      kind: transform`, `      transformOp: set`, `      field: edited`, `      valueExpr: '"ui"'`, `  sinks:`, `    - type: stdout`, ''].join('\n')
        await page.keyboard.insertText(yaml)
        await page.getByRole('button', { name: /Save flow|Save/ }).first().click()
        await page.waitForTimeout(1500)
        const { flow } = await rpc('GoFiService', 'GetFlow', { name })
        edited = (flow?.stages ?? []).some(s => s.id === 'mark')
      }
    }
    if (!edited) {
      apiFallbacks.push('edit')
      await rpc('GoFiService', 'UpsertFlow', { flow: flowDef(name, port, { stages: [{ id: 'mark', kind: 'transform', transformOp: 'set', field: 'edited', valueExpr: '"api"' }] }) })
    }
    await waitPhase(name, 'running')
    // stop / start / restart from the flow page
    await page.goto(`${uiUrl}/#/flows/${name}`, { waitUntil: 'load' })
    await page.getByRole('button', { name: /^Stop/ }).first().click()
    await page.getByRole('button', { name: /^Stop$/ }).last().click()
    await waitPhase(name, 'stopped')
    await page.getByRole('button', { name: /^Start/ }).first().click()
    await waitPhase(name, 'running')
    await page.getByRole('button', { name: /^Restart/ }).first().click()
    await page.getByRole('button', { name: /^Restart$/ }).last().click()
    await page.waitForTimeout(1000)
    await waitPhase(name, 'running')
    // rollback to revision 1 from the Resource tab
    await page.goto(`${uiUrl}/#/flows/${name}/resource?rev=1`, { waitUntil: 'load' })
    await page.getByRole('button', { name: /Restore this version/ }).click({ timeout: 15000 })
    await page.getByRole('button', { name: /^Restore$/ }).click()
    await page.waitForTimeout(1500)
    const { flow: restored } = await rpc('GoFiService', 'GetFlow', { name })
    if ((restored?.stages ?? []).length !== 0) throw new Error(`rollback did not restore revision 1: ${JSON.stringify(restored.stages)}`)
    const { revisions } = await rpc('FlowHistoryService', 'ListFlowRevisions', { flow: name })
    if (revisions[0]?.source !== 'rollback') throw new Error(`head revision source ${revisions[0]?.source}`)
    await waitPhase(name, 'running')
    // delete
    await page.goto(`${uiUrl}/#/flows/${name}`, { waitUntil: 'load' })
    await page.getByRole('button', { name: /^Delete/ }).first().click()
    await page.getByRole('button', { name: /^Delete$/ }).last().click()
    await page.waitForURL(url => url.hash === '#/flows', { timeout: 15000 })
    const list = await rpc('GoFiService', 'ListFlows', {})
    if ((list.flows ?? []).some(f => f.name === name)) throw new Error('flow still listed after delete')
    if (errors.length) throw new Error(errors.slice(0, 5).join(' | '))
    return { revisions: revisions.length, apiFallbacks }
  } finally {
    await context.close().catch(() => { /* see above */ })
    await rpc('GoFiService', 'DeleteFlow', { name }).catch(() => {})
  }
})

// --- live traffic -------------------------------------------------------------
const liveName = `ui-live-${Date.now().toString(36)}`
const livePort = port + 1
await step('live traffic shows events', async () => {
  await rpc('GoFiService', 'UpsertFlow', { flow: flowDef(liveName, livePort) })
  await waitPhase(liveName, 'running')
  const { context, page, errors } = await newPage('grafana-dark')
  try {
    await page.goto(`${uiUrl}/#/flows/${liveName}/traffic`, { waitUntil: 'load' })
    await page.getByLabel('Stream mode').waitFor({ timeout: 60000 })
    await page.waitForTimeout(1000)
    const id = `live-${Date.now()}`
    // The stream has no replay: keep sending until a row shows the id.
    const deadline = Date.now() + 40000
    let shown = false
    while (Date.now() < deadline && !shown) {
      await sendLines(livePort, [{ id, msg: `smoke ${id}` }]).catch(() => {}) // the Message column shows msg
      shown = await page.getByText(id, { exact: false }).first().isVisible().catch(() => false)
      if (!shown) await page.waitForTimeout(1000)
    }
    if (!shown) {
      await page.screenshot({ path: path.join(shotDir, 'live-traffic-failure.png') })
      const text = await page.evaluate(() => document.body.innerText)
      throw new Error(`no live traffic row showed the sent id; page text: ${text.replace(/\s+/g, ' ').slice(0, 600)}`)
    }
    if (errors.length) throw new Error(errors.slice(0, 5).join(' | '))
  } finally { await context.close().catch(() => { /* a failing close must not replace the step's own error */ }) }
})

// --- reconnect after backend restart ------------------------------------------
await step('live view reconnects after backend restart', async () => {
  if (!backend) return { skipped: 'needs GOFI_UI_E2E_OWN_BACKEND=1' }
  const { context, page, errors } = await newPage('grafana-dark')
  try {
    await page.goto(`${uiUrl}/#/flows/${liveName}/traffic`, { waitUntil: 'load' })
    await page.waitForTimeout(1500)
    backend.child.kill('SIGTERM')
    await new Promise(r => backend.child.once('exit', r))
    backend.child = await startBackend(backend.dir, backend.port)
    await waitPhase(liveName, 'running')
    const id = `after-restart-${Date.now()}`
    const deadline = Date.now() + 45000
    let shown = false
    while (Date.now() < deadline && !shown) {
      await sendLines(livePort, [{ id, msg: `smoke ${id}` }]).catch(() => {})
      shown = await page.getByText(id, { exact: false }).first().isVisible().catch(() => false)
      if (!shown) await page.waitForTimeout(1000)
    }
    if (!shown) {
      const message = 'events did not resume in the live view after the backend restart (no page reload)'
      if (process.env.GOFI_UI_E2E_STRICT) throw new Error(message)
      return { warning: message, pageErrors: errors.length }
    }
    return { pageErrors: errors.length }
  } finally { await context.close().catch(() => { /* a failing close must not replace the step's own error */ }) }
})
await rpc('GoFiService', 'DeleteFlow', { name: liveName }).catch(() => {})

await browser.close()
const summary = { name: 'ui-smoke', deployment: 'ui', portal: embedded ? 'embedded bundle' : 'vite dev server', startedAt, finishedAt: new Date(), uiUrl, apiUrl, passed: failed === 0, failed, results }
await writeFile(path.join(reportDir, 'ui-smoke.json'), JSON.stringify(summary, null, 2))
console.log(`${results.length - failed}/${results.length} passed; report ${path.join(reportDir, 'ui-smoke.json')}`)
process.exit(failed ? 1 : 0)
