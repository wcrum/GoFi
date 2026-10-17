// Command-palette matching: case-insensitive, ranked
// prefix > word-start > substring > subsequence (fuzzy), with the matched
// character positions for highlighting. Pure, so it is unit-tested in node.

export interface Match {
  /** Higher is better; only comparable between results of the same query. */
  score: number
  /** Indices into the original text of the matched characters. */
  indices: number[]
}

const PREFIX = 4000
const WORD_START = 3000
const SUBSTRING = 2000
const SUBSEQUENCE = 1000

const isWordChar = (ch: string) => /[\p{L}\p{N}]/u.test(ch)
/** Position i starts a word: start of text, after a separator, or a camelCase hump. */
function isWordStart(text: string, i: number): boolean {
  if (i === 0) return true
  const prev = text[i - 1], ch = text[i]
  if (!isWordChar(prev)) return isWordChar(ch)
  return prev === prev.toLowerCase() && ch !== ch.toLowerCase()
}
const range = (start: number, length: number) => Array.from({ length }, (_, k) => start + k)

/** Scores text against query; null when it does not match. An empty
 * query matches everything with score 0. */
export function matchText(query: string, text: string): Match | null {
  const q = query.trim().toLowerCase()
  if (!q) return { score: 0, indices: [] }
  const t = text.toLowerCase()
  // Shorter texts win ties within a tier, so "Flows" beats "Flows › foo".
  const brevity = Math.max(0, 200 - t.length)
  if (t.startsWith(q)) return { score: PREFIX + brevity, indices: range(0, q.length) }
  for (let i = t.indexOf(q, 1); i !== -1; i = t.indexOf(q, i + 1)) {
    if (isWordStart(text, i)) return { score: WORD_START + brevity - i, indices: range(i, q.length) }
  }
  const at = t.indexOf(q)
  if (at !== -1) return { score: SUBSTRING + brevity - at, indices: range(at, q.length) }
  return subsequence(q.replace(/\s+/g, ''), text, t, brevity)
}

/** Fuzzy match: every query character in order. Greedy, but prefers a
 * word-start occurrence when one exists before the next forced match, and
 * rewards consecutive runs and word starts while penalising gaps. */
function subsequence(q: string, text: string, t: string, brevity: number): Match | null {
  if (!q) return null
  const indices: number[] = []
  let from = 0
  for (let k = 0; k < q.length; k++) {
    const ch = q[k]
    let pick = t.indexOf(ch, from)
    if (pick === -1) return null
    // Look ahead for the same character at a word start; take it only if
    // the rest of the query still fits after it.
    if (!isWordStart(text, pick) && (indices.length === 0 || pick !== indices[indices.length - 1] + 1)) {
      for (let j = t.indexOf(ch, pick + 1); j !== -1; j = t.indexOf(ch, j + 1)) {
        if (isWordStart(text, j) && fits(q, k + 1, t, j + 1)) { pick = j; break }
      }
    }
    indices.push(pick)
    from = pick + 1
  }
  let bonus = 0
  for (let k = 0; k < indices.length; k++) {
    if (isWordStart(text, indices[k])) bonus += 30
    if (k > 0) bonus += indices[k] === indices[k - 1] + 1 ? 20 : -Math.min(15, indices[k] - indices[k - 1])
  }
  return { score: SUBSEQUENCE + brevity + bonus - indices[0], indices }
}

function fits(q: string, k: number, t: string, from: number): boolean {
  for (; k < q.length; k++) {
    const at = t.indexOf(q[k], from)
    if (at === -1) return false
    from = at + 1
  }
  return true
}

export interface Ranked<T> extends Match { item: T }

/** Matches and sorts items, best first; ties keep their input order. Text
 * from `keywords` matches too, at a lower score and without highlights. */
export function rank<T>(items: T[], query: string, text: (item: T) => string, keywords?: (item: T) => string | undefined): Ranked<T>[] {
  const out: (Ranked<T> & { order: number })[] = []
  items.forEach((item, order) => {
    let match = matchText(query, text(item))
    const extra = keywords?.(item)
    if (!match && extra) {
      const alt = matchText(query, extra)
      if (alt && alt.score >= SUBSTRING) match = { score: alt.score - SUBSEQUENCE * 1.5, indices: [] }
    }
    if (match) out.push({ ...match, item, order })
  })
  out.sort((a, b) => b.score - a.score || a.order - b.order)
  return out.map(({ order: _order, ...rest }) => rest)
}
