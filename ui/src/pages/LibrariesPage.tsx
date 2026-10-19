import { LinkButton } from '@grafana/ui'
import SandboxManager from '../components/SandboxManager'
import { paths } from '../router'
import { Page } from '../shell/Page'
import { t } from '../i18n'

/** `selected` opens that library in the editor on arrival (e.g. from the New function wizard). */
export default function LibrariesPage({ selected }: { selected?: string }) {
  return <Page title={t('gofi.pages.functions.title', 'Functions')} icon="brackets-curly" subTitle={t('gofi.pages.functions.subtitle', 'Reusable functions, grouped in libraries, that flows call for framing, decoding, parsing, transforming, and delivery. Each library runs in its own isolated worker; today libraries are written in Python.')}
    actions={<LinkButton href={paths.newFunction()} icon="plus" variant="primary">{t('gofi.pages.functions.new-function', 'New function')}</LinkButton>}>
    <SandboxManager initialSelected={selected} />
  </Page>
}
