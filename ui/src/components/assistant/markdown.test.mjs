import assert from 'node:assert/strict'
import { test } from 'node:test'
import { linkKind, pageHref, parseBlocks, parseInline, splitRow } from './markdown.ts'

test('blank lines separate paragraphs; soft breaks stay inside one', () => {
  assert.deepEqual(parseBlocks('one\ntwo\n\nthree'), [
    { type: 'paragraph', text: 'one\ntwo' },
    { type: 'paragraph', text: 'three' },
  ])
})

test('fenced code keeps its language and raw lines', () => {
  assert.deepEqual(parseBlocks('before\n```yaml\nname: a\n\nsources: []\n```\nafter'), [
    { type: 'paragraph', text: 'before' },
    { type: 'code', language: 'yaml', code: 'name: a\n\nsources: []' },
    { type: 'paragraph', text: 'after' },
  ])
})

test('an unclosed fence (still streaming) is code to the end', () => {
  assert.deepEqual(parseBlocks('```\nx = 1\ny ='), [{ type: 'code', language: '', code: 'x = 1\ny =' }])
})

test('headings and lists', () => {
  assert.deepEqual(parseBlocks('## Flows\n- a\n- b\n1. c\n2. d\ntail'), [
    { type: 'heading', level: 2, text: 'Flows' },
    { type: 'list', ordered: false, items: ['a', 'b'] },
    { type: 'list', ordered: true, items: ['c', 'd'] },
    { type: 'paragraph', text: 'tail' },
  ])
})

test('indented continuation joins the previous list item', () => {
  assert.deepEqual(parseBlocks('- a\n  more'), [{ type: 'list', ordered: false, items: ['a\nmore'] }])
})

test('inline code, links and bold', () => {
  assert.deepEqual(parseInline('Open [telemetry](#/flows/telemetry) and run `gofi apply` **now**.'), [
    { type: 'text', text: 'Open ' },
    { type: 'link', text: 'telemetry', href: '#/flows/telemetry', children: [{ type: 'text', text: 'telemetry' }] },
    { type: 'text', text: ' and run ' },
    { type: 'code', text: 'gofi apply' },
    { type: 'text', text: ' ' },
    { type: 'strong', text: 'now', children: [{ type: 'text', text: 'now' }] },
    { type: 'text', text: '.' },
  ])
  assert.deepEqual(parseInline('plain'), [{ type: 'text', text: 'plain' }])
  assert.deepEqual(parseInline(''), [])
})

// Regression: a reply named a flow as **`route-check`** and as a link whose
// text was code; the old regexes kept the backticks (or dropped the text).
test('code nests inside bold and link text', () => {
  assert.deepEqual(parseInline('The flow **`route-check`** is stopped.'), [
    { type: 'text', text: 'The flow ' },
    { type: 'strong', text: '`route-check`', children: [{ type: 'code', text: 'route-check' }] },
    { type: 'text', text: ' is stopped.' },
  ])
  assert.deepEqual(parseInline('[`route-check`](#/flows/route-check)'), [
    { type: 'link', text: '`route-check`', href: '#/flows/route-check', children: [{ type: 'code', text: 'route-check' }] },
  ])
})

test('backticks next to punctuation, dashes and double backticks', () => {
  assert.deepEqual(parseInline('(`route-check`), `tcp:18640`: busy – see `a``b`.'), [
    { type: 'text', text: '(' }, { type: 'code', text: 'route-check' }, { type: 'text', text: '), ' },
    { type: 'code', text: 'tcp:18640' }, { type: 'text', text: ': busy – see ' }, { type: 'code', text: 'a``b' }, { type: 'text', text: '.' },
  ])
  assert.deepEqual(parseInline('``has ` inside``'), [{ type: 'code', text: 'has ` inside' }])
  assert.deepEqual(parseInline('`` ` ``'), [{ type: 'code', text: '`' }])
})

test('unclosed markers (a reply still streaming) stay literal and lose nothing', () => {
  assert.deepEqual(parseInline('look at `route-che'), [{ type: 'text', text: 'look at `route-che' }])
  assert.deepEqual(parseInline('**bold'), [{ type: 'text', text: '**bold' }])
  assert.deepEqual(parseInline('[route-check'), [{ type: 'text', text: '[route-check' }])
  assert.deepEqual(parseInline('[route-check]'), [{ type: 'text', text: '[route-check]' }])
  assert.deepEqual(parseInline('[](#/x) and [x]( )'), [{ type: 'text', text: '[](#/x) and [x]( )' }])
  assert.deepEqual(parseInline('a ` b'), [{ type: 'text', text: 'a ` b' }])
  assert.deepEqual(parseInline('``'), [{ type: 'text', text: '``' }])
})

test('fences: language is lowercased, longer fences and tildes close themselves', () => {
  assert.deepEqual(parseBlocks('```YAML\nname: a\n```'), [{ type: 'code', language: 'yaml', code: 'name: a' }])
  assert.deepEqual(parseBlocks('````md\n```\ninner\n```\n````\nafter'), [{ type: 'code', language: 'md', code: '```\ninner\n```' }, { type: 'paragraph', text: 'after' }])
  assert.deepEqual(parseBlocks('~~~python\nx = 1\n~~~'), [{ type: 'code', language: 'python', code: 'x = 1' }])
})

