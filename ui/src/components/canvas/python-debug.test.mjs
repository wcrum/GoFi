import { test } from 'node:test'
import assert from 'node:assert/strict'
import { stepInto, stepOver, stepOut, stepBack, continueToBreakpoint, frameStep, globalsAt, stdoutAt, pythonSteps } from '../python/debugger/navigation.ts'

// A synthetic recording: transform (depth 0) loops and calls helper (depth 1)
// twice; helper calls inner (depth 2) once.
const steps = [
  { event: 'call', line: 10, depth: 0 },   // 0 transform
  { event: 'line', line: 11, depth: 0 },   // 1
  { event: 'call', line: 2, depth: 1 },    // 2 helper
  { event: 'line', line: 3, depth: 1 },    // 3
  { event: 'call', line: 6, depth: 2 },    // 4 inner
  { event: 'line', line: 7, depth: 2 },    // 5
  { event: 'return', line: 7, depth: 2 },  // 6
  { event: 'line', line: 4, depth: 1 },    // 7
  { event: 'return', line: 4, depth: 1 },  // 8
  { event: 'line', line: 12, depth: 0 },   // 9
  { event: 'call', line: 2, depth: 1 },    // 10 helper again
  { event: 'line', line: 3, depth: 1 },    // 11
  { event: 'return', line: 4, depth: 1 },  // 12
  { event: 'line', line: 13, depth: 0 },   // 13
  { event: 'return', line: 13, depth: 0 }, // 14
]

test('step into moves to the next recorded step and stops at the end', () => {
  assert.equal(stepInto(steps, 1), 2)
  assert.equal(stepInto(steps, 14), 14)
})

test('step over skips nested calls at deeper depth', () => {
  assert.equal(stepOver(steps, 1), 9)   // over the helper call
  assert.equal(stepOver(steps, 3), 7)   // over inner, stays in helper
  assert.equal(stepOver(steps, 13), 14)
  assert.equal(stepOver(steps, 14), 14)
})

test('step out returns to the caller', () => {
  assert.equal(stepOut(steps, 5), 7)    // inner → helper
  assert.equal(stepOut(steps, 3), 9)    // helper → transform
  assert.equal(stepOut(steps, 1), 14)   // outermost: run to the end
})

test('step back moves one step and stops at the start', () => {
  assert.equal(stepBack(steps, 5), 4)
  assert.equal(stepBack(steps, 0), 0)
})

test('continue stops on the next breakpoint line, else at the end', () => {
  assert.equal(continueToBreakpoint(steps, 0, [3]), 3)
  assert.equal(continueToBreakpoint(steps, 3, [3]), 11)   // next hit, in the second call
  assert.equal(continueToBreakpoint(steps, 11, [3]), 14)
  assert.equal(continueToBreakpoint(steps, 0, new Set([7])), 5) // `return` events are not stops
  assert.equal(continueToBreakpoint([], 0, [1]), 0)
})

test('frameStep finds the most recent step in a stack frame', () => {
  assert.equal(frameStep(steps, 5, 0), 1)   // transform was last at step 1
  assert.equal(frameStep(steps, 5, 1), 3)
  assert.equal(frameStep(steps, 5, 2), 5)
})

test('globalsAt merges changes up to the step and marks this step\'s changes', () => {
  const recorded = [
    { event: 'call', line: 1, depth: 0, changedGlobals: [] },
    { event: 'line', line: 2, depth: 0, changedGlobals: [{ name: 'COUNT', type: 'int', repr: '1' }] },
    { event: 'line', line: 3, depth: 0, changedGlobals: [{ name: 'COUNT', type: 'int', repr: '2' }, { name: 'NEW', type: 'str', repr: "'x'" }] },
  ]
  const initial = [{ name: 'COUNT', type: 'int', repr: '0' }, { name: 'LIMIT', type: 'int', repr: '10' }]
  assert.deepEqual(globalsAt(initial, recorded, 0).map(v => [v.name, v.repr, v.changed]), [['COUNT', '0', false], ['LIMIT', '10', false]])
  assert.deepEqual(globalsAt(initial, recorded, 1).map(v => [v.name, v.repr, v.changed]), [['COUNT', '1', true], ['LIMIT', '10', false]])
  assert.deepEqual(globalsAt(initial, recorded, 2).map(v => [v.name, v.repr, v.changed]), [['COUNT', '2', true], ['LIMIT', '10', false], ['NEW', "'x'", true]])
  assert.equal(initial[0].repr, '0') // input is not mutated
})

test('stdoutAt returns the prefix printed by the step', () => {
  assert.equal(stdoutAt('one\ntwo\n', { event: 'line', line: 1, depth: 0, stdoutLength: 4 }), 'one\n')
  assert.equal(stdoutAt('one\n', undefined), 'one\n')
})

test('pythonSteps lists the flow\'s Python steps in flow order', () => {
  const flow = {
    framing: { mode: 'python', python: { library: 'hooks', function: 'split' } },
    parser: { decoders: [{ type: 'base64' }, { type: 'python', python: { library: 'hooks', function: 'strip' } }] },
    stages: [{ id: 'a', kind: 'filter' }, { id: 'enrich', kind: 'python', python: { library: '', function: '', inlineSource: 'def transform(f, m):\n    return f' } }],
    sinks: [{ type: 'stdout' }, { type: 'python', python: { library: 'out', function: 'emit' } }],
  }
  assert.deepEqual(pythonSteps(flow).map(step => [step.value, step.description]), [['framer', 'hooks.split'], ['decoder:1', 'hooks.strip'], ['enrich', 'inline Python'], ['sink:1', 'out.emit']])
  assert.deepEqual(pythonSteps({ stages: [] }), [])
})
