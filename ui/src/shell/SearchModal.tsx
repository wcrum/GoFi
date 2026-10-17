import { Fragment, useEffect, useId, useLayoutEffect, useMemo, useRef, useState, type KeyboardEvent as ReactKeyboardEvent } from 'react'
import { css, cx } from '@emotion/css'
import type { GrafanaTheme2, IconName } from '@grafana/data'
import { Icon, IconButton, Portal, useStyles2 } from '@grafana/ui'
import { navigate, paths } from '../router'
import { rank, type Ranked } from './search-match'
import type { NavNode } from './AppChrome'

type Section = 'Pages' | 'Flows' | 'Actions'
interface Entry { id: string; section: Section; label: string; icon?: IconName; href: string; keywords?: string }
interface Group { section: Section; rows: Ranked<Entry>[] }

/** Per-group cap while filtering; the empty query lists everything. */
const LIMIT = 8

const isMac = () => typeof navigator !== 'undefined' && /Mac|iPhone|iPad|iPod/.test(navigator.platform || navigator.userAgent)

/** The same destinations as the top-bar "New" menu. */
const ACTIONS: Entry[] = [
  { id: 'action:new-flow', section: 'Actions', label: 'New flow', icon: 'exchange-alt', href: paths.newFlow(), keywords: 'create add flow' },
  { id: 'action:new-library', section: 'Actions', label: 'New Python library', icon: 'brackets-curly', href: paths.libraries(), keywords: 'create add python library' },
  { id: 'action:new-cel', section: 'Actions', label: 'New CEL expression', icon: 'book-open', href: paths.expressions('new'), keywords: 'create add cel expression' },
]

/** Flattens the main menu: flows into their own section, other children
 * labelled with their parent ("Cluster › Networking"). */
function entries(tree: NavNode[]): Entry[] {
  const out: Entry[] = []
  for (const node of tree) {
    out.push({ id: node.id, section: 'Pages', label: node.text, icon: node.icon, href: node.href })
    for (const child of node.children ?? []) {
      if (node.id === 'flows') out.push({ id: child.id, section: 'Flows', label: child.text, icon: 'exchange-alt', href: child.href, keywords: 'flow' })
      else out.push({ id: child.id, section: 'Pages', label: `${node.text} › ${child.text}`, icon: child.icon ?? node.icon, href: child.href })
    }
  }
  return [...out, ...ACTIONS]
}

function search(all: Entry[], query: string): Group[] {
  const ranked = rank(all, query, entry => entry.label, entry => entry.keywords)
  const groups: Group[] = []
  for (const row of ranked) {
    let group = groups.find(g => g.section === row.item.section)
    if (!group) groups.push(group = { section: row.item.section, rows: [] })
    if (!query.trim() || group.rows.length < LIMIT) group.rows.push(row)
  }
  // Empty query: fixed order. Otherwise groups follow their best match,
  // which ranked order already gives.
  if (!query.trim()) groups.sort((a, b) => ORDER.indexOf(a.section) - ORDER.indexOf(b.section))
  return groups
}
const ORDER: Section[] = ['Pages', 'Flows', 'Actions']

/** Top-bar "Search or jump to..." trigger plus the ⌘/Ctrl+K command palette. */
export function SearchCommand({ tree }: { tree: NavNode[] }) {
  const styles = useStyles2(getStyles)
  const [open, setOpen] = useState(false)
  const [initial, setInitial] = useState('')
  const mac = useMemo(isMac, [])
  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      if (!(event.metaKey || event.ctrlKey) || event.altKey || event.shiftKey || event.key.toLowerCase() !== 'k') return
      // Monaco uses Ctrl+K as a chord prefix (Ctrl+K Ctrl+C, ...); leave it be.
      if (event.target instanceof Element && event.target.closest('.monaco-editor')) return
      event.preventDefault()
      setInitial('')
      setOpen(value => !value)
    }
    // Capture phase, so widgets that stop propagation cannot swallow it.
    window.addEventListener('keydown', onKey, true)
    return () => window.removeEventListener('keydown', onKey, true)
  }, [])
  const show = (text = '') => { setInitial(text); setOpen(true) }
  return <>
    <button type="button" className={styles.trigger} aria-haspopup="dialog" aria-label="Search or jump to" onClick={() => show()}
      onKeyDown={event => {
        // Typing on the focused trigger opens the palette with that text.
        if (event.key.length === 1 && !event.metaKey && !event.ctrlKey && !event.altKey && event.key !== ' ') { event.preventDefault(); show(event.key) }
      }}>
      <Icon name="search" className={styles.triggerIcon} />
      <span className={styles.triggerText}>Search or jump to...</span>
      <kbd className={styles.kbd}>{mac ? '⌘K' : 'ctrl+k'}</kbd>
    </button>
    <IconButton name="search" size="lg" className={styles.narrowOnly} tooltip={`Search (${mac ? '⌘K' : 'Ctrl+K'})`} aria-haspopup="dialog" onClick={() => show()} />
    {open && <SearchModal tree={tree} initialQuery={initial} onClose={() => setOpen(false)} />}
  </>
}

