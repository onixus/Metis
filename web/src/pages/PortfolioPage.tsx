import { useMemo, useState } from 'react'
import { Link } from 'react-router-dom'
import { ApiError } from '../api/client'
import { useHubs, useLinks, useMe, usePortfolioPnL, useProducts, useWinLoss } from '../api/hooks'
import type { PnL } from '../api/types'
import { Badge, Empty, ErrorBox, Loading } from '../components/Status'
import { ru } from '../i18n/ru'
import { fmtMoney, pick } from '../lib/format'

/** Текущий месяц в формате YYYY-MM: период по умолчанию для финансовых отчётов. */
function currentPeriod(): string {
  const now = new Date()
  return `${now.getUTCFullYear()}-${String(now.getUTCMonth() + 1).padStart(2, '0')}`
}

export function PortfolioPage() {
  const me = useMe()
  const products = useProducts()
  const hubs = useHubs()
  const links = useLinks()
  const hubById = new Map((hubs.data ?? []).map(hub => [hub.product_id, hub]))
  return <section className="stack">
    <div className="page-head"><div><h1>Портфель</h1><p className="muted">Продукты, роль хабов и зависимости доступного портфеля.</p></div><div className="row wrap-row"><Link className="btn" to="/graph">Открыть граф</Link><Link className="btn" to="/">Все продукты</Link></div></div>
    {(products.isPending || hubs.isPending || links.isPending) && <Loading />}
    {products.isError && <ErrorBox error={products.error} onRetry={() => void products.refetch()} />}
    {hubs.isError && <ErrorBox error={hubs.error} onRetry={() => void hubs.refetch()} />}
    {links.isError && <ErrorBox error={links.error} onRetry={() => void links.refetch()} />}
    {products.data && <>
      <div className="stats"><Stat label="Доступно продуктов" value={String(products.data.length)} /><Stat label="Хабов" value={hubs.data ? String(hubs.data.filter(hub => hub.manual || hub.computed).length) : ru.app.dash} /><Stat label="Связей" value={links.data ? String(links.data.length) : ru.app.dash} /></div>
      <div className="card stack"><h2>Состав портфеля</h2>{!products.data.length ? <Empty /> : <div className="table-wrap"><table className="table"><thead><tr><th>Продукт</th><th>Тип</th><th>Владелец</th><th>Жизненный цикл</th><th>Роль хаба</th><th>РБПО</th><th>Зависимости</th></tr></thead><tbody>{products.data.map(product => {
        const hub = hubById.get(product.id)
        const related = (links.data ?? []).filter(link => link.from_product_id === product.id || link.to_product_id === product.id)
        return <tr key={product.id}><td><Link to={`/products/${product.id}`}>{product.name}</Link></td><td>{pick(ru.product.types, product.type)}</td><td>{product.owner || ru.app.dash}</td><td>{pick(ru.product.lifecycles, product.lifecycle)}</td><td>{hub?.manual && <Badge tone="warn">{ru.product.hubManual}</Badge>}{' '}{hub?.computed && <Badge tone="info">{ru.product.hubComputed}</Badge>}{!hub?.manual && !hub?.computed && ru.app.dash}</td><td>{product.ssdlc_certified ? ru.app.yes : ru.app.no}</td><td>{links.data ? <Link to={`/graph?product=${encodeURIComponent(product.id)}`}>{related.length}</Link> : ru.app.dash}</td></tr>
      })}</tbody></table></div>}</div>
    </>}
    {me.data?.finance && me.data.finance !== 'none' ? <PortfolioFinance /> : <p className="muted">Финансовые отчёты доступны при наличии финансового уровня доступа.</p>}
  </section>
}

