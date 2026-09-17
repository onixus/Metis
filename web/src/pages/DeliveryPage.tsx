import { useMemo } from 'react'
import { Link } from 'react-router-dom'
import { ApiError } from '../api/client'
import { useFeaturesOfProducts, useProducts } from '../api/hooks'
import type { Feature } from '../api/types'
import { Badge, Empty, ErrorBox, Loading } from '../components/Status'
import { ru } from '../i18n/ru'
import { daysBetween, fmtDate, pick } from '../lib/format'

interface Row {
  f: Feature
  productId: string
  delta: number | null
}

export function DeliveryPage() {
  const products = useProducts()
  const ids = useMemo(() => (products.data ?? []).map((p) => p.id), [products.data])
  const nameById = useMemo(() => new Map((products.data ?? []).map((p) => [p.id, p.name])), [products.data])
  const queries = useFeaturesOfProducts(ids)

  if (products.isPending) return <Loading />
  if (products.isError) return <ErrorBox error={products.error} onRetry={() => void products.refetch()} />

  const loading = queries.some((q) => q.isPending)
  const hidden = queries.filter((q) => q.isError && q.error instanceof ApiError && q.error.status === 403).length
  const otherErrors = queries.filter((q) => q.isError && !(q.error instanceof ApiError && q.error.status === 403))

  const rows: Row[] = queries.flatMap((q, i) =>
    (q.data ?? [])
      .filter((f) => f.status === 'in_progress' || f.status === 'planned')
      .map((f) => ({ f, productId: ids[i], delta: daysBetween(f.planned_date, f.implied_date) })),
  )
  const inProgress = rows.filter((r) => r.f.status === 'in_progress').length
  const planned = rows.filter((r) => r.f.status === 'planned').length
  const affected = rows.filter((r) => r.f.affected)
  const late = rows.filter((r) => (r.delta ?? 0) > 0).length

  return (
    <section className="stack">
      <div className="page-head">
        <div>
          <h1>{ru.delivery.title}</h1>
          <div className="muted">{ru.delivery.subtitle}</div>
        </div>
      </div>
      {hidden > 0 && <p className="muted">{ru.delivery.hiddenProducts(hidden)}</p>}
      {otherErrors.map((q, i) => (
        <ErrorBox key={i} error={q.error} />
      ))}
      <div className="stats">
        <Stat label={ru.delivery.inProgress} value={inProgress} />
        <Stat label={ru.delivery.planned} value={planned} />
        <Stat label={ru.delivery.late} value={late} tone={late > 0 ? 'danger' : 'ok'} />
        <Stat label={ru.delivery.affected} value={affected.length} tone={affected.length > 0 ? 'warn' : 'ok'} />
      </div>

      <div className="card stack">
        <h2>{ru.delivery.planFact}</h2>
        {loading && <Loading />}
        {!loading && rows.length === 0 ? (
          <Empty text={ru.productPage.noFeatures} />
        ) : (
          <FeatureTable rows={rows} nameById={nameById} />
        )}
      </div>

      <div className="card stack">
        <h2>{ru.delivery.affectedList}</h2>
        {!loading && affected.length === 0 ? <Empty text={ru.hub.noAffected} /> : <FeatureTable rows={affected} nameById={nameById} />}
      </div>

      <div className="card stack card-placeholder">
        <h2>{ru.delivery.sprintMetrics}</h2>
        <p className="muted">{ru.delivery.sprintMetricsPlaceholder}</p>
      </div>
    </section>
  )
}

function Stat({ label, value, tone = 'neutral' }: { label: string; value: number; tone?: 'neutral' | 'ok' | 'warn' | 'danger' }) {
  return (
    <div className={`stat stat-${tone}`}>
      <div className="stat-value">{value}</div>
      <div className="stat-label">{label}</div>
    </div>
  )
}

function FeatureTable({ rows, nameById }: { rows: Row[]; nameById: Map<string, string> }) {
  const sorted = [...rows].sort((a, b) => (b.delta ?? -1) - (a.delta ?? -1))
  return (
    <div className="table-wrap">
      <table className="table">
        <thead>
          <tr>
            <th>{ru.delivery.product}</th>
            <th>{ru.feature.name}</th>
            <th>{ru.feature.status}</th>
            <th>{ru.feature.plannedDate}</th>
            <th>{ru.feature.impliedDate}</th>
            <th>{ru.feature.delta}</th>
            <th>{ru.feature.affected}</th>
          </tr>
        </thead>
        <tbody>
          {sorted.map(({ f, productId, delta }) => (
            <tr key={f.id} className={f.affected ? 'row-affected' : undefined}>
              <td>
                <Link to={`/products/${productId}`}>{nameById.get(productId) ?? productId}</Link>
              </td>
              <td>
                {f.name}
                {f.external_key && <span className="muted mono"> {f.external_key}</span>}
              </td>
              <td>
                <Badge tone={f.status === 'in_progress' ? 'warn' : 'info'}>{pick(ru.feature.statuses, f.status)}</Badge>
              </td>
              <td>{fmtDate(f.planned_date)}</td>
              <td>{fmtDate(f.implied_date)}</td>
              <td className="num">
                {delta === null ? (
                  <span className="muted">{ru.delivery.noDate}</span>
                ) : delta > 0 ? (
                  <Badge tone="danger">+{delta}</Badge>
                ) : (
                  <Badge tone="ok">{delta}</Badge>
                )}
              </td>
              <td>{f.affected ? <Badge tone="danger">{ru.app.yes}</Badge> : ru.app.no}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}
