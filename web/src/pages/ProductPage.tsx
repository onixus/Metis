import { useMemo, useState, type FormEvent } from 'react'
import { Link, useNavigate, useParams } from 'react-router-dom'
import { errorMessage } from '../api/client'
import {
  accessLevel,
  useDeleteProduct,
  useFeatureValues,
  useFeatures,
  useHypotheses,
  useLinkSignal,
  useLinkSignalToHypothesis,
  useMe,
  useMergeSignals,
  useShiftDate,
  useSimilarSignals,
  useStrategic,
  useTriageQueue,
} from '../api/hooks'
import type { Contract, Feature, FeatureValue, Me, Signal } from '../api/types'
import { FeatureDetails } from '../components/FeatureDetails'
import { RankingBlock } from '../components/RankingBlock'
import { Badge, Empty, ErrorBox, Loading } from '../components/Status'
import { ru } from '../i18n/ru'
import { fmtDate, fmtMoney, pick } from '../lib/format'
import { canWriteCompliance, canWriteDiscovery, hasRole } from '../lib/roles'

const STATUS_TONE: Record<Feature['status'], 'neutral' | 'ok' | 'warn' | 'danger' | 'info'> = {
  idea: 'neutral',
  discovery: 'neutral',
  planned: 'info',
  in_progress: 'warn',
  done: 'ok',
  rejected: 'danger',
}

