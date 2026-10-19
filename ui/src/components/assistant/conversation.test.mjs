import assert from 'node:assert/strict'
import { test } from 'node:test'
import { addHighlight, addNavigation, addProposal, addToolCall, addToolResult, appendText, decideProposal, decideToolCall, failProposal, failToolCall, finishTurn, loadConversationId, pendingProposals, pendingToolCalls, saveConversationId, startExchange, turnsFromTranscript } from './conversation.ts'
import { clampToolSteps, codeLanguage, outputLanguage, pageContextOf, prettyJson, proposalInput, summarizeInput, toolInputView, truncateOutput } from './helpers.ts'

const ids = { user: 'u1', assistant: 'a1' }

test('an exchange starts with the user turn and an empty streaming reply', () => {
  const turns = startExchange([], 'hi', ids)
  assert.deepEqual(turns, [
    { kind: 'user', id: 'u1', text: 'hi' },
    { kind: 'assistant', id: 'a1', text: '', tools: [], proposals: [], highlights: [], opened: [], streaming: true },
  ])
})

test('portal tools never become rows; highlights and opened pages are recorded once', () => {
  let turns = startExchange([], 'hi', ids)
  turns = addToolCall(turns, { id: 'p1', name: 'point_at_flow_block', inputJson: '{"flow":"c2-cfg","block":"stage:py1"}' })
  turns = addToolResult(turns, { id: 'p1', name: 'point_at_flow_block', output: 'highlighted stage:py1 of c2-cfg for the user', isError: false })
  turns = addToolCall(turns, { id: 'o1', name: 'open_page', inputJson: '{"page":"flow","name":"c2-cfg"}' })
  turns = addToolResult(turns, { id: 'o1', name: 'open_page', output: 'opened #/flows/c2-cfg', isError: false })
  assert.deepEqual(turns[1].tools, [])
  turns = addHighlight(turns, { flow: 'c2-cfg', block: 'stage:py1', note: 'first' })
  turns = addHighlight(turns, { flow: 'c2-cfg', block: 'stage:py1', note: 'second' })
  turns = addHighlight(turns, { flow: 'c2-cfg', block: 'sink:0', note: '' })
  assert.deepEqual(turns[1].highlights, [{ flow: 'c2-cfg', block: 'stage:py1', note: 'second' }, { flow: 'c2-cfg', block: 'sink:0', note: '' }])
  turns = addNavigation(turns, '#/flows/c2-cfg')
  turns = addNavigation(turns, '#/flows/c2-cfg')
  turns = addNavigation(turns, '')
  assert.deepEqual(turns[1].opened, ['#/flows/c2-cfg'])
})

test('a paused tool call waits for approval, then the stream fills its result', () => {
  let turns = startExchange([], 'hi', ids)
  turns = addToolCall(turns, { id: 't1', name: 'list_flows', inputJson: '{}', needsApproval: true })
  turns = addToolCall(turns, { id: 't2', name: 'get_flow', inputJson: '{}' })
  assert.equal(pendingToolCalls(turns).length, 1)
  assert.equal('needsApproval' in turns[1].tools[1], false)
  turns = failToolCall(turns, 't1', 'offline')
  assert.equal(turns[1].tools[0].error, 'offline')
  turns = decideToolCall(turns, 't1', true)
  assert.deepEqual(turns[1].tools[0], { id: 't1', name: 'list_flows', inputJson: '{}', done: false, needsApproval: true, decision: 'approved' })
  assert.deepEqual(pendingToolCalls(turns), [])
  turns = addToolResult(turns, { id: 't1', name: 'list_flows', output: '[]', isError: false })
  assert.equal(turns[1].tools[0].done, true)
  assert.equal(decideToolCall(turns, 'missing', false), turns)
  const denied = decideToolCall(turns, 't2', false)
  assert.equal(denied[1].tools[1].decision, 'denied')
})

