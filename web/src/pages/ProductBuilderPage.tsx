import { useState } from 'react'
import { Link, useNavigate, useParams } from 'react-router-dom'
import { useCreateFeature, useCreateLink, useCreateProduct, useMe, useProduct, useProducts, useUpdateProduct, type ProductInput } from '../api/hooks'
import { errorMessage } from '../api/client'
import type { Criticality, Lifecycle, LinkType, Product } from '../api/types'
import { ErrorBox, Loading } from '../components/Status'
import { ru } from '../i18n/ru'

type Kind = 'product' | 'platform'
type ProductType = ProductInput['type']

const TYPES: ProductType[] = ['security', 'infrastructure', 'platform', 'other']
const LIFECYCLES: Lifecycle[] = ['idea', 'active', 'sunset', 'retired']
const LINK_TYPES: LinkType[] = ['integration', 'shared_component', 'commercial', 'bundled']
const CRITS: Criticality[] = ['blocks', 'accelerates', 'desirable']
const KEY_RE = /^[a-z0-9][a-z0-9-]{0,31}$/

interface FeatureDraft {
  name: string
  planned_date: string
}

interface LinkDraft {
  peer: string
  direction: 'consumer' | 'provider' // consumer: новый продукт зависит от peer
  type: LinkType
  criticality: Criticality
}

let seq = 0
const nextId = () => `d${++seq}`

export function ProductBuilderPage() {
  const { id: editId = '' } = useParams()
  const editing = editId !== ''
  const me = useMe()
  const products = useProducts()
  const existing = useProduct(editId, editing)

  const canCreate = (me.data?.roles ?? []).some((r) => r === 'cpo' || r === 'admin')
  if (me.isPending || products.isPending || (editing && existing.isPending)) return <Loading />
  if (products.isError) return <ErrorBox error={products.error} onRetry={() => void products.refetch()} />
  if (editing && existing.isError) return <ErrorBox error={existing.error} onRetry={() => void existing.refetch()} />
  if (!canCreate) return <p className="muted">{ru.builder.forbidden}</p>
  // key перезапускает форму при смене продукта, поэтому состояние инициализируется из initial без эффектов.
  return <BuilderForm key={editId || 'new'} editId={editId} initial={editing ? existing.data : undefined} products={products.data} />
}

