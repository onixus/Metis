import { Boxes, Network, Layers, Truck, ChartNoAxesCombined, BriefcaseBusiness, ListChecks, ShieldCheck, Settings } from 'lucide-react'
import { ThemeSwitch } from './ThemeSwitch'
import { useEffect } from 'react'
import { NavLink, Outlet } from 'react-router-dom'
import { ApiError } from '../api/client'
import { useMe } from '../api/hooks'
import { useAuth } from '../auth/useAuth'
import { ru } from '../i18n/ru'
import { canReadPortfolioDecisions, canSeeFinance, canSeeCompliance, isAdmin } from '../lib/roles'
import { Badge, Loading } from './Status'

export function Layout() {
  const { logout, expireSession } = useAuth()
  const me = useMe()
  const admin = isAdmin(me.data?.roles)
  const unauthorized = me.isError && me.error instanceof ApiError && me.error.status === 401

  useEffect(() => {
    if (unauthorized) void expireSession().catch(() => undefined)
  }, [unauthorized, expireSession])

  if (unauthorized) return <Loading />

  return (
    <div className="app">
      <header className="topbar">
        <div className="brand">
          <span className="brand-title">{ru.app.title}</span>
          <span className="brand-sub">{ru.app.subtitle}</span>
        </div>
        <nav className="nav">
          <NavLink to="/" end><Boxes aria-hidden="true" />{ru.nav.products}</NavLink>
          <NavLink to="/graph"><Network aria-hidden="true" />{ru.nav.graph}</NavLink>
          <NavLink to="/hub"><Layers aria-hidden="true" />{ru.nav.hub}</NavLink>
          <NavLink to="/delivery"><Truck aria-hidden="true" />{ru.nav.delivery}</NavLink>
          {me.data?.roles.includes('finance') && me.data.finance === 'full' && <NavLink to="/economics"><ChartNoAxesCombined aria-hidden="true" />Экономика</NavLink>}
          {canSeeFinance(me.data) && <NavLink to="/portfolio"><BriefcaseBusiness aria-hidden="true" />{ru.nav2.portfolio}</NavLink>}
          {canReadPortfolioDecisions(me.data) && <NavLink to="/decisions"><ListChecks aria-hidden="true" />{ru.nav2.decisions}</NavLink>}
          {canSeeCompliance(me.data) && <NavLink to="/compliance"><ShieldCheck aria-hidden="true" />{ru.nav2.compliance}</NavLink>}
          {admin && <NavLink to="/admin"><Settings aria-hidden="true" />{ru.nav.admin}</NavLink>}
        </nav>
        <div className="me">
          <ThemeSwitch />
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