test('a stored transcript rebuilds the turns', () => {
  let n = 0
  const nextId = () => `id${++n}`
  const turns = turnsFromTranscript({
    messages: [
      { role: 'user', text: 'which flow has a problem', calls: [], results: [] },
      { role: 'assistant', text: '', calls: [{ id: 'c1', name: 'list_flows', inputJson: '{}' }, { id: 'c2', name: 'point_at_flow_block', inputJson: '{"flow":"d-qa","block":"source","note":"port busy"}' }, { id: 'c3', name: 'open_page', inputJson: '{"page":"flow","name":"d-qa"}' }, { id: 'c4', name: 'apply_flow', inputJson: '{"flow_yaml":"name: d-qa"}' }], results: [] },
      { role: 'tool', text: '', calls: [], results: [{ id: 'c1', name: 'list_flows', output: '- name: d-qa', isError: false }, { id: 'c2', name: 'point_at_flow_block', output: 'highlighted source of d-qa for the user', isError: false }, { id: 'c3', name: 'open_page', output: 'opened #/flows/d-qa', isError: false }] },
      { role: 'assistant', text: 'd-qa cannot bind its port.', calls: [{ id: 'c5', name: 'get_flow', inputJson: '{"name":"d-qa"}' }], results: [] },
    ],
    pendingProposals: [{ id: 'c4', name: 'apply_flow', inputJson: '{"flow_yaml":"name: d-qa"}', summary: 'save flow d-qa' }],
  }, nextId)
  assert.deepEqual(turns, [
    { kind: 'user', id: 'id1', text: 'which flow has a problem' },
    { kind: 'assistant', id: 'id2', text: '', streaming: false, tools: [{ id: 'c1', name: 'list_flows', inputJson: '{}', output: '- name: d-qa', isError: false, done: true }], proposals: [], highlights: [{ flow: 'd-qa', block: 'source', note: 'port busy' }], opened: ['#/flows/d-qa'] },
    { kind: 'assistant', id: 'id3', text: 'd-qa cannot bind its port.', streaming: false, tools: [{ id: 'c5', name: 'get_flow', inputJson: '{"name":"d-qa"}', done: true }], proposals: [{ id: 'c4', name: 'apply_flow', inputJson: '{"flow_yaml":"name: d-qa"}', summary: 'save flow d-qa' }], highlights: [], opened: [] },
  ])
  assert.equal(pendingProposals(turns).length, 1)
  // Pending proposals with no assistant turn get one of their own.
  const only = turnsFromTranscript({ messages: [{ role: 'user', text: 'save', calls: [], results: [] }], pendingProposals: [{ id: 'p', name: 'apply_flow', inputJson: '{}', summary: 's' }] }, nextId)
  assert.equal(only.length, 2)
  assert.equal(only[1].kind, 'assistant')
  assert.equal(only[1].proposals.length, 1)
  assert.deepEqual(turnsFromTranscript({ messages: [], pendingProposals: [] }, nextId), [])
})

test('tool rows: input split into fields and code, output language, one-line summary', () => {
  assert.deepEqual(toolInputView('{"name":"x","flow_yaml":"name: x\\nstages: []\\n"}'), { fields: '{\n  "name": "x"\n}', code: [{ key: 'flow_yaml', language: 'yaml', source: 'name: x\nstages: []\n' }] })
  assert.deepEqual(toolInputView('{"source":"import gofi\\n"}'), { fields: '', code: [{ key: 'source', language: 'python', source: 'import gofi\n' }] })
  assert.deepEqual(toolInputView('{"expression":"a\\n&& b"}').code[0].language, 'cel')
  assert.deepEqual(toolInputView(''), { fields: '', code: [] })
  assert.deepEqual(toolInputView('nope'), { fields: 'nope', code: [] })
  assert.deepEqual(toolInputView('[1]'), { fields: '[\n  1\n]', code: [] })
  assert.equal(outputLanguage('- name: a\n  sinks:\n  - ""'), 'yaml')
  assert.equal(outputLanguage('name: a\nstate: running'), 'yaml')
  assert.equal(outputLanguage('{"a":1}'), 'json')
  assert.equal(outputLanguage('saved flow x'), 'plaintext')
  assert.equal(outputLanguage(''), 'plaintext')
  assert.equal(summarizeInput('{"name":"d-qa","flow_yaml":"a\\nb","n":2,"ok":true,"list":[1,2],"obj":{},"empty":""}'), 'name: d-qa · flow_yaml: 2 lines · n: 2 · ok: true · list: [2] · obj: {…}')
  assert.equal(summarizeInput('{}'), '')
  assert.equal(summarizeInput('x'.repeat(100), 10).length, 10)
  assert.equal(summarizeInput('{"name":"' + 'x'.repeat(100) + '"}', 20), 'name: ' + 'x'.repeat(13) + '…')
  assert.equal(codeLanguage('YAML'), 'yaml'); assert.equal(codeLanguage('py'), 'python'); assert.equal(codeLanguage('bash'), 'shell'); assert.equal(codeLanguage('cel'), 'cel'); assert.equal(codeLanguage('weird'), 'plaintext'); assert.equal(codeLanguage(''), 'plaintext')
})

