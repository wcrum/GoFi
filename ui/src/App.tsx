import { lazy, Suspense } from 'react'
import { useRoute } from './router'
import { AppChrome } from './shell/AppChrome'
import { NotFoundPage } from './pages/NotFoundPage'
import { PageLoading } from './components/PageLoading'
import { t } from './i18n'

const HomePage = lazy(() => import('./pages/HomePage'))
const FlowsPage = lazy(() => import('./pages/FlowsPage'))
const FlowPage = lazy(() => import('./pages/FlowPage'))
const NewFlowPage = lazy(() => import('./pages/NewFlowPage'))
const ExplorePage = lazy(() => import('./pages/ExplorePage'))
const LibrariesPage = lazy(() => import('./pages/LibrariesPage'))
const NewFunctionPage = lazy(() => import('./pages/NewFunctionPage'))
const ExpressionsPage = lazy(() => import('./pages/ExpressionsPage'))
const EndpointsPage = lazy(() => import('./pages/EndpointsPage'))
const ClusterPage = lazy(() => import('./pages/ClusterPage'))
const AdministrationPage = lazy(() => import('./pages/AdministrationPage'))
const AssistantAdminPage = lazy(() => import('./pages/administration/AssistantAdminPage'))

export default function App() {
  const route = useRoute()
  return <AppChrome route={route}>
    <Suspense fallback={<PageLoading text={t('gofi.common.loading', 'Loading...')} />}>
      {route.page === 'home' ? <HomePage />
        : route.page === 'flows' ? <FlowsPage />
        : route.page === 'flow-new' ? <NewFlowPage />
        : route.page === 'flow' ? <FlowPage key={route.name} name={route.name} tab={route.tab} />
        : route.page === 'explore' ? <ExplorePage flow={route.flow} />
        : route.page === 'libraries' ? <LibrariesPage selected={route.name} />
        : route.page === 'function-new' ? <NewFunctionPage />
        : route.page === 'expressions' ? <ExpressionsPage selected={route.name} />
        : route.page === 'endpoints' ? <EndpointsPage />
        : route.page === 'cluster' ? <ClusterPage tab={route.tab} />
        : route.page === 'administration' ? (route.section === 'assistant' ? <AssistantAdminPage /> : <AdministrationPage />)
        : <NotFoundPage path={route.path} />}
    </Suspense>
  </AppChrome>
}
