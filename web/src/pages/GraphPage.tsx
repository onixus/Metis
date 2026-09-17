import { memo, useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import {
  Background,
  Controls,
  Handle,
  MarkerType,
  Position,
  ReactFlow,
  useEdgesState,
  useNodesState,
  type Connection,
  type Edge,
  type Node,
  type NodeProps,
  type OnBeforeDelete,
} from '@xyflow/react'
import '@xyflow/react/dist/style.css'
import {
  useCreateLink,
  useDeleteLink,
  useExpandedFeatures,
  useHubs,
  useLinks,
  useProducts,
  type GraphFeature,
  type LinkInput,
} from '../api/hooks'
import { ApiError, errorMessage } from '../api/client'
import type { Criticality, Lifecycle, LinkType, Product } from '../api/types'
import { ErrorBox, Loading } from '../components/Status'
import { fmtDate } from '../lib/format'
import { ru } from '../i18n/ru'

const LINK_TYPES: LinkType[] = ['integration', 'shared_component', 'commercial', 'bundled']
const CRITICALITIES: Criticality[] = ['blocks', 'accelerates', 'desirable']
const LIFECYCLES: Lifecycle[] = ['idea', 'active', 'sunset', 'retired']

const LINK_COLORS: Record<LinkType, string> = {
  integration: '#2f6fed',
  shared_component: '#8e44ad',
  commercial: '#1e9e63',
  bundled: '#e67e22',
}

const CRIT_WIDTH: Record<Criticality, number> = { blocks: 3, accelerates: 2, desirable: 1 }

/** Идентификаторы хендлов фичи внутри узла продукта: входной (потребитель) и выходной (поставщик). */
const featureIn = (featureId: string) => `fi:${featureId}`
const featureOut = (featureId: string) => `fo:${featureId}`
const isFeatureHandle = (h: string | null | undefined) => !!h && (h.startsWith('fi:') || h.startsWith('fo:'))
const featureIdOf = (h: string) => h.slice(3)

interface ProductNodeData extends Record<string, unknown> {
  product: Product
  hub: boolean
  expanded: boolean
  features: GraphFeature[] | undefined
  loading: boolean
  onToggle: (id: string) => void
  onOpen: (id: string) => void
}

type ProductNode = Node<ProductNodeData, 'product'>

/** Узел продукта: заголовок с кнопками, хендлы продукта и раскрываемый список фич с собственными хендлами. */
const ProductNodeView = memo(function ProductNodeView({ id, data }: NodeProps<ProductNode>) {
  const strategicOnly = data.features?.some((f) => !f.privateAccess) ?? false
  return (
    <div>
      <Handle type="target" position={Position.Top} id="p:in" className="handle-product" />
      <div className="node-head">
        <span className="node-title" onClick={() => data.onOpen(id)} title={ru.graph.open}>
          {data.product.name}
        </span>
        <button
          type="button"
          className="node-btn nodrag"
          onClick={(e) => {
            e.stopPropagation()
            data.onToggle(id)
          }}
          title={data.expanded ? ru.graph.collapse : ru.graph.expand}
          aria-expanded={data.expanded}
        >
          {data.expanded ? '▴' : '▾'}
        </button>
      </div>
      {data.expanded && (
        <>
          {data.loading && <div className="node-note">{ru.graph.loadingFeatures}</div>}
          {!data.loading && (data.features?.length ?? 0) === 0 && <div className="node-note">{ru.graph.noFeatures}</div>}
          {!data.loading && strategicOnly && <div className="node-note">{ru.graph.strategicOnly}</div>}
          {!data.loading && (data.features?.length ?? 0) > 0 && (
            <ul className="node-list nowheel">
              {data.features!.map((f) => (
                <li key={f.id} className={f.affected ? 'node-feature affected' : 'node-feature'} title={ru.feature.statuses[f.status as keyof typeof ru.feature.statuses] ?? f.status}>
                  <Handle type="target" position={Position.Left} id={featureIn(f.id)} />
                  <span className="dot" />
                  <span>{f.name}</span>
                  {f.planned_date && <span className="fdate">{fmtDate(f.planned_date)}</span>}
                  <Handle type="source" position={Position.Right} id={featureOut(f.id)} />
                </li>
              ))}
            </ul>
          )}
        </>
      )}
      <Handle type="source" position={Position.Bottom} id="p:out" className="handle-product" />
    </div>
  )
})

const nodeTypes = { product: ProductNodeView }

/** Круговая раскладка для первого показа; дальше позиции живут в состоянии React Flow. */
function layout(products: Product[]): Map<string, { x: number; y: number }> {
  const n = Math.max(products.length, 1)
  const r = Math.max(200, n * 50)
  const pos = new Map<string, { x: number; y: number }>()
  products.forEach((p, i) => {
    const a = (2 * Math.PI * i) / n - Math.PI / 2
    pos.set(p.id, { x: Math.round(r * Math.cos(a)) + r, y: Math.round(r * Math.sin(a)) + r })
  })
  return pos
}

interface PendingLink {
  fromProduct: string
  toProduct: string
  fromFeature?: string
  toFeature?: string
}

export function GraphPage() {
  const products = useProducts()
  const links = useLinks()
  const hubs = useHubs()
  const createLink = useCreateLink()
  const deleteLink = useDeleteLink()
  const navigate = useNavigate()

  const [types, setTypes] = useState<Set<LinkType>>(() => new Set(LINK_TYPES))
  const [productId, setProductId] = useState('')
  const [lifecycle, setLifecycle] = useState('')
  const [expanded, setExpanded] = useState<Set<string>>(() => new Set())
  const [pending, setPending] = useState<PendingLink | null>(null)
  const [linkType, setLinkType] = useState<LinkType>('integration')
  const [crit, setCrit] = useState<Criticality>('blocks')
  const [toast, setToast] = useState<{ text: string; error?: boolean; cycle?: string[] } | null>(null)

  const expandedIds = useMemo(() => Array.from(expanded), [expanded])
  const featureQueries = useExpandedFeatures(expandedIds)

  const hubIds = useMemo(
    () => new Set((hubs.data ?? []).filter((h) => h.manual || h.computed).map((h) => h.product_id)),
    [hubs.data],
  )

  const toggle = useCallback((id: string) => {
    setExpanded((prev) => {
      const next = new Set(prev)
      if (next.has(id)) next.delete(id)
      else next.add(id)
      return next
    })
  }, [])
  const open = useCallback((id: string) => navigate(`/products/${id}`), [navigate])

  // Фичи по продуктам для подписи рёбер и путей циклов.
  const featureNames = useMemo(() => {
    const m = new Map<string, string>()
    featureQueries.forEach((q) => q.data?.forEach((f) => m.set(f.id, f.name)))
    return m
  }, [featureQueries])

  const visible = useMemo(() => {
    const all = products.data ?? []
    const allLinks = links.data ?? []
    let visibleLinks = allLinks.filter((l) => types.has(l.type))
    if (productId) {
      visibleLinks = visibleLinks.filter((l) => l.from_product_id === productId || l.to_product_id === productId)
    }
    let vis = all.filter((p) => !lifecycle || (p.lifecycle ?? '') === lifecycle)
    if (productId) {
      const neighbors = new Set<string>([productId])
      for (const l of visibleLinks) {
        neighbors.add(l.from_product_id)
        neighbors.add(l.to_product_id)
      }
      vis = vis.filter((p) => neighbors.has(p.id))
    }
    const ids = new Set(vis.map((p) => p.id))
    visibleLinks = visibleLinks.filter((l) => ids.has(l.from_product_id) && ids.has(l.to_product_id))
    return { products: vis, links: visibleLinks }
  }, [products.data, links.data, types, productId, lifecycle])

  const [nodes, setNodes, onNodesChange] = useNodesState<ProductNode>([])
  const [edges, setEdges, onEdgesChange] = useEdgesState<Edge>([])
  const positions = useRef(new Map<string, { x: number; y: number }>())

  // Узлы: позиции сохраняются между перерисовками, данные обновляются.
  useEffect(() => {
    const fresh = layout(visible.products)
    setNodes(
      visible.products.map((p) => {
        const idx = expandedIds.indexOf(p.id)
        const q = idx >= 0 ? featureQueries[idx] : undefined
        const pos = positions.current.get(p.id) ?? fresh.get(p.id) ?? { x: 0, y: 0 }
        positions.current.set(p.id, pos)
        const hub = hubIds.has(p.id)
        return {
          id: p.id,
          type: 'product',
          position: pos,
          data: {
            product: p, hub, expanded: expanded.has(p.id), features: q?.data, loading: q?.isPending ?? false, onToggle: toggle, onOpen: open,
          },
          className: hub ? 'node node-hub' : 'node',
          style: { opacity: p.lifecycle === 'retired' ? 0.5 : 1 },
        }
      }),
    )
  }, [visible.products, expanded, expandedIds, featureQueries, hubIds, toggle, open, setNodes])

  // Рёбра: связь фич рисуется между хендлами фич, если оба узла раскрыты, иначе между продуктами.
  useEffect(() => {
    setEdges(
      visible.links.map((l) => {
        const color = LINK_COLORS[l.type]
        const featureLevel = !!l.from_feature_id && !!l.to_feature_id
        const bothExpanded = featureLevel && expanded.has(l.from_product_id) && expanded.has(l.to_product_id)
        const critLabel = ru.link.criticality[l.criticality]
        let label = l.contract_id ? `${ru.link.types[l.type]} · ${critLabel}` : critLabel
        if (featureLevel && !bothExpanded) {
          const fn = featureNames.get(l.from_feature_id!)
          const tn = featureNames.get(l.to_feature_id!)
          if (fn && tn) label = `${fn} → ${tn} · ${critLabel}`
        }
        return {
          id: l.id,
          source: l.from_product_id,
          target: l.to_product_id,
          sourceHandle: bothExpanded ? featureOut(l.from_feature_id!) : 'p:out',
          targetHandle: bothExpanded ? featureIn(l.to_feature_id!) : 'p:in',
          label,
          style: { stroke: color, strokeWidth: CRIT_WIDTH[l.criticality], strokeDasharray: featureLevel && !bothExpanded ? '6 3' : undefined },
          labelStyle: { fill: color, fontSize: 11 },
          markerEnd: { type: MarkerType.ArrowClosed, color },
          animated: l.criticality === 'blocks',
        }
      }),
    )
  }, [visible.links, expanded, featureNames, setEdges])

  const onNodeDragStop = useCallback((_e: unknown, node: Node) => {
    positions.current.set(node.id, node.position)
  }, [])

  const onConnect = useCallback((c: Connection) => {
    if (!c.source || !c.target) return
    const fromFeature = isFeatureHandle(c.sourceHandle) ? featureIdOf(c.sourceHandle!) : undefined
    const toFeature = isFeatureHandle(c.targetHandle) ? featureIdOf(c.targetHandle!) : undefined
    // Связь фич требует обе фичи; смешанное соединение считаем продуктовым.
    const both = !!fromFeature && !!toFeature
    setPending({ fromProduct: c.source, toProduct: c.target, fromFeature: both ? fromFeature : undefined, toFeature: both ? toFeature : undefined })
    setToast(null)
  }, [])

  const submit = async () => {
    if (!pending) return
    const body: LinkInput = pending.fromFeature
      ? { type: linkType, criticality: crit, from_feature_id: pending.fromFeature, to_feature_id: pending.toFeature }
      : { type: linkType, criticality: crit, from_product_id: pending.fromProduct, to_product_id: pending.toProduct }
    try {
      await createLink.mutateAsync(body)
      setPending(null)
      setToast({ text: ru.graph.created })
    } catch (err) {
      if (err instanceof ApiError && err.cycle) {
        setToast({ text: ru.graph.cycleTitle, error: true, cycle: err.cycle.map((id) => featureNames.get(id) ?? id) })
      } else {
        setToast({ text: errorMessage(err), error: true })
      }
    }
  }

  const onBeforeDelete = useCallback<OnBeforeDelete<ProductNode, Edge>>(
    async ({ edges: toDelete }) => {
      if (toDelete.length === 0) return false
      if (!window.confirm(ru.graph.deleteConfirm(toDelete.length))) return false
      try {
        for (const e of toDelete) await deleteLink.mutateAsync(e.id)
        setToast({ text: ru.graph.deleted })
      } catch (err) {
        setToast({ text: errorMessage(err), error: true })
      }
      return { nodes: [], edges: [] }
    },
    [deleteLink],
  )

  const toggleType = (t: LinkType) =>
    setTypes((prev) => {
      const next = new Set(prev)
      if (next.has(t)) next.delete(t)
      else next.add(t)
      return next
    })

  const nameOf = (id: string) => products.data?.find((p) => p.id === id)?.name ?? id

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
        <ReactFlow
          nodes={nodes}
          edges={edges}
          nodeTypes={nodeTypes}
          onNodesChange={onNodesChange}
          onEdgesChange={onEdgesChange}
          onNodeDragStop={onNodeDragStop}
          onConnect={onConnect}
          onBeforeDelete={onBeforeDelete}
          deleteKeyCode={['Delete', 'Backspace']}
          fitView
          nodesDraggable
          nodesConnectable
          elementsSelectable
        >
          <Background />
          <Controls showInteractive={false} />
        </ReactFlow>
        {pending && (
          <form
            className="graph-dialog"
            onSubmit={(e) => {
              e.preventDefault()
              void submit()
            }}
          >
            <h3>{ru.graph.newLink}</h3>
            <div className="muted">
              {pending.fromFeature
                ? ru.graph.fromTo(featureNames.get(pending.fromFeature) ?? pending.fromFeature, featureNames.get(pending.toFeature!) ?? pending.toFeature!)
                : ru.graph.fromTo(nameOf(pending.fromProduct), nameOf(pending.toProduct))}
            </div>
            <div className="muted">{pending.fromFeature ? ru.graph.featureLink : ru.graph.productLink}</div>
            <label className="field">
              <span>{ru.graph.filterLinkType}</span>
              <select value={linkType} onChange={(e) => setLinkType(e.target.value as LinkType)}>
                {LINK_TYPES.map((t) => (
                  <option key={t} value={t}>
                    {ru.link.types[t]}
                  </option>
                ))}
              </select>
            </label>
            <label className="field">
              <span>{ru.link.criticalityLabel}</span>
              <select value={crit} onChange={(e) => setCrit(e.target.value as Criticality)}>
                {CRITICALITIES.map((c) => (
                  <option key={c} value={c}>
                    {ru.link.criticality[c]}
                  </option>
                ))}
              </select>
            </label>
            <div className="row">
              <button type="submit" className="btn btn-primary btn-sm" disabled={createLink.isPending}>
                {ru.graph.create}
              </button>
              <button type="button" className="btn btn-sm" onClick={() => setPending(null)}>
                {ru.app.cancel}
              </button>
            </div>
          </form>
        )}
        {toast && (
          <div className={toast.error ? 'toast error' : 'toast'} role="status" onClick={() => setToast(null)}>
            {toast.text}
            {toast.cycle && (
              <div>
                {ru.graph.cyclePath}: {toast.cycle.join(' → ')}
              </div>
            )}
          </div>
        )}
      </div>
    </section>
  )
}