test('text deltas append to the last assistant turn only', () => {
  let turns = startExchange([], 'hi', ids)
  turns = appendText(turns, 'Hel')
  turns = appendText(turns, 'lo')
  turns = appendText(turns, '')
  assert.equal(turns[1].text, 'Hello')
  assert.equal(turns[0].text, 'hi')
  assert.deepEqual(appendText([], 'x'), [])
})

test('tool calls get their result by id; unknown results become rows', () => {
  let turns = startExchange([], 'hi', ids)
  turns = addToolCall(turns, { id: 't1', name: 'list_flows', inputJson: '{}' })
  turns = addToolResult(turns, { id: 't1', name: 'list_flows', output: '[]', isError: false })
  turns = addToolResult(turns, { id: 't2', name: 'get_flow', output: 'boom', isError: true })
  assert.deepEqual(turns[1].tools, [
    { id: 't1', name: 'list_flows', inputJson: '{}', output: '[]', isError: false, done: true },
    { id: 't2', name: 'get_flow', inputJson: '', output: 'boom', isError: true, done: true },
  ])
})

test('finishing stops streaming, settles pending tools and keeps the outcome', () => {
  let turns = startExchange([], 'hi', ids)
  turns = addToolCall(turns, { id: 't1', name: 'x', inputJson: '' })
  const done = finishTurn(turns, { stopReason: 'end_turn' })
  assert.equal(done[1].streaming, false)
  assert.equal(done[1].stopReason, 'end_turn')
  assert.equal(done[1].tools[0].done, true)
  const failed = finishTurn(turns, { error: 'nope' })
  assert.equal(failed[1].error, 'nope')
  assert.equal('stopReason' in failed[1], false)
})

test('finishing by id leaves a newer exchange streaming', () => {
  let turns = startExchange([], 'first', { user: 'u1', assistant: 'a1' })
  turns = startExchange(turns, 'second', { user: 'u2', assistant: 'a2' })
  turns = finishTurn(turns, { error: 'cancelled' }, 'a1')
  assert.equal(turns[1].streaming, false)
  assert.equal(turns[1].error, 'cancelled')
  assert.equal(turns[3].streaming, true)
  assert.deepEqual(finishTurn(turns, {}, 'missing'), turns)
})

const proposal = { id: 'p1', name: 'apply_flow', inputJson: '{"flow_yaml":"name: x\\n"}', summary: 'save flow x' }

test('proposals join the current assistant turn and stay pending until decided', () => {
  let turns = startExchange([], 'save it', ids)
  turns = addProposal(turns, proposal)
  assert.deepEqual(turns[1].proposals, [proposal])
  assert.deepEqual(pendingProposals(turns), [proposal])
  assert.deepEqual(addProposal([], proposal), [])
  // Finishing the turn (the model stops after describing it) leaves the proposal pending.
  turns = finishTurn(turns, { stopReason: 'end_turn' })
  assert.equal(turns[1].streaming, false)
  assert.equal(pendingProposals(turns).length, 1)
})

