import assert from 'node:assert/strict'
import { test } from 'node:test'
import { durationSeconds, everyProblem, parseConfigText, pythonSourceProblems, sourceFunctionOf, sourceFunctionOptions, sourceFunctions, sourceLibraries } from './sources/pythonSource.ts'
import { discoverFunctions } from './python-templates.ts'
import { sourceSummary, sourceTypeOf } from '../../types.ts'

const FUNCTIONS = [
  { library: 'radar', name: 'poll', kind: 'fetcher', every: '10s', description: 'Poll radar\nmore' },
  { library: 'radar', name: 'feed', kind: 'streamer' },
  { library: 'radar', name: 'normalize', kind: 'transformer' },
  { library: 'parsers', name: 'json', kind: 'parser' },
  { library: 'flow-x-1234', name: 'stage', kind: 'fetcher', managedBy: 'flow:x' },
]

test('only fetchers and streamers of user libraries are offered', () => {
  assert.deepEqual(sourceFunctions(FUNCTIONS).map(fn => fn.name), ['poll', 'feed'])
  assert.deepEqual(sourceLibraries(FUNCTIONS), ['radar'])
  assert.deepEqual(sourceLibraries(FUNCTIONS, 'gone'), ['gone', 'radar'])
  const options = sourceFunctionOptions(FUNCTIONS, 'radar')
  assert.deepEqual(options.map(o => o.value), ['poll', 'feed'])
  assert.equal(options[0].description, 'fetcher · every 10s · Poll radar')
  // A saved choice that is not (or no longer) a source stays visible, marked.
  assert.match(sourceFunctionOptions(FUNCTIONS, 'radar', 'normalize').at(-1).label, /unavailable/)
  assert.equal(sourceFunctionOf(FUNCTIONS, { library: 'radar', function: 'feed' })?.kind, 'streamer')
  assert.equal(sourceFunctionOf(FUNCTIONS, { library: 'radar', function: 'normalize' }), undefined)
})

test('every mirrors the server: Go durations of at least 1s', () => {
  assert.equal(durationSeconds('1m30s'), 90)
  assert.equal(durationSeconds('500ms'), 0.5)
  assert.equal(durationSeconds('10'), undefined)
  assert.equal(everyProblem(undefined), undefined)
  assert.equal(everyProblem('10s'), undefined)
  assert.ok(everyProblem('500ms'))
  assert.ok(everyProblem('soon'))
})

test('config text is a JSON object or empty', () => {
  assert.deepEqual(parseConfigText(''), {})
  assert.deepEqual(parseConfigText('{}'), {})
  assert.deepEqual(parseConfigText('{"limit": 3}'), { config: { limit: 3 } })
  assert.ok(parseConfigText('[1]').error)
  assert.ok(parseConfigText('{bad').error)
})

test('save problems include the registry kind check when it is loaded', () => {
  assert.deepEqual(pythonSourceProblems({ library: '', function: '' }), [
    { kind: 'missing-library', message: 'Choose a library.' },
    { kind: 'missing-function', message: 'Choose a fetcher or streamer.' },
  ])
  assert.deepEqual(pythonSourceProblems({ library: 'radar', function: 'poll', every: '2s' }, FUNCTIONS), [])
  assert.deepEqual(pythonSourceProblems({ library: 'radar', function: 'poll', every: 'soon' }, FUNCTIONS).map(p => p.kind), ['every'])
  const wrongKind = pythonSourceProblems({ library: 'radar', function: 'normalize' }, FUNCTIONS)
  assert.deepEqual(wrongKind.map(p => p.kind), ['wrong-kind'])
  assert.match(wrongKind[0].message, /is a transformer/)
  assert.deepEqual(pythonSourceProblems({ library: 'radar', function: 'normalize' }), [])
})

test('python sources are a source type with a summary, and the editor detects them', () => {
  const source = { python: { library: 'radar', function: 'poll', every: '10s' } }
  assert.equal(sourceTypeOf(source), 'python')
  assert.equal(sourceSummary(source), 'Python radar.poll every 10s')
  const found = discoverFunctions('import gofi\n\n@gofi.fetcher(every="5s")\ndef poll(ctx):\n    return b""\n\n@gofi.streamer("feed")\nasync def stream(ctx):\n    yield b""\n')
  assert.deepEqual(found.map(fn => [fn.kind, fn.name]), [['fetcher', 'poll'], ['streamer', 'feed']])
})
