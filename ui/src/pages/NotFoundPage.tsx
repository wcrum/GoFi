import { LinkButton } from '@grafana/ui'
import { EmptyState } from '../components/EmptyState'
import { Page } from '../shell/Page'
import { paths } from '../router'

export function NotFoundPage({ path, message }: { path: string; message?: string }) {
  return <Page title="Page not found" icon="exclamation-triangle">
    <EmptyState variant="not-found" message={message ?? `Nothing is available at ${path || '/'}.`} button={<LinkButton href={paths.home()} icon="home-alt">Go to Home</LinkButton>} />
  </Page>
}