test('deciding records the outcome and clears an earlier failure', () => {
  let turns = addProposal(startExchange([], 'save it', ids), proposal)
  turns = failProposal(turns, 'p1', { message: 'changes are off' })
  assert.equal(turns[1].proposals[0].error, 'changes are off')
  assert.equal(pendingProposals(turns).length, 1)
  const approved = decideProposal(turns, 'p1', { approved: true, output: 'saved flow x', isError: false })
  assert.deepEqual(approved[1].proposals[0], { ...proposal, decision: 'approved', output: 'saved flow x', isError: false })
  assert.deepEqual(pendingProposals(approved), [])
  const rejected = decideProposal(turns, 'p1', { approved: false, output: 'rejected by the user', isError: false })
  assert.equal(rejected[1].proposals[0].decision, 'rejected')
  const failed = decideProposal(turns, 'p1', { approved: true, output: 'invalid yaml', isError: true })
  assert.equal(failed[1].proposals[0].isError, true)
})

test('an expired proposal stops being pending; unknown ids change nothing', () => {
  let turns = addProposal(startExchange([], 'save it', ids), proposal)
  // Proposals in an older turn are still found after a newer exchange starts.
  turns = startExchange(turns, 'later', { user: 'u2', assistant: 'a2' })
  const expired = failProposal(turns, 'p1', { expired: true })
  assert.equal(expired[1].proposals[0].decision, 'expired')
  assert.equal('error' in expired[1].proposals[0], false)
  assert.deepEqual(pendingProposals(expired), [])
  assert.equal(expired[3].proposals.length, 0)
  assert.equal(decideProposal(turns, 'missing', { approved: true, output: '', isError: false }), turns)
  assert.equal(failProposal(turns, 'missing', { expired: true }), turns)
})

test('proposal input splits the code field from the other fields', () => {
  assert.deepEqual(proposalInput('apply_flow', '{"flow_yaml":"name: x\\n"}'), { code: 'name: x\n', rest: '' })
  assert.deepEqual(proposalInput('apply_library', '{"name":"lib","source":"import gofi"}'), { code: 'import gofi', rest: '{\n  "name": "lib"\n}' })
  assert.deepEqual(proposalInput('apply_expression', '{"name":"x","expression":"true"}'), { code: 'true', rest: '{\n  "name": "x"\n}' })
  assert.deepEqual(proposalInput('apply_flow', '{"name":"x"}'), { rest: '{\n  "name": "x"\n}' })
  assert.deepEqual(proposalInput('other_tool', '{"a":1}'), { rest: '{\n  "a": 1\n}' })
  assert.deepEqual(proposalInput('apply_flow', 'not json'), { rest: 'not json' })
  assert.deepEqual(proposalInput('apply_flow', '[1]'), { rest: '[\n  1\n]' })
})

test('the conversation id round-trips through storage and tolerates failures', () => {
  const store = new Map()
  const storage = { getItem: k => store.get(k) ?? null, setItem: (k, v) => store.set(k, v), removeItem: k => store.delete(k) }
  saveConversationId('c1', storage)
  assert.equal(loadConversationId(storage), 'c1')
  saveConversationId('', storage)
  assert.equal(loadConversationId(storage), '')
  const broken = { getItem: () => { throw new Error('denied') }, setItem: () => { throw new Error('denied') }, removeItem: () => {} }
  saveConversationId('c2', broken)
  assert.equal(loadConversationId(broken), '')
})

test('tool steps clamp to 1-20 with 8 for unset', () => {
  assert.equal(clampToolSteps(0), 8)
  assert.equal(clampToolSteps(NaN), 8)
  assert.equal(clampToolSteps(-3), 8)
  assert.equal(clampToolSteps(1), 1)
  assert.equal(clampToolSteps(99), 20)
  assert.equal(clampToolSteps(3.6), 4)
})

test('helpers: pretty JSON, truncation, page context', () => {
  assert.equal(prettyJson('{"a":1}'), '{\n  "a": 1\n}')
  assert.equal(prettyJson('not json'), 'not json')
  assert.equal(prettyJson('  '), '')
  assert.deepEqual(truncateOutput('abcdef', 4), { text: 'abcd', cut: 2 })
  assert.deepEqual(truncateOutput('abc', 4), { text: 'abc', cut: 0 })
  assert.equal(pageContextOf(''), '#/')
  assert.equal(pageContextOf('#'), '#/')
  assert.equal(pageContextOf('#/flows/a'), '#/flows/a')
})
