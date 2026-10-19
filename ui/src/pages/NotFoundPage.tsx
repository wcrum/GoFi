import { LinkButton } from '@grafana/ui'
import { EmptyState } from '../components/EmptyState'
import { Page } from '../shell/Page'
import { paths } from '../router'
import { t } from '../i18n'

export function NotFoundPage({ path, message }: { path: string; message?: string }) {
  return <Page title={t('gofi.pages.not-found.title', 'Page not found')} icon="exclamation-triangle">
    <EmptyState variant="not-found" message={message ?? t('gofi.pages.not-found.message', 'Nothing is available at {{path}}.', { path: path || '/' })} button={<LinkButton href={paths.home()} icon="home-alt">{t('gofi.pages.not-found.go-home', 'Go to Home')}</LinkButton>} />
  </Page>
}
