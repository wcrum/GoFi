import { test } from 'node:test'
import assert from 'node:assert/strict'
import { componentLabel, runActions, runStateView } from './runstate.ts'

test('run state shows restarts and tone', () => {
  assert.equal(runStateView({ desired: 'running', phase: 'running', restarts: 0 }).text, 'Running')
  const crashed = runStateView({ desired: 'running', phase: 'crashed', restarts: 3, message: 'exited' })
  assert.equal(crashed.text, 'Crashed · 3 restarts')
  assert.equal(crashed.tone, 'error')
  assert.equal(crashed.tooltip, 'exited')
  assert.equal(runStateView({ desired: 'stopped', phase: 'stopped', restarts: 2 }).text, 'Stopped')
  assert.equal(runStateView(undefined).text, 'Unknown')
})

test('actions follow the declared state', () => {
  assert.deepEqual(runActions({ desired: 'stopped', phase: 'stopped', restarts: 0 }), { start: true, stop: false, restart: false })
  assert.deepEqual(runActions({ desired: 'running', phase: 'crashed', restarts: 1 }), { start: false, stop: true, restart: true })
  assert.deepEqual(runActions({ desired: 'running', phase: 'invalid', restarts: 0 }), { start: false, stop: false, restart: false })
})

test('components read as people name them', () => {
  assert.equal(componentLabel('sink:0'), 'Destination 1')
  assert.equal(componentLabel('stage:enrich'), 'Stage enrich')
  assert.equal(componentLabel('deadLetter'), 'Dead letter')
})
