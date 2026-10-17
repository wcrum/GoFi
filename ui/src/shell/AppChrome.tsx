import { useEffect, useMemo, useState, type ReactNode } from 'react'
import { css, cx } from '@emotion/css'
import type { GrafanaTheme2, IconName } from '@grafana/data'
import { Badge, Dropdown, Icon, IconButton, Menu, Stack, ToolbarButton, Tooltip, useStyles2, useTheme2 } from '@grafana/ui'
import { navigate, paths, type Route } from '../router'
import { useGoFi } from '../state'
import { ThemePicker } from '../theme/ThemePicker'
import { StatusIndicator } from '../components/StatusIndicator'
import { GoFiMark } from '../components/GoFiMark'
import { SearchCommand } from './SearchModal'

export interface NavNode { id: string; text: string; icon?: IconName; href: string; children?: NavNode[] }
interface Crumb { text: string; href: string }
const MENU_KEY = 'gofi.megamenu.docked'
const COMPACT_KEY = 'gofi.megamenu.compact'

/** The deployment mode's icon: Kubernetes, Docker (also Compose), or the
 * host machine for the binary. */
function modeIcon(mode: string): IconName {
  return mode === 'kubernetes' ? 'kubernetes' : mode === 'docker' || mode === 'compose' ? 'docker' : 'monitor'
}

const modeTooltip = (mode: { mode: string; driverKind?: string }) => `Deployment mode: ${mode.mode} · driver: ${mode.driverKind || 'unknown'}`

/** The main menu. Cluster pages exist only with the Kubernetes driver. */
function navTree(flows: string[], kubernetes: boolean): NavNode[] {
  return [
    { id: 'home', text: 'Home', icon: 'home-alt', href: paths.home() },
    { id: 'flows', text: 'Flows', icon: 'exchange-alt', href: paths.flows(), children: flows.map(name => ({ id: `flow:${name}`, text: name, href: paths.flow(name) })) },
    { id: 'explore', text: 'Explore', icon: 'compass', href: paths.explore() },
    { id: 'libraries', text: 'Python libraries', icon: 'brackets-curly', href: paths.libraries() },
    { id: 'expressions', text: 'CEL library', icon: 'book-open', href: paths.expressions() },
    { id: 'connections', text: 'Connections', icon: 'adjust-circle', href: paths.endpoints(), children: [{ id: 'endpoints', text: 'Endpoints', href: paths.endpoints() }] },
    ...kubernetes ? [{ id: 'cluster', text: 'Cluster', icon: 'cloud', href: paths.cluster(), children: [{ id: 'cluster:overview', text: 'Overview', href: paths.cluster() }, { id: 'cluster:networking', text: 'Networking', href: paths.cluster('networking') }] } satisfies NavNode] : [],
    { id: 'admin', text: 'Administration', icon: 'cog', href: paths.configuration(), children: [{ id: 'configuration', text: 'Configuration', href: paths.configuration() }] },
  ]
}
function activeId(route: Route): string {
  switch (route.page) {
    case 'flow': return `flow:${route.name}`
    case 'flow-new': return 'flows'
    case 'cluster': return `cluster:${route.tab}`
    case 'not-found': return ''
    default: return route.page
  }
}
function breadcrumbs(route: Route): Crumb[] {
  const home = { text: 'Home', href: paths.home() }
  const flows = { text: 'Flows', href: paths.flows() }
  switch (route.page) {
    case 'home': return [home]
    case 'flows': return [home, flows]
    case 'flow-new': return [home, flows, { text: 'New flow', href: paths.newFlow() }]
    case 'flow': {
      const tabs = { flow: 'Flow', traffic: 'Live traffic', connectivity: 'Connectivity', resource: 'Resource', edit: 'Edit' }
      return [home, flows, { text: route.name, href: paths.flow(route.name) }, { text: tabs[route.tab], href: paths.flow(route.name, route.tab) }]
    }
    case 'explore': return [home, { text: 'Explore', href: paths.explore() }]
    case 'libraries': return [home, { text: 'Python libraries', href: paths.libraries() }]
    case 'expressions': return route.name ? [home, { text: 'CEL library', href: paths.expressions() }, { text: route.name, href: paths.expressions(route.name) }] : [home, { text: 'CEL library', href: paths.expressions() }]
    case 'endpoints': return [home, { text: 'Connections', href: paths.endpoints() }, { text: 'Endpoints', href: paths.endpoints() }]
    case 'configuration': return [home, { text: 'Administration', href: paths.configuration() }, { text: 'Configuration', href: paths.configuration() }]
    case 'cluster': return route.tab === 'overview' ? [home, { text: 'Cluster', href: paths.cluster() }] : [home, { text: 'Cluster', href: paths.cluster() }, { text: 'Networking', href: paths.cluster('networking') }]
    case 'not-found': return [home, { text: 'Page not found', href: `#${route.path}` }]
  }
}