function PortfolioFinance() {
  const [period, setPeriod] = useState(currentPeriod)
  const products = useProducts()
  const pnl = usePortfolioPnL(period)
  const winLoss = useWinLoss('')
  const nameById = useMemo(
    () => new Map((products.data ?? []).map((p) => [p.id, p.name])),
    [products.data],
  )

  const forbidden = pnl.isError && pnl.error instanceof ApiError && pnl.error.status === 403

  return (
    <section className="stack">
      <div className="page-head">
        <div>
          <h2>{ru.portfolio.title}</h2>
          <div className="muted">{ru.portfolio.subtitle}</div>
        </div>
        <label className="field">
          <span>{ru.portfolio.period}</span>
          <input
            type="month"
            value={period}
            onChange={(e) => setPeriod(e.target.value)}
            aria-label={ru.portfolio.period}
          />
        </label>
      </div>

      {forbidden && <p className="muted">{ru.portfolio.forbidden}</p>}
      {pnl.isError && !forbidden && <ErrorBox error={pnl.error} onRetry={() => void pnl.refetch()} />}
      {pnl.isPending && <Loading />}

      {pnl.data && (
        <>
          <div className="stats">
            <Stat label={ru.portfolio.revenue} value={fmtMoney(pnl.data.revenue)} />
            <Stat label={ru.portfolio.costs} value={fmtMoney(pnl.data.costs)} />
            <Stat
              label={ru.portfolio.profit}
              value={fmtMoney(pnl.data.profit)}
              tone={pnl.data.profit.amount < 0 ? 'danger' : 'ok'}
            />
          </div>
          <div className="card stack">
            <h2>{ru.portfolio.pnlTitle}</h2>
            {pnl.data.products.length === 0 ? <Empty text={ru.portfolio.noData} /> : <PnLTable rows={pnl.data.products} nameById={nameById} />}
          </div>
        </>
      )}

      <div className="card stack">
        <h2>{ru.portfolio.winLoss}</h2>
        {winLoss.isPending && <Loading />}
        {winLoss.isError && (winLoss.error instanceof ApiError && winLoss.error.status === 403 ? <p className="muted">{ru.portfolio.winLossForbidden}</p> : <ErrorBox error={winLoss.error} onRetry={() => void winLoss.refetch()} />)}
        {winLoss.data && (
          <>
            <div className="stats">
              <Stat label={ru.portfolio.won} value={String(winLoss.data.won)} tone="ok" />
              <Stat label={ru.portfolio.lost} value={String(winLoss.data.lost)} tone={winLoss.data.lost > 0 ? 'warn' : 'ok'} />
            </div>
            {(winLoss.data.by_reason ?? []).length === 0 ? (
              <Empty text={ru.portfolio.noWinLoss} />
            ) : (
              <table className="table">
                <thead>
                  <tr>
                    <th>{ru.portfolio.reason}</th>
                    <th>{ru.portfolio.won}</th>
                    <th>{ru.portfolio.lost}</th>
                    <th>{ru.portfolio.amount}</th>
                  </tr>
                </thead>
                <tbody>
                  {(winLoss.data.by_reason ?? []).map((r) => (
                    <tr key={r.key}>
                      <td>{r.key}</td>
                      <td>{r.won}</td>
                      <td>{r.lost}</td>
                      <td>{fmtMoney(r.amount)}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            )}
          </>
        )}
      </div>
    </section>
  )
}

function PnLTable({ rows, nameById }: { rows: PnL[]; nameById: Map<string, string> }) {
  return (
    <div className="table-wrap">
      <table className="table">
        <thead>
          <tr>
            <th>{ru.portfolio.product}</th>
            <th>{ru.portfolio.revenue}</th>
            <th>{ru.portfolio.bundleRevenue}</th>
            <th>{ru.portfolio.directCosts}</th>
            <th>{ru.portfolio.hubLoad}</th>
            <th>{ru.portfolio.directProfit}</th>
            <th>{ru.portfolio.loadedProfit}</th>
          </tr>
        </thead>
        <tbody>
          {rows.map((row) => (
            <tr key={row.product_id}>
              <td>
                <Link to={`/products/${row.product_id}`}>{nameById.get(row.product_id) ?? row.product_id}</Link>
              </td>
              <td>{fmtMoney(row.revenue)}</td>
              <td>{fmtMoney(row.bundle_revenue)}</td>
              <td>{fmtMoney(row.direct_costs)}</td>
              <td>{fmtMoney(row.hub_load)}</td>
              <td>{fmtMoney(row.direct_profit)}</td>
              <td className={row.loaded_profit.amount < 0 ? 'text-danger' : ''}>{fmtMoney(row.loaded_profit)}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}

function Stat({ label, value, tone }: { label: string; value: string; tone?: 'ok' | 'warn' | 'danger' }) {
  return (
    <div className={`stat${tone ? ` stat-${tone}` : ''}`}>
      <div className="stat-value">{value}</div>
      <div className="stat-label">{label}</div>
    </div>
  )
}
