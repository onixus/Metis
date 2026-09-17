import { useState, type FormEvent } from 'react'
import { Link, useParams } from 'react-router-dom'
import { errorMessage } from '../api/client'
import {
  accessLevel,
  useAcceptDecision,
  useCreateDecision,
  useDecisions,
  useMe,
  useProduct,
  useProducts,
  useRejectDecision,
  useRequestDecisionPage,
  useSupersedeDecision,
} from '../api/hooks'
import type { Decision, DecisionInput, DecisionLink, DecisionOption, DecisionStatus } from '../api/types'
import { Badge, Empty, ErrorBox, Loading } from '../components/Status'
import { ru } from '../i18n/ru'
import { fmtDate, fmtDateTime } from '../lib/format'
import { canWriteDecisions } from '../lib/roles'

const STATUSES: DecisionStatus[] = ['proposed', 'accepted', 'superseded', 'rejected']
const LINK_KINDS: DecisionLink['kind'][] = ['hypothesis', 'feature', 'signal', 'commitment', 'release', 'track']
const TONE: Record<DecisionStatus, 'neutral' | 'ok' | 'warn' | 'danger' | 'info'> = {
  proposed: 'info',
  accepted: 'ok',
  superseded: 'neutral',
  rejected: 'danger',
}

export function DecisionsPage() {
  const { id } = useParams()
  const me = useMe()
  const product = useProduct(id ?? '', !!id)
  const products = useProducts()
  const [status, setStatus] = useState<DecisionStatus | ''>('')
  const [creating, setCreating] = useState(false)
  const [pageRequested, setPageRequested] = useState<Set<string>>(new Set())
  const list = useDecisions(id, status || undefined, pageRequested)
  const accept = useAcceptDecision()
  const reject = useRejectDecision()
  const supersede = useSupersedeDecision()
  const requestPage = useRequestDecisionPage()
  const [superseding, setSuperseding] = useState<{ id: string; by: string } | null>(null)
  const level = id ? accessLevel(me.data, id) : me.data?.all_products ?? 'none'
  const canWrite = canWriteDecisions(me.data, level)

  if (me.isPending || list.isPending || (id && product.isPending)) return <Loading />
  if (id && product.isError) return <ErrorBox error={product.error} onRetry={() => void product.refetch()} />
  if (list.isError) return <ErrorBox error={list.error} onRetry={() => void list.refetch()} />

  const productName = new Map((products.data ?? []).map((p) => [p.id, p.name]))
  const actionError = accept.error ?? reject.error ?? supersede.error ?? requestPage.error
  const decisions = list.data

  return (
    <section className="stack">
      <div className="page-head">
        <div>
          <h1>{id ? ru.decisions.forProduct(product.data?.name ?? '') : ru.decisions.portfolio}</h1>
          {id ? <Link to={`/products/${id}`}>{ru.product.open}</Link> : <span className="muted">{ru.decisions.title}</span>}
        </div>
        <div className="row">
          <label className="field">
            <span>{ru.common.filterStatus}</span>
            <select value={status} onChange={(e) => setStatus(e.target.value as DecisionStatus | '')}>
              <option value="">{ru.decisions.all}</option>
              {STATUSES.map((s) => (
                <option key={s} value={s}>
                  {ru.decisions.statuses[s]}
                </option>
              ))}
            </select>
          </label>
          {canWrite && (
            <button type="button" className="btn btn-primary" onClick={() => setCreating((v) => !v)}>
              {ru.decisions.create}
            </button>
          )}
        </div>
      </div>
      {!canWrite && <p className="muted">{ru.common.forbiddenWrite}</p>}
      {actionError && <ErrorBox error={actionError} />}
      {requestPage.isSuccess && <div className="alert alert-ok">{ru.decisions.pageRequested}</div>}
      {creating && <DecisionForm productId={id} onDone={() => setCreating(false)} />}
      {decisions.length === 0 ? (
        <Empty text={ru.decisions.empty} />
      ) : (
        decisions.map((d: Decision) => (
          <div key={d.id} className="card stack">
            <div className="row wrap-row">
              <h2>{d.title}</h2>
              <Badge tone={TONE[d.status]}>{ru.decisions.statuses[d.status]}</Badge>
              {!id && d.product_id && (
                <Link to={`/products/${d.product_id}/decisions`}>{productName.get(d.product_id) ?? d.product_id}</Link>
              )}
              <span className="muted">
                {ru.decisions.author}: {d.author} · {fmtDateTime(d.created_at)}
              </span>
            </div>
            <dl className="kv">
              <dt>{ru.decisions.context}</dt>
              <dd className="wrap">{d.context}</dd>
              {(d.options ?? []).length > 0 && (
                <>
                  <dt>{ru.decisions.options}</dt>
                  <dd>
                    <ul>
                      {(d.options ?? []).map((o) => (
                        <li key={o.key}>
                          {o.key === d.chosen_key ? <strong>{o.title}</strong> : o.title}
                          {o.description && <span className="muted"> — {o.description}</span>}
                        </li>
                      ))}
                    </ul>
                  </dd>
                </>
              )}
              {d.rationale && (
                <>
                  <dt>{ru.decisions.rationale}</dt>
                  <dd className="wrap">{d.rationale}</dd>
                </>
              )}
              {d.expected_effect && (
                <>
                  <dt>{ru.decisions.expectedEffect}</dt>
                  <dd className="wrap">{d.expected_effect}</dd>
                </>
              )}
              <dt>{ru.decisions.reviewDate}</dt>
              <dd>{fmtDate(d.review_date)}</dd>
              {(d.links ?? []).length > 0 && (
                <>
                  <dt>{ru.decisions.links}</dt>
                  <dd>
                    {(d.links ?? []).map((l) => (
                      <span key={`${l.kind}:${l.id}`} className="badge badge-neutral" style={{ marginRight: 6 }}>
                        {ru.decisions.linkKinds[l.kind]} <span className="mono">{l.id}</span>
                      </span>
                    ))}
                  </dd>
                </>
              )}
              {d.superseded_by && (
                <>
                  <dt>{ru.decisions.supersededBy}</dt>
                  <dd>{decisions.find((x) => x.id === d.superseded_by)?.title ?? <span className="mono">{d.superseded_by}</span>}</dd>
                </>
              )}
              <dt>{ru.decisions.pageId}</dt>
              <dd>{d.page_id ? <span className="mono">{d.page_id}</span> : pageRequested.has(d.id) ? ru.app.loading : ru.app.dash}</dd>
            </dl>
            {canWrite && (
              <div className="row">
                {d.status === 'proposed' && (
                  <>
                    <button type="button" className="btn btn-sm btn-primary" disabled={accept.isPending} onClick={() => accept.mutate(d.id)}>
                      {ru.decisions.accept}
                    </button>
                    <button type="button" className="btn btn-sm btn-danger" disabled={reject.isPending} onClick={() => reject.mutate(d.id)}>
                      {ru.decisions.reject}
                    </button>
                  </>
                )}
                {d.status === 'accepted' && (
                  <button type="button" className="btn btn-sm" onClick={() => setSuperseding(superseding?.id === d.id ? null : { id: d.id, by: '' })}>
                    {ru.decisions.supersede}
                  </button>
                )}
                {!d.page_id && (
                  <button
                    type="button"
                    className="btn btn-sm"
                    disabled={requestPage.isPending || pageRequested.has(d.id)}
                    onClick={() =>
                      requestPage.mutate(d.id, { onSuccess: () => setPageRequested((s) => new Set(s).add(d.id)) })
                    }
                  >
                    {ru.decisions.requestPage}
                  </button>
                )}
              </div>
            )}
            {superseding?.id === d.id && (
              <form
                className="row"
                onSubmit={(e) => {
                  e.preventDefault()
                  if (!superseding.by) return
                  supersede.mutate({ id: d.id, by: superseding.by }, { onSuccess: () => setSuperseding(null) })
                }}
              >
                <label className="field grow">
                  <span>{ru.decisions.chooseSuperseder}</span>
                  <select value={superseding.by} onChange={(e) => setSuperseding({ id: d.id, by: e.target.value })} required>
                    <option value="">{ru.app.dash}</option>
                    {decisions
                      .filter((x) => x.id !== d.id)
                      .map((x) => (
                        <option key={x.id} value={x.id}>
                          {x.title} ({ru.decisions.statuses[x.status]})
                        </option>
                      ))}
                  </select>
                </label>
                <button type="submit" className="btn btn-sm btn-primary" disabled={supersede.isPending}>
                  {ru.decisions.supersede}
                </button>
              </form>
            )}
          </div>
        ))
      )}
    </section>
  )
}