/** Grafana application chrome: top bar with breadcrumbs and search, a
 * dockable mega menu, and the routed page. */
export function AppChrome({ route, children }: { route: Route; children: ReactNode }) {
  const styles = useStyles2(getStyles)
  const theme = useTheme2()
  const { flows, mode, error, reload } = useGoFi()
  const narrow = () => window.matchMedia(`(max-width: ${theme.breakpoints.values.lg}px)`).matches
  const [docked, setDocked] = useState(() => { try { return !narrow() && localStorage.getItem(MENU_KEY) !== 'false' } catch { return !narrow() } })
  const [open, setOpen] = useState(docked)
  // A docked menu can collapse to an icon-only rail.
  const [compactPref, setCompactPref] = useState(() => { try { return localStorage.getItem(COMPACT_KEY) === 'true' } catch { return false } })
  const compact = docked && compactPref
  const toggleCompact = () => {
    const next = !compactPref
    setCompactPref(next)
    try { localStorage.setItem(COMPACT_KEY, String(next)) } catch { /* storage unavailable */ }
  }
  const kubernetes = mode?.driverKind === 'kubernetes'
  const tree = useMemo(() => navTree(flows.map(flow => flow.name), kubernetes), [flows, kubernetes])
  const crumbs = breadcrumbs(route)
  const current = activeId(route)
  // An undocked menu is a transient overlay: close it after navigating.
  useEffect(() => { if (!docked) setOpen(false) }, [route, docked])
  const toggleDock = () => {
    const next = !docked
    setDocked(next); setOpen(next)
    try { localStorage.setItem(MENU_KEY, String(next)) } catch { /* storage unavailable */ }
  }
  const newMenu = <Menu>
    <Menu.Item label="New flow" icon="exchange-alt" onClick={() => navigate(paths.newFlow())} />
    <Menu.Item label="New Python library" icon="brackets-curly" onClick={() => navigate(paths.libraries())} />
    <Menu.Item label="New CEL expression" icon="book-open" onClick={() => navigate(paths.expressions('new'))} />
  </Menu>

  return <div className={styles.root}>
    <header className={styles.topBar}>
      <ToolbarButton narrow variant="default" icon="bars" aria-label={open ? 'Close menu' : 'Open menu'} aria-expanded={open} onClick={() => setOpen(value => !value)} />
      <a href={paths.home()} className={styles.logo} aria-label="GoFi home"><GoFiMark /><span className={styles.wordmark}>GoFi</span></a>
      <nav aria-label="Breadcrumbs" className={styles.breadcrumbs}>
        <ol>{crumbs.map((crumb, index) => <li key={crumb.href + index}>
          {index > 0 && <Icon name="angle-right" className={styles.separator} />}
          {index === crumbs.length - 1 ? <span aria-current="page">{crumb.text}</span> : <a href={crumb.href}>{crumb.text}</a>}
        </li>)}</ol>
      </nav>
      <SearchCommand tree={tree} />
      <Stack gap={1} alignItems="center">
        {error && <StatusIndicator tone="error" text="API unavailable" tooltip={error} />}
        <IconButton name="sync" size="md" tooltip="Refresh flows" onClick={() => void reload()} />
        <ThemePicker />
        <Dropdown overlay={newMenu} placement="bottom-end"><ToolbarButton icon="plus" isOpen={false} aria-label="New">New</ToolbarButton></Dropdown>
      </Stack>
    </header>
    <div className={styles.body}>
      {open && !docked && <div className={styles.backdrop} onClick={() => setOpen(false)} />}
      {open && <aside className={cx(styles.menu, !docked && styles.menuOverlay, compact && styles.menuCompact)} aria-label="Main menu">
        <nav className={styles.menuItems}><ul>{tree.map(node => compact
          ? <RailItem key={node.id} node={node} activeId={current} />
          : <MegaMenuItem key={node.id} node={node} activeId={current} />)}</ul></nav>
        <div className={cx(styles.menuFooter, compact && styles.menuFooterCompact)}>
          {/* Deployment mode, bottom-left like Grafana's edition/version. */}
          {mode && <span className={cx(styles.modeBadge, compact && styles.modeBadgeCompact)}>{compact
            ? <Tooltip content={modeTooltip(mode)} placement="right"><span className={styles.modeIcon} aria-label={`Deployment mode: ${mode.mode}`}><Icon name={modeIcon(mode.mode)} size="lg" /></span></Tooltip>
            : <Badge color="blue" icon={modeIcon(mode.mode)} text={mode.mode} tooltip={modeTooltip(mode)} />}</span>}
          {docked && <IconButton name={compact ? 'arrow-to-right' : 'arrow-from-right'} size={compact ? 'lg' : 'md'} tooltip={compact ? 'Expand menu' : 'Collapse to icons'} onClick={toggleCompact} className={styles.hideNarrow} />}
          {!compact && <IconButton name="columns" tooltip={docked ? 'Undock menu' : 'Dock menu'} onClick={toggleDock} className={styles.hideNarrow} />}
          {!docked && <IconButton name="times" aria-label="Close menu" onClick={() => setOpen(false)} />}
        </div>
      </aside>}
      <main className={styles.main}>{children}</main>
    </div>
  </div>
}

