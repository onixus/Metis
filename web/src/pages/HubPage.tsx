import { useMemo } from 'react'
import { Link } from 'react-router-dom'
import { ApiError } from '../api/client'
import { useContracts, useFeatureValues, useFeatures, useFeaturesOfProducts, useHubs, useLinks, useProducts } from '../api/hooks'
import type { Feature } from '../api/types'
import { Badge, Empty, ErrorBox, Loading } from '../components/Status'
import { ru } from '../i18n/ru'
import { fmtDate, fmtMoney, pick } from '../lib/format'
import { ContractsTable } from './ProductPage'

export function HubPage() {
  const hubs = useHubs()
  const products = useProducts()
  const links = useLinks()
  const contracts = useContracts()

  const hub = useMemo(() => {
    const list = hubs.data ?? []
    if (list.length === 0) return null
    return [...list].sort((a, b) => b.in_degree - a.in_degree)[0]
  }, [hubs.data])

  const productById = useMemo(() => new Map((products.data ?? []).map((p) => [p.id, p])), [products.data])

  const dependents = useMemo(() => {
    if (!hub) return []
    const ids = new Set<string>()
    for (const l of links.data ?? []) {
      if (l.to_product_id === hub.product_id && l.from_product_id !== hub.product_id) ids.add(l.from_product_id)
    }
    return [...ids].filter((id) => productById.has(id))
  }, [hub, links.data, productById])

  const hubId = hub?.product_id ?? ''
  const hubFeatures = useFeatures(hubId, hubId !== '')
  const hubValues = useFeatureValues(hubId, hubId !== '')
  const dependentFeatures = useFeaturesOfProducts(dependents)

  if (hubs.isPending || products.isPending || links.isPending) return <Loading />
  if (hubs.isError) return <ErrorBox error={hubs.error} onRetry={() => void hubs.refetch()} />
  if (products.isError) return <ErrorBox error={products.error} onRetry={() => void products.refetch()} />
  if (links.isError) return <ErrorBox error={links.error} onRetry={() => void links.refetch()} />
  if (!hub) return <Empty text={ru.hub.noHub} />

  const hubProduct = productById.get(hub.product_id)
  const hubContracts = (contracts.data ?? []).filter(
    (c) => c.provider_product_id === hub.product_id || c.consumer_product_id === hub.product_id,
  )
  const nameById = new Map((hubFeatures.data ?? []).map((f) => [f.id, f.name]))
  const topDemand = [...(hubValues.data ?? [])]
    .sort((a, b) => b.derived_value.amount - a.derived_value.amount)
    .slice(0, 10)
  const otherHubs = (hubs.data ?? []).filter((h) => h.product_id !== hub.product_id && (h.manual || h.computed))

  return (
    <section className="stack">
      <div className="page-head">
        <div>
          <h1>{ru.hub.title}</h1>
          <div className="muted">
            {ru.hub.hubProduct}:{' '}
            {hubProduct ? <Link to={`/products/${hubProduct.id}`}>{hubProduct.name}</Link> : hub.product_id} ·{' '}
            {ru.hub.inDegree(hub.in_degree)} {hub.manual && <Badge tone="warn">{ru.product.hubManual}</Badge>}
          </div>
        </div>
      </div>

      <div className="card stack">
        <h2>{ru.hub.contracts}</h2>
        {contracts.isError && <ErrorBox error={contracts.error} />}
        <ContractsTable contracts={hubContracts} />
      </div>

      <div className="card stack">
        <h2>{ru.hub.topDemand}</h2>
        {hubValues.isError && <ErrorBox error={hubValues.error} />}
        {hubValues.isPending && <Loading />}
        {hubValues.data && topDemand.length === 0 && <Empty text={ru.productPage.noFeatures} />}
        {topDemand.length > 0 && (
          <div className="table-wrap">
            <table className="table">
              <thead>
                <tr>
                  <th>{ru.feature.name}</th>
                  <th>{ru.feature.ownValue}</th>
                  <th>{ru.feature.derivedValue}</th>
                  <th>{ru.feature.totalValue}</th>
                </tr>
              </thead>
              <tbody>
                {topDemand.map((v) => (
                  <tr key={v.feature_id}>
                    <td>{nameById.get(v.feature_id) ?? <span className="mono">{v.feature_id}</span>}</td>
                    <td className="num">{fmtMoney(v.own_value)}</td>
                    <td className="num">{fmtMoney(v.derived_value)}</td>
                    <td className="num">{fmtMoney(v.total_value)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </div>

      <div className="card stack">
        <h2>{ru.hub.affected}</h2>
        <p className="muted">
          {ru.hub.dependents}:{' '}
          {dependents.length === 0
            ? ru.app.dash
            : dependents.map((id, i) => (
                <span key={id}>
                  {i > 0 && ', '}
                  <Link to={`/products/${id}`}>{productById.get(id)?.name ?? id}</Link>
                </span>
              ))}
        </p>
        <AffectedTable
          rows={dependentFeatures.flatMap((q, i) =>
            q.data ? q.data.filter((f) => f.affected).map((f) => ({ f, productId: dependents[i] })) : [],
          )}
          productName={(id) => productById.get(id)?.name ?? id}
          noAccess={dependentFeatures
            .map((q, i) => (q.isError && q.error instanceof ApiError && q.error.status === 403 ? dependents[i] : null))
            .filter((x): x is string => x !== null)
            .map((id) => productById.get(id)?.name ?? id)}
          loading={dependentFeatures.some((q) => q.isPending)}
        />
      </div>

      {otherHubs.length > 0 && (
        <div className="card stack">
          <h2>{ru.hub.otherHubs}</h2>
          <ul>
            {otherHubs.map((h) => (
              <li key={h.product_id}>
                <Link to={`/products/${h.product_id}`}>{productById.get(h.product_id)?.name ?? h.product_id}</Link>{' '}
                <span className="muted">{ru.hub.inDegree(h.in_degree)}</span>
              </li>
            ))}
          </ul>
        </div>
      )}
    </section>
  )
}

function AffectedTable({
  rows,
  productName,
  noAccess,
  loading,
}: {
  rows: { f: Feature; productId: string }[]
  productName: (id: string) => string
  noAccess: string[]
  loading: boolean
}) {
  return (
    <>
      {loading && <Loading />}
      {noAccess.length > 0 && (
        <p className="muted">
          {ru.hub.noAccess}: {noAccess.join(', ')}
        </p>
      )}
      {!loading && rows.length === 0 ? (
        <Empty text={ru.hub.noAffected} />
      ) : (
        <div className="table-wrap">
          <table className="table">
            <thead>
              <tr>
                <th>{ru.delivery.product}</th>
                <th>{ru.feature.name}</th>
                <th>{ru.feature.status}</th>
                <th>{ru.feature.plannedDate}</th>
                <th>{ru.feature.impliedDate}</th>
              </tr>
            </thead>
            <tbody>
              {rows.map(({ f, productId }) => (
                <tr key={f.id} className="row-affected">
                  <td>
                    <Link to={`/products/${productId}`}>{productName(productId)}</Link>
                  </td>
                  <td>{f.name}</td>
                  <td>{pick(ru.feature.statuses, f.status)}</td>
                  <td>{fmtDate(f.planned_date)}</td>
                  <td>{fmtDate(f.implied_date)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </>
  )
}
