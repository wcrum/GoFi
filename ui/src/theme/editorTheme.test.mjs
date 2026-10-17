import { test } from 'node:test'
import assert from 'node:assert/strict'
import { buildEditorTheme, getEditorTheme, opaqueColor, setEditorTheme, subscribeEditorTheme, toMonacoColor, withAlpha } from './editorTheme.ts'

const grafanaLike = (isDark, accent = '#3d71d9') => ({
  isDark,
  colors: {
    primary: { main: accent, text: '#6e9fff', border: accent },
    text: { primary: 'rgb(204, 204, 220)', secondary: 'rgba(204, 204, 220, 0.65)', disabled: 'rgba(204, 204, 220, 0.6)' },
    background: { canvas: '#111217', primary: '#181b1f', secondary: '#22252b', elevated: '#22252b' },
    border: { weak: 'rgba(204, 204, 220, 0.12)', medium: 'rgba(204, 204, 220, 0.20)', strong: 'rgba(204, 204, 220, 0.30)' },
    action: { hover: 'rgba(204, 204, 220, 0.16)', selected: 'rgba(204, 204, 220, 0.12)' },
  },
  components: { input: { background: '#111217' } },
  visualization: { getColorByName: name => ({ purple: '#A352CC', orange: '#FF9830', green: '#73BF69' })[name] },
})

const syntax = { comment: '#5c6370', keyword: '#c678dd', string: '#98c379', number: '#d19a66', type: '#e5c07b', function: '#61afef', variable: '#e06c75', constant: '#d19a66', operator: '#56b6c2', delimiter: '#abb2bf', tag: '#e06c75', attribute: '#d19a66', regexp: '#56b6c2', background: '#282c34' }
const oneDark = { id: 'one-dark', name: 'One Dark', mode: 'dark', family: 'Atom', syntax }
const grafanaDark = { id: 'grafana-dark', name: 'Dark', mode: 'dark', family: 'Grafana' }

test('colors normalize to #rrggbb or #rrggbbaa', () => {
  assert.equal(toMonacoColor('#ABC'), '#aabbcc')
  assert.equal(toMonacoColor('#abcd'), '#aabbccdd')
  assert.equal(toMonacoColor('rgb(204, 204, 220)'), '#ccccdc')
  assert.equal(toMonacoColor('rgba(204, 204, 220, 0.5)'), '#ccccdc80')
  assert.equal(toMonacoColor('rgb(255 0 0 / 50%)'), '#ff000080')
  assert.equal(toMonacoColor('hsl(0, 0%, 0%)'), undefined)
  assert.equal(withAlpha('#3d71d9', 0.5), '#3d71d980')
  assert.equal(opaqueColor('rgba(255, 255, 255, 0.5)', '#000000'), '#808080')
})

test('every theme color is Monaco-safe hex', () => {
  for (const definition of [oneDark, grafanaDark]) {
    const data = buildEditorTheme(definition, grafanaLike(true))
    for (const [key, value] of Object.entries(data.colors)) assert.match(value, /^#[0-9a-f]{6}([0-9a-f]{2})?$/, key)
    for (const rule of data.rules) assert.match(rule.foreground, /^[0-9a-f]{6}$/, rule.token)
  }
})

test('syntax themes map tokens; stock themes keep Grafana fallbacks', () => {
  const data = buildEditorTheme(oneDark, grafanaLike(true))
  assert.equal(data.base, 'vs-dark')
  assert.equal(data.colors['editor.background'], '#282c34')
  const rule = token => data.rules.find(r => r.token === token)
  assert.deepEqual(rule('comment'), { token: 'comment', foreground: '5c6370', fontStyle: 'italic' })
  assert.equal(rule('string.key.json').foreground, 'd19a66')
  assert.equal(rule('keyword').foreground, 'c678dd')
  const stock = buildEditorTheme(grafanaDark, grafanaLike(true))
  assert.equal(stock.colors['editor.background'], '#111217')
  assert.deepEqual(stock.rules.map(r => r.token), ['predefined', 'operator', 'tag'])
  assert.equal(stock.rules[0].foreground, 'a352cc')
})

test('accent drives cursor, focus, and selection', () => {
  const data = buildEditorTheme(oneDark, grafanaLike(true, '#ff7700'))
  assert.equal(data.colors['editorCursor.foreground'], '#ff7700')
  assert.equal(data.colors['focusBorder'], '#ff7700')
  assert.match(data.colors['editor.selectionBackground'], /^#ff7700[0-9a-f]{2}$/)
})

test('the store notifies only on change', () => {
  let calls = 0
  const off = subscribeEditorTheme(() => calls++)
  setEditorTheme(oneDark, grafanaLike(true))
  setEditorTheme(oneDark, grafanaLike(true))
  assert.equal(calls, 1)
  setEditorTheme(oneDark, grafanaLike(true, '#ff7700'))
  assert.equal(calls, 2)
  assert.equal(getEditorTheme().colors['editorCursor.foreground'], '#ff7700')
  off()
})
