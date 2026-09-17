import { Link, useNavigate, useParams } from 'react-router-dom'
import { useTrace } from '../api/hooks'
import type { TraceKind, TraceNode } from '../api/types'
import { Badge, Empty, ErrorBox, Loading } from '../components/Status'
import { ru } from '../i18n/ru'

const KINDS: TraceKind[] = ['signal', 'insight', 'hypothesis', 'feature', 'decision']
const ROOT_KINDS = new Set(['signal', 'insight', 'hypothesis', 'feature'])

/** Трассировка DS-04: узлы по типам (колонки в порядке цепочки) и список рёбер. */
export function TracePage() {
  const { kind = '', id = '' } = useParams()
  const navigate = useNavigate()
  const valid = ROOT_KINDS.has(kind)
  const trace = useTrace(valid ? (kind as Exclude<TraceKind, 'decision'>) : 'feature', id)

  if (!valid) return <Empty text={ru.app.notFound} />
  if (trace.isPending) return <Loading />
  if (trace.isError) return <ErrorBox error={trace.error} onRetry={() => void trace.refetch()} />

  const g = trace.data
  const byRef = new Map<string, TraceNode>(g.nodes.map((n) => [`${n.kind}:${n.id}`, n]))
  const title = (ref: { kind: string; id: string }) => byRef.get(`${ref.kind}:${ref.id}`)?.title ?? ref.id
  const isRoot = (n: TraceNode) => n.kind === g.root.kind && n.id === g.root.id
  const nodeLink = (n: TraceNode): string | null => {
    if (n.kind === 'decision') return n.product_id ? `/products/${n.product_id}/decisions` : '/decisions'
    if (n.kind === 'feature' && n.product_id) return `/products/${n.product_id}`
    if ((n.kind === 'hypothesis' || n.kind === 'insight') && n.product_id) return `/products/${n.product_id}/discovery`
    return null
  }

  return (
    <section className="stack">
      <div className="page-head">
        <div>
          <h1>{ru.trace.title}</h1>
          <div className="muted">{ru.trace.subtitle}</div>
          <div className="muted">
            {ru.trace.root}: <Badge tone="info">{ru.trace.kind[g.root.kind]}</Badge> {title(g.root)}
          </div>
        </div>
        <button type="button" className="btn" onClick={() => navigate(-1)}>
          {ru.trace.back}
        </button>
      </div>
      <div className="columns">
        {KINDS.map((k) => {
          const nodes = g.nodes.filter((n) => n.kind === k)
          return (
            <div key={k} className="card stack">
              <h2>
                {ru.trace.kinds[k]} <span className="muted">{nodes.length}</span>
              </h2>
              {nodes.length === 0 ? (
                <span className="muted">{ru.app.dash}</span>
              ) : (
                <ul className="items">
                  {nodes.map((n) => {
                    const to = nodeLink(n)
                    return (
                      <li key={n.id} className="item">
                        <div className="row wrap-row">
                          {to ? <Link to={to}>{n.title}</Link> : <span>{n.title}</span>}
                          {isRoot(n) && <Badge tone="warn">{ru.trace.root}</Badge>}
                        </div>
                        <span className="mono muted">{n.id}</span>
                      </li>
                    )
                  })}
                </ul>
              )}
            </div>
          )
        })}
      </div>
      <div className="card stack">
        <h2>
          {ru.trace.edges} <span className="muted">{g.edges.length}</span>
        </h2>
        {g.edges.length === 0 ? (
          <Empty text={ru.trace.noEdges} />
        ) : (
          <ul>
            {g.edges.map((e, i) => (
              <li key={i}>
                <Badge tone="neutral">{ru.trace.kind[e.from.kind]}</Badge> {title(e.from)} → <Badge tone="neutral">{ru.trace.kind[e.to.kind]}</Badge>{' '}
                {title(e.to)}
              </li>
            ))}
          </ul>
        )}
      </div>
    </section>
  )
}
