import { test } from 'node:test'
import assert from 'node:assert/strict'
import { matchText, rank } from './search-match.ts'

test('empty query matches everything', () => {
  assert.deepEqual(matchText('  ', 'Home'), { score: 0, indices: [] })
})

test('case-insensitive prefix with highlight indices', () => {
  assert.deepEqual(matchText('HO', 'Home').indices, [0, 1])
})

test('tiers: prefix > word-start > substring > subsequence', () => {
  const prefix = matchText('net', 'Networking').score
  const word = matchText('net', 'Cluster › Networking').score
  const sub = matchText('net', 'Cabinet').score
  const fuzzy = matchText('nwk', 'Networking').score
  assert.ok(prefix > word, `${prefix} > ${word}`)
  assert.ok(word > sub, `${word} > ${sub}`)
  assert.ok(sub > fuzzy, `${sub} > ${fuzzy}`)
})

test('word-start match reports its position', () => {
  assert.deepEqual(matchText('net', 'Cluster › Networking').indices, [10, 11, 12])
})

test('subsequence prefers word starts and ignores query spaces', () => {
  assert.deepEqual(matchText('cn', 'Cluster › Networking').indices, [0, 10])
  assert.deepEqual(matchText('clu net', 'Cluster › Networking').indices, [0, 1, 2, 10, 11, 12])
  assert.deepEqual(matchText('ac', 'Administration › Configuration').indices, [0, 17])
})

test('no match returns null', () => {
  assert.equal(matchText('xyz', 'Home'), null)
  assert.equal(matchText('emoh', 'Home'), null)
})

test('rank sorts best first and keeps input order on ties', () => {
  const items = ['Python libraries', 'Flows', 'Explore', 'Flows › flow-a', 'Administration › Configuration']
  assert.deepEqual(rank(items, 'fl', s => s).map(r => r.item), ['Flows', 'Flows › flow-a'])
  assert.deepEqual(rank(items, '', s => s).map(r => r.item), items)
  assert.equal(rank(items, 'conf', s => s)[0].item, 'Administration › Configuration')
})

test('keywords match without highlights, below label matches', () => {
  const items = [{ label: 'New flow', kw: 'create add' }, { label: 'Create report', kw: '' }]
  const out = rank(items, 'create', i => i.label, i => i.kw)
  assert.deepEqual(out.map(r => r.item.label), ['Create report', 'New flow'])
  assert.deepEqual(out[1].indices, [])
})
