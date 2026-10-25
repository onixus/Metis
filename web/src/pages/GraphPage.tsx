import { memo, useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useNavigate, useSearchParams } from 'react-router-dom'
import {
  Background,
  Controls,
  Handle,
  MarkerType,
  Position,
  ReactFlow,
  ReactFlowProvider,
  useEdgesState,
  useNodesInitialized,
  useUpdateNodeInternals,
  useReactFlow,
  useNodesState,
  type Connection,
  type Edge,
  type Node,
  type NodeProps,
  type OnBeforeDelete,
} from '@xyflow/react'
import '@xyflow/react/dist/style.css'
import dagre from '@dagrejs/dagre'
import {
  useCreateLink,
  useDeleteLink,
  useExpandedFeatures,
  useHubs,
  useLinks,
  useContracts,
  useProducts,
  useMe,
  type GraphFeature,
} from '../api/hooks'
import { ApiError, errorMessage } from '../api/client'
import type { Criticality, Lifecycle, LinkType, Product } from '../api/types'
import { ErrorBox, Loading } from '../components/Status'
import { fmtDate } from '../lib/format'
import { ru } from '../i18n/ru'
import { ContractsPanel } from '../components/ContractsPanel'
import { canWriteGraph, featureHandlesReady, filterGraph, linkBody } from '../lib/graph'

const LINK_TYPES: LinkType[] = ['integration', 'shared_component', 'commercial', 'bundled']
const CRITICALITIES: Criticality[] = ['blocks', 'accelerates', 'desirable']
const LIFECYCLES: Lifecycle[] = ['idea', 'active', 'sunset', 'retired']

const LINK_COLORS: Record<LinkType, string> = {
  integration: 'var(--viz-1)',
  shared_component: 'var(--viz-4)',
  commercial: 'var(--viz-3)',
  bundled: 'var(--viz-2)',
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
  error: unknown
  retry: () => unknown
  writable: boolean
  onToggle: (id: string) => void
  onOpen: (id: string) => void
}

type ProductNode = Node<ProductNodeData, 'product'>

/** Узел продукта: заголовок с кнопками, хендлы продукта и раскрываемый список фич с собственными хендлами. */
const ProductNodeView = memo(function ProductNodeView({ id, data }: NodeProps<ProductNode>) {
  const updateNodeInternals = useUpdateNodeInternals()
  useEffect(() => { updateNodeInternals(id) }, [id, data.expanded, data.features, data.loading, data.error, updateNodeInternals])
  const strategicOnly = data.features?.some((f) => !f.privateAccess) ?? false
  return (
    <div>
      <Handle type="target" position={Position.Top} id="p:in" className="handle-product" />
      <div className="node-head">
        <button type="button" className="node-title nodrag" onClick={() => data.onOpen(id)} title={ru.graph.open}>
          {data.product.name}
        </button>
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
          {!data.loading && !data.error && (data.features?.length ?? 0) === 0 && <div className="node-note">{ru.graph.noFeatures}</div>}
          {!!data.error && <div className="node-note"><ErrorBox error={data.error} onRetry={() => void data.retry()} /></div>}
          {!data.loading && strategicOnly && <div className="node-note">{ru.graph.strategicOnly}</div>}
          {!data.loading && (data.features?.length ?? 0) > 0 && (
            <ul className="node-list nowheel">
              {data.features!.map((f) => (
                <li key={f.id} className={f.affected ? 'node-feature affected' : 'node-feature'} title={ru.feature.statuses[f.status as keyof typeof ru.feature.statuses] ?? f.status}>
                  <Handle type="target" position={Position.Left} id={featureIn(f.id)} />
                  <span className="dot" />
                  <span>{f.name}</span>
                  {f.planned_date && <span className="fdate">{fmtDate(f.planned_date)}</span>}
                  <Handle type="source" position={Position.Right} id={featureOut(f.id)} isConnectable={data.writable} />
                </li>
              ))}
            </ul>
          )}
        </>
      )}
      <Handle type="source" position={Position.Bottom} id="p:out" className="handle-product" isConnectable={data.writable} />
    </div>
  )
})

