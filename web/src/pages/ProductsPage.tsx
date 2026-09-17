import { useMemo } from 'react'
import { Link } from 'react-router-dom'
import { accessLevel, useHubs, useMe, useProducts } from '../api/hooks'
import { Badge, Empty, ErrorBox, Loading } from '../components/Status'
import { ru } from '../i18n/ru'
import { pick } from '../lib/format'

export function ProductsPage() {
  const products = useProducts()
  const hubs = useHubs()
  const me = useMe()

  const canCreate = (me.data?.roles ?? []).some((r) => r === 'cpo' || r === 'admin')
  const hubById = useMemo(() => new Map((hubs.data ?? []).map((h) => [h.product_id, h])), [hubs.data])

  if (products.isPending) return <Loading />
  if (products.isError) return <ErrorBox error={products.error} onRetry={() => void products.refetch()} />
  const list = [...products.data].sort((a, b) => a.name.localeCompare(b.name, 'ru'))

  return (
    <section className="stack">
      <div className="page-head">
        <h1>{ru.products.title}</h1>
        <span className="muted">{ru.products.count(list.length)}</span>
        {canCreate && (
          <Link className="btn btn-primary btn-sm" to="/products/new">
            {ru.products.create}
          </Link>
        )}
      </div>
      {hubs.isError && <ErrorBox error={hubs.error} />}
      {list.length === 0 ? (
        <Empty />
      ) : (
        <div className="table-wrap">
          <table className="table">
            <thead>
              <tr>
                <th>{ru.product.key}</th>
                <th>{ru.product.name}</th>
                <th>{ru.product.type}</th>
                <th>{ru.product.lifecycle}</th>
                <th>{ru.product.owner}</th>
                <th>{ru.product.hub}</th>
                <th>{ru.product.inDegree}</th>
                <th>{ru.product.access}</th>
                <th />
              </tr>
            </thead>
            <tbody>
              {list.map((p) => {
                const hub = hubById.get(p.id)
                const level = accessLevel(me.data, p.id)
                return (
                  <tr key={p.id}>
                    <td className="mono">{p.key}</td>
                    <td>
                      <Link to={`/products/${p.id}`}>{p.name}</Link>
                    </td>
                    <td>{pick(ru.product.types, p.type)}</td>
                    <td>{pick(ru.product.lifecycles, p.lifecycle)}</td>
                    <td>{p.owner ?? ru.app.dash}</td>
                    <td>
                      {hub?.manual && <Badge tone="warn">{ru.product.hubManual}</Badge>}{' '}
                      {hub?.computed && <Badge tone="info">{ru.product.hubComputed}</Badge>}
                    </td>
                    <td>{hub?.in_degree ?? 0}</td>
                    <td>{ru.me.access[level]}</td>
                    <td>
                      <Link className="btn btn-sm" to={`/products/${p.id}/roadmap`}>
                        {ru.product.roadmap}
                      </Link>
                    </td>
                  </tr>
                )
              })}
            </tbody>
          </table>
        </div>
      )}
    </section>
  )
}