function MegaMenuItem({ node, activeId, level = 0 }: { node: NavNode; activeId: string; level?: number }) {
  const styles = useStyles2(getStyles)
  const containsActive = !!node.children?.some(child => child.id === activeId)
  const [expanded, setExpanded] = useState(containsActive || node.id === 'flows')
  useEffect(() => { if (containsActive) setExpanded(true) }, [containsActive])
  const active = node.id === activeId
  return <li>
    <div className={cx(styles.item, active && styles.itemActive)} style={{ paddingLeft: level ? 40 : 8 }}>
      <a href={node.href} aria-current={active ? 'page' : undefined} className={styles.itemLink}>
        {node.icon && <Icon name={node.icon} />}
        <span className={styles.itemText}>{node.text}</span>
      </a>
      {node.children && node.children.length > 0 && <IconButton name={expanded ? 'angle-down' : 'angle-right'} aria-label={`${expanded ? 'Collapse' : 'Expand'} ${node.text}`} onClick={() => setExpanded(value => !value)} />}
    </div>
    {expanded && node.children && node.children.length > 0 && <ul>{node.children.map(child => <MegaMenuItem key={child.id} node={child} activeId={activeId} level={level + 1} />)}</ul>}
  </li>
}

/** Icon-only menu entry: children are reachable from the parent page, and a
 * parent shows as active while one of its children is the current page. */
function RailItem({ node, activeId }: { node: NavNode; activeId: string }) {
  const styles = useStyles2(getStyles)
  const active = node.id === activeId || !!node.children?.some(child => child.id === activeId)
  return <li>
    <Tooltip content={node.text} placement="right">
      <a href={node.href} aria-label={node.text} aria-current={node.id === activeId ? 'page' : undefined} className={cx(styles.item, styles.railItem, active && styles.itemActive)}>
        {node.icon && <Icon name={node.icon} size="lg" />}
      </a>
    </Tooltip>
  </li>
}