export function ProductPage() {
  const { id = '' } = useParams()
  const me = useMe()
  const level = accessLevel(me.data, id)
  const isPrivate = level === 'private'
  const strategic = useStrategic(id)
  const del = useDeleteProduct()
  const navigate = useNavigate()
  const [deleteError, setDeleteError] = useState('')
  const canManage = (me.data?.roles ?? []).some((r) => r === 'cpo' || r === 'admin')

  if (me.isPending || strategic.isPending) return <Loading />
  if (strategic.isError) return <ErrorBox error={strategic.error} onRetry={() => void strategic.refetch()} />
  const { product, features, contracts } = strategic.data

  return (
    <section className="stack">
      <div className="page-head">
        <div>
          <h1>{product.name}</h1>
          <div className="muted">
            <span className="mono">{product.key}</span> · {pick(ru.product.types, product.type)} ·{' '}
            {pick(ru.product.lifecycles, product.lifecycle)} · {ru.me.access[level]}
          </div>
        </div>
        <div className="row">
          <Link className="btn" to={`/products/${id}/roadmap`}>
            {ru.product.roadmap}
          </Link>
          {isPrivate && (
            <>
              <Link className="btn" to={`/products/${id}/discovery`}>
                {ru.productLinks.discovery}
              </Link>
              <Link className="btn" to={`/products/${id}/commitments`}>
                {ru.productLinks.commitments}
              </Link>
            </>
          )}
          <Link className="btn" to={`/products/${id}/compliance`}>
            {ru.productLinks.compliance}
          </Link>
          <Link className="btn" to={`/products/${id}/decisions`}>
            {ru.productLinks.decisions}
          </Link>
          {canManage && (
            <>
              <Link className="btn" to={`/products/${id}/edit`}>
                {ru.product.edit}
              </Link>
              <button
                type="button"
                className="btn btn-danger"
                disabled={del.isPending}
                onClick={() => {
                  if (!window.confirm(ru.product.deleteConfirm(product.name))) return
                  setDeleteError('')
                  del.mutate(id, {
                    onSuccess: () => navigate('/'),
                    onError: (err) => setDeleteError(errorMessage(err)),
                  })
                }}
              >
                {ru.product.delete}
              </button>
            </>
          )}
        </div>
      </div>
      {deleteError && <div className="alert error">{deleteError}</div>}

      <div className="card stack">
        <h2>{ru.productPage.strategic}</h2>
        {features.length === 0 ? (
          <Empty text={ru.productPage.noFeatures} />
        ) : (
          <div className="table-wrap">
            <table className="table">
              <thead>
                <tr>
                  <th>{ru.feature.name}</th>
                  <th>{ru.feature.status}</th>
                  <th>{ru.feature.totalValue}</th>
                  <th>{ru.feature.plannedDate}</th>
                  <th>{ru.feature.affected}</th>
                </tr>
              </thead>
              <tbody>
                {features.map((f) => (
                  <tr key={f.id}>
                    <td>{f.name}</td>
                    <td>{pick(ru.feature.statuses, f.status)}</td>
                    <td className="num">{fmtMoney(f.total_value)}</td>
                    <td>{fmtDate(f.planned_date)}</td>
                    <td>{f.affected ? <Badge tone="danger">{ru.app.yes}</Badge> : ru.app.no}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
        <h3>{ru.productPage.contracts}</h3>
        <ContractsTable contracts={contracts} />
      </div>

      {isPrivate ? (
        <>
          <Backlog productId={id} me={me.data} />
          <RankingBlock productId={id} />
          <TriageQueue productId={id} me={me.data} />
        </>
      ) : (
        <p className="muted">{ru.productPage.backlogPrivateOnly}</p>
      )}
    </section>
  )
}

export function ContractsTable({ contracts }: { contracts: Contract[] }) {
  if (contracts.length === 0) return <Empty text={ru.productPage.noContracts} />
  return (
    <div className="table-wrap">
      <table className="table">
        <thead>
          <tr>
            <th>{ru.contract.name}</th>
            <th>{ru.contract.status}</th>
            <th>{ru.contract.criticality}</th>
            <th>{ru.contract.version}</th>
            <th>{ru.contract.readyDate}</th>
            <th>{ru.contract.signalValue}</th>
          </tr>
        </thead>
        <tbody>
          {contracts.map((c) => (
            <tr key={c.id}>
              <td>{c.name}</td>
              <td>{pick(ru.contract.statuses, c.status)}</td>
              <td>{pick(ru.link.criticality, c.criticality)}</td>
              <td className="mono">{c.interface_version ?? ru.app.dash}</td>
              <td>{fmtDate(c.ready_date)}</td>
              <td className="num">{fmtMoney(c.signal_value)}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}

function Backlog({ productId, me }: { productId: string; me: Me | undefined }) {
  const features = useFeatures(productId)
  const values = useFeatureValues(productId)
  const [shifting, setShifting] = useState<Feature | null>(null)
  const [expanded, setExpanded] = useState<string | null>(null)
  const canCompliance = canWriteCompliance(me, accessLevel(me, productId))
  const canPriority = hasRole(me, 'admin', 'cpo', 'pm')

  const valueById = useMemo(
    () => new Map<string, FeatureValue>((values.data ?? []).map((v) => [v.feature_id, v])),
    [values.data],
  )
  const nameById = useMemo(() => new Map((features.data ?? []).map((f) => [f.id, f.name])), [features.data])

  if (features.isPending) return <Loading />
  if (features.isError) return <ErrorBox error={features.error} onRetry={() => void features.refetch()} />

  return (
    <div className="card stack">
      <h2>{ru.productPage.backlog}</h2>
      {values.isError && <ErrorBox error={values.error} />}
      {features.data.length === 0 ? (
        <Empty text={ru.productPage.noFeatures} />
      ) : (
        <div className="table-wrap">
          <table className="table">
            <thead>
              <tr>
                <th>{ru.feature.name}</th>
                <th>{ru.feature.status}</th>
                <th>{ru.feature.ownValue}</th>
                <th>{ru.feature.derivedValue}</th>
                <th>{ru.feature.totalValue}</th>
                <th>{ru.feature.plannedDate}</th>
                <th>{ru.feature.impliedDate}</th>
                <th>{ru.feature.affected}</th>
                <th />
              </tr>
            </thead>
            <tbody>
              {features.data.map((f) => {
                const v = valueById.get(f.id)
                return (
                  <FeatureRows key={f.id} expanded={expanded === f.id} details={
                    <FeatureDetails featureId={f.id} productId={productId} canCompliance={canCompliance} canPriority={canPriority} />
                  }>
                  <tr className={f.affected ? 'row-affected' : undefined}>
                    <td>
                      {f.name}
                      {f.external_key && <span className="muted mono"> {f.external_key}</span>}
                    </td>
                    <td>
                      <Badge tone={STATUS_TONE[f.status]}>{pick(ru.feature.statuses, f.status)}</Badge>
                    </td>
                    <td className="num">{fmtMoney(f.own_value)}</td>
                    <td className="num">{fmtMoney(v?.derived_value)}</td>
                    <td className="num">{fmtMoney(v?.total_value)}</td>
                    <td>{fmtDate(f.planned_date)}</td>
                    <td>{fmtDate(f.implied_date)}</td>
                    <td>
                      {f.affected ? (
                        <Badge tone="danger">
                          {ru.app.yes}
                          {f.affected_by && ` · ${nameById.get(f.affected_by) ?? f.affected_by}`}
                        </Badge>
                      ) : (
                        ru.app.no
                      )}
                    </td>
                    <td>
                      <div className="row">
                        <button type="button" className="btn btn-sm" onClick={() => setShifting(f)}>
                          {ru.feature.shiftDate}
                        </button>
                        <button type="button" className="btn btn-sm" onClick={() => setExpanded(expanded === f.id ? null : f.id)}>
                          {expanded === f.id ? ru.productLinks.hide : ru.productLinks.details}
                        </button>
                        <Link className="btn btn-sm" to={`/trace/feature/${f.id}`}>
                          {ru.discovery.hypothesis.trace}
                        </Link>
                      </div>
                    </td>
                  </tr>
                  </FeatureRows>
                )
              })}
            </tbody>
          </table>
        </div>
      )}
      {shifting && <ShiftDateForm productId={productId} feature={shifting} onClose={() => setShifting(null)} />}
    </div>
  )
}

/** Строка бэклога и раскрываемая под ней карточка этапа 2. */
function FeatureRows({ expanded, details, children }: { expanded: boolean; details: React.ReactNode; children: React.ReactNode }) {
  return (
    <>
      {children}
      {expanded && (
        <tr>
          <td colSpan={9}>{details}</td>
        </tr>
      )}
    </>
  )
}

function ShiftDateForm({ productId, feature, onClose }: { productId: string; feature: Feature; onClose: () => void }) {
  const shift = useShiftDate(productId)
  const [date, setDate] = useState(feature.planned_date ?? '')
  const [reason, setReason] = useState('')
  const [validation, setValidation] = useState<string | null>(null)

  const submit = (e: FormEvent) => {
    e.preventDefault()
    if (!date) return setValidation(ru.feature.dateRequired)
    if (!reason.trim()) return setValidation(ru.feature.reasonRequired)
    setValidation(null)
    shift.mutate({ featureId: feature.id, planned_date: date, reason: reason.trim() })
  }

  return (
    <form className="card form-inline stack" onSubmit={submit}>
      <h3>
        {ru.feature.shiftDateTitle}: {feature.name}
      </h3>
      <div className="row">
        <label className="field">
          <span>{ru.feature.newDate}</span>
          <input type="date" value={date} onChange={(e) => setDate(e.target.value)} required />
        </label>
        <label className="field grow">
          <span>{ru.feature.reason}</span>
          <input type="text" value={reason} onChange={(e) => setReason(e.target.value)} required />
        </label>
      </div>
      {validation && <div className="alert alert-error">{validation}</div>}
      {shift.isError && <div className="alert alert-error">{errorMessage(shift.error)}</div>}
      {shift.isSuccess && (
        <div className="alert alert-ok">
          {ru.feature.shiftDone(shift.data.affected.length, shift.data.contracts.length)}
        </div>
      )}
      <div className="row">
        <button type="submit" className="btn btn-primary" disabled={shift.isPending}>
          {ru.app.save}
        </button>
        <button type="button" className="btn" onClick={onClose}>
          {ru.app.cancel}
        </button>
      </div>
    </form>
  )
}

function TriageQueue({ productId, me }: { productId: string; me: Me | undefined }) {
  const queue = useTriageQueue(productId)
  const features = useFeatures(productId)
  const hyps = useHypotheses(productId)
  const link = useLinkSignal(productId)
  const linkHyp = useLinkSignalToHypothesis(productId)
  const [choice, setChoice] = useState<Record<string, string>>({})
  const [hypChoice, setHypChoice] = useState<Record<string, string>>({})
  const [similarFor, setSimilarFor] = useState<Signal | null>(null)
  const canDiscovery = canWriteDiscovery(me, accessLevel(me, productId))

  if (queue.isPending) return <Loading />
  if (queue.isError) return <ErrorBox error={queue.error} onRetry={() => void queue.refetch()} />

  const linkable = (s: Signal) => s.status === 'new' || s.status === 'in_review'

  return (
    <div className="card stack">
      <h2>{ru.productPage.signals}</h2>
      {link.isError && <ErrorBox error={link.error} />}
      {link.isSuccess && <div className="alert alert-ok">{ru.signal.linked}</div>}
      {linkHyp.isError && <ErrorBox error={linkHyp.error} />}
      {linkHyp.isSuccess && <div className="alert alert-ok">{ru.signal2.linkedHypothesis}</div>}
      {queue.data.length === 0 ? (
        <Empty text={ru.productPage.noSignals} />
      ) : (
        <div className="table-wrap">
          <table className="table">
            <thead>
              <tr>
                <th>{ru.signal.text}</th>
                <th>{ru.signal.source}</th>
                <th>{ru.signal.status}</th>
                <th>{ru.signal.weight}</th>
                <th>{ru.signal.blocksDeal}</th>
                <th>{ru.signal.dueDate}</th>
                <th>{ru.signal.linkTo}</th>
                <th>{ru.signal2.actions}</th>
              </tr>
            </thead>
            <tbody>
              {queue.data.map((s) => (
                <tr key={s.id}>
                  <td className="wrap">{s.text}</td>
                  <td>{pick(ru.signal.sources, s.source)}</td>
                  <td>{pick(ru.signal.statuses, s.status)}</td>
                  <td className="num">{fmtMoney(s.weight)}</td>
                  <td>{s.blocks_deal ? <Badge tone="danger">{ru.app.yes}</Badge> : ru.app.no}</td>
                  <td>{fmtDate(s.due_date)}</td>
                  <td>
                    {linkable(s) ? (
                      <div className="row">
                        <select
                          value={choice[s.id] ?? ''}
                          onChange={(e) => setChoice((c) => ({ ...c, [s.id]: e.target.value }))}
                        >
                          <option value="">{ru.signal.chooseFeature}</option>
                          {(features.data ?? []).map((f) => (
                            <option key={f.id} value={f.id}>
                              {f.name}
                            </option>
                          ))}
                        </select>
                        <button
                          type="button"
                          className="btn btn-sm"
                          disabled={!choice[s.id] || link.isPending}
                          onClick={() => link.mutate({ signalId: s.id, featureId: choice[s.id] })}
                        >
                          {ru.signal.linkSubmit}
                        </button>
                      </div>
                    ) : (
                      s.feature_id ?? ru.app.dash
                    )}
                  </td>
                  <td>
                    <div className="row">
                      <Link className="btn btn-sm" to={`/trace/signal/${s.id}`}>
                        {ru.discovery.hypothesis.trace}
                      </Link>
                      {linkable(s) && (
                        <button type="button" className="btn btn-sm" onClick={() => setSimilarFor(similarFor?.id === s.id ? null : s)}>
                          {ru.signal2.similar}
                        </button>
                      )}
                      {canDiscovery && linkable(s) && (
                        <>
                          <select value={hypChoice[s.id] ?? ''} onChange={(e) => setHypChoice((c) => ({ ...c, [s.id]: e.target.value }))}>
                            <option value="">{ru.signal2.chooseHypothesis}</option>
                            {(hyps.data ?? []).map((h) => (
                              <option key={h.id} value={h.id}>
                                {h.title}
                              </option>
                            ))}
                          </select>
                          <button
                            type="button"
                            className="btn btn-sm"
                            disabled={!hypChoice[s.id] || linkHyp.isPending}
                            onClick={() => linkHyp.mutate({ signalId: s.id, hypothesisId: hypChoice[s.id] })}
                          >
                            {ru.signal2.linkHypothesis}
                          </button>
                        </>
                      )}
                    </div>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      {similarFor && <SimilarSignals productId={productId} signal={similarFor} onClose={() => setSimilarFor(null)} />}
    </div>
  )
}

/** Похожие сигналы (SG-04) с объединением дубликатов в текущий. */
function SimilarSignals({ productId, signal, onClose }: { productId: string; signal: Signal; onClose: () => void }) {
  const similar = useSimilarSignals(signal.id, true)
  const merge = useMergeSignals(productId)
  const [picked, setPicked] = useState<string[]>([])
  return (
    <div className="card form-inline stack">
      <div className="row wrap-row">
        <h3>
          {ru.signal2.similar}: {signal.text.slice(0, 80)}
        </h3>
        <button type="button" className="btn btn-sm" onClick={onClose}>
          {ru.common.close}
        </button>
      </div>
      {similar.isPending && <Loading />}
      {similar.isError && <ErrorBox error={similar.error} />}
      {similar.data && similar.data.length === 0 && <Empty text={ru.signal2.similarEmpty} />}
      {similar.data && similar.data.length > 0 && (
        <>
          <div className="table-wrap">
            <table className="table table-compact">
              <thead>
                <tr>
                  <th />
                  <th>{ru.signal.text}</th>
                  <th>{ru.signal.status}</th>
                  <th>{ru.signal2.score}</th>
                </tr>
              </thead>
              <tbody>
                {similar.data.map(({ signal: s, score }) => (
                  <tr key={s.id}>
                    <td>
                      <input
                        type="checkbox"
                        aria-label={s.text}
                        checked={picked.includes(s.id)}
                        disabled={s.status === 'merged'}
                        onChange={(e) => setPicked((p) => (e.target.checked ? [...p, s.id] : p.filter((x) => x !== s.id)))}
                      />
                    </td>
                    <td className="wrap">{s.text}</td>
                    <td>{pick(ru.signal.statuses, s.status)}</td>
                    <td className="num mono">{score.toFixed(2)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
          <p className="muted">{ru.signal2.mergeHint}</p>
          <div className="row">
            <button
              type="button"
              className="btn btn-sm btn-primary"
              disabled={picked.length === 0 || merge.isPending}
              onClick={() => merge.mutate({ signalId: signal.id, duplicate_ids: picked }, { onSuccess: () => setPicked([]) })}
            >
              {ru.signal2.merge}
            </button>
            {merge.isSuccess && <span className="alert alert-ok">{ru.signal2.merged}</span>}
            {merge.isError && <span className="alert alert-error">{errorMessage(merge.error)}</span>}
          </div>
        </>
      )}
    </div>
  )
}