function SearchModal({ tree, initialQuery, onClose }: { tree: NavNode[]; initialQuery: string; onClose: () => void }) {
  const styles = useStyles2(getStyles)
  const [query, setQuery] = useState(initialQuery)
  const [active, setActive] = useState(0)
  const input = useRef<HTMLInputElement>(null)
  const list = useRef<HTMLDivElement>(null)
  const ids = useId()
  const optionId = (index: number) => `${ids}-option-${index}`
  const listId = `${ids}-listbox`
  const all = useMemo(() => entries(tree), [tree])
  const groups = useMemo(() => search(all, query), [all, query])
  const rows = useMemo(() => groups.flatMap(group => group.rows), [groups])

  // Remember what had focus and give it back on close.
  useLayoutEffect(() => {
    const previous = document.activeElement instanceof HTMLElement ? document.activeElement : null
    input.current?.focus()
    return () => { if (previous?.isConnected) previous.focus() }
  }, [])
  useEffect(() => { setActive(0) }, [query])
  useEffect(() => {
    list.current?.querySelector<HTMLElement>(`[id="${CSS.escape(optionId(active))}"]`)?.scrollIntoView({ block: 'nearest' })
  }, [active, groups])

  const go = (entry: Entry) => { onClose(); navigate(entry.href) }
  const onKeyDown = (event: ReactKeyboardEvent) => {
    const count = rows.length
    switch (event.key) {
      case 'ArrowDown': event.preventDefault(); if (count) setActive(i => (i + 1) % count); break
      case 'ArrowUp': event.preventDefault(); if (count) setActive(i => (i - 1 + count) % count); break
      case 'Home': if (count && event.ctrlKey) { event.preventDefault(); setActive(0) } break
      case 'End': if (count && event.ctrlKey) { event.preventDefault(); setActive(count - 1) } break
      case 'Enter': event.preventDefault(); if (rows[active]) go(rows[active].item); break
      case 'Escape': event.preventDefault(); event.stopPropagation(); onClose(); break
      // The input is the dialog's only tab stop: keep focus inside.
      case 'Tab': event.preventDefault(); input.current?.focus(); break
    }
  }

  let index = -1
  return <Portal>
    <div className={styles.backdrop} onMouseDown={onClose} />
    <div className={styles.dialog} role="dialog" aria-modal="true" aria-label="Search or jump to" onKeyDown={onKeyDown}>
      <div className={styles.inputRow}>
        <Icon name="search" size="lg" className={styles.inputIcon} />
        <input ref={input} className={styles.input} value={query} onChange={event => setQuery(event.currentTarget.value)}
          placeholder="Search pages, flows and actions..." spellCheck={false} autoComplete="off"
          role="combobox" aria-expanded="true" aria-controls={listId} aria-autocomplete="list"
          aria-activedescendant={rows.length ? optionId(active) : undefined} />
        <kbd className={styles.kbd}>esc</kbd>
      </div>
      <div ref={list} id={listId} role="listbox" aria-label="Results" className={styles.results}>
        {rows.length === 0 && <div className={styles.empty}>No results for “{query.trim()}”</div>}
        {groups.map(group => <Fragment key={group.section}>
          <div role="group" aria-labelledby={`${ids}-${group.section}`}>
            <div id={`${ids}-${group.section}`} className={styles.groupHeader} role="presentation">{group.section}</div>
            {group.rows.map(row => {
              const i = ++index
              const selected = i === active
              return <div key={row.item.id} id={optionId(i)} role="option" aria-selected={selected}
                className={cx(styles.row, selected && styles.rowActive)}
                onMouseMove={() => { if (!selected) setActive(i) }}
                onMouseDown={event => event.preventDefault()} // keep focus in the input
                onClick={() => go(row.item)}>
                <Icon name={row.item.icon ?? 'file-alt'} className={styles.rowIcon} />
                <span className={styles.rowLabel}><Highlight text={row.item.label} indices={row.indices} className={styles.mark} /></span>
                {selected && <kbd className={cx(styles.kbd, styles.rowHint)} aria-hidden>↵</kbd>}
              </div>
            })}
          </div>
        </Fragment>)}
      </div>
      <div className={styles.footer} aria-hidden>
        <span><kbd className={styles.kbd}>↑</kbd><kbd className={styles.kbd}>↓</kbd> to navigate</span>
        <span><kbd className={styles.kbd}>↵</kbd> to select</span>
        <span><kbd className={styles.kbd}>esc</kbd> to close</span>
      </div>
    </div>
  </Portal>
}

/** Text with the matched characters emphasised. */
function Highlight({ text, indices, className }: { text: string; indices: number[]; className: string }) {
  if (!indices.length) return <>{text}</>
  const hit = new Set(indices)
  const parts: { text: string; hit: boolean }[] = []
  for (let i = 0; i < text.length; i++) {
    const last = parts[parts.length - 1]
    if (last && last.hit === hit.has(i)) last.text += text[i]
    else parts.push({ text: text[i], hit: hit.has(i) })
  }
  return <>{parts.map((part, i) => part.hit ? <mark key={i} className={className}>{part.text}</mark> : <Fragment key={i}>{part.text}</Fragment>)}</>
}