test('link kinds: in-app hashes navigate, http opens, the rest is inert', () => {
  assert.equal(linkKind('#/flows/a'), 'app')
  assert.equal(linkKind('https://grafana.com'), 'external')
  assert.equal(linkKind('javascript:alert(1)'), 'unsafe')
  assert.equal(linkKind('data:text/html,x'), 'unsafe')
})

// Regression: a reply compared two flows in a pipe table; the parser had no
// table block, so the rows rendered as one paragraph of pipes and dashes.
test('pipe tables: header, alignment, <br> cells, escaped pipes, padded rows', () => {
  const text = 'Compare:\n| Flow | Stages | Status |\n|------|:------:|-------:|\n| c2-cfg | 1 | *running* |\n| d-qa | 1) rate-limit <br>2) dedup | failed \\| busy |\n| short |\n\nafter'
  assert.deepEqual(parseBlocks(text), [
    { type: 'paragraph', text: 'Compare:' },
    { type: 'table', align: [null, 'center', 'right'], header: ['Flow', 'Stages', 'Status'], rows: [
      ['c2-cfg', '1', '*running*'],
      ['d-qa', '1) rate-limit\n2) dedup', 'failed | busy'],
      ['short', '', ''],
    ] },
    { type: 'paragraph', text: 'after' },
  ])
  assert.deepEqual(splitRow('a | b | c'), ['a', 'b', 'c'])
  assert.deepEqual(splitRow('| `x|y` |'), ['`x', 'y`'])
})

test('a lone pipe line or a header without its delimiter row stays a paragraph', () => {
  assert.deepEqual(parseBlocks('a | b'), [{ type: 'paragraph', text: 'a | b' }])
  assert.deepEqual(parseBlocks('| Flow | Port |'), [{ type: 'paragraph', text: '| Flow | Port |' }])
  assert.deepEqual(parseBlocks('| a | b |\n|---|'), [{ type: 'paragraph', text: '| a | b |\n|---|' }])
})

test('a table ends at a blank line, a fence, or a line without pipes', () => {
  assert.deepEqual(parseBlocks('| a |\n|---|\n| 1 |\ntail\n```\nx\n```'), [
    { type: 'table', align: [null], header: ['a'], rows: [['1']] },
    { type: 'paragraph', text: 'tail' },
    { type: 'code', language: '', code: 'x' },
  ])
})

// Regression: a model wrote its open_page call into the reply text, which
// rendered as dead prose; it now becomes the link the call would have opened.
test('an open_page call written as text becomes an in-app link', () => {
  assert.deepEqual(parseInline('Feel free to open each flow: open_page{page:"flow", name:"c2-cfg"} and open_page({"page": "flow", "name": "d-qa"}).'), [
    { type: 'text', text: 'Feel free to open each flow: ' },
    { type: 'link', text: 'c2-cfg', href: '#/flows/c2-cfg', children: [{ type: 'text', text: 'c2-cfg' }] },
    { type: 'text', text: ' and ' },
    { type: 'link', text: 'd-qa', href: '#/flows/d-qa', children: [{ type: 'text', text: 'd-qa' }] },
    { type: 'text', text: '.' },
  ])
  assert.deepEqual(parseInline("open_page{page:'flow-problems', name:'a b'}"), [{ type: 'link', text: 'a b', href: '#/flows/a%20b/problems', children: [{ type: 'text', text: 'a b' }] }])
  assert.deepEqual(parseInline('open_page{page:"flows"}'), [{ type: 'link', text: 'flows', href: '#/flows', children: [{ type: 'text', text: 'flows' }] }])
  // Unknown pages, a flow page without a name, an unclosed call, and the word inside another stay literal.
  assert.deepEqual(parseInline('open_page{page:"nope"}'), [{ type: 'text', text: 'open_page{page:"nope"}' }])
  assert.deepEqual(parseInline('open_page{page:"flow"}'), [{ type: 'text', text: 'open_page{page:"flow"}' }])
  assert.deepEqual(parseInline('open_page{page:"flow", na'), [{ type: 'text', text: 'open_page{page:"flow", na' }])
  assert.deepEqual(parseInline('the open_page tool'), [{ type: 'text', text: 'the open_page tool' }])
  assert.deepEqual(parseInline('reopen_page{page:"flows"}'), [{ type: 'text', text: 'reopen_page{page:"flows"}' }])
})

test('pageHref mirrors the server page map', () => {
  assert.equal(pageHref('home'), '#/')
  assert.equal(pageHref('new-flow'), '#/flows/new')
  assert.equal(pageHref('flow-traffic', 'x/y'), '#/flows/x%2Fy/traffic')
  assert.equal(pageHref('explore', 'x'), '#/explore?flow=x')
  assert.equal(pageHref('explore'), '#/explore')
  assert.equal(pageHref('functions', 'lib'), '#/functions?name=lib')
  assert.equal(pageHref('cel', 'e'), '#/cel?name=e')
  assert.equal(pageHref('endpoints'), '#/connections/endpoints')
  assert.equal(pageHref('cluster'), '#/cluster')
  assert.equal(pageHref('admin'), '#/administration')
  assert.equal(pageHref('flow'), null)
  assert.equal(pageHref('nope'), null)
})
