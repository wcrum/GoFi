import type { ReactNode } from 'react'
import { css } from '@emotion/css'
import type { GrafanaTheme2, IconName } from '@grafana/data'
import { Icon, Stack, Tab, TabsBar, Text, useStyles2 } from '@grafana/ui'

export interface PageTab { label: string; href: string; active: boolean; icon?: IconName; counter?: number }
interface PageProps {
  title: string
  icon?: IconName
  subTitle?: ReactNode
  /** Right-aligned header controls, typically Grafana Buttons/LinkButtons. */
  actions?: ReactNode
  tabs?: PageTab[]
  /** `canvas` removes content padding and fills the remaining height. */
  layout?: 'standard' | 'canvas'
  children: ReactNode
}

/** Grafana page layout: header (icon, title, subtitle, actions), optional
 * tab bar, then padded scrolling content or a full-bleed canvas. */
export function Page({ title, icon, subTitle, actions, tabs, layout = 'standard', children }: PageProps) {
  const styles = useStyles2(getStyles)
  return <div className={styles.page}>
    <header className={styles.header}>
      <Stack justifyContent="space-between" alignItems="flex-start" wrap="wrap" gap={2}>
        <div className={styles.titleBlock}>
          <Stack alignItems="center" gap={1.5}>
            {icon && <Icon name={icon} size="xl" className={styles.titleIcon} />}
            <Text element="h1" variant="h2" truncate>{title}</Text>
          </Stack>
          {subTitle && <div className={styles.subTitle}><Text color="secondary" variant="body">{subTitle}</Text></div>}
        </div>
        {actions && <Stack gap={1} alignItems="center" wrap="wrap">{actions}</Stack>}
      </Stack>
      {tabs && <TabsBar className={styles.tabs}>{tabs.map(tab => <Tab key={tab.href} label={tab.label} href={tab.href} active={tab.active} icon={tab.icon} counter={tab.counter} />)}</TabsBar>}
    </header>
    <div className={layout === 'canvas' ? styles.canvas : styles.content}>{children}</div>
  </div>
}

const getStyles = (theme: GrafanaTheme2) => ({
  page: css({ display: 'flex', flexDirection: 'column', height: '100%', minHeight: 0, minWidth: 0 }),
  header: css({ padding: theme.spacing(2, 3, 0), flexShrink: 0, [theme.breakpoints.down('md')]: { padding: theme.spacing(2, 2, 0) } }),
  titleBlock: css({ minWidth: 0, flex: '1 1 320px', paddingBottom: theme.spacing(2) }),
  titleIcon: css({ color: theme.colors.text.secondary }),
  subTitle: css({ marginTop: theme.spacing(0.5), maxWidth: '80ch' }),
  tabs: css({ marginTop: theme.spacing(-1) }),
  content: css({ flex: 1, minHeight: 0, overflow: 'auto', padding: theme.spacing(2, 3, 3), [theme.breakpoints.down('md')]: { padding: theme.spacing(2) } }),
  canvas: css({ flex: 1, minHeight: 0, position: 'relative', borderTop: `1px solid ${theme.colors.border.weak}` }),
})
