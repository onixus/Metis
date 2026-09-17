import { Navigate, NavLink, Outlet } from 'react-router-dom'
import { ApiError } from '../api/client'
import { useMe } from '../api/hooks'
import { useAuth } from '../auth/useAuth'
import { ru } from '../i18n/ru'
import { canReadPortfolioDecisions, canSeeFinance, canSeeCompliance, isAdmin } from '../lib/roles'
import { Badge } from './Status'

export function Layout() {
  const { logout } = useAuth()
  const me = useMe()
  const admin = isAdmin(me.data?.roles)

  if (me.isError && me.error instanceof ApiError && me.error.status === 401) {
    return <Navigate to="/login" replace />
  }

  return (
    <div className="app">
      <header className="topbar">
        <div className="brand">
          <span className="brand-title">{ru.app.title}</span>
          <span className="brand-sub">{ru.app.subtitle}</span>
        </div>
        <nav className="nav">
          <NavLink to="/" end>{ru.nav.products}</NavLink>
          <NavLink to="/graph">{ru.nav.graph}</NavLink>
          <NavLink to="/hub">{ru.nav.hub}</NavLink>
          <NavLink to="/delivery">{ru.nav.delivery}</NavLink>
          {canSeeFinance(me.data) && <NavLink to="/portfolio">{ru.nav2.portfolio}</NavLink>}
          {canReadPortfolioDecisions(me.data) && <NavLink to="/decisions">{ru.nav2.decisions}</NavLink>}
          {canSeeCompliance(me.data) && <NavLink to="/compliance">{ru.nav2.compliance}</NavLink>}
          {admin && <NavLink to="/admin">{ru.nav.admin}</NavLink>}
        </nav>
        <div className="me">
          {me.data && (
            <>
              <span className="me-subject">{me.data.subject}</span>
              <Badge tone={me.data.audience === 'sales_safe' ? 'warn' : 'info'}>
                {me.data.audience === 'sales_safe' ? ru.me.audienceSalesSafe : ru.me.audienceInternal}
              </Badge>
            </>
          )}
          <button type="button" className="btn btn-sm" onClick={() => void logout()}>
            {ru.app.logout}
          </button>
        </div>
      </header>
      <main className="content">
        <Outlet />
      </main>
    </div>
  )
}
