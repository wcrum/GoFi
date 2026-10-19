import assert from 'node:assert/strict'
import { test } from 'node:test'
import { zenohKeyProblem, zenohOriginProblem } from './sources/zenohValidation.ts'
import { sourceSummary, subscribeKindOf } from '../../types.ts'

test('zenoh key expressions mirror the server rules', () => {
  for (const key of ['demo/sensors/**', '*/temperature', '**']) assert.equal(zenohKeyProblem(key, true), undefined, key)
  for (const key of ['', '@/router/**', 'demo/../x', 'demo//a', 'demo/*eval', 'demo/$*', 'demo?x=1', '**/**']) assert.ok(zenohKeyProblem(key, true), key)
  assert.ok(zenohKeyProblem('demo/*', false))
  assert.equal(zenohKeyProblem('demo/key', false), undefined)
})

test('zenoh router URL is an origin without credentials', () => {
  assert.equal(zenohOriginProblem('http://zenoh-router:8000'), undefined)
  for (const url of ['tcp://router:7447', 'http://router:8000/x', 'http://u:p@router:8000', 'http://router:8000?q', 'router']) assert.ok(zenohOriginProblem(url), url)
})

test('zenoh subscriptions are a subscribe kind with a summary', () => {
  const source = { subscribe: { zenoh: { url: 'http://r:8000', keyExpr: 'demo/**' } } }
  assert.equal(subscribeKindOf(source.subscribe), 'zenoh')
  assert.equal(sourceSummary(source), 'Zenoh demo/**')
})
