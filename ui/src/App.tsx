import { lazy, Suspense } from 'react'
import { useRoute } from './router'
import { AppChrome } from './shell/AppChrome'
import { NotFoundPage } from './pages/NotFoundPage'
import { PageLoading } from './components/PageLoading'

const HomePage = lazy(() => import('./pages/HomePage'))
const FlowsPage = lazy(() => import('./pages/FlowsPage'))
const FlowPage = lazy(() => import('./pages/FlowPage'))
const NewFlowPage = lazy(() => import('./pages/NewFlowPage'))
const ExplorePage = lazy(() => import('./pages/ExplorePage'))
const LibrariesPage = lazy(() => import('./pages/LibrariesPage'))
const ExpressionsPage = lazy(() => import('./pages/ExpressionsPage'))
const EndpointsPage = lazy(() => import('./pages/EndpointsPage'))
const ConfigurationPage = lazy(() => import('./pages/ConfigurationPage'))
const ClusterPage = lazy(() => import('./pages/ClusterPage'))

export default function App() {
  const route = useRoute()
  return <AppChrome route={route}>
    <Suspense fallback={<PageLoading text="Loading..." />}>
      {route.page === 'home' ? <HomePage />
        : route.page === 'flows' ? <FlowsPage />
        : route.page === 'flow-new' ? <NewFlowPage />
        : route.page === 'flow' ? <FlowPage key={route.name} name={route.name} tab={route.tab} />
        : route.page === 'explore' ? <ExplorePage flow={route.flow} />
        : route.page === 'libraries' ? <LibrariesPage />
        : route.page === 'expressions' ? <ExpressionsPage selected={route.name} />
        : route.page === 'endpoints' ? <EndpointsPage />
        : route.page === 'configuration' ? <ConfigurationPage />
        : route.page === 'cluster' ? <ClusterPage tab={route.tab} />
        : <NotFoundPage path={route.path} />}
    </Suspense>
  </AppChrome>
}