const nodeTypes = { product: ProductNodeView }

const NODE_W = 200
const NODE_HEAD_H = 40
const FEATURE_ROW_H = 24
const LIST_MAX_H = 220

/** Иерархическая раскладка dagre (ADR-0003): рёбра потребитель → поставщик, хабы внизу.
 * Размер узла учитывает раскрытый список фич. */
function layout(
  products: Product[],
  links: { from_product_id?: string; to_product_id?: string }[],
  sizeOf: (id: string) => { width: number; height: number },
): Map<string, { x: number; y: number }> {
  const g = new dagre.graphlib.Graph()
  g.setGraph({ rankdir: 'TB', nodesep: 60, ranksep: 110, marginx: 20, marginy: 20 })
  g.setDefaultEdgeLabel(() => ({}))
  for (const p of products) g.setNode(p.id, sizeOf(p.id))
  const seen = new Set<string>()
  for (const l of links) {
    const key = `${l.from_product_id}>${l.to_product_id}`
    if (!l.from_product_id || !l.to_product_id || l.from_product_id === l.to_product_id || seen.has(key)) continue
    seen.add(key)
    g.setEdge(l.from_product_id, l.to_product_id)
  }
  dagre.layout(g)
  const pos = new Map<string, { x: number; y: number }>()
  for (const p of products) {
    const n = g.node(p.id)
    const { width, height } = sizeOf(p.id)
    // dagre отдаёт центр узла, React Flow ждёт левый верхний угол.
    pos.set(p.id, { x: Math.round(n.x - width / 2), y: Math.round(n.y - height / 2) })
  }
  return pos
}

interface PendingLink {
  fromProduct: string
  toProduct: string
  fromFeature?: string
  toFeature?: string
}

export function GraphPage() {
  return (
    <ReactFlowProvider>
      <GraphInner />
    </ReactFlowProvider>
  )
}