const getStyles = (theme: GrafanaTheme2) => ({
  trigger: css({
    display: 'flex', alignItems: 'center', gap: theme.spacing(1), width: 'min(36vw, 420px)', height: theme.spacing(theme.components.height.md),
    padding: theme.spacing(0, 1), background: theme.components.input.background, border: `1px solid ${theme.components.input.borderColor}`,
    borderRadius: theme.shape.radius.default, color: theme.colors.text.disabled, fontSize: theme.typography.body.fontSize, cursor: 'text', textAlign: 'left',
    '&:hover': { borderColor: theme.components.input.borderHover },
    '&:focus-visible': { outline: `2px solid ${theme.colors.primary.main}`, outlineOffset: 1 },
    [theme.breakpoints.down('md')]: { display: 'none' },
  }),
  triggerIcon: css({ color: theme.colors.text.secondary, flexShrink: 0 }),
  triggerText: css({ flex: 1, minWidth: 0, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }),
  narrowOnly: css({ display: 'none', [theme.breakpoints.down('md')]: { display: 'inline-flex' } }),
  kbd: css({
    fontFamily: theme.typography.fontFamily, fontSize: theme.typography.bodySmall.fontSize, lineHeight: 1.4, color: theme.colors.text.secondary,
    border: `1px solid ${theme.colors.border.weak}`, borderRadius: theme.shape.radius.default, padding: theme.spacing(0, 0.5), whiteSpace: 'nowrap',
    background: theme.colors.background.secondary,
  }),
  backdrop: css({ position: 'fixed', inset: 0, background: theme.components.overlay.background, zIndex: theme.zIndex.modalBackdrop }),
  dialog: css({
    position: 'fixed', top: '10vh', left: '50%', transform: 'translateX(-50%)', width: 'min(640px, calc(100vw - 32px))', maxHeight: '80vh',
    display: 'flex', flexDirection: 'column', zIndex: theme.zIndex.modal, overflow: 'hidden',
    background: theme.colors.background.elevated, border: `1px solid ${theme.colors.border.weak}`, borderRadius: theme.shape.radius.default, boxShadow: theme.shadows.z3,
    [theme.breakpoints.down('sm')]: { top: theme.spacing(2) },
  }),
  inputRow: css({ display: 'flex', alignItems: 'center', gap: theme.spacing(1.5), padding: theme.spacing(1.5, 2), borderBottom: `1px solid ${theme.colors.border.weak}` }),
  inputIcon: css({ color: theme.colors.text.secondary, flexShrink: 0 }),
  input: css({
    flex: 1, minWidth: 0, border: 'none', outline: 'none', background: 'transparent', padding: 0,
    color: theme.colors.text.primary, fontSize: theme.typography.h5.fontSize, fontFamily: theme.typography.fontFamily,
    '&::placeholder': { color: theme.colors.text.disabled },
  }),
  results: css({ flex: 1, minHeight: 0, overflowY: 'auto', padding: theme.spacing(0.5, 1, 1), overscrollBehavior: 'contain' }),
  groupHeader: css({
    padding: theme.spacing(1.5, 1, 0.5), color: theme.colors.text.secondary, fontSize: theme.typography.bodySmall.fontSize,
    fontWeight: theme.typography.fontWeightMedium, textTransform: 'uppercase', letterSpacing: '0.04em',
  }),
  row: css({
    display: 'flex', alignItems: 'center', gap: theme.spacing(1.5), minHeight: theme.spacing(4.5), padding: theme.spacing(0.5, 1), marginBottom: 2,
    borderRadius: theme.shape.radius.default, border: '1px solid transparent', color: theme.colors.text.primary, cursor: 'pointer',
  }),
  // Same accent as the selected main-menu item: border on a tinted fill.
  rowActive: css({ borderColor: theme.colors.primary.border, background: theme.colors.primary.transparent }),
  rowIcon: css({ color: theme.colors.text.secondary, flexShrink: 0 }),
  rowLabel: css({ flex: 1, minWidth: 0, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }),
  rowHint: css({ color: theme.colors.text.secondary, flexShrink: 0 }),
  mark: css({ background: 'none', color: theme.colors.primary.text, fontWeight: theme.typography.fontWeightBold, padding: 0 }),
  empty: css({ padding: theme.spacing(3, 2), textAlign: 'center', color: theme.colors.text.secondary }),
  footer: css({
    display: 'flex', flexWrap: 'wrap', gap: theme.spacing(2), padding: theme.spacing(1, 2), borderTop: `1px solid ${theme.colors.border.weak}`,
    color: theme.colors.text.secondary, fontSize: theme.typography.bodySmall.fontSize,
    span: { display: 'inline-flex', alignItems: 'center', gap: theme.spacing(0.5) },
  }),
})
