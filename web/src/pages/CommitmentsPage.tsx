import { useMemo, useState, type FormEvent } from 'react'
import { Link, useParams } from 'react-router-dom'
import { errorMessage } from '../api/client'
import {
  accessLevel,
  useAckAlert,
  useCancelCommitment,
  useCommitmentAlerts,
  useCommitments,
  useCreateCommitment,
  useEnsureRenewals,
  useFeatures,
  useFulfilCommitment,
  useMe,
  useProduct,
  useReleases,
  useUpdateCommitment,
} from '../api/hooks'
import type { Commitment, CommitmentInput } from '../api/types'
import { Badge, Empty, ErrorBox, Loading } from '../components/Status'
import { ru } from '../i18n/ru'
import { fmtDate, fmtDateTime, pick } from '../lib/format'
import { canWriteCommitments, hasRole } from '../lib/roles'

const KINDS = ['customer', 'regulatory'] as const
const SUBTYPES = ['certificate_expiry', 'support_end', 'vuln_fix_deadline'] as const
const STATUS_TONE: Record<Commitment['status'], 'neutral' | 'ok' | 'warn' | 'danger' | 'info'> = {
  active: 'info',
  fulfilled: 'ok',
  breached: 'danger',
  cancelled: 'neutral',
}

export function CommitmentsPage() {
  const { id = '' } = useParams()
  const me = useMe()
  const product = useProduct(id)
  const list = useCommitments(id)
  const features = useFeatures(id)
  const releases = useReleases(id)
  const fulfil = useFulfilCommitment(id)
  const cancel = useCancelCommitment(id)
  const renewals = useEnsureRenewals(id)
  const [editing, setEditing] = useState<Commitment | 'new' | null>(null)
  const level = accessLevel(me.data, id)
  const canWrite = canWriteCommitments(me.data, level)
  const canPlan = hasRole(me.data, 'cpo', 'admin', 'pm')
  const featureName = useMemo(() => new Map((features.data ?? []).map((f) => [f.id, f.name])), [features.data])
  const releaseName = useMemo(() => new Map((releases.data ?? []).map((r) => [r.id, `${r.name} ${r.version}`])), [releases.data])

  if (me.isPending || product.isPending || list.isPending) return <Loading />
  if (product.isError) return <ErrorBox error={product.error} onRetry={() => void product.refetch()} />
  if (list.isError) return <ErrorBox error={list.error} onRetry={() => void list.refetch()} />

  const actionError = fulfil.error ?? cancel.error ?? renewals.error

  return (
    <section className="stack">
      <div className="page-head">
        <div>
          <h1>
            {ru.commitments.title}: {product.data.name}
          </h1>
          <Link to={`/products/${id}`}>{ru.product.open}</Link>
        </div>
        <div className="row">
          {canPlan && (
            <button type="button" className="btn" disabled={renewals.isPending} onClick={() => renewals.mutate(undefined)}>
              {ru.commitments.ensureRenewals}
            </button>
          )}
          {canWrite && (
            <button type="button" className="btn btn-primary" onClick={() => setEditing(editing === 'new' ? null : 'new')}>
              {ru.commitments.create}
            </button>
          )}
        </div>
      </div>
      {!canWrite && <p className="muted">{ru.common.forbiddenWrite}</p>}
      {actionError && <ErrorBox error={actionError} />}
      {renewals.isSuccess && <div className="alert alert-ok">{ru.commitments.ensureRenewalsDone(renewals.data.length)}</div>}
      {editing && (
        <CommitmentForm
          productId={id}
          initial={editing === 'new' ? null : editing}
          features={(features.data ?? []).map((f) => ({ id: f.id, name: f.name }))}
          releases={(releases.data ?? []).map((r) => ({ id: r.id, name: `${r.name} ${r.version}` }))}
          onDone={() => setEditing(null)}
        />
      )}
      <div className="card stack">
        <h2>{ru.commitments.registry}</h2>
        {list.data.length === 0 ? (
          <Empty text={ru.commitments.empty} />
        ) : (
          <div className="table-wrap">
            <table className="table">
              <thead>
                <tr>
                  <th>{ru.commitments.kind}</th>
                  <th>{ru.commitments.counterparty}</th>
                  <th>{ru.commitments.subject}</th>
                  <th>{ru.commitments.dueDate}</th>
                  <th>{ru.commitments.basis}</th>
                  <th>{ru.commitments.owner}</th>
                  <th>{ru.commitments.feature}</th>
                  <th>{ru.commitments.status}</th>
                  <th />
                </tr>
              </thead>
              <tbody>
                {list.data.map((c) => (
                  <tr key={c.id}>
                    <td>
                      {ru.commitments.kinds[c.kind]}
                      {c.subtype && <div className="muted">{pick(ru.commitments.subtypes, c.subtype)}</div>}
                    </td>
                    <td>{c.counterparty}</td>
                    <td className="wrap">{c.subject}</td>
                    <td>{fmtDate(c.due_date)}</td>
                    <td className="wrap">{c.basis}</td>
                    <td>{c.owner}</td>
                    <td>
                      {c.feature_id ? (featureName.get(c.feature_id) ?? c.feature_id) : ru.app.dash}
                      {c.release_id && <div className="muted">{releaseName.get(c.release_id) ?? c.release_id}</div>}
                    </td>
                    <td>
                      <Badge tone={STATUS_TONE[c.status]}>{ru.commitments.statuses[c.status]}</Badge>
                      {c.renewal_item_id && <div className="muted">{ru.commitments.renewalItem}</div>}
                    </td>
                    <td>
                      {canWrite && c.status === 'active' && (
                        <div className="row">
                          <button type="button" className="btn btn-sm" onClick={() => setEditing(c)}>
                            {ru.common.edit}
                          </button>
                          <button type="button" className="btn btn-sm" disabled={fulfil.isPending} onClick={() => fulfil.mutate(c.id)}>
                            {ru.commitments.fulfil}
                          </button>
                          <button
                            type="button"
                            className="btn btn-sm btn-danger"
                            disabled={cancel.isPending}
                            onClick={() => window.confirm(ru.commitments.cancelConfirm) && cancel.mutate(c.id)}
                          >
                            {ru.commitments.cancel}
                          </button>
                        </div>
                      )}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </div>
      <Alerts productId={id} canWrite={canWrite} />
    </section>
  )
}

function CommitmentForm({
  productId,
  initial,
  features,
  releases,
  onDone,
}: {
  productId: string
  initial: Commitment | null
  features: { id: string; name: string }[]
  releases: { id: string; name: string }[]
  onDone: () => void
}) {
  const create = useCreateCommitment(productId)
  const update = useUpdateCommitment(productId)
  const [form, setForm] = useState<CommitmentInput>(
    initial
      ? {
          kind: initial.kind,
          subtype: initial.subtype,
          counterparty: initial.counterparty,
          subject: initial.subject,
          due_date: initial.due_date,
          basis: initial.basis,
          owner: initial.owner,
          feature_id: initial.feature_id,
          release_id: initial.release_id,
        }
      : { kind: 'customer', counterparty: '', subject: '', due_date: '', basis: '', owner: '' },
  )
  const set = <K extends keyof CommitmentInput>(k: K, v: CommitmentInput[K]) => setForm((f) => ({ ...f, [k]: v }))
  const m = initial ? update : create

  const submit = (e: FormEvent) => {
    e.preventDefault()
    const body: CommitmentInput = {
      ...form,
      subtype: form.kind === 'regulatory' ? form.subtype : undefined,
      feature_id: form.feature_id || undefined,
      release_id: form.release_id || undefined,
    }
    if (initial) update.mutate({ id: initial.id, body }, { onSuccess: onDone })
    else create.mutate(body, { onSuccess: onDone })
  }

  return (
    <form className="card form-inline stack" onSubmit={submit}>
      <h3>{initial ? ru.common.edit : ru.commitments.create}</h3>
      <div className="row">
        <label className="field">
          <span>{ru.commitments.kind}</span>
          <select value={form.kind} onChange={(e) => set('kind', e.target.value as CommitmentInput['kind'])}>
            {KINDS.map((k) => (
              <option key={k} value={k}>
                {ru.commitments.kinds[k]}
              </option>
            ))}
          </select>
        </label>
        {form.kind === 'regulatory' && (
          <label className="field">
            <span>{ru.commitments.subtype}</span>
            <select value={form.subtype ?? ''} onChange={(e) => set('subtype', (e.target.value || undefined) as CommitmentInput['subtype'])} required>
              <option value="">{ru.app.dash}</option>
              {SUBTYPES.map((s) => (
                <option key={s} value={s}>
                  {ru.commitments.subtypes[s]}
                </option>
              ))}
            </select>
          </label>
        )}
        <label className="field grow">
          <span>{ru.commitments.counterparty}</span>
          <input value={form.counterparty} onChange={(e) => set('counterparty', e.target.value)} required />
        </label>
        <label className="field">
          <span>{ru.commitments.dueDate}</span>
          <input type="date" value={form.due_date} onChange={(e) => set('due_date', e.target.value)} required />
        </label>
        <label className="field">
          <span>{ru.commitments.owner}</span>
          <input value={form.owner} onChange={(e) => set('owner', e.target.value)} required />
        </label>
      </div>
      <div className="row">
        <label className="field grow">
          <span>{ru.commitments.subject}</span>
          <input value={form.subject} onChange={(e) => set('subject', e.target.value)} required />
        </label>
        <label className="field grow">
          <span>{ru.commitments.basis}</span>
          <input value={form.basis} onChange={(e) => set('basis', e.target.value)} required />
        </label>
        <label className="field">
          <span>{ru.commitments.feature}</span>
          <select value={form.feature_id ?? ''} onChange={(e) => set('feature_id', e.target.value)}>
            <option value="">{ru.app.dash}</option>
            {features.map((f) => (
              <option key={f.id} value={f.id}>
                {f.name}
              </option>
            ))}
          </select>
        </label>
        <label className="field">
          <span>{ru.commitments.release}</span>
          <select value={form.release_id ?? ''} onChange={(e) => set('release_id', e.target.value)}>
            <option value="">{ru.app.dash}</option>
            {releases.map((r) => (
              <option key={r.id} value={r.id}>
                {r.name}
              </option>
            ))}
          </select>
        </label>
      </div>
      {m.isError && <div className="alert alert-error">{errorMessage(m.error)}</div>}
      <div className="row">
        <button type="submit" className="btn btn-primary" disabled={m.isPending}>
          {ru.app.save}
        </button>
        <button type="button" className="btn" onClick={onDone}>
          {ru.app.cancel}
        </button>
      </div>
    </form>
  )
}

function Alerts({ productId, canWrite }: { productId: string; canWrite: boolean }) {
  const [all, setAll] = useState(false)
  const alerts = useCommitmentAlerts(productId, !all)
  const ack = useAckAlert(productId)
  return (
    <div className="card stack">
      <div className="row wrap-row">
        <h2>{ru.commitments.alerts}</h2>
        <label className="check">
          <input type="checkbox" checked={all} onChange={(e) => setAll(e.target.checked)} />
          {ru.commitments.showAll}
        </label>
      </div>
      {alerts.isPending && <Loading />}
      {alerts.isError && <ErrorBox error={alerts.error} onRetry={() => void alerts.refetch()} />}
      {ack.isError && <ErrorBox error={ack.error} />}
      {alerts.data && alerts.data.length === 0 && <Empty text={ru.commitments.alertsEmpty} />}
      {alerts.data && alerts.data.length > 0 && (
        <div className="table-wrap">
          <table className="table">
            <thead>
              <tr>
                <th>{ru.commitments.raisedAt}</th>
                <th>{ru.commitments.alertMessage}</th>
                <th>{ru.commitments.newDate}</th>
                <th>{ru.commitments.dueDate}</th>
                <th>{ru.commitments.status}</th>
                <th />
              </tr>
            </thead>
            <tbody>
              {alerts.data.map((a) => (
                <tr key={a.id} className={a.acknowledged ? undefined : 'row-affected'}>
                  <td>{fmtDateTime(a.raised_at)}</td>
                  <td className="wrap">{a.message}</td>
                  <td>{fmtDate(a.new_date)}</td>
                  <td>{fmtDate(a.due_date)}</td>
                  <td>
                    {a.acknowledged ? (
                      <Badge tone="ok">
                        {ru.commitments.acked} · {a.acknowledged_by}
                      </Badge>
                    ) : (
                      <Badge tone="danger">{ru.signal.statuses.new}</Badge>
                    )}
                  </td>
                  <td>
                    {canWrite && !a.acknowledged && (
                      <button type="button" className="btn btn-sm" disabled={ack.isPending} onClick={() => ack.mutate(a.id)}>
                        {ru.commitments.ack}
                      </button>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  )
}