const getStyles = (theme: GrafanaTheme2) => ({
  root: css({ height: '100vh', minHeight: 480, display: 'flex', flexDirection: 'column', background: theme.colors.background.canvas, overflow: 'hidden' }),
  topBar: css({
    height: 'var(--gofi-topbar-height)', flexShrink: 0, display: 'flex', alignItems: 'center', gap: theme.spacing(1),
    padding: theme.spacing(0, 1), background: theme.colors.background.primary, borderBottom: `1px solid ${theme.colors.border.weak}`,
  }),
  logo: css({ display: 'flex', alignItems: 'center', gap: theme.spacing(1), padding: theme.spacing(0, 0.5), marginRight: theme.spacing(1) }),
  wordmark: css({ fontSize: theme.typography.h5.fontSize, fontWeight: theme.typography.fontWeightMedium, color: theme.colors.text.primary, letterSpacing: '-0.01em', [theme.breakpoints.down('md')]: { display: 'none' } }),
  breadcrumbs: css({
    flex: 1, minWidth: 0, overflow: 'hidden',
    ol: { display: 'flex', alignItems: 'center', listStyle: 'none', margin: 0, padding: 0, whiteSpace: 'nowrap' },
    li: { display: 'flex', alignItems: 'center', minWidth: 0, fontSize: theme.typography.bodySmall.fontSize },
    'li:not(:last-child)': { [theme.breakpoints.down('md')]: { display: 'none' } },
    a: { color: theme.colors.text.secondary, '&:hover': { color: theme.colors.text.primary, textDecoration: 'underline' } },
    span: { color: theme.colors.text.primary, overflow: 'hidden', textOverflow: 'ellipsis' },
  }),
  separator: css({ color: theme.colors.text.secondary, margin: theme.spacing(0, 0.5) }),
  hideNarrow: css({ [theme.breakpoints.down('md')]: { display: 'none' } }),
  body: css({ flex: 1, minHeight: 0, display: 'flex', position: 'relative' }),
  backdrop: css({ position: 'absolute', inset: 0, background: theme.components.overlay.background, zIndex: theme.zIndex.navbarFixed }),
  menu: css({
    width: 'var(--gofi-menu-width)', flexShrink: 0, display: 'flex', flexDirection: 'column',
    background: theme.colors.background.primary, borderRight: `1px solid ${theme.colors.border.weak}`,
  }),
  menuOverlay: css({ position: 'absolute', top: 0, bottom: 0, left: 0, zIndex: theme.zIndex.navbarFixed + 1, boxShadow: theme.shadows.z3, maxWidth: '85vw' }),
  menuCompact: css({ width: 56 }),
  menuFooterCompact: css({ flexDirection: 'column', alignItems: 'center', padding: theme.spacing(1, 0) }),
  railItem: css({ justifyContent: 'center', height: theme.spacing(5), padding: 0, color: theme.colors.text.secondary }),
  modeBadge: css({ marginRight: 'auto', display: 'flex', alignItems: 'center' }),
  modeBadgeCompact: css({ marginRight: 0, marginBottom: theme.spacing(0.5) }),
  modeIcon: css({ display: 'flex', color: theme.colors.text.secondary, padding: theme.spacing(0.5), '&:hover': { color: theme.colors.text.primary } }),
  menuFooter: css({ display: 'flex', alignItems: 'center', justifyContent: 'flex-end', gap: theme.spacing(1), padding: theme.spacing(1, 1.5), borderTop: `1px solid ${theme.colors.border.weak}` }),
  menuItems: css({ flex: 1, minHeight: 0, overflowY: 'auto', padding: theme.spacing(1, 1, 2), ul: { listStyle: 'none', margin: 0, padding: 0 } }),
  item: css({
    position: 'relative', display: 'flex', alignItems: 'center', gap: theme.spacing(0.5), minHeight: theme.spacing(4), marginBottom: 2,
    paddingRight: theme.spacing(0.5), borderRadius: theme.shape.radius.default, color: theme.colors.text.secondary,
    // Transparent until selected, so the accent border never shifts layout.
    border: '1px solid transparent',
    '&:hover': { background: theme.colors.action.hover, color: theme.colors.text.primary },
    // The rail entry is itself the link; mega-menu links style themselves below.
    '&:focus-visible': { outline: `2px solid ${theme.colors.primary.main}`, outlineOffset: 1 },
  }),
  // Selected: accent border on an accent-tinted fill, accent text.
  itemActive: css({
    borderColor: theme.colors.primary.border, background: theme.colors.primary.transparent,
    color: theme.colors.primary.text, fontWeight: theme.typography.fontWeightMedium,
    '&:hover': { background: theme.colors.primary.transparent, color: theme.colors.primary.text, borderColor: theme.colors.primary.main },
  }),
  itemLink: css({ flex: 1, minWidth: 0, display: 'flex', alignItems: 'center', gap: theme.spacing(1.5), padding: theme.spacing(0.5, 1), color: 'inherit', borderRadius: theme.shape.radius.default, '&:focus-visible': { outline: `2px solid ${theme.colors.primary.main}`, outlineOffset: -2 } }),
  itemText: css({ overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }),
  main: css({ flex: 1, minWidth: 0, minHeight: 0, display: 'flex', flexDirection: 'column' }),
})
