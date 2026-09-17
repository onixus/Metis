import { useCallback, useMemo, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import {
  Background,
  Controls,
  MarkerType,
  ReactFlow,
  type Edge,
  type Node,
  type NodeMouseHandler,
} from '@xyflow/react'
import '@xyflow/react/dist/style.css'
import { useHubs, useLinks, useProducts } from '../api/hooks'
import type { Criticality, Lifecycle, LinkType, Product } from '../api/types'
import { ErrorBox, Loading } from '../components/Status'
import { ru } from '../i18n/ru'

const LINK_TYPES: LinkType[] = ['integration', 'shared_component', 'commercial', 'bundled']
const LIFECYCLES: Lifecycle[] = ['idea', 'active', 'sunset', 'retired']

const LINK_COLORS: Record<LinkType, string> = {
  integration: '#2f6fed',
  shared_component: '#8e44ad',
  commercial: '#1e9e63',
  bundled: '#e67e22',
}

const CRIT_WIDTH: Record<Criticality, number> = { blocks: 3, accelerates: 2, desirable: 1 }

type ProductNode = Node<{ label: string; hub: boolean; product: Product }>

/** Круговая раскладка: без внешних библиотек и детерминированно. */
function layout(products: Product[]): Map<string, { x: number; y: number }> {
  const n = Math.max(products.length, 1)
  const r = Math.max(180, n * 45)
  const pos = new Map<string, { x: number; y: number }>()
  products.forEach((p, i) => {
    const a = (2 * Math.PI * i) / n - Math.PI / 2
    pos.set(p.id, { x: Math.round(r * Math.cos(a)) + r, y: Math.round(r * Math.sin(a)) + r })
  })
  return pos
}

export function GraphPage() {
  const products = useProducts()
  const links = useLinks()
  const hubs = useHubs()
  const navigate = useNavigate()

  const [types, setTypes] = useState<Set<LinkType>>(() => new Set(LINK_TYPES))
  const [productId, setProductId] = useState('')
  const [lifecycle, setLifecycle] = useState('')

  const hubIds = useMemo(
    () => new Set((hubs.data ?? []).filter((h) => h.manual || h.computed).map((h) => h.product_id)),
    [hubs.data],
  )

  const { nodes, edges } = useMemo(() => {
    const all = products.data ?? []
    const allLinks = links.data ?? []

    let visibleLinks = allLinks.filter((l) => types.has(l.type))
    if (productId) {
      visibleLinks = visibleLinks.filter((l) => l.from_product_id === productId || l.to_product_id === productId)
    }
    let visible = all.filter((p) => !lifecycle || (p.lifecycle ?? '') === lifecycle)
    if (productId) {
      const neighbors = new Set<string>([productId])
      for (const l of visibleLinks) {
        neighbors.add(l.from_product_id)
        neighbors.add(l.to_product_id)
      }
      visible = visible.filter((p) => neighbors.has(p.id))
    }
    const visibleIds = new Set(visible.map((p) => p.id))
    visibleLinks = visibleLinks.filter((l) => visibleIds.has(l.from_product_id) && visibleIds.has(l.to_product_id))

    const pos = layout(visible)
    const nodes: ProductNode[] = visible.map((p) => {
      const hub = hubIds.has(p.id)
      return {
        id: p.id,
        position: pos.get(p.id) ?? { x: 0, y: 0 },
        data: { label: p.name, hub, product: p },
        className: hub ? 'node node-hub' : 'node',
        style: { opacity: p.lifecycle === 'retired' ? 0.5 : 1 },
      }
    })
    const edges: Edge[] = visibleLinks.map((l) => {
      const color = LINK_COLORS[l.type]
      const critLabel = ru.link.criticality[l.criticality]
      const key = l.contract_id ? `${ru.link.types[l.type]} · ${critLabel}` : critLabel
      return {
        id: l.id,
        source: l.from_product_id,
        target: l.to_product_id,
        label: key,
        style: { stroke: color, strokeWidth: CRIT_WIDTH[l.criticality] },
        labelStyle: { fill: color, fontSize: 11 },
        markerEnd: { type: MarkerType.ArrowClosed, color },
        animated: l.criticality === 'blocks',
      }
    })
    return { nodes, edges }
  }, [products.data, links.data, hubIds, types, productId, lifecycle])

  const onNodeClick = useCallback<NodeMouseHandler>(
    (_e, node) => {
      navigate(`/products/${node.id}`)
    },
    [navigate],
  )

  const toggleType = (t: LinkType) =>
    setTypes((prev) => {
      const next = new Set(prev)
      if (next.has(t)) next.delete(t)
      else next.add(t)
      return next
    })

  if (products.isPending || links.isPending) return <Loading />
  if (products.isError) return <ErrorBox error={products.error} onRetry={() => void products.refetch()} />
  if (links.isError) return <ErrorBox error={links.error} onRetry={() => void links.refetch()} />

  return (
    <section className="stack graph-page">
      <div className="page-head">
        <h1>{ru.graph.title}</h1>
        <span className="muted">
          {ru.graph.nodes(nodes.length)} · {ru.graph.edges(edges.length)}
        </span>
      </div>
      <div className="filters">
        <fieldset className="filter-group">
          <legend>{ru.graph.filterLinkType}</legend>
          {LINK_TYPES.map((t) => (
            <label key={t} className="check">
              <input type="checkbox" checked={types.has(t)} onChange={() => toggleType(t)} />
              <span className="swatch" style={{ background: LINK_COLORS[t] }} />
              {ru.link.types[t]}
            </label>
          ))}
        </fieldset>
        <label className="field">
          <span>{ru.graph.filterProduct}</span>
          <select value={productId} onChange={(e) => setProductId(e.target.value)}>
            <option value="">{ru.app.all}</option>
            {products.data.map((p) => (
              <option key={p.id} value={p.id}>
                {p.name}
              </option>
            ))}
          </select>
        </label>
        <label className="field">
          <span>{ru.graph.filterStatus}</span>
          <select value={lifecycle} onChange={(e) => setLifecycle(e.target.value)}>
            <option value="">{ru.app.all}</option>
            {LIFECYCLES.map((l) => (
              <option key={l} value={l}>
                {ru.product.lifecycles[l]}
              </option>
            ))}
          </select>
        </label>
        <button
          type="button"
          className="btn btn-sm"
          onClick={() => {
            setTypes(new Set(LINK_TYPES))
            setProductId('')
            setLifecycle('')
          }}
        >
          {ru.app.reset}
        </button>
      </div>
      <p className="muted">{ru.graph.hint}</p>
      {hubs.isError && <ErrorBox error={hubs.error} />}
      <div className="graph-canvas">
        <ReactFlow nodes={nodes} edges={edges} onNodeClick={onNodeClick} fitView nodesDraggable nodesConnectable={false}>
          <Background />
          <Controls showInteractive={false} />
        </ReactFlow>
      </div>
    </section>
  )
}
