// A small Markdown subset for assistant replies: paragraphs, fenced code
// blocks, headings, bullet/numbered lists, pipe tables, and inline code,
// bold, and links. Pure functions; Markdown.tsx renders the result. Unclosed
// fences (text still streaming) count as code until the end.

export type Alignment = 'left' | 'center' | 'right' | null
export type Block =
  | { type: 'paragraph'; text: string }
  | { type: 'heading'; level: number; text: string }
  | { type: 'code'; language: string; code: string }
  | { type: 'list'; ordered: boolean; items: string[] }
  /** A GFM pipe table; every row has as many cells as the header. */
  | { type: 'table'; align: Alignment[]; header: string[]; rows: string[][] }

export type Inline =
  | { type: 'text'; text: string }
  | { type: 'code'; text: string }
  /** `children`: the text parsed again (code spans inside bold or link text). */
  | { type: 'strong'; text: string; children: Inline[] }
  | { type: 'link'; text: string; href: string; children: Inline[] }

const FENCE = /^\s*(`{3,}|~{3,})\s*([\w+#.-]*)\s*$/
const HEADING = /^(#{1,6})\s+(.*?)\s*#*\s*$/
const BULLET = /^\s*[-*+]\s+(.*)$/
const NUMBERED = /^\s*\d+[.)]\s+(.*)$/
const TABLE_DELIMITER = /^\s*\|?\s*:?-+:?\s*(\|\s*:?-+:?\s*)*\|?\s*$/
const LINE_BREAK = /<br\s*\/?>/gi

/** Splits text into blocks. Blank lines separate paragraphs; a paragraph's
 * lines stay joined with '\n' (render with pre-wrap for soft breaks). */
export function parseBlocks(text: string): Block[] {
  const blocks: Block[] = []
  const lines = text.replace(/\r\n?/g, '\n').split('\n')
  let paragraph: string[] = []
  let list: { ordered: boolean; items: string[] } | null = null
  const flushParagraph = () => { if (paragraph.length) { blocks.push({ type: 'paragraph', text: paragraph.join('\n') }); paragraph = [] } }
  const flushList = () => { if (list) { blocks.push({ type: 'list', ...list }); list = null } }
  const flush = () => { flushParagraph(); flushList() }
  for (let i = 0; i < lines.length; i++) {
    const line = lines[i]
    const fence = FENCE.exec(line)
    if (fence) {
      flush()
      const code: string[] = []
      const closing = new RegExp(`^\\s*${fence[1][0] === '~' ? '~' : '`'}{${fence[1].length},}\\s*$`)
      for (i++; i < lines.length; i++) {
        if (closing.test(lines[i])) break
        code.push(lines[i])
      }
      blocks.push({ type: 'code', language: (fence[2] ?? '').toLowerCase(), code: code.join('\n') })
      continue
    }
    if (!line.trim()) { flush(); continue }
    const table = tableAt(lines, i)
    if (table) {
      flush()
      blocks.push(table.block)
      i = table.end - 1
      continue
    }
    const heading = HEADING.exec(line)
    if (heading) { flush(); blocks.push({ type: 'heading', level: heading[1].length, text: heading[2] }); continue }
    const bullet = BULLET.exec(line)
    const numbered = bullet ? null : NUMBERED.exec(line)
    const item = bullet ?? numbered
    if (item) {
      flushParagraph()
      const ordered = !!numbered
      if (list && list.ordered !== ordered) flushList()
      if (!list) list = { ordered, items: [] }
      list.items.push(item[1])
      continue
    }
    // A continuation line of the last list item (indented text under it).
    if (list && /^\s{2,}\S/.test(line)) { list.items[list.items.length - 1] += `\n${line.trim()}`; continue }
    flushList()
    paragraph.push(line)
  }
  flush()
  return blocks
}

/** A pipe table starting at line `start` (a header row followed by a
 * delimiter row such as |---|:--:|), or null. Body rows run until a blank
 * line, a fence, or a line without a pipe; short rows are padded and long
 * rows trimmed to the header's width, so a row still streaming renders. */
function tableAt(lines: string[], start: number): { block: Block; end: number } | null {
  const headerLine = lines[start]
  const delimiter = lines[start + 1]
  if (!headerLine.includes('|') || delimiter === undefined || !TABLE_DELIMITER.test(delimiter)) return null
  const header = splitRow(headerLine)
  const align = splitRow(delimiter).map<Alignment>(cell => {
    const left = cell.startsWith(':'), right = cell.endsWith(':')
    return left && right ? 'center' : right ? 'right' : left ? 'left' : null
  })
  if (header.length === 0 || header.length !== align.length) return null
  const rows: string[][] = []
  let i = start + 2
  for (; i < lines.length; i++) {
    const line = lines[i]
    if (!line.trim() || !line.includes('|') || FENCE.test(line)) break
    const cells = splitRow(line).slice(0, header.length)
    while (cells.length < header.length) cells.push('')
    rows.push(cells)
  }
  return { block: { type: 'table', align, header, rows }, end: i }
}

/** The cells of a table row: outer pipes dropped, split on unescaped
 * pipes, \| unescaped, <br> turned into a newline, cells trimmed. */
export function splitRow(line: string): string[] {
  let text = line.trim()
  if (text.startsWith('|')) text = text.slice(1)
  if (text.endsWith('|') && !text.endsWith('\\|')) text = text.slice(0, -1)
  const cells: string[] = []
  let cell = ''
  for (let i = 0; i < text.length; i++) {
    const ch = text[i]
    if (ch === '\\' && text[i + 1] === '|') { cell += '|'; i++; continue }
    if (ch === '|') { cells.push(cell); cell = ''; continue }
    cell += ch
  }
  cells.push(cell)
  return cells.map(c => c.replace(LINE_BREAK, '\n').split('\n').map(part => part.trim()).join('\n'))
}

/** Splits a line of text into inline runs: `code` (any number of
 * backticks, CommonMark style, so a span may contain single backticks),
 * [text](href), and **strong**. Bold and link text may contain code, which
 * nests as a child run; an unclosed marker stays literal text, so a reply
 * that is still streaming never loses characters. */
export function parseInline(text: string): Inline[] {
  const out: Inline[] = []
  let literal = ''
  const flush = () => { if (literal) { out.push({ type: 'text', text: literal }); literal = '' } }
  let i = 0
  while (i < text.length) {
    const ch = text[i]
    if (ch === '`') {
      const span = codeSpan(text, i)
      if (span) { flush(); out.push({ type: 'code', text: span.code }); i = span.end; continue }
      // Unmatched run of backticks: literal.
      let j = i; while (j < text.length && text[j] === '`') j++
      literal += text.slice(i, j); i = j; continue
    }
    if (ch === '*' && text[i + 1] === '*') {
      const close = findClose(text, i + 2, '**')
      if (close > i + 2) { flush(); out.push({ type: 'strong', text: text.slice(i + 2, close), children: parseInline(text.slice(i + 2, close)) }); i = close + 2; continue }
    }
    if (ch === '[') {
      const link = linkAt(text, i)
      if (link) { flush(); out.push({ type: 'link', text: link.text, href: link.href, children: parseInline(link.text) }); i = link.end; continue }
    }
    if (ch === 'o' && text.startsWith(OPEN_PAGE, i) && (i === 0 || !/\w/.test(text[i - 1]))) {
      const call = openPageAt(text, i)
      if (call) { flush(); out.push({ type: 'link', text: call.text, href: call.href, children: [{ type: 'text', text: call.text }] }); i = call.end; continue }
    }
    literal += ch; i++
  }
  flush()
  return out
}