function BuilderForm({ editId, initial, products }: { editId: string; initial: Product | undefined; products: Product[] }) {
  const editing = editId !== ''
  const createProduct = useCreateProduct()
  const updateProduct = useUpdateProduct()
  const createFeature = useCreateFeature()
  const createLink = useCreateLink()
  const navigate = useNavigate()

  const [kind, setKind] = useState<Kind>(initial?.type === 'platform' && initial.hub_manual ? 'platform' : 'product')
  const [key, setKey] = useState(initial?.key ?? '')
  const [name, setName] = useState(initial?.name ?? '')
  const [description, setDescription] = useState(initial?.description ?? '')
  const [type, setType] = useState<ProductType>(initial?.type ?? 'security')
  const [owner, setOwner] = useState(initial?.owner ?? '')
  const [lifecycle, setLifecycle] = useState<Lifecycle>(initial?.lifecycle ?? 'active')
  const [ssdlc, setSsdlc] = useState(initial?.ssdlc_certified ?? false)
  const [hub, setHub] = useState(initial?.hub_manual ?? false)
  const [features, setFeatures] = useState<Record<string, FeatureDraft>>({})
  const [links, setLinks] = useState<Record<string, LinkDraft>>({})
  const [errors, setErrors] = useState<string[]>([])
  const [busy, setBusy] = useState(false)
  const [created, setCreated] = useState<Product | null>(null)

  const effectiveType: ProductType = kind === 'platform' ? 'platform' : type
  const effectiveHub = kind === 'platform' ? true : hub

  const validate = (): string[] => {
    const out: string[] = []
    if (!KEY_RE.test(key)) out.push(ru.builder.keyInvalid)
    if (!name.trim()) out.push(ru.builder.nameRequired)
    return out
  }

  const submit = async () => {
    const v = validate()
    setErrors(v)
    if (v.length) return
    setBusy(true)
    const failures: string[] = []
    const body: ProductInput = {
      key, name: name.trim(), type: effectiveType, owner: owner.trim() || undefined, lifecycle, ssdlc_certified: ssdlc, hub_manual: effectiveHub,
      description,
    }
    if (editing) {
      try {
        const p = await updateProduct.mutateAsync({ id: editId, body })
        setCreated(p)
      } catch (err) {
        setErrors([errorMessage(err)])
      } finally {
        setBusy(false)
      }
      return
    }
    try {
      const p = await createProduct.mutateAsync(body)
      for (const f of Object.values(features)) {
        if (!f.name.trim()) continue
        try {
          await createFeature.mutateAsync({ productId: p.id, body: { name: f.name.trim(), status: 'planned', planned_date: f.planned_date || undefined } })
        } catch (err) {
          failures.push(`${f.name}: ${errorMessage(err)}`)
        }
      }
      for (const l of Object.values(links)) {
        if (!l.peer) continue
        const body = l.direction === 'consumer'
          ? { type: l.type, criticality: l.criticality, from_product_id: p.id, to_product_id: l.peer }
          : { type: l.type, criticality: l.criticality, from_product_id: l.peer, to_product_id: p.id }
        try {
          await createLink.mutateAsync(body)
        } catch (err) {
          failures.push(`${ru.link.types[l.type]}: ${errorMessage(err)}`)
        }
      }
      setCreated(p)
      setErrors(failures)
    } catch (err) {
      setErrors([errorMessage(err)])
    } finally {
      setBusy(false)
    }
  }


  if (created) {
    return (
      <section className="stack">
        <h1>{editing ? ru.builder.editTitle : ru.builder.title}</h1>
        <div className="card stack">
          <p>{editing ? ru.builder.saved(created.name) : ru.builder.done(created.name)}</p>
          {errors.length > 0 && (
            <div className="alert">
              <p>{ru.builder.partial(errors.length)}</p>
              <ul>
                {errors.map((e) => (
                  <li key={e}>{e}</li>
                ))}
              </ul>
            </div>
          )}
          <div className="row">
            <Link className="btn btn-primary" to={`/products/${created.id}`}>
              {ru.builder.openProduct}
            </Link>
            <Link className="btn" to="/graph">
              {ru.builder.openGraph}
            </Link>
            <button type="button" className="btn" onClick={() => navigate(0)}>
              {ru.products.create}
            </button>
          </div>
        </div>
      </section>
    )
  }

  const peers = [...products].sort((a, b) => a.name.localeCompare(b.name, 'ru'))

  return (
    <section className="stack builder">
      <div className="page-head">
        <h1>{editing ? ru.builder.editTitle : ru.builder.title}</h1>
      </div>
      <p className="muted">{editing ? ru.builder.editHint : ru.builder.subtitle}</p>
      <form
        className="stack"
        onSubmit={(e) => {
          e.preventDefault()
          void submit()
        }}
      >
        <div className="card stack">
          <h2>{ru.builder.section.product}</h2>
          <fieldset className="filter-group">
            <legend>{ru.builder.kind}</legend>
            <label className="check">
              <input type="radio" name="kind" checked={kind === 'product'} onChange={() => setKind('product')} /> {ru.builder.kindProduct}
            </label>
            <label className="check">
              <input type="radio" name="kind" checked={kind === 'platform'} onChange={() => setKind('platform')} /> {ru.builder.kindPlatform}
            </label>
          </fieldset>
          {kind === 'platform' && <p className="muted">{ru.builder.kindHint}</p>}
          <label className="field">
            <span>Назначение, границы и источники</span>
            <textarea rows={8} maxLength={12000} value={description} onChange={(e) => setDescription(e.target.value)} />
          </label>
          <div className="grid-2">
            <label className="field">
              <span>{ru.product.key}</span>
              <input value={key} onChange={(e) => setKey(e.target.value.trim().toLowerCase())} placeholder="edr" required />
              <small className="muted">{ru.builder.keyHint}</small>
            </label>
            <label className="field">
              <span>{ru.product.name}</span>
              <input value={name} onChange={(e) => setName(e.target.value)} required />
            </label>
            <label className="field">
              <span>{ru.product.type}</span>
              <select value={effectiveType} disabled={kind === 'platform'} onChange={(e) => setType(e.target.value as ProductType)}>
                {TYPES.map((t) => (
                  <option key={t} value={t}>
                    {ru.product.types[t]}
                  </option>
                ))}
              </select>
            </label>
            <label className="field">
              <span>{ru.product.owner}</span>
              <input value={owner} onChange={(e) => setOwner(e.target.value)} placeholder="pm-edr" />
            </label>
            <label className="field">
              <span>{ru.product.lifecycle}</span>
              <select value={lifecycle} onChange={(e) => setLifecycle(e.target.value as Lifecycle)}>
                {LIFECYCLES.map((l) => (
                  <option key={l} value={l}>
                    {ru.product.lifecycles[l]}
                  </option>
                ))}
              </select>
            </label>
            <div className="field">
              <span>&nbsp;</span>
              <label className="check">
                <input type="checkbox" checked={ssdlc} onChange={(e) => setSsdlc(e.target.checked)} /> {ru.builder.ssdlcCertified}
              </label>
              <label className="check">
                <input type="checkbox" checked={effectiveHub} disabled={kind === 'platform'} onChange={(e) => setHub(e.target.checked)} /> {ru.builder.hubManual}
              </label>
            </div>
          </div>
        </div>

        {!editing && (
        <div className="card stack">
          <h2>{ru.builder.section.features}</h2>
          {Object.entries(features).map(([id, f]) => (
            <div key={id} className="row">
              <input
                className="grow"
                placeholder={ru.builder.featureName}
                value={f.name}
                onChange={(e) => setFeatures({ ...features, [id]: { ...f, name: e.target.value } })}
              />
              <input type="date" value={f.planned_date} onChange={(e) => setFeatures({ ...features, [id]: { ...f, planned_date: e.target.value } })} title={ru.builder.featureDate} />
              <button
                type="button"
                className="btn btn-sm"
                onClick={() => {
                  const next = { ...features }
                  delete next[id]
                  setFeatures(next)
                }}
              >
                {ru.builder.remove}
              </button>
            </div>
          ))}
          <div>
            <button type="button" className="btn btn-sm" onClick={() => setFeatures({ ...features, [nextId()]: { name: '', planned_date: '' } })}>
              {ru.builder.addFeature}
            </button>
          </div>
        </div>
        )}

        {!editing && (
        <div className="card stack">
          <h2>{ru.builder.section.links}</h2>
          {Object.entries(links).map(([id, l]) => (
            <div key={id} className="row wrap">
              <select value={l.direction} onChange={(e) => setLinks({ ...links, [id]: { ...l, direction: e.target.value as LinkDraft['direction'] } })} title={ru.builder.linkDirection}>
                <option value="consumer">{ru.builder.dirConsumer}</option>
                <option value="provider">{ru.builder.dirProvider}</option>
              </select>
              <select value={l.peer} onChange={(e) => setLinks({ ...links, [id]: { ...l, peer: e.target.value } })} title={ru.builder.linkPeer}>
                <option value="">{ru.builder.linkPeer}…</option>
                {peers.map((p) => (
                  <option key={p.id} value={p.id}>
                    {p.name}
                  </option>
                ))}
              </select>
              <select value={l.type} onChange={(e) => setLinks({ ...links, [id]: { ...l, type: e.target.value as LinkType } })} title={ru.graph.filterLinkType}>
                {LINK_TYPES.map((t) => (
                  <option key={t} value={t}>
                    {ru.link.types[t]}
                  </option>
                ))}
              </select>
              <select value={l.criticality} onChange={(e) => setLinks({ ...links, [id]: { ...l, criticality: e.target.value as Criticality } })} title={ru.link.criticalityLabel}>
                {CRITS.map((c) => (
                  <option key={c} value={c}>
                    {ru.link.criticality[c]}
                  </option>
                ))}
              </select>
              <button
                type="button"
                className="btn btn-sm"
                onClick={() => {
                  const next = { ...links }
                  delete next[id]
                  setLinks(next)
                }}
              >
                {ru.builder.remove}
              </button>
            </div>
          ))}
          <div>
            <button
              type="button"
              className="btn btn-sm"
              onClick={() =>
                setLinks({
                  ...links,
                  [nextId()]: { peer: '', direction: kind === 'platform' ? 'provider' : 'consumer', type: 'integration', criticality: 'blocks' },
                })
              }
            >
              {ru.builder.addLink}
            </button>
          </div>
        </div>
        )}

        {errors.length > 0 && (
          <div className="alert error">
            <ul>
              {errors.map((e) => (
                <li key={e}>{e}</li>
              ))}
            </ul>
          </div>
        )}
        <div className="row">
          <button type="submit" className="btn btn-primary" disabled={busy}>
            {busy ? ru.builder.creating : editing ? ru.builder.save : ru.builder.submit}
          </button>
          <Link className="btn" to={editing ? `/products/${editId}` : '/'}>
            {ru.app.cancel}
          </Link>
        </div>
      </form>
    </section>
  )
}
