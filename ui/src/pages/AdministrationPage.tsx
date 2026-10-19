import type { ReactNode } from 'react'
import { css } from '@emotion/css'
import type { GrafanaTheme2 } from '@grafana/data'
import { Card, useStyles2 } from '@grafana/ui'
import { FaRobot } from 'react-icons/fa'
import { paths } from '../router'
import { Page } from '../shell/Page'
import { cardSurface } from '../theme/surfaces'
import { t } from '../i18n'

interface Section { id: string; title: string; description: string; icon: ReactNode; href: string }

/** Administration overview: the controller-wide settings, one card per section. */
export default function AdministrationPage() {
  const styles = useStyles2(getStyles)
  const sections: Section[] = [
    {
      id: 'assistant', icon: <FaRobot size={28} aria-hidden />, href: paths.administration('assistant'),
      title: t('gofi.pages.administration.assistant-card-title', 'AI assistant'),
      description: t('gofi.pages.administration.assistant-card-description', 'GoFiBot: model endpoint, API key, what it may change, and the tools it is offered.'),
    },
  ]
  return <Page title={t('gofi.pages.administration.title', 'Administration')} icon="cog" subTitle={t('gofi.pages.administration.subtitle', 'Settings for this GoFi controller.')}>
    <div className={styles.grid}>
      {sections.map(section => <Card key={section.id} href={section.href} noMargin className={styles.card}>
        <Card.Heading>{section.title}</Card.Heading>
        <Card.Description>{section.description}</Card.Description>
        <Card.Figure><span className={styles.figure}>{section.icon}</span></Card.Figure>
      </Card>)}
    </div>
  </Page>
}

const getStyles = (theme: GrafanaTheme2) => ({
  grid: css({ display: 'grid', gridTemplateColumns: 'repeat(auto-fill, minmax(280px, 1fr))', gap: theme.spacing(2), maxWidth: 960 }),
  card: cardSurface(theme),
  figure: css({ color: theme.colors.text.secondary, display: 'inline-flex', alignItems: 'center' }),
})
