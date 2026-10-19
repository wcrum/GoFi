import { Fragment, useEffect, useId, useLayoutEffect, useMemo, useRef, useState, type KeyboardEvent as ReactKeyboardEvent } from 'react'
import { css, cx } from '@emotion/css'
import type { GrafanaTheme2, IconName } from '@grafana/data'
import { Icon, Portal, ToolbarButton, useStyles2 } from '@grafana/ui'
import { navigate, paths } from '../router'
import { rank, type Ranked } from './search-match'
import type { NavNode } from './AppChrome'
import { LOCALES, t, useLocale, type SupportedLocale } from '../i18n'

type Section = 'Pages' | 'Flows' | 'Actions'
const sectionLabel = (section: Section) => section === 'Pages' ? t('gofi.shell.search.section-pages', 'Pages') : section === 'Flows' ? t('gofi.shell.search.section-flows', 'Flows') : t('gofi.shell.search.section-actions', 'Actions')
/** A destination (href) or an action to run (run), e.g. switching language. */
interface Entry { id: string; section: Section; label: string; icon?: IconName; href?: string; run?: () => void; keywords?: string }
interface Group { section: Section; rows: Ranked<Entry>[] }

/** Per-group cap while filtering; the empty query lists everything. */
const LIMIT = 8

const isMac = () => typeof navigator !== 'undefined' && /Mac|iPhone|iPad|iPod/.test(navigator.platform || navigator.userAgent)

/** The same destinations as the top-bar "New" menu. */
const actions = (): Entry[] => [
  { id: 'action:new-flow', section: 'Actions', label: t('gofi.shell.search.action-new-flow', 'New flow'), icon: 'exchange-alt', href: paths.newFlow(), keywords: t('gofi.shell.search.action-new-flow-keywords', 'create add flow') },
  { id: 'action:new-library', section: 'Actions', label: t('gofi.shell.search.action-new-function-library', 'New function library'), icon: 'brackets-curly', href: paths.libraries(), keywords: t('gofi.shell.search.action-new-python-library-keywords', 'create add python library') },
  { id: 'action:new-cel', section: 'Actions', label: t('gofi.shell.search.action-new-cel-expression', 'New CEL expression'), icon: 'book-open', href: paths.expressions('new'), keywords: t('gofi.shell.search.action-new-cel-expression-keywords', 'create add cel expression') },
]

/** "Language: Українська" for every locale but the active one; the native
 * name is the label so it is findable whatever the current language, the
 * English name and tag are search keywords. */
const languageActions = (current: SupportedLocale, setLocale: (locale: SupportedLocale) => Promise<void>): Entry[] =>
  LOCALES.filter(option => option.tag !== current).map(option => ({
    id: `action:language:${option.tag}`,
    section: 'Actions',
    icon: 'globe',
    label: t('gofi.shell.search.action-language', 'Language: {{name}}', { name: option.nativeName }),
    keywords: `${option.englishName} ${option.tag} ${t('gofi.shell.search.action-language-keywords', 'language locale translate switch')}`,
    run: () => { void setLocale(option.tag) },
  }))

/** Flattens the main menu: flows into their own section, other children
 * labelled with their parent ("Cluster › Networking"). */
function entries(tree: NavNode[]): Entry[] {
  const out: Entry[] = []
  for (const node of tree) {
    out.push({ id: node.id, section: 'Pages', label: node.text, icon: node.icon, href: node.href })
    for (const child of node.children ?? []) {
      if (node.id === 'flows') out.push({ id: child.id, section: 'Flows', label: child.text, icon: 'exchange-alt', href: child.href, keywords: t('gofi.shell.search.flow-keywords', 'flow') })
      else out.push({ id: child.id, section: 'Pages', label: t('gofi.shell.search.nested-page-label', '{{parent}} › {{child}}', { parent: node.text, child: child.text }), icon: child.icon ?? node.icon, href: child.href })
    }
  }
  return [...out, ...actions()]
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

/** Top-bar search button plus the ⌘/Ctrl+K command palette. */
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
    <ToolbarButton icon="search" aria-label={t('gofi.shell.search.search-or-jump-to', 'Search or jump to')} tooltip={t('gofi.shell.search.search-or-jump-to-shortcut', 'Search or jump to ({{shortcut}})', { shortcut: mac ? '⌘K' : 'Ctrl+K' })} aria-haspopup="dialog" onClick={() => show()} />
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
  const [locale, setLocale] = useLocale()
  const all = useMemo(() => [...entries(tree), ...languageActions(locale, setLocale)], [tree, locale, setLocale])
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

  const go = (entry: Entry) => {
    onClose()
    if (entry.run) entry.run()
    else if (entry.href) navigate(entry.href)
  }
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
    <div className={styles.dialog} role="dialog" aria-modal="true" aria-label={t('gofi.shell.search.search-or-jump-to', 'Search or jump to')} onKeyDown={onKeyDown}>
      <div className={styles.inputRow}>
        <Icon name="search" size="lg" className={styles.inputIcon} />
        <input ref={input} className={styles.input} value={query} onChange={event => setQuery(event.currentTarget.value)}
          placeholder={t('gofi.shell.search.placeholder', 'Search pages, flows and actions...')} spellCheck={false} autoComplete="off"
          role="combobox" aria-expanded="true" aria-controls={listId} aria-autocomplete="list"
          aria-activedescendant={rows.length ? optionId(active) : undefined} />
        <kbd className={styles.kbd}>esc</kbd>
      </div>
      <div ref={list} id={listId} role="listbox" aria-label={t('gofi.shell.search.results', 'Results')} className={styles.results}>
        {rows.length === 0 && <div className={styles.empty}>{t('gofi.shell.search.no-results', 'No results for “{{query}}”', { query: query.trim() })}</div>}
        {groups.map(group => <Fragment key={group.section}>
          <div role="group" aria-labelledby={`${ids}-${group.section}`}>
            <div id={`${ids}-${group.section}`} className={styles.groupHeader} role="presentation">{sectionLabel(group.section)}</div>
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
        <span><kbd className={styles.kbd}>↑</kbd><kbd className={styles.kbd}>↓</kbd> {t('gofi.shell.search.hint-navigate', 'to navigate')}</span>
        <span><kbd className={styles.kbd}>↵</kbd> {t('gofi.shell.search.hint-select', 'to select')}</span>
        <span><kbd className={styles.kbd}>esc</kbd> {t('gofi.shell.search.hint-close', 'to close')}</span>
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
