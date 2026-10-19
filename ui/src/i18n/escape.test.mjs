import { test } from 'node:test'
import assert from 'node:assert/strict'
import { initI18n, t } from './index.ts'

test('interpolated values are not HTML-escaped', async () => {
  await initI18n()
  assert.equal(t('gofi.test.escape', 'When {{expression}}', { expression: 'fields["accepted"] && a < b' }), 'When fields["accepted"] && a < b')
})
