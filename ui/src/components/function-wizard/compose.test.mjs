import { test } from 'node:test'
import assert from 'node:assert/strict'
import { FUNCTION_TEMPLATES, discoverFunctions } from '../canvas/python-templates.ts'
import {
  keyValueModel,
  DEFAULT_ANSWERS, GOALS, addKeywordParams, assistantMessage, composeSource, conditionToPython, findTemplate, libraryNameProblem,
  librarySlug, pythonIdentifier, renameFunction, replaceBody, setDecoratorEvery, setDocstringSummary, setKeywordDefault, splitList, suggestName,
} from './compose.ts'

const answers = (overrides) => ({ ...DEFAULT_ANSWERS, ...overrides })

test('setKeywordDefault replaces string, list, and number defaults in the signature only', () => {
  const source = 'def f(fields: dict, *, url: str = "https://a", labels: list = ["a", "b"], threshold: float = 10.0) -> dict:\n    """url: the url = nothing"""\n'
  const out = setKeywordDefault(setKeywordDefault(setKeywordDefault(source, 'url', '"https://b"'), 'labels', '["x"]'), 'threshold', '2')
  assert.match(out, /url: str = "https:\/\/b", labels: list = \["x"\], threshold: float = 2\)/)
  assert.match(out, /the url = nothing/, 'docstring untouched')
  assert.equal(setKeywordDefault(source, 'missing', '1'), source, 'unknown keyword is a no-op')
})

test('addKeywordParams adds the star marker only when needed', () => {
  assert.equal(addKeywordParams('def s(fields: dict, meta: dict) -> None:\n    pass', 's', ['url: str = "u"']), 'def s(fields: dict, meta: dict, *, url: str = "u") -> None:\n    pass')
  assert.equal(addKeywordParams('def s(fields, *, a: int = 1):\n    pass', 's', ['b: int = 2']), 'def s(fields, *, a: int = 1, b: int = 2):\n    pass')
  assert.equal(addKeywordParams('def other():\n    pass', 's', ['b: int = 2']), 'def other():\n    pass')
})

test('renameFunction renames the decorator registration and the def it decorates', () => {
  const source = 'def _helper():\n    pass\n\n\n@gofi.parser("telemetry")\ndef parse_telemetry(data: bytes) -> dict:\n    return {}\n'
  const out = renameFunction(source, 'parser', 'events')
  assert.match(out, /@gofi\.parser\("events"\)\ndef events\(data: bytes\)/)
  assert.match(out, /def _helper\(\)/)
  assert.equal(renameFunction(source, 'sink', 'x'), source)
  assert.match(renameFunction('@gofi.transformer("transform")\ndef transform(f):\n    pass\n', 'transformer', 'enrich'), /^@gofi\.transformer\("enrich"\)\ndef enrich\(f\)/)
})

