import type { ReactNode } from 'react'
import { css } from '@emotion/css'
import type { GrafanaTheme2, IconName } from '@grafana/data'
import { Icon, Stack, Tab, TabsBar, Text, useStyles2 } from '@grafana/ui'

export interface PageTab { label: string; href: string; active: boolean; icon?: IconName; counter?: number }
interface PageProps {
  title: string
  /** A Grafana icon name, or any element (an icon from another set) sized like one. */
  icon?: IconName | ReactNode
  subTitle?: ReactNode
  /** Right-aligned header controls, typically Grafana Buttons/LinkButtons. */
  actions?: ReactNode
  /** Full-width alerts between the title and the tabs (runtime failures). */
  banner?: ReactNode
  tabs?: PageTab[]
  /** `canvas` removes content padding and fills the remaining height. */
  layout?: 'standard' | 'canvas'
  children: ReactNode
}

/** Grafana page layout: header (icon, title, subtitle, actions), optional
 * tab bar, then padded scrolling content or a full-bleed canvas. */
export function Page({ title, icon, subTitle, actions, banner, tabs, layout = 'standard', children }: PageProps) {
  const styles = useStyles2(getStyles)
  return <div className={styles.page}>
    <header className={styles.header}>
      <Stack justifyContent="space-between" alignItems="flex-start" wrap="wrap" gap={2}>
        <div className={styles.titleBlock}>
          <Stack alignItems="center" gap={1.5}>
            {typeof icon === 'string' ? <Icon name={icon as IconName} size="xl" className={styles.titleIcon} /> : icon ? <span className={styles.titleIcon} aria-hidden>{icon}</span> : null}
            <Text element="h1" variant="h2" truncate>{title}</Text>
          </Stack>
          {subTitle && <div className={styles.subTitle}><Text color="secondary" variant="body">{subTitle}</Text></div>}
        </div>
        {actions && <div className={styles.actions}><Stack gap={1} alignItems="center" wrap="wrap">{actions}</Stack></div>}
      </Stack>
      {banner && <div className={styles.banner}>{banner}</div>}
      {tabs && <TabsBar className={styles.tabs}>{tabs.map(tab => <Tab key={tab.href} label={tab.label} href={tab.href} active={tab.active} icon={tab.icon} counter={tab.counter} />)}</TabsBar>}
    </header>
    <div className={layout === 'canvas' ? styles.canvas : styles.content}>{children}</div>
  </div>
}

const getStyles = (theme: GrafanaTheme2) => ({
  page: css({ display: 'flex', flexDirection: 'column', height: '100%', minHeight: 0, minWidth: 0 }),
  header: css({ padding: theme.spacing(2, 3, 0), flexShrink: 0, [theme.breakpoints.down('md')]: { padding: theme.spacing(2, 2, 0) } }),
  titleBlock: css({ minWidth: 0, flex: '1 1 320px', paddingBottom: theme.spacing(2) }),
  // Same bottom padding as the title block, so actions that wrap under the
  // title (narrow main, a side panel open) never sit on the tab bar.
  actions: css({ paddingBottom: theme.spacing(2) }),
  titleIcon: css({ color: theme.colors.text.secondary, display: 'inline-flex', alignItems: 'center', svg: { width: 24, height: 24 } }),
  subTitle: css({ marginTop: theme.spacing(0.5), maxWidth: '80ch' }),
  banner: css({ display: 'flex', flexDirection: 'column', gap: theme.spacing(1), paddingBottom: theme.spacing(2), '&:empty': { display: 'none' } }),
  tabs: css({ marginTop: theme.spacing(-1) }),
  content: css({ flex: 1, minHeight: 0, position: 'relative', overflow: 'auto', padding: theme.spacing(2, 3, 3), [theme.breakpoints.down('md')]: { padding: theme.spacing(2) } }),
  canvas: css({ flex: 1, minHeight: 0, position: 'relative', borderTop: `1px solid ${theme.colors.border.weak}` }),
})