function GraphInner() {
  const rf = useReactFlow()
  const [theme, setTheme] = useState(document.documentElement.dataset.theme)
  useEffect(() => {
    const observer = new MutationObserver(() => setTheme(document.documentElement.dataset.theme))
    observer.observe(document.documentElement, { attributes: true, attributeFilter: ['data-theme'] })
    return () => observer.disconnect()
  }, [])
  const nodesReady = useNodesInitialized()
  const me = useMe()
  const [searchParams, setSearchParams] = useSearchParams()
  const products = useProducts()
  const links = useLinks()
  const contracts = useContracts()
  const hubs = useHubs()
  const createLink = useCreateLink()
  const deleteLink = useDeleteLink()
  const navigate = useNavigate()

  const [types, setTypes] = useState<Set<LinkType>>(() => new Set(LINK_TYPES))
  const productId = searchParams.get('product') ?? ''
  const setProductId = (id: string) => setSearchParams(previous => { const next = new URLSearchParams(previous); if (id) next.set('product', id); else next.delete('product'); return next })
  const [lifecycle, setLifecycle] = useState('')
  const [expanded, setExpanded] = useState<Set<string>>(() => new Set())
  const [pending, setPending] = useState<PendingLink | null>(null)
  const [linkType, setLinkType] = useState<LinkType>('integration')
  const [formOpen, setFormOpen] = useState(false)
  const [featureLevel, setFeatureLevel] = useState(false)
  const [contractStatus, setContractStatus] = useState('')
  const [crit, setCrit] = useState<Criticality>('blocks')
  const [toast, setToast] = useState<{ text: string; error?: boolean; cycle?: string[] } | null>(null)

  const expandedIds = useMemo(() => Array.from(new Set([...expanded, ...(formOpen && featureLevel && pending ? [pending.fromProduct, pending.toProduct].filter(Boolean) : [])])), [expanded, formOpen, featureLevel, pending])
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
    const filteredLinks = (links.data ?? []).filter(link => !contractStatus || !!link.contract_id && contracts.data?.some(contract => contract.id === link.contract_id && contract.status === contractStatus))
    return filterGraph(products.data ?? [], filteredLinks, { types, productId, lifecycle })
  }, [products.data, links.data, contracts.data, types, productId, lifecycle, contractStatus])
  const featureQueryFor = (id: string) => featureQueries[expandedIds.indexOf(id)]
  const writableProducts = (products.data ?? []).filter(product => canWriteGraph(me.data, product.id))

  const [nodes, setNodes, onNodesChange] = useNodesState<ProductNode>([])
  const [edges, setEdges, onEdgesChange] = useEdgesState<Edge>([])
  const positions = useRef(new Map<string, { x: number; y: number }>())
  const [layoutVersion, setLayoutVersion] = useState(0)

  // Ключ структуры: состав узлов, рёбер и раскрытых списков. При его смене раскладка пересчитывается,
  // а перетаскивание пользователем сохраняется только между такими сменами.
  const structureKey = useMemo(() => {
    const ids = visible.products.map((p) => p.id).sort().join(',')
    const es = visible.links.map((l) => `${l.from_product_id}>${l.to_product_id}`).sort().join(',')
    const ex = expandedIds.filter(id => expanded.has(id)).map((id) => `${id}:${featureQueries[expandedIds.indexOf(id)]?.data?.length ?? -1}`).sort().join(',')
    return `${ids}|${es}|${ex}|${layoutVersion}`
  }, [visible.products, visible.links, expanded, expandedIds, featureQueries, layoutVersion])
  const lastKey = useRef('')

  // После смены раскладки подгоняем вид под все узлы.
  useEffect(() => {
    if (!nodesReady) return
    const t = window.setTimeout(() => void rf.fitView({ padding: 0.15, duration: window.matchMedia('(prefers-reduced-motion: reduce)').matches ? 0 : 300 }), 120)
    return () => window.clearTimeout(t)
  }, [structureKey, nodesReady, rf])

  // Узлы: данные обновляются всегда, позиции — по раскладке при смене структуры.
  useEffect(() => {
    const sizeOf = (id: string) => {
      const idx = expanded.has(id) ? expandedIds.indexOf(id) : -1
      const n = idx >= 0 ? (featureQueries[idx]?.data?.length ?? 0) : 0
      const list = idx >= 0 ? Math.min(LIST_MAX_H, Math.max(n, 1) * FEATURE_ROW_H + 8) : 0
      return { width: NODE_W, height: NODE_HEAD_H + list }
    }
    if (lastKey.current !== structureKey) {
      lastKey.current = structureKey
      positions.current = layout(visible.products, visible.links, sizeOf)
    }
    setNodes(
      visible.products.map((p) => {
        const idx = expandedIds.indexOf(p.id)
        const q = idx >= 0 ? featureQueries[idx] : undefined
        const pos = positions.current.get(p.id) ?? { x: 0, y: 0 }
        positions.current.set(p.id, pos)
        const hub = hubIds.has(p.id)
        return {
          id: p.id,
          type: 'product',
          position: pos,
          data: {
            product: p, hub, expanded: expanded.has(p.id), features: q?.data, loading: q?.isPending ?? false, error: q?.error, retry: q?.refetch ?? (() => {}), writable: canWriteGraph(me.data, p.id), onToggle: toggle, onOpen: open,
          },
          className: hub ? 'node node-hub' : 'node',
          style: { opacity: p.lifecycle === 'retired' ? 0.5 : 1 },
        }
      }),
    )
  }, [visible.products, visible.links, expanded, expandedIds, featureQueries, hubIds, toggle, open, setNodes, structureKey, me.data])

  // Рёбра: связь фич рисуется между хендлами фич, если оба узла раскрыты, иначе между продуктами.
  useEffect(() => {
    setEdges(
      visible.links.map((l) => {
        const color = getComputedStyle(document.documentElement).getPropertyValue(LINK_COLORS[l.type].slice(4, -1)).trim()
        const featureLevel = !!l.from_feature_id && !!l.to_feature_id
        const bothExpanded = featureLevel && expanded.has(l.from_product_id) && expanded.has(l.to_product_id) && featureHandlesReady(l.from_feature_id, l.to_feature_id, featureQueries[expandedIds.indexOf(l.from_product_id)]?.data, featureQueries[expandedIds.indexOf(l.to_product_id)]?.data)
        const critLabel = ru.link.criticality[l.criticality]
        let label = l.contract_id ? `${ru.link.types[l.type]} · ${critLabel}` : critLabel
        if (featureLevel && !bothExpanded) {
          const fn = featureNames.get(l.from_feature_id!)
          const tn = featureNames.get(l.to_feature_id!)
          if (fn && tn) label = `${fn} → ${tn} · ${critLabel}`
        }
        const contract = contracts.data?.find((c) => c.id === l.contract_id)
        if (l.contract_id) label += ` · ${contract?.status ? ru.contract.statuses[contract.status] : 'статус не загружен'}`
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
          animated: false,
          deletable: !l.contract_id && canWriteGraph(me.data, l.from_product_id),
        }
      }),
    )
  }, [visible.links, expanded, expandedIds, featureQueries, featureNames, contracts.data, me.data, setEdges, theme])

  const onNodeDragStop = useCallback((_e: unknown, node: Node) => {
    positions.current.set(node.id, node.position)
  }, [])

  const onConnect = useCallback((c: Connection) => {
    if (!c.source || !c.target || !canWriteGraph(me.data, c.source)) return
    const fromFeature = isFeatureHandle(c.sourceHandle) ? featureIdOf(c.sourceHandle!) : undefined
    const toFeature = isFeatureHandle(c.targetHandle) ? featureIdOf(c.targetHandle!) : undefined
    if (!!fromFeature !== !!toFeature) {
      setToast({ text: 'Соедините два продукта или две фичи. Смешанная связь не сохраняется.', error: true })
      return
    }
    setFeatureLevel(!!fromFeature)
    setFormOpen(true)
    setPending({ fromProduct: c.source, toProduct: c.target, fromFeature, toFeature })
    setToast(null)
  }, [me.data])

  const submit = async () => {
    if (!pending) return
    try {
      if (!canWriteGraph(me.data, pending.fromProduct)) throw Error("Нет права изменять граф продукта-потребителя")
      if (featureLevel && (!pending.fromFeature || !pending.toFeature)) throw Error("Выберите обе фичи")
      await createLink.mutateAsync(linkBody(pending, linkType, crit))
      setPending(null)
      setFormOpen(false)
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
      if (toDelete.length === 0 || toDelete.some(edge => { const link = links.data?.find(link => link.id === edge.id); return !link || !!link.contract_id || !canWriteGraph(me.data, link.from_product_id) })) return false
      if (!window.confirm(ru.graph.deleteConfirm(toDelete.length))) return false
      try {
        for (const e of toDelete) await deleteLink.mutateAsync(e.id)
        setToast({ text: ru.graph.deleted })
      } catch (err) {
        setToast({ text: errorMessage(err), error: true })
      }
      return { nodes: [], edges: [] }
    },
    [deleteLink, links.data, me.data],
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
            setContractStatus('')
          }}
        >
          {ru.app.reset}
        </button>
        <label className="field"><span>Статус контракта</span><select value={contractStatus} onChange={e => setContractStatus(e.target.value)}><option value="">Все</option>{Object.entries(ru.contract.statuses).map(([value, label]) => <option key={value} value={value}>{label}</option>)}</select></label>
        {writableProducts.length > 0 && <button type="button" className="btn btn-primary btn-sm" onClick={() => { setPending({ fromProduct: productId && writableProducts.some(product => product.id === productId) ? productId : writableProducts[0].id, toProduct: '' }); setFeatureLevel(false); setFormOpen(true); setToast(null) }}>Создать связь</button>}
        <button type="button" className="btn btn-sm" onClick={() => setLayoutVersion((v) => v + 1)}>
          {ru.graph.relayout}
        </button>
      </div>
      <p className="muted">{writableProducts.length ? ru.graph.hint : 'Просмотр графа. Изменения доступны владельцу продукта, руководителю портфеля и администратору.'}</p>
      {me.isError && <ErrorBox error={me.error} onRetry={() => void me.refetch()} />}
      {contracts.isError && <ErrorBox error={contracts.error} onRetry={() => void contracts.refetch()} />}
      {!visible.products.length && <p className="muted">Нет продуктов по выбранным фильтрам.</p>}
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
          fitViewOptions={{ padding: 0.15 }}
          minZoom={0.1}
          nodesDraggable
          nodesConnectable
          elementsSelectable
        >
          <Background />
          <Controls showInteractive={false} />
        </ReactFlow>
        {formOpen && pending && (
          <form
            className="graph-dialog stack"
            aria-label="Создание связи"
            style={{ maxHeight: 'calc(100% - 24px)', overflowY: 'auto' }}
            onSubmit={(e) => {
              e.preventDefault()
              void submit()
            }}
          >
            <h3>{ru.graph.newLink}</h3>
            <label className="field"><span>Уровень связи</span><select value={featureLevel ? 'feature' : 'product'} onChange={e => { setFeatureLevel(e.target.value === 'feature'); setPending({ ...pending, fromFeature: undefined, toFeature: undefined }) }}><option value="product">Продукты</option><option value="feature">Фичи</option></select></label>
            <label className="field"><span>Продукт-потребитель</span><select required value={pending.fromProduct} onChange={e => setPending({ ...pending, fromProduct: e.target.value, fromFeature: undefined })}><option value="">Выберите продукт</option>{writableProducts.map(product => <option key={product.id} value={product.id}>{product.name}</option>)}</select></label>
            <label className="field"><span>Продукт-поставщик</span><select required value={pending.toProduct} onChange={e => setPending({ ...pending, toProduct: e.target.value, toFeature: undefined })}><option value="">Выберите продукт</option>{products.data.map(product => <option key={product.id} value={product.id}>{product.name}</option>)}</select></label>
            {featureLevel && (['from', 'to'] as const).map(side => {
              const product = side === 'from' ? pending.fromProduct : pending.toProduct
              const query = featureQueryFor(product)
              return <div key={side} className="stack">
                <label className="field"><span>{side === 'from' ? 'Фича-потребитель' : 'Фича-поставщик'}</span><select required disabled={!product || query?.isPending || !!query?.error} value={(side === 'from' ? pending.fromFeature : pending.toFeature) ?? ''} onChange={e => setPending({ ...pending, [side === 'from' ? 'fromFeature' : 'toFeature']: e.target.value || undefined })}><option value="">{query?.isPending ? 'Загрузка…' : 'Выберите фичу'}</option>{query?.data?.map(feature => <option key={feature.id} value={feature.id}>{feature.name}</option>)}</select></label>
                {!!query?.error && <ErrorBox error={query.error} onRetry={() => void query.refetch()} />}
              </div>
            })}
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
              <button type="submit" className="btn btn-primary btn-sm" disabled={createLink.isPending || !canWriteGraph(me.data, pending.fromProduct) || !pending.toProduct || featureLevel && (!pending.fromFeature || !pending.toFeature)}>
                {ru.graph.create}
              </button>
              <button type="button" className="btn btn-sm" onClick={() => { setPending(null); setFormOpen(false) }}>
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
      <ContractsPanel />
    </section>
  )
}