test('setDocstringSummary swaps the first docstring line and keeps the rest', () => {
  const source = 'def poll(ctx):\n    """Return items.\n\n    ctx.state is kept.\n    """\n    return []\n'
  const out = setDocstringSummary(source, 'poll', 'Poll "the" API.')
  assert.match(out, /"""Poll the API\.\n\n    ctx\.state is kept\./)
  assert.equal(setDocstringSummary('def poll(ctx):\n    return []\n', 'poll', 'x'), 'def poll(ctx):\n    return []\n')
})

test('replaceBody keeps the docstring and the following definitions', () => {
  const source = 'import gofi\n\n\n@gofi.sink("s")\ndef s(fields, meta) -> None:\n    """Doc."""\n    print(fields)\n    print(meta)\n\n\n@gofi.sink("t")\ndef t(fields, meta):\n    pass\n'
  const out = replaceBody(source, 's', 'if fields:\n    raise RuntimeError("x")')
  assert.equal(out, 'import gofi\n\n\n@gofi.sink("s")\ndef s(fields, meta) -> None:\n    """Doc."""\n    if fields:\n        raise RuntimeError("x")\n\n\n@gofi.sink("t")\ndef t(fields, meta):\n    pass\n')
  const multi = replaceBody('def p(data):\n    """One.\n\n    Two.\n    """\n    a = 1\n    return a\n', 'p', 'return None')
  assert.equal(multi, 'def p(data):\n    """One.\n\n    Two.\n    """\n    return None\n')
})

test('small helpers', () => {
  assert.deepEqual(splitList(' a, b\nc,,a '), ['a', 'b', 'c'])
  assert.equal(pythonIdentifier('Parse JSON!'), 'parse_json')
  assert.equal(pythonIdentifier('1st'), '_1st')
  assert.equal(librarySlug('My HTTP Sink!'), 'my-http-sink')
  assert.equal(libraryNameProblem(''), 'required')
  assert.equal(libraryNameProblem('Bad_Name'), 'format')
  assert.equal(libraryNameProblem('-x'), 'format')
  assert.equal(libraryNameProblem('ai-enrich-2'), '')
  assert.match(setDecoratorEvery('@gofi.fetcher("p", every="30s")', '5m'), /every="5m"/)
  assert.equal(findTemplate(FUNCTION_TEMPLATES, ['nope', 'sink']).id, 'sink')
})

test('conditionToPython reads field-operator-value sentences', () => {
  assert.equal(conditionToPython('temperature > 30'), 'float(fields.get("temperature") or 0) > 30')
  assert.equal(conditionToPython('level is error'), 'fields.get("level") == "error"')
  assert.equal(conditionToPython('status is not "ok"'), 'fields.get("status") != "ok"')
  assert.equal(conditionToPython('message contains timeout'), '"timeout" in str(fields.get("message") or "")')
  assert.equal(conditionToPython('enabled equals true'), 'fields.get("enabled") == True')
  assert.equal(conditionToPython('only the important ones please'), null)
  assert.equal(conditionToPython(''), null)
})

test('composeSource registers exactly one function of the expected kind for every goal', () => {
  const kinds = { parse: 'parser', transform: 'transformer', filter: 'transformer', sink: 'sink', source: 'fetcher', 'ai-classify': 'transformer', 'ai-enrich': 'transformer', 'ai-extract': 'transformer', 'ai-sink': 'sink' }
  for (const goal of GOALS) {
    const { source } = composeSource(answers({ goal }), FUNCTION_TEMPLATES)
    assert.match(source, /^import gofi\n/, goal)
    const found = discoverFunctions(source)
    assert.equal(found.length, 1, `${goal}: ${found.map(fn => fn.name)}`)
    assert.equal(found[0].kind, kinds[goal], goal)
  }
})

test('composeSource: parse JSON with a sample saves a test and documents the example', () => {
  const result = composeSource(answers({ goal: 'parse', parseFormat: 'json', sample: '{"a": 1}', functionName: 'My Events' }), FUNCTION_TEMPLATES)
  assert.match(result.source, /^import json$/m)
  assert.match(result.source, /@gofi\.parser\("my_events"\)\ndef my_events\(data: bytes\) -> dict \| None:\n    """Parse one JSON object per frame into fields\. Example input: \{a: 1\}"""\n    return json\.loads\(data\)\n/)
  assert.deepEqual(result.test, { name: 'Sample input', function: 'my_events', kind: 'parser', raw: '{"a": 1}' })
  assert.equal(result.templateId, 'parser')
  const csv = composeSource(answers({ goal: 'parse', parseFormat: 'csv', columns: 'time, host, value' }), FUNCTION_TEMPLATES).source
  assert.match(csv, /^import csv$/m)
  assert.match(csv, /^COLUMNS = \["time", "host", "value"\]$/m)
  assert.match(csv, /dict\(zip\(COLUMNS, values\)\)/)
  const binary = composeSource(answers({ goal: 'parse', parseFormat: 'binary' }), FUNCTION_TEMPLATES).source
  assert.match(binary, /partition|struct|raw/, 'binary keeps a body')
})

test('composeSource: transform and filter rewrite the transformer body', () => {
  const rename = composeSource(answers({ goal: 'transform', transformAction: 'rename', field: 'temp', transformValue: 'temperature', sample: '{"temp": 1}' }), FUNCTION_TEMPLATES)
  assert.match(rename.source, /fields\["temperature"\] = fields\.pop\("temp"\)/)
  assert.deepEqual(rename.test?.fields, { temp: 1 })
  const threshold = composeSource(answers({ goal: 'transform', transformAction: 'threshold', field: 'temperature', transformValue: '42' }), FUNCTION_TEMPLATES).source
  assert.match(threshold, /threshold: float = 42,/)
  assert.match(threshold, /fields\.get\("temperature", 0\)/)
  const filter = composeSource(answers({ goal: 'filter', condition: 'level is error', functionName: 'errors only' }), FUNCTION_TEMPLATES).source
  assert.match(filter, /def errors_only\(fields: dict, meta: dict\) -> dict:/)
  assert.match(filter, /keep = fields\.get\("level"\) == "error"\n    return fields if keep else None/)
  const vague = composeSource(answers({ goal: 'filter', condition: 'the interesting ones' }), FUNCTION_TEMPLATES).source
  assert.match(vague, /# Keep a message when: the interesting ones\n    keep = True  # TODO/)
})

test('composeSource: HTTP sink gets url and timeout keywords and the imports it uses', () => {
  const { source } = composeSource(answers({ goal: 'sink', sinkKind: 'http', url: 'https://hooks.example/in' }), FUNCTION_TEMPLATES)
  assert.match(source, /def deliver\(fields: dict, meta: dict, \*, url: str = "https:\/\/hooks\.example\/in", timeout: float = 10\.0\) -> None:/)
  assert.match(source, /^import json$/m)
  assert.match(source, /^import urllib\.request$/m)
  assert.match(source, /urllib\.request\.urlopen\(request, timeout=timeout\)/)
})

test('composeSource: sources substitute the schedule and streamer interval', () => {
  const fetcher = composeSource(answers({ goal: 'source', sourceKind: 'fetcher', url: 'https://api.example/v2', every: '5m' }), FUNCTION_TEMPLATES).source
  assert.match(fetcher, /@gofi\.fetcher\("poll", every="5m"\)/)
  assert.match(fetcher, /url: str = "https:\/\/api\.example\/v2"/)
  const streamer = composeSource(answers({ goal: 'source', sourceKind: 'streamer', every: '2.5' }), FUNCTION_TEMPLATES)
  assert.match(streamer.source, /interval: float = 2\.5/)
  assert.match(streamer.source, /@gofi\.streamer\("poll"\)\ndef poll\(/)
})

test('composeSource: AI goals substitute labels, prompts, and output keys', () => {
  const classify = composeSource(answers({ goal: 'ai-classify', labels: 'ok, warn, bad', dropLabels: 'ok, unknown', question: 'How bad is it?' }), FUNCTION_TEMPLATES).source
  assert.match(classify, /labels: list = \["ok", "warn", "bad"\]/)
  assert.match(classify, /drop: list = \["ok"\]/, 'drop keeps only known labels')
  assert.match(classify, /instructions: str = "How bad is it\?"/)
  assert.match(classify, /"""Label each message as one of ok, warn, bad and drop ok\./)
  const enrich = composeSource(answers({ goal: 'ai-enrich', question: 'Is this urgent?', outputField: 'Urgent?' }), FUNCTION_TEMPLATES).source
  assert.match(enrich, /prompt: str = "Is this urgent\?\\n\\n\{fields\}"/)
  assert.match(enrich, /output_key: str = "urgent"/)
  const extract = composeSource(answers({ goal: 'ai-extract', question: 'host, port', outputField: '' }), FUNCTION_TEMPLATES).source
  assert.match(extract, /Extract host, port from this event/)
  assert.match(extract, /    host: str \| None = None\n    port: str \| None = None\n/, 'the model lists the wanted fields')
  assert.doesNotMatch(extract, /vendor: str/, 'template placeholder fields are replaced')
  const sink = composeSource(answers({ goal: 'ai-sink', question: 'Open a ticket.' }), FUNCTION_TEMPLATES).source
  assert.match(sink, /prompt: str = "Open a ticket\.\\n\\n\{fields\}"/)
  assert.match(sink, /@gofi\.sink\("handle"\)/)
})