/** A CommonMark code span starting at `start`: N backticks, content without
 * a run of exactly N backticks, N backticks. One leading and trailing space
 * are stripped when both are present (so `` ` `` works). */
function codeSpan(text: string, start: number): { code: string; end: number } | null {
  let n = 0; while (text[start + n] === '`') n++
  const open = '`'.repeat(n)
  let i = start + n
  while (i < text.length) {
    if (text[i] === '\n') return null
    if (text[i] === '`') {
      let m = 0; while (text[i + m] === '`') m++
      if (m === n) {
        let code = text.slice(start + n, i)
        if (code.length > 2 && code.startsWith(' ') && code.endsWith(' ') && code.trim()) code = code.slice(1, -1)
        return code ? { code, end: i + n } : null
      }
      i += m; continue
    }
    i++
  }
  return null
}

/** The index of `marker` after `from`, skipping code spans and newlines; -1 when absent. */
function findClose(text: string, from: number, marker: string): number {
  let i = from
  while (i < text.length) {
    if (text[i] === '\n') return -1
    if (text[i] === '`') { const span = codeSpan(text, i); if (span) { i = span.end; continue } }
    if (text.startsWith(marker, i)) return i
    i++
  }
  return -1
}

/** [text](href) at `start`; the text may hold code spans and balanced brackets. */
function linkAt(text: string, start: number): { text: string; href: string; end: number } | null {
  let depth = 0
  let i = start
  while (i < text.length) {
    const ch = text[i]
    if (ch === '\n') return null
    if (ch === '`') { const span = codeSpan(text, i); if (span) { i = span.end; continue } }
    if (ch === '[') depth++
    else if (ch === ']') {
      depth--
      if (depth === 0) {
        if (text[i + 1] !== '(') return null
        const close = text.indexOf(')', i + 2)
        if (close < 0) return null
        const href = text.slice(i + 2, close).trim()
        const label = text.slice(start + 1, i)
        if (!href || /\s/.test(href) || !label) return null
        return { text: label, href, end: close + 1 }
      }
    }
    i++
  }
  return null
}

