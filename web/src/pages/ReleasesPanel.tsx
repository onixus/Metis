import { useState, type FormEvent } from 'react'
import { ApiError, errorMessage } from '../api/client'
import {
  useCreateRelease,
  useFeatures,
  useMarkReleaseReady,
  useReleaseReadiness,
  useReleases,
  useSetReleaseEol,
  useSetReleaseFeatures,
  useSetReleaseNotes,
  useUpdateRelease,
} from '../api/hooks'
import type { CompatRow, Release, ReleaseInput } from '../api/types'
import { Badge, Empty, ErrorBox, Loading } from '../components/Status'
import { ru } from '../i18n/ru'
import { fmtDate } from '../lib/format'
import { MultiSelect } from './DiscoveryPage'

const BRANCHES: Release['branch'][] = ['evolving', 'certified']
const STATUS_TONE: Record<Release['status'], 'neutral' | 'ok' | 'warn' | 'danger' | 'info'> = {
  planned: 'info',
  ready_for_certification: 'warn',
  released: 'ok',
  eol: 'neutral',
}

/** Матрица совместимости из контрактов (RM-05); доступна и в sales-safe срезе. */
export function CompatMatrix({ rows }: { rows: CompatRow[] | undefined }) {
  if (!rows || rows.length === 0) return <Empty text={ru.release.compatEmpty} />
  return (
    <div className="table-wrap">
      <table className="table table-compact">
        <thead>
          <tr>
            <th>{ru.release.contract}</th>
            <th>{ru.release.providerVersion}</th>
            <th>{ru.release.consumerVersion}</th>
            <th>{ru.release.compatible}</th>
          </tr>
        </thead>
        <tbody>
          {rows.map((r, i) => (
            <tr key={`${r.contract_id}-${i}`}>
              <td>{r.contract_name}</td>
              <td className="mono">{r.provider_version}</td>
              <td className="mono">{r.consumer_version}</td>
              <td>{r.compatible ? <Badge tone="ok">{ru.app.yes}</Badge> : <Badge tone="danger">{ru.app.no}</Badge>}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}

export function ReleaseBadges({ r }: { r: Pick<Release, 'status' | 'branch' | 'eol'> }) {
  return (
    <>
      <Badge tone={STATUS_TONE[r.status]}>{ru.release.statuses[r.status]}</Badge>
      <Badge tone={r.branch === 'certified' ? 'ok' : 'neutral'}>{ru.release.branches[r.branch]}</Badge>
      {r.eol && (
        <span className="muted">
          {ru.release.eol}: {fmtDate(r.eol)}
        </span>
      )}
    </>
  )
}

export function ReleasesPanel({ productId, canWrite }: { productId: string; canWrite: boolean }) {
  const releases = useReleases(productId)
  const features = useFeatures(productId)
  const [creating, setCreating] = useState(false)

  if (releases.isPending) return <Loading />
  if (releases.isError) return <ErrorBox error={releases.error} onRetry={() => void releases.refetch()} />
  const featureOptions = (features.data ?? []).map((f) => ({ id: f.id, label: f.name }))

  return (
    <div className="stack">
      {canWrite && (
        <div className="row">
          <button type="button" className="btn btn-primary" onClick={() => setCreating((v) => !v)}>
            {ru.release.create}
          </button>
        </div>
      )}
      {creating && <ReleaseForm productId={productId} releases={releases.data} onDone={() => setCreating(false)} />}
      {releases.data.length === 0 && <Empty text={ru.release.empty} />}
      {releases.data.map((r) => (
        <ReleaseCard key={r.id} release={r} releases={releases.data} features={featureOptions} canWrite={canWrite} />
      ))}
    </div>
  )
}

function ReleaseForm({
  productId,
  releases,
  initial,
  onDone,
}: {
  productId: string
  releases: Release[]
  initial?: Release
  onDone: () => void
}) {
  const create = useCreateRelease(productId)
  const update = useUpdateRelease(productId)
  const [form, setForm] = useState<ReleaseInput>({
    name: initial?.name ?? '',
    version: initial?.version ?? '',
    planned_date: initial?.planned_date ?? undefined,
    branch: initial?.branch ?? 'evolving',
    base_release_id: initial?.base_release_id,
    eol: initial?.eol ?? undefined,
  })
  const set = <K extends keyof ReleaseInput>(k: K, v: ReleaseInput[K]) => setForm((f) => ({ ...f, [k]: v }))
  const m = initial ? update : create
  const submit = (e: FormEvent) => {
    e.preventDefault()
    const body: ReleaseInput = {
      ...form,
      planned_date: form.planned_date || undefined,
      base_release_id: form.base_release_id || undefined,
      eol: form.eol || undefined,
      status: initial?.status === 'ready_for_certification' ? undefined : initial?.status,
    }
    if (initial) update.mutate({ id: initial.id, body }, { onSuccess: onDone })
    else create.mutate(body, { onSuccess: onDone })
  }
  return (
    <form className="card form-inline stack" onSubmit={submit}>
      <h3>{initial ? ru.common.edit : ru.release.create}</h3>
      <div className="row">
        <label className="field grow">
          <span>{ru.release.name}</span>
          <input value={form.name} onChange={(e) => set('name', e.target.value)} required />
        </label>
        <label className="field">
          <span>{ru.release.version}</span>
          <input value={form.version} onChange={(e) => set('version', e.target.value)} required />
        </label>
        <label className="field">
          <span>{ru.release.plannedDate}</span>
          <input type="date" value={form.planned_date ?? ''} onChange={(e) => set('planned_date', e.target.value)} />
        </label>
        <label className="field">
          <span>{ru.release.branch}</span>
          <select value={form.branch} onChange={(e) => set('branch', e.target.value as Release['branch'])}>
            {BRANCHES.map((b) => (
              <option key={b} value={b}>
                {ru.release.branches[b]}
              </option>
            ))}
          </select>
        </label>
        <label className="field">
          <span>{ru.release.baseRelease}</span>
          <select value={form.base_release_id ?? ''} onChange={(e) => set('base_release_id', e.target.value)}>
            <option value="">{ru.release.noBase}</option>
            {releases
              .filter((r) => r.id !== initial?.id)
              .map((r) => (
                <option key={r.id} value={r.id}>
                  {r.name} {r.version}
                </option>
              ))}
          </select>
        </label>
        <label className="field">
          <span>{ru.release.eol}</span>
          <input type="date" value={form.eol ?? ''} onChange={(e) => set('eol', e.target.value)} />
        </label>
      </div>
      {form.branch === 'certified' && <p className="muted">{ru.release.certifiedHint}</p>}
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

function ReleaseCard({
  release: r,
  releases,
  features,
  canWrite,
}: {
  release: Release
  releases: Release[]
  features: { id: string; label: string }[]
  canWrite: boolean
}) {
  const setFeatures = useSetReleaseFeatures(r.product_id)
  const setNotes = useSetReleaseNotes(r.product_id)
  const setEol = useSetReleaseEol(r.product_id)
  const markReady = useMarkReleaseReady(r.product_id)
  const [editing, setEditing] = useState(false)
  const [ids, setIds] = useState<string[]>(r.feature_ids ?? [])
  const [notes, setNotesText] = useState(r.release_notes ?? '')
  const [eol, setEolText] = useState(r.eol ?? '')
  const [showReadiness, setShowReadiness] = useState(false)
  const readiness = useReleaseReadiness(r.id, showReadiness)
  const base = releases.find((x) => x.id === r.base_release_id)
  const featureLabel = new Map(features.map((f) => [f.id, f.label]))
  const conflict = markReady.error instanceof ApiError && markReady.error.status === 409

  return (
    <div className="card stack">
      <div className="row wrap-row">
        <h2>
          {r.name} <span className="mono muted">{r.version}</span>
        </h2>
        <ReleaseBadges r={r} />
        <span className="muted">{fmtDate(r.planned_date)}</span>
        {base && (
          <span className="muted">
            {ru.release.baseRelease}: {base.name} {base.version}
          </span>
        )}
        {canWrite && (
          <button type="button" className="btn btn-sm" onClick={() => setEditing((v) => !v)}>
            {ru.common.edit}
          </button>
        )}
      </div>
      {editing && <ReleaseForm productId={r.product_id} releases={releases} initial={r} onDone={() => setEditing(false)} />}
      {r.branch === 'certified' && <p className="muted">{ru.release.certifiedHint}</p>}

      <h3>{ru.release.features}</h3>
      {canWrite ? (
        <div className="stack">
          <MultiSelect label={ru.release.features} options={features} value={ids} onChange={setIds} />
          <div className="row">
            <button type="button" className="btn btn-sm" disabled={setFeatures.isPending} onClick={() => setFeatures.mutate({ id: r.id, feature_ids: ids })}>
              {ru.release.saveFeatures}
            </button>
            {setFeatures.isError && <span className="alert alert-error">{errorMessage(setFeatures.error)}</span>}
          </div>
        </div>
      ) : (
        <div>{(r.feature_ids ?? []).map((id) => featureLabel.get(id) ?? id).join(', ') || ru.app.dash}</div>
      )}

      <h3>{ru.release.notes}</h3>
      {canWrite ? (
        <div className="stack">
          <textarea rows={3} value={notes} onChange={(e) => setNotesText(e.target.value)} />
          <div className="row">
            <button type="button" className="btn btn-sm" disabled={setNotes.isPending} onClick={() => setNotes.mutate({ id: r.id, release_notes: notes })}>
              {ru.release.saveNotes}
            </button>
            {setNotes.isError && <span className="alert alert-error">{errorMessage(setNotes.error)}</span>}
          </div>
        </div>
      ) : (
        <p className="wrap">{r.release_notes || ru.app.dash}</p>
      )}

      <h3>{ru.release.compat}</h3>
      <CompatMatrix rows={r.compatibility_matrix} />

      {canWrite && (
        <div className="row">
          <label className="field">
            <span>{ru.release.eol}</span>
            <input type="date" value={eol} onChange={(e) => setEolText(e.target.value)} />
          </label>
          <button type="button" className="btn btn-sm" disabled={!eol || setEol.isPending} onClick={() => setEol.mutate({ id: r.id, eol })}>
            {ru.release.setEol}
          </button>
          {r.status === 'planned' && (
            <button
              type="button"
              className="btn btn-sm btn-primary"
              disabled={markReady.isPending}
              onClick={() => markReady.mutate(r.id, { onError: () => setShowReadiness(true) })}
            >
              {ru.release.markReady}
            </button>
          )}
          <button type="button" className="btn btn-sm" onClick={() => setShowReadiness((v) => !v)}>
            {ru.release.checkReadiness}
          </button>
        </div>
      )}
      {setEol.isError && <div className="alert alert-error">{errorMessage(setEol.error)}</div>}
      {markReady.isError && !conflict && <div className="alert alert-error">{errorMessage(markReady.error)}</div>}
      {markReady.isSuccess && <div className="alert alert-ok">{ru.release.ready}</div>}
      {showReadiness && (
        <div className="stack">
          {readiness.isPending && <Loading />}
          {readiness.isError && <ErrorBox error={readiness.error} />}
          {readiness.data && (
            <div className={`alert ${readiness.data.ready ? 'alert-ok' : 'alert-warn'}`}>
              <div>
                <strong>{readiness.data.ready ? ru.release.ready : ru.release.notReady}</strong>
                {readiness.data.open_items.length > 0 && (
                  <>
                    <div>{ru.release.openItems}:</div>
                    <ul>
                      {readiness.data.open_items.map((it) => (
                        <li key={it}>{it}</li>
                      ))}
                    </ul>
                  </>
                )}
              </div>
            </div>
          )}
        </div>
      )}
    </div>
  )
}
