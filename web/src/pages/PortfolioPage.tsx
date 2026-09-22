import { useMemo, useState } from 'react'
import { Link } from 'react-router-dom'
import { ApiError } from '../api/client'
import { usePortfolioPnL, useProducts, useWinLoss } from '../api/hooks'
import type { PnL } from '../api/types'
import { Empty, ErrorBox, Loading } from '../components/Status'
import { ru } from '../i18n/ru'
import { fmtMoney } from '../lib/format'

/** Текущий месяц в формате YYYY-MM: период по умолчанию для финансовых отчётов. */
function currentPeriod(): string {
  const now = new Date()
  return `${now.getUTCFullYear()}-${String(now.getUTCMonth() + 1).padStart(2, '0')}`
}

export function PortfolioPage() {
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
          <h1>{ru.portfolio.title}</h1>
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
        {winLoss.isError && <p className="muted">{ru.portfolio.winLossForbidden}</p>}
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
