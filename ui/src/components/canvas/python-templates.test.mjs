import { test } from 'node:test'
import assert from 'node:assert/strict'
import { insertTemplate, configWithDefaults, coerceParam, missingParams, evaluateTest, suggestLibraryName, promoteInlineSource, FUNCTION_TEMPLATES, TEMPLATE_GROUPS, DEFAULT_LIBRARY } from './python-templates.ts'

test('insertTemplate adds import gofi only when missing', () => {
  const added = insertTemplate('', FUNCTION_TEMPLATES[0].source)
  assert.match(added, /^import gofi\n/)
  const kept = insertTemplate('import gofi\n\nx = 1', '@gofi.sink("s")\ndef s(f):\n    pass')
  assert.equal(kept.match(/import gofi/g).length, 1)
  assert.ok(kept.endsWith('pass\n'))
})

test('templates cover every step decorator and never the removed ingress hook', () => {
  const all = FUNCTION_TEMPLATES.map((template) => template.source).join('\n') + DEFAULT_LIBRARY
  for (const kind of ['fetcher', 'streamer', 'framer', 'decoder', 'parser', 'transformer', 'encoder', 'sink']) assert.match(all, new RegExp(`@gofi\\.${kind}\\b`))
  assert.doesNotMatch(all, /@gofi\.(ingress|port|listener)\b/)
})

test('configWithDefaults overlays saved values on declared defaults', () => {
  const params = [{ name: 'threshold', type: 'float', default: 10 }, { name: 'label', type: 'str', required: true }]
  assert.deepEqual(configWithDefaults(params, { label: 'hot' }), { threshold: 10, label: 'hot' })
  assert.deepEqual(missingParams(params, { threshold: 1 }), ['label'])
})

test('coerceParam parses by declared type', () => {
  assert.equal(coerceParam('int', '42'), 42)
  assert.equal(coerceParam('float', '2.5'), 2.5)
  assert.equal(coerceParam('bool', 'true'), true)
  assert.deepEqual(coerceParam('list', '[1,2]'), [1, 2])
  assert.throws(() => coerceParam('int', 'x'))
  assert.throws(() => coerceParam('dict', '[1]'))
  assert.equal(coerceParam('str', 'abc'), 'abc')
})

test('evaluateTest compares expectations by subset', () => {
  assert.equal(evaluateTest({}, { fields: { a: 1 } }).status, 'recorded')
  assert.equal(evaluateTest({ expectFields: { a: 1 } }, { fields: { a: 1, b: 2 } }).status, 'pass')
  assert.equal(evaluateTest({ expectFields: { a: 2 } }, { fields: { a: 1 } }).status, 'fail')
  assert.equal(evaluateTest({ expectDrop: true }, { drop: true }).status, 'pass')
  assert.equal(evaluateTest({ expectDrop: false }, { error: 'boom' }).status, 'fail')
})

test('promotion helpers produce valid names and decorated source', () => {
  assert.equal(suggestLibraryName('Telemetry Demo', 'stage_1'), 'telemetry-demo-stage-1')
  assert.match(suggestLibraryName('123', 'x'), /^[a-z]/)
  const promoted = promoteInlineSource('def transform(fields, meta):\n    return fields\n', 'enrich-1')
  assert.match(promoted, /^@gofi\.transformer\("enrich_1"\)\ndef enrich_1\(fields, meta\):/)
})

test('discoverFunctions finds every step decorator without executing', async () => {
  const { discoverFunctions } = await import('./python-templates.ts')
  const found = discoverFunctions(DEFAULT_LIBRARY + '\n@gofi.framer("split")\ndef split(b, final):\n    return [], b\n\n@gofi.encoder\ndef pack(d):\n    return d\n\n@gofi.ingress(gofi.UDP(7001))\ndef removed(d):\n    return d\n\n@gofi.sink\ndef plain(f):\n    pass\n')
  const byKey = Object.fromEntries(found.map(fn => [`${fn.kind}:${fn.name}`, fn]))
  assert.ok(byKey['decoder:trim'])
  assert.ok(byKey['framer:split'])
  assert.ok(byKey['encoder:pack'])
  assert.equal(found.some((fn) => fn.name === 'removed'), false, 'the removed ingress decorator is not a step')
  assert.ok(byKey['parser:telemetry'], 'parser registered under its decorator name, not the def name')
  assert.equal(byKey['parser:parse_telemetry'], undefined)
  assert.equal(byKey['transformer:enrich'].kind, 'transformer')
  assert.ok(byKey['sink:plain'])
})

test('discoverFunctions reports the def line where input enters, past stacked decorators', async () => {
  const { discoverFunctions } = await import('./python-templates.ts')
  const [fn] = discoverFunctions('import gofi\n\n@gofi.transformer("t")\n@other\n\ndef t(fields):\n    return fields\n')
  assert.equal(fn.line, 3)
  assert.equal(fn.defLine, 6)
})

test('promotionConflict refuses names the target library already uses', async () => {
  const { promotionConflict } = await import('./python-templates.ts')
  const library = 'import gofi\n\n@gofi.transformer("c2_inline")\ndef c2_inline(fields, meta):\n    return fields\n\ndef helper(x):\n    return x\n'
  assert.match(promotionConflict(library, 'c2_inline'), /registers a transformer/)
  assert.match(promotionConflict(library, 'c2-inline'), /registers a transformer/) // sanitized the same way as the promotion
  assert.match(promotionConflict(library, 'helper'), /defines a function/)
  assert.equal(promotionConflict(library, 'fresh'), '')
})