test('composeSource falls back to a minimal function when no template matches', () => {
  const { source, templateId } = composeSource(answers({ goal: 'ai-enrich', question: 'Why?' }), [])
  assert.equal(templateId, undefined)
  assert.equal(discoverFunctions(source).length, 1)
  assert.match(source, /@gofi\.transformer\("enrich"\)/)
})

test('suggestName and assistantMessage reflect the answers', () => {
  assert.equal(suggestName(answers({ goal: 'parse', parseFormat: 'keyvalue' })), 'parse-key-value')
  assert.equal(suggestName(answers({ goal: 'transform', transformAction: 'rename', field: 'Temp' })), 'rename-temp')
  assert.equal(libraryNameProblem(suggestName(answers({ goal: 'filter', condition: '' }))), '')
  const message = assistantMessage(answers({ goal: 'filter', condition: 'level is error' }), 'import gofi\n')
  assert.match(message, /keep only messages where level is error/)
  assert.match(message, /```python\nimport gofi\n```$/)
})

test('the key=value parser tokenizes with a regex and types fields with a pydantic model from the sample', () => {
  const { source } = composeSource(answers({ goal: 'parse', parseFormat: 'keyvalue', sample: 'host=a1 temperature=31.5\nok=true, src-ip="10.0.0.1" count=3' }), FUNCTION_TEMPLATES)
  assert.match(source, /^import re$/m)
  assert.match(source, /^from pydantic import BaseModel, ConfigDict, Field, ValidationError$/m)
  assert.match(source, /^PAIR = re\.compile\(/m)
  assert.match(source, /class Fields\(BaseModel\):/)
  assert.match(source, /    host: str \| None = None\n    temperature: float \| None = None\n    ok: bool \| None = None\n    src_ip: str \| None = Field\(default=None, alias="src-ip"\)\n    count: int \| None = None/)
  assert.match(source, /Fields\.model_validate\(pairs\)\.model_dump\(\)/)
  assert.equal(discoverFunctions(source).length, 1)
  assert.match(keyValueModel(''), /# Add the fields you expect/)
})
