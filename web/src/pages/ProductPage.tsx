import { useMemo, useState, type FormEvent } from 'react'
import { Link, useNavigate, useParams } from 'react-router-dom'
import { errorMessage } from '../api/client'
import {
  accessLevel,
  useDeleteProduct,
  useFeatureValues,
  useFeatures,
  useMe,
  useShiftDate,
  useStrategic,
} from '../api/hooks'
import type { Contract, Feature, FeatureValue, Me } from '../api/types'
import { FeatureEditor } from '../components/FeatureEditor'
import { SignalsPanel } from '../components/SignalsPanel'
import { FeatureDetails } from '../components/FeatureDetails'
import { RankingBlock } from '../components/RankingBlock'
import { Badge, Empty, ErrorBox, Loading } from '../components/Status'
import { ru } from '../i18n/ru'
import { fmtDate, fmtMoney, pick } from '../lib/format'
import { canWriteCompliance, canWriteRoadmap } from '../lib/roles'

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
          <nav className="card row wrap-row" aria-label={ru.workflow.nextStep}>
            <a className="btn" href="#backlog">{ru.workflow.backlogStep}</a>
            <a className="btn" href="#signals">{ru.workflow.signalStep}</a>
            <a className="btn" href="#ranking">{ru.workflow.rankingStep}</a>
            <Link className="btn" to={`/products/${id}/roadmap`}>{ru.workflow.roadmapStep}</Link>
          </nav>
          <Backlog productId={id} me={me.data} />
          <RankingBlock productId={id} />
          <SignalsPanel productId={id} me={me.data} />
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
  const canPriority = canWriteRoadmap(me, accessLevel(me, productId))
  const [editing, setEditing] = useState<Feature | 'new' | null>(null)

  const valueById = useMemo(
    () => new Map<string, FeatureValue>((values.data ?? []).map((v) => [v.feature_id, v])),
    [values.data],
  )
  const nameById = useMemo(() => new Map((features.data ?? []).map((f) => [f.id, f.name])), [features.data])

  if (features.isPending) return <Loading />
  if (features.isError) return <ErrorBox error={features.error} onRetry={() => void features.refetch()} />

  return (
    <div className="card stack" id="backlog">
      <div className="page-head"><h2>{ru.productPage.backlog}</h2>{canPriority && <button type="button" className="btn btn-primary" onClick={() => setEditing('new')}>{ru.workflow.createFeature}</button>}</div>
      {editing && <FeatureEditor key={editing === 'new' ? 'new' : editing.id} productId={productId} feature={editing === 'new' ? undefined : editing} onDone={() => setEditing(null)} />}
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
                        {canPriority && <>
                          <button type="button" className="btn btn-sm" onClick={() => setEditing(f)}>{ru.workflow.edit}</button>
                          <button type="button" className="btn btn-sm" onClick={() => setShifting(f)}>{ru.feature.shiftDate}</button>
                          <Link className="btn btn-sm" to={`/products/${productId}/roadmap?feature=${f.id}`}>{ru.workflow.addToRoadmap}</Link>
                        </>}
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
