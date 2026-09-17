import { lazy, Suspense } from 'react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { BrowserRouter, Navigate, Outlet, Route, Routes, useLocation } from 'react-router-dom'
import { ApiError } from './api/client'
import { AuthProvider } from './auth/AuthContext'
import { useAuth } from './auth/useAuth'
import { Layout } from './components/Layout'
import { Loading } from './components/Status'
import { ru } from './i18n/ru'
import { AdminPage } from './pages/AdminPage'
import { CallbackPage } from './pages/CallbackPage'
import { DeliveryPage } from './pages/DeliveryPage'
import { HubPage } from './pages/HubPage'
import { LoginPage } from './pages/LoginPage'
import { ProductBuilderPage } from './pages/ProductBuilderPage'
import { ProductPage } from './pages/ProductPage'
import { ProductsPage } from './pages/ProductsPage'
import { RoadmapPage } from './pages/RoadmapPage'

const GraphPage = lazy(() => import('./pages/GraphPage').then((m) => ({ default: m.GraphPage })))

const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      retry: (count, err) => !(err instanceof ApiError && err.status < 500) && count < 2,
      refetchOnWindowFocus: false,
    },
  },
})

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
    <QueryClientProvider client={queryClient}>
      <AuthProvider>
        <BrowserRouter>
          <Routes>
            <Route path="/login" element={<LoginPage />} />
            <Route path="/callback" element={<CallbackPage />} />
            <Route element={<RequireAuth />}>
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
                <Route path="/hub" element={<HubPage />} />
                <Route path="/delivery" element={<DeliveryPage />} />
                <Route path="/admin" element={<AdminPage />} />
                <Route path="*" element={<NotFound />} />
              </Route>
            </Route>
          </Routes>
        </BrowserRouter>
      </AuthProvider>
    </QueryClientProvider>
  )
}