test('worker log helpers: severity, bounding, filtering, time, ids', async () => {
  const { logSeverity, appendBounded, matchesLogFilter, logTime, shortId, newRunId } = await import('./python-templates.ts')
  assert.equal(logSeverity({ stream: 'stderr', line: 'x' }), 'error')
  assert.equal(logSeverity({ stream: 'stdout', line: 'x' }), 'info')
  assert.equal(logSeverity({ stream: 'system', line: 'x', level: 'warn' }), 'warn')
  assert.equal(logSeverity({ stream: 'system', line: 'x' }), 'system')
  assert.deepEqual(appendBounded([1, 2, 3], [4, 5], 3), [3, 4, 5])
  assert.deepEqual(appendBounded([1], [2], 3), [1, 2])
  const entry = { stream: 'stdout', line: 'enriching alpha', function: 'enrich', invocationId: 'run-abc' }
  assert.ok(matchesLogFilter(entry, 'ALPHA'))
  assert.ok(matchesLogFilter(entry, 'enrich'))
  assert.ok(!matchesLogFilter(entry, 'beta'))
  assert.ok(matchesLogFilter(entry, '', 'run-abc'))
  assert.ok(!matchesLogFilter(entry, '', 'run-other'))
  assert.equal(logTime('2026-10-16T09:27:56.43Z'), '09:27:56.430')
  assert.equal(logTime('2026-10-16T09:27:56Z'), '09:27:56.000')
  assert.equal(logTime(undefined), '')
  assert.equal(shortId('run-0123456789'), 'run-0123')
  assert.match(newRunId(), /^run-[0-9a-f]{16}$/)
  assert.notEqual(newRunId(), newRunId())
})

test('highlight helpers: input preview, line ranges, unwrapping inline lines', async () => {
  const { inputPreview, lineRanges, unwrapLines } = await import('./python-templates.ts')
  assert.equal(inputPreview({ fields: { sensor: 'a', temperature: 1 } }), '{sensor, temperature}')
  assert.equal(inputPreview({ fields: { a: 1, b: 2, c: 3, d: 4, e: 5 } }), '{a, b, c, d, …}')
  assert.equal(inputPreview({ raw: '  {"x":\n 1}  ' }), '{"x": 1}')
  assert.equal(inputPreview({ raw: 'x'.repeat(60) }, 10), 'xxxxxxxxx…')
  assert.equal(inputPreview({}), '')
  assert.deepEqual(lineRanges([5, 3, 4, 4, 9, 0, -1, 10]), [[3, 5], [9, 10]])
  assert.deepEqual(lineRanges([]), [])
  // A 3-line wrapper above a 4-line snippet: wrapper and out-of-range lines drop.
  assert.deepEqual(unwrapLines([1, 3, 4, 5, 7, 8], 3, 4), [1, 2, 4])
})

test('AI templates: wizard ids and keyword names, gofi.llm only, no provider, endpoint, or key', async () => {
  const { discoverFunctions } = await import('./python-templates.ts')
  const ai = FUNCTION_TEMPLATES.filter((template) => template.id.startsWith('ai-'))
  assert.deepEqual(ai.map((template) => template.id), ['ai-classify', 'ai-enrich', 'ai-extract', 'ai-sink'])
  for (const template of ai) {
    assert.match(template.label, /^AI /)
    assert.match(template.source, /gofi\.llm\.(classify|chat|extract)\(/, template.id)
    assert.match(template.source, /^# Needs "Functions may use the AI endpoint" on under Administration › AI assistant; GoFi supplies the endpoint and egress\.$/m, template.id)
    assert.doesNotMatch(template.source, /api_key|base_url|provider|langchain_|egress:|https?:\/\//, `${template.id} names no provider, endpoint, or key`)
    assert.doesNotMatch(template.source, /[`]|\$\{/, `${template.id} is a plain template literal`)
    assert.equal(discoverFunctions(insertTemplate('', template.source)).length, 1, template.id)
  }
  const found = discoverFunctions(ai.map((template) => template.source).join('\n'))
  assert.deepEqual(found.map((fn) => `${fn.kind}:${fn.name}`), ['transformer:ai_classify', 'transformer:ai_enrich', 'transformer:ai_extract', 'sink:ai_notify'])
  // The new-function wizard prefills these keyword parameters by name.
  assert.match(ai[0].source, /\*, labels: list = \[.*\], drop: list = \[.*\], instructions: str = /)
  assert.match(ai[1].source, /\*, prompt: str = ".*\{fields\}.*", output_key: str = "summary"\)/)
  assert.match(ai[2].source, /\*, prompt: str = "Extract .*\{fields\}", output_key: str = "extracted"\)/)
  assert.match(ai[2].source, /class Extracted\(BaseModel\):\n    \"\"\"[^\n]*\"\"\"\n\n    vendor: str \| None = None\n/, 'the wizard rewrites the model fields')
  assert.match(ai[2].source, /gofi\.llm\.extract\(.*, Extracted\)/)
  assert.match(ai[3].source, /\*, prompt: str = ".*\{fields\}.*"\) -> None/)
})

test('every template belongs to exactly one picker group', () => {
  const grouped = TEMPLATE_GROUPS.flatMap(group => group.ids)
  assert.deepEqual([...grouped].sort(), FUNCTION_TEMPLATES.map(template => template.id).sort())
  assert.equal(new Set(grouped).size, grouped.length)
  for (const group of TEMPLATE_GROUPS) assert.ok(group.label)
})
