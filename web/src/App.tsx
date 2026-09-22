import { lazy, Suspense, useEffect, useState, type ReactNode } from 'react'
import { QueryClientProvider } from '@tanstack/react-query'
import { BrowserRouter, Navigate, Outlet, Route, Routes, useLocation } from 'react-router-dom'
import { createSessionClient, discardSessionClient } from './api/sessionCache'
import { AuthProvider } from './auth/AuthContext'
import { useAuth } from './auth/useAuth'
import { Layout } from './components/Layout'
import { Loading } from './components/Status'
import { ru } from './i18n/ru'
import { AdminPage } from './pages/AdminPage'
import { CallbackPage } from './pages/CallbackPage'
import { CommitmentsPage } from './pages/CommitmentsPage'
import { ComplianceDashboardPage, CompliancePage } from './pages/CompliancePage'
import { DecisionsPage } from './pages/DecisionsPage'
import { DiscoveryPage } from './pages/DiscoveryPage'
import { TracePage } from './pages/TracePage'
import { DeliveryPage } from './pages/DeliveryPage'
import { EconomicsPage } from './pages/EconomicsPage'
import { HubPage } from './pages/HubPage'
import { LoginPage } from './pages/LoginPage'
import { ProductBuilderPage } from './pages/ProductBuilderPage'
import { ProductPage } from './pages/ProductPage'
import { ProductsPage } from './pages/ProductsPage'
import { RoadmapPage } from './pages/RoadmapPage'

const GraphPage = lazy(() => import('./pages/GraphPage').then((m) => ({ default: m.GraphPage })))

/** Each identity owns a separate cache; late mutation callbacks retain only their old client. */
function SessionCache({ children }: { children: ReactNode }) {
  const [client] = useState(createSessionClient)
  useEffect(() => () => {
    discardSessionClient(client)
  }, [client])
  return <QueryClientProvider client={client}>{children}</QueryClientProvider>
}

function SessionBoundary({ children }: { children: ReactNode }) {
  const { sessionVersion } = useAuth()
  return <SessionCache key={sessionVersion}>{children}</SessionCache>
}

function RequireAuth() {
  const { ready, authenticated } = useAuth()
  const location = useLocation()
  if (!ready) return <Loading />
  if (!authenticated) return <Navigate to="/login" replace state={{ from: location.pathname }} />
  return <Outlet />
}

function NotFound() {
  return <p className="muted">{ru.app.notFound}</p>
}

export default function App() {
  return (
    <AuthProvider>
        <BrowserRouter>
          <Routes>
            <Route path="/login" element={<LoginPage />} />
            <Route path="/callback" element={<CallbackPage />} />
            <Route element={<SessionBoundary><RequireAuth /></SessionBoundary>}>
              <Route element={<Layout />}>
                <Route path="/" element={<ProductsPage />} />
                <Route
                  path="/graph"
                  element={
                    <Suspense fallback={<Loading />}>
                      <GraphPage />
                    </Suspense>
                  }
                />
                <Route path="/products/new" element={<ProductBuilderPage />} />
                <Route path="/products/:id/edit" element={<ProductBuilderPage />} />
                <Route path="/products/:id" element={<ProductPage />} />
                <Route path="/products/:id/roadmap" element={<RoadmapPage />} />
                <Route path="/products/:id/discovery" element={<DiscoveryPage />} />
                <Route path="/products/:id/commitments" element={<CommitmentsPage />} />
                <Route path="/products/:id/compliance" element={<CompliancePage />} />
                <Route path="/products/:id/decisions" element={<DecisionsPage />} />
                <Route path="/decisions" element={<DecisionsPage />} />
                <Route path="/compliance" element={<ComplianceDashboardPage />} />
                <Route path="/trace/:kind/:id" element={<TracePage />} />
                <Route path="/hub" element={<HubPage />} />
                <Route path="/delivery" element={<DeliveryPage />} />
                <Route path="/economics" element={<EconomicsPage />} />
                <Route path="/admin" element={<AdminPage />} />
                <Route path="*" element={<NotFound />} />
              </Route>
            </Route>
          </Routes>
        </BrowserRouter>
    </AuthProvider>
  )
}
