import { test } from 'node:test'
import assert from 'node:assert/strict'
import { isAllowlisted, lintSource, loadAllowlist, summarize } from './i18n-lint.mjs'

const allowlist = loadAllowlist()
const lint = source => lintSource('/repo/src/components/canvas/Thing.tsx', source, allowlist).map(hit => `${hit.line}:${hit.category}:${hit.text}`)

test('reports JSX text and listed string props, not translated ones', () => {
  const hits = lint(`
import { t, Trans } from '../../i18n'
export function Thing({ name }: { name: string }) {
  return <div>
    <h3>Theme</h3>
    <h3>{t('gofi.canvas.thing.title', 'Theme')}</h3>
    <Button tooltip="Save flow" aria-label={t('gofi.canvas.thing.save', 'Save')} />
    <Input placeholder={'Type here'} title={\`Hello \${name}\`} data-testid="thing" />
    <Trans i18nKey="gofi.canvas.thing.rich">See <a href="/docs">the docs</a></Trans>
    <span>{name}</span>
  </div>
}`)
  assert.deepEqual(hits, [
    '5:jsx-text:Theme',
    '7:jsx-prop:Save flow',
    '8:jsx-prop:Type here',
    '8:jsx-prop:Hello',
  ])
})

test('object-literal props with the listed names count too', () => {
  const hits = lint(`
const OPTIONS = [{ label: 'Flows', value: 'flows' }, { label: t('gofi.x.y', 'Pages'), value: 'pages' }]
const styles = { color: 'red' }`)
  assert.deepEqual(hits, ['2:object-prop:Flows'])
})

test('i18n-ignore comments and allowlisted terms are skipped', () => {
  const hits = lint(`
export function Thing() {
  return <div>
    <span>GoFi</span>
    <span>CEL → YAML</span>
    <code>fields["temperature"] > 30.0</code>
    {/* i18n-ignore */}
    <span>Ignored by the previous line</span>
    <span>Ignored inline</span>{/* i18n-ignore */}
    <span>Reported</span>
    <kbd>Ctrl+K</kbd>
    <span>42</span>
  </div>
}`)
  // CEL field access is a code placeholder (allowlist pattern), so only "Reported" is left.
  assert.deepEqual(hits, ['10:jsx-text:Reported'])
  assert.ok(isAllowlisted('GoFi / Kafka', allowlist))
  assert.ok(isAllowlisted('{{count}}', allowlist))
  assert.ok(!isAllowlisted('Save GoFi flow', allowlist))
  // Code-shaped placeholders match patterns; plain words and sentences never do.
  for (const code of ['nats://broker:4222', 'SELECT * FROM events WHERE id > 0', 'fields["a"] + fields["b"]', '$.items[*]', '{"limit": 100}', '-----BEGIN CERTIFICATE-----', 'app=nats', 'hot-readings', 'newFieldName', 'sensors.>', 'X-API-Key', 'postgres.db.svc.cluster.local', 'devices/+/telemetry', '^event-']) {
    assert.ok(isAllowlisted(code, allowlist), code)
  }
  for (const text of ['Clear', 'now', 'Reconnecting…', 'Scroll to newest', 'REJECTED', 'events dropped']) assert.ok(!isAllowlisted(text, allowlist), text)
})

test('summarize counts by category and area', () => {
  const hits = lintSource('/repo/src/shell/AppChrome.tsx', '<a title="Home page">Welcome</a>', allowlist)
  const summary = summarize(hits)
  assert.equal(summary.total, 2)
  assert.deepEqual(summary.byCategory, { 'jsx-prop': 1, 'jsx-text': 1 })
  assert.deepEqual(summary.byArea, { shell: 2 })
})