const OPEN_PAGE = 'open_page'

/** Some models write the open_page tool call into the reply instead of
 * calling it: open_page{page:"flow", name:"x"} or open_page({"page": "flow"}).
 * Such a call at `start` becomes an in-app link so the user can still get
 * there; an unknown page stays literal text. */
function openPageAt(text: string, start: number): { text: string; href: string; end: number } | null {
  let i = start + OPEN_PAGE.length
  while (text[i] === ' ') i++
  const parens = text[i] === '('
  if (parens) { i++; while (text[i] === ' ') i++ }
  if (text[i] !== '{') return null
  let depth = 0
  let close = -1
  for (let j = i; j < text.length; j++) {
    if (text[j] === '\n') return null
    if (text[j] === '{') depth++
    else if (text[j] === '}') { depth--; if (depth === 0) { close = j; break } }
  }
  if (close < 0) return null
  let end = close + 1
  if (parens) { while (text[end] === ' ') end++; if (text[end] !== ')') return null; end++ }
  const args = text.slice(i + 1, close)
  const page = /["']?\bpage["']?\s*[:=]\s*["']?([\w-]+)/.exec(args)?.[1]
  const name = /["']?\bname["']?\s*[:=]\s*(?:"([^"]*)"|'([^']*)'|([^\s,}]+))/.exec(args)
  if (!page) return null
  const flowName = (name?.[1] ?? name?.[2] ?? name?.[3] ?? '').trim()
  const href = pageHref(page, flowName)
  if (!href) return null
  return { text: flowName || page, href, end }
}

/** The portal href of an assistant page id, mirroring pageHref in assistant/tools.go
 * (and the router's paths); null when the page is unknown or needs a name it lacks. */
export function pageHref(page: string, name = ''): string | null {
  const enc = encodeURIComponent(name)
  switch (page) {
    case 'home': return '#/'
    case 'flows': return '#/flows'
    case 'new-flow': return '#/flows/new'
    case 'flow': case 'flow-edit': case 'flow-traffic': case 'flow-problems':
      if (!name) return null
      return page === 'flow' ? `#/flows/${enc}` : `#/flows/${enc}/${page.slice('flow-'.length)}`
    case 'explore': return name ? `#/explore?flow=${enc}` : '#/explore'
    case 'functions': case 'libraries': return name ? `#/functions?name=${enc}` : '#/functions'
    case 'cel': case 'expressions': return name ? `#/cel?name=${enc}` : '#/cel'
    case 'endpoints': return '#/connections/endpoints'
    case 'cluster': return '#/cluster'
    case 'administration': case 'admin': return '#/administration'
    default: return null
  }
}

export type LinkKind = 'app' | 'external' | 'unsafe'
/** In-app hash links navigate; http(s) opens a new tab; anything else (javascript:, data:) stays plain text. */
export function linkKind(href: string): LinkKind {
  if (/^#\//.test(href) || href === '#') return 'app'
  if (/^https?:\/\//i.test(href)) return 'external'
  return 'unsafe'
}