function DecisionForm({ productId, onDone }: { productId: string | undefined; onDone: () => void }) {
  const create = useCreateDecision()
  const products = useProducts()
  const [product, setProduct] = useState(productId ?? '')
  const [title, setTitle] = useState('')
  const [context, setContext] = useState('')
  const [options, setOptions] = useState<DecisionOption[]>([{ key: 'a', title: '' }])
  const [chosen, setChosen] = useState('')
  const [rationale, setRationale] = useState('')
  const [effect, setEffect] = useState('')
  const [review, setReview] = useState('')
  const [links, setLinks] = useState<DecisionLink[]>([])
  const [validation, setValidation] = useState<string | null>(null)

  const submit = (e: FormEvent) => {
    e.preventDefault()
    if (!title.trim()) return setValidation(ru.common.required(ru.common.title))
    if (!context.trim()) return setValidation(ru.common.required(ru.decisions.context))
    setValidation(null)
    const body: DecisionInput = {
      product_id: product || undefined,
      title: title.trim(),
      context: context.trim(),
      options: options.filter((o) => o.key.trim() && o.title.trim()).map((o) => ({ ...o, description: o.description?.trim() || undefined })),
      chosen_key: chosen || undefined,
      rationale: rationale.trim() || undefined,
      expected_effect: effect.trim() || undefined,
      review_date: review || undefined,
      links: links.filter((l) => l.id.trim()).map((l) => ({ kind: l.kind, id: l.id.trim() })),
    }
    create.mutate(body, { onSuccess: onDone })
  }
  const setOpt = (i: number, patch: Partial<DecisionOption>) => setOptions((os) => os.map((o, j) => (j === i ? { ...o, ...patch } : o)))
  const setLink = (i: number, patch: Partial<DecisionLink>) => setLinks((ls) => ls.map((l, j) => (j === i ? { ...l, ...patch } : l)))

  return (
    <form className="card form-inline stack" onSubmit={submit}>
      <h3>{ru.decisions.create}</h3>
      <div className="row">
        {!productId && (
          <label className="field">
            <span>{ru.decisions.product}</span>
            <select value={product} onChange={(e) => setProduct(e.target.value)}>
              <option value="">{ru.common.portfolio}</option>
              {(products.data ?? []).map((p) => (
                <option key={p.id} value={p.id}>
                  {p.name}
                </option>
              ))}
            </select>
          </label>
        )}
        <label className="field grow">
          <span>{ru.common.title}</span>
          <input value={title} onChange={(e) => setTitle(e.target.value)} required />
        </label>
        <label className="field">
          <span>{ru.decisions.reviewDate}</span>
          <input type="date" value={review} onChange={(e) => setReview(e.target.value)} />
        </label>
      </div>
      <label className="field">
        <span>{ru.decisions.context}</span>
        <textarea rows={3} value={context} onChange={(e) => setContext(e.target.value)} required />
      </label>
      <fieldset className="filter-group stack">
        <legend>{ru.decisions.options}</legend>
        {options.map((o, i) => (
          <div key={i} className="row">
            <label className="field">
              <span>{ru.decisions.optionKey}</span>
              <input value={o.key} onChange={(e) => setOpt(i, { key: e.target.value })} />
            </label>
            <label className="field grow">
              <span>{ru.decisions.optionTitle}</span>
              <input value={o.title} onChange={(e) => setOpt(i, { title: e.target.value })} />
            </label>
            <label className="field grow">
              <span>{ru.decisions.optionDescription}</span>
              <input value={o.description ?? ''} onChange={(e) => setOpt(i, { description: e.target.value })} />
            </label>
            <button type="button" className="btn btn-sm" onClick={() => setOptions((os) => os.filter((_, j) => j !== i))}>
              {ru.common.remove}
            </button>
          </div>
        ))}
        <div className="row">
          <button type="button" className="btn btn-sm" onClick={() => setOptions((os) => [...os, { key: String.fromCharCode(97 + os.length), title: '' }])}>
            {ru.decisions.addOption}
          </button>
        </div>
      </fieldset>
      <div className="row">
        <label className="field">
          <span>{ru.decisions.chosen}</span>
          <select value={chosen} onChange={(e) => setChosen(e.target.value)}>
            <option value="">{ru.decisions.chooseOption}</option>
            {options
              .filter((o) => o.key.trim())
              .map((o) => (
                <option key={o.key} value={o.key}>
                  {o.key}: {o.title}
                </option>
              ))}
          </select>
        </label>
        <label className="field grow">
          <span>{ru.decisions.rationale}</span>
          <input value={rationale} onChange={(e) => setRationale(e.target.value)} />
        </label>
        <label className="field grow">
          <span>{ru.decisions.expectedEffect}</span>
          <input value={effect} onChange={(e) => setEffect(e.target.value)} />
        </label>
      </div>
      <fieldset className="filter-group stack">
        <legend>{ru.decisions.links}</legend>
        {links.map((l, i) => (
          <div key={i} className="row">
            <label className="field">
              <span>{ru.decisions.linkKind}</span>
              <select value={l.kind} onChange={(e) => setLink(i, { kind: e.target.value as DecisionLink['kind'] })}>
                {LINK_KINDS.map((k) => (
                  <option key={k} value={k}>
                    {ru.decisions.linkKinds[k]}
                  </option>
                ))}
              </select>
            </label>
            <label className="field grow">
              <span>{ru.decisions.linkId}</span>
              <input className="mono" value={l.id} onChange={(e) => setLink(i, { id: e.target.value })} />
            </label>
            <button type="button" className="btn btn-sm" onClick={() => setLinks((ls) => ls.filter((_, j) => j !== i))}>
              {ru.common.remove}
            </button>
          </div>
        ))}
        <div className="row">
          <button type="button" className="btn btn-sm" onClick={() => setLinks((ls) => [...ls, { kind: 'feature', id: '' }])}>
            {ru.decisions.addLink}
          </button>
        </div>
      </fieldset>
      {validation && <div className="alert alert-error">{validation}</div>}
      {create.isError && <div className="alert alert-error">{errorMessage(create.error)}</div>}
      <div className="row">
        <button type="submit" className="btn btn-primary" disabled={create.isPending}>
          {ru.common.create}
        </button>
        <button type="button" className="btn" onClick={onDone}>
          {ru.app.cancel}
        </button>
      </div>
    </form>
  )
}
