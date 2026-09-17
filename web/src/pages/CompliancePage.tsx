import { useMemo, useState, type FormEvent } from 'react'
import { Link, useParams } from 'react-router-dom'
import { ApiError, errorMessage } from '../api/client'
import {
  accessLevel,
  useAppendTrackEvidence,
  useBaselines,
  useCheckGateItem,
  useFailGate,
  useMe,
  usePassGate,
  useProduct,
  useProducts,
  useReleases,
  useSetEvidenceItemStatus,
  useStartTrack,
  useTrackEvidence,
  useTrackTemplates,
  useTracks,
  useTracksOfProducts,
  useUpdateGate,
  useVerifyEvidenceLog,
} from '../api/hooks'
import type { EvidenceItem, Gate, ProductType, Track } from '../api/types'
import { Badge, Empty, ErrorBox, Loading } from '../components/Status'
import { ru } from '../i18n/ru'
import { fmtDate, fmtDateTime, fmtMoney, formatMoneyInput, MONEY_INPUT_STEP, parseMoneyInput } from '../lib/format'
import { canSeeCompliance, canWriteCompliance, hasRole } from '../lib/roles'

const GATE_TONE: Record<Gate['status'], 'neutral' | 'ok' | 'warn' | 'danger' | 'info'> = {
  pending: 'neutral',
  in_progress: 'info',
  passed: 'ok',
  failed: 'danger',
}
const TRACK_TONE: Record<Track['status'], 'neutral' | 'ok' | 'warn' | 'danger' | 'info'> = { active: 'info', certified: 'ok', failed: 'danger' }
const EV_TONE: Record<EvidenceItem['status'], 'neutral' | 'ok' | 'warn' | 'danger' | 'info'> = { submitted: 'warn', accepted: 'ok', rejected: 'danger' }

// ---- Страница продукта -----------------------------------------------------

export function CompliancePage() {
  const { id = '' } = useParams()
  const me = useMe()
  const product = useProduct(id)
  const tracks = useTracks(id)
  const baselines = useBaselines(id)
  const level = accessLevel(me.data, id)
  const canWrite = canWriteCompliance(me.data, level)
  const [starting, setStarting] = useState(false)

  if (me.isPending || product.isPending || tracks.isPending) return <Loading />
  if (product.isError) return <ErrorBox error={product.error} onRetry={() => void product.refetch()} />
  if (tracks.isError) return <ErrorBox error={tracks.error} onRetry={() => void tracks.refetch()} />

  return (
    <section className="stack">
      <div className="page-head">
        <div>
          <h1>
            {ru.compliance.title}: {product.data.name}
          </h1>
          <Link to={`/products/${id}`}>{ru.product.open}</Link>
        </div>
        {canWrite && (
          <button type="button" className="btn btn-primary" onClick={() => setStarting((v) => !v)}>
            {ru.compliance.startTrack}
          </button>
        )}
      </div>
      {!canWrite && <p className="muted">{ru.common.forbiddenWrite}</p>}
      {starting && <StartTrackForm productId={id} productType={product.data.type} onDone={() => setStarting(false)} />}
      <h2>{ru.compliance.tracks}</h2>
      {tracks.data.length === 0 && <Empty text={ru.compliance.tracksEmpty} />}
      {tracks.data.map((t) => (
        <TrackCard key={t.id} track={t} canWrite={canWrite} />
      ))}
      <div className="card stack">
        <h2>{ru.compliance.baselines}</h2>
        {baselines.isPending && <Loading />}
        {baselines.isError && <ErrorBox error={baselines.error} />}
        {baselines.data && baselines.data.length === 0 && <Empty text={ru.compliance.baselinesEmpty} />}
        {baselines.data && baselines.data.length > 0 && (
          <div className="table-wrap">
            <table className="table">
              <thead>
                <tr>
                  <th>{ru.common.version}</th>
                  <th>{ru.compliance.certificate}</th>
                  <th>{ru.compliance.certifiedAt}</th>
                  <th>{ru.compliance.eol}</th>
                  <th>{ru.compliance.track}</th>
                </tr>
              </thead>
              <tbody>
                {baselines.data.map((b) => (
                  <tr key={b.id}>
                    <td className="mono">{b.version}</td>
                    <td>{b.certificate_no}</td>
                    <td>{fmtDate(b.certified_at)}</td>
                    <td>{fmtDate(b.eol)}</td>
                    <td className="mono">{b.track_id ?? ru.app.dash}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </div>
    </section>
  )
}

function StartTrackForm({ productId, productType, onDone }: { productId: string; productType: ProductType; onDone: () => void }) {
  const releases = useReleases(productId)
  const templates = useTrackTemplates(productType)
  const start = useStartTrack(productId)
  const [releaseId, setReleaseId] = useState('')
  const [version, setVersion] = useState('')
  const [templateId, setTemplateId] = useState('')

  const submit = (e: FormEvent) => {
    e.preventDefault()
    if (!releaseId) return
    start.mutate({ release_id: releaseId, version: version.trim(), template_id: templateId || undefined }, { onSuccess: onDone })
  }
  return (
    <form className="card form-inline stack" onSubmit={submit}>
      <h3>{ru.compliance.startTrack}</h3>
      <div className="row">
        <label className="field">
          <span>{ru.common.release}</span>
          <select
            value={releaseId}
            onChange={(e) => {
              setReleaseId(e.target.value)
              const r = (releases.data ?? []).find((x) => x.id === e.target.value)
              if (r && !version) setVersion(r.version)
            }}
            required
          >
            <option value="">{ru.app.dash}</option>
            {(releases.data ?? []).map((r) => (
              <option key={r.id} value={r.id}>
                {r.name} {r.version}
              </option>
            ))}
          </select>
        </label>
        <label className="field">
          <span>{ru.common.version}</span>
          <input value={version} onChange={(e) => setVersion(e.target.value)} required />
        </label>
        <label className="field grow">
          <span>{ru.compliance.template}</span>
          <select value={templateId} onChange={(e) => setTemplateId(e.target.value)}>
            <option value="">{ru.compliance.templateDefault}</option>
            {(templates.data ?? []).map((t) => (
              <option key={t.id} value={t.id}>
                {t.name}
              </option>
            ))}
          </select>
        </label>
      </div>
      {start.isError && <div className="alert alert-error">{errorMessage(start.error)}</div>}
      <div className="row">
        <button type="submit" className="btn btn-primary" disabled={start.isPending}>
          {ru.compliance.startTrack}
        </button>
        <button type="button" className="btn" onClick={onDone}>
          {ru.app.cancel}
        </button>
      </div>
    </form>
  )
}

export function TrackCard({ track, canWrite, compact = false }: { track: Track; canWrite: boolean; compact?: boolean }) {
  const [open, setOpen] = useState(!compact)
  const evidence = useTrackEvidence(track.id, open)
  const gates = useMemo(() => [...track.gates].sort((a, b) => a.order - b.order || a.key.localeCompare(b.key)), [track.gates])
  const passed = gates.filter((g) => g.status === 'passed').length

  return (
    <div className="card stack">
      <div className="row wrap-row">
        <h2>
          {ru.compliance.track} <span className="mono">{track.version}</span>
        </h2>
        <Badge tone={TRACK_TONE[track.status]}>{ru.compliance.trackStatuses[track.status]}</Badge>
        <span className="muted">
          {ru.compliance.gates}: {passed}/{gates.length}
        </span>
        {track.baseline_id && <Badge tone="ok">{ru.compliance.baselines}</Badge>}
        {compact && (
          <button type="button" className="btn btn-sm" onClick={() => setOpen((v) => !v)}>
            {open ? ru.productLinks.hide : ru.productLinks.details}
          </button>
        )}
      </div>
      {open && (
        <>
          <div className="stack">
            {gates.map((g) => (
              <GateRow key={g.id} track={track} gate={g} evidence={evidence.data ?? []} canWrite={canWrite} />
            ))}
          </div>
          <EvidenceLog track={track} items={evidence.data} loading={evidence.isPending} error={evidence.error} canWrite={canWrite} />
        </>
      )}
    </div>
  )
}

function GateRow({ track, gate, evidence, canWrite }: { track: Track; gate: Gate; evidence: EvidenceItem[]; canWrite: boolean }) {
  const pass = usePassGate(track.product_id)
  const fail = useFailGate(track.product_id)
  const check = useCheckGateItem(track.product_id)
  const update = useUpdateGate(track.product_id)
  const [mode, setMode] = useState<'none' | 'fail' | 'edit'>('none')
  const [reason, setReason] = useState('')
  const [owner, setOwner] = useState(gate.owner ?? '')
  const [due, setDue] = useState(gate.due_date ?? '')
  const [amount, setAmount] = useState(formatMoneyInput(gate.cost.amount))
  const [choice, setChoice] = useState<Record<string, string>>({})
  const gateEvidence = evidence.filter((e) => e.gate_id === gate.id && e.status !== 'rejected')
  const err = pass.error ?? fail.error ?? check.error ?? update.error
  const editable = canWrite && track.status === 'active' && gate.status !== 'passed'

  return (
    <div className="item">
      <div className="row wrap-row">
        <strong>
          {gate.order}. {gate.name}
        </strong>
        <span className="mono muted">{gate.key}</span>
        <Badge tone="neutral">{ru.compliance.gateKinds[gate.kind]}</Badge>
        {gate.parallel_group && (
          <Badge tone="info">
            {ru.compliance.parallel}: {gate.parallel_group}
          </Badge>
        )}
        <Badge tone={GATE_TONE[gate.status]}>{ru.compliance.gateStatuses[gate.status]}</Badge>
        {gate.requirement_set_code && (
          <span className="muted">
            {ru.compliance.requirementSet}: <span className="mono">{gate.requirement_set_code}</span>
          </span>
        )}
      </div>
      <div className="muted">
        {ru.common.owner}: {gate.owner || ru.app.dash} · {ru.common.dueDate}: {fmtDate(gate.due_date)} · {ru.common.cost}: {fmtMoney(gate.cost)}
        {gate.passed_at && ` · ${ru.compliance.gateStatuses.passed}: ${fmtDateTime(gate.passed_at)}`}
      </div>
      {gate.checklist.length > 0 && (
        <table className="table table-compact">
          <tbody>
            {gate.checklist.map((item) => (
              <tr key={item.key}>
                <td style={{ width: 24 }}>
                  <input type="checkbox" checked={item.done} readOnly aria-label={item.text} />
                </td>
                <td className="wrap">
                  {item.text} <span className="mono muted">{item.key}</span>
                  {item.evidence_id && (
                    <div className="muted mono">
                      {ru.compliance.evidenceFor}: {item.evidence_id}
                    </div>
                  )}
                </td>
                <td>
                  {editable && !item.done && (
                    <div className="row">
                      <select value={choice[item.key] ?? ''} onChange={(e) => setChoice((c) => ({ ...c, [item.key]: e.target.value }))}>
                        <option value="">{ru.compliance.chooseEvidence}</option>
                        {gateEvidence.map((e) => (
                          <option key={e.id} value={e.id}>
                            #{e.seq} {e.url}
                          </option>
                        ))}
                      </select>
                      <button
                        type="button"
                        className="btn btn-sm"
                        disabled={!choice[item.key] || check.isPending}
                        onClick={() => check.mutate({ trackId: track.id, gateId: gate.id, key: item.key, evidence_id: choice[item.key] })}
                      >
                        {ru.compliance.check}
                      </button>
                    </div>
                  )}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
      {editable && (
        <div className="row">
          <button type="button" className="btn btn-sm btn-primary" disabled={pass.isPending} onClick={() => pass.mutate({ trackId: track.id, gateId: gate.id })}>
            {ru.compliance.pass}
          </button>
          <button type="button" className="btn btn-sm btn-danger" onClick={() => setMode(mode === 'fail' ? 'none' : 'fail')}>
            {ru.compliance.fail}
          </button>
          <button type="button" className="btn btn-sm" onClick={() => setMode(mode === 'edit' ? 'none' : 'edit')}>
            {ru.compliance.editGate}
          </button>
        </div>
      )}
      {mode === 'fail' && (
        <form
          className="row"
          onSubmit={(e) => {
            e.preventDefault()
            if (!reason.trim()) return
            fail.mutate({ trackId: track.id, gateId: gate.id, reason: reason.trim() }, { onSuccess: () => setMode('none') })
          }}
        >
          <label className="field grow">
            <span>{ru.compliance.failReason}</span>
            <input value={reason} onChange={(e) => setReason(e.target.value)} required />
          </label>
          <button type="submit" className="btn btn-sm btn-danger" disabled={fail.isPending}>
            {ru.compliance.fail}
          </button>
        </form>
      )}
      {mode === 'edit' && (
        <form
          className="row"
          onSubmit={(e) => {
            e.preventDefault()
            update.mutate(
              {
                trackId: track.id,
                gateId: gate.id,
                body: {
                  owner: owner.trim() || undefined,
                  due_date: due || undefined,
                  cost: { amount: parseMoneyInput(amount) ?? gate.cost.amount, currency: gate.cost.currency || 'RUB' },
                },
              },
              { onSuccess: () => setMode('none') },
            )
          }}
        >
          <label className="field">
            <span>{ru.common.owner}</span>
            <input value={owner} onChange={(e) => setOwner(e.target.value)} />
          </label>
          <label className="field">
            <span>{ru.common.dueDate}</span>
            <input type="date" value={due} onChange={(e) => setDue(e.target.value)} />
          </label>
          <label className="field">
            <span>
              {ru.common.cost}, {gate.cost.currency || 'RUB'}
            </span>
            <input type="number" min={0} step={MONEY_INPUT_STEP} value={amount} onChange={(e) => setAmount(e.target.value)} />
          </label>
          <button type="submit" className="btn btn-sm btn-primary" disabled={update.isPending}>
            {ru.app.save}
          </button>
        </form>
      )}
      {err && <div className="alert alert-error">{errorMessage(err)}</div>}
    </div>
  )
}

function EvidenceLog({
  track,
  items,
  loading,
  error,
  canWrite,
}: {
  track: Track
  items: EvidenceItem[] | undefined
  loading: boolean
  error: unknown
  canWrite: boolean
}) {
  const append = useAppendTrackEvidence(track.id)
  const setStatus = useSetEvidenceItemStatus(track.id)
  const [adding, setAdding] = useState(false)
  const [gateId, setGateId] = useState(track.gates[0]?.id ?? '')
  const [url, setUrl] = useState('')
  const [sha, setSha] = useState('')
  const [comment, setComment] = useState('')
  const gateName = new Map(track.gates.map((g) => [g.id, g.name]))

  const submit = (e: FormEvent) => {
    e.preventDefault()
    append.mutate(
      { gate_id: gateId, url: url.trim(), sha256: sha.trim().toLowerCase(), comment: comment.trim() || undefined },
      {
        onSuccess: () => {
          setUrl('')
          setSha('')
          setComment('')
          setAdding(false)
        },
      },
    )
  }

  return (
    <div className="stack">
      <div className="row wrap-row">
        <h3>{ru.compliance.evidence}</h3>
        {canWrite && track.status === 'active' && (
          <button type="button" className="btn btn-sm" onClick={() => setAdding((v) => !v)}>
            {ru.compliance.addEvidence}
          </button>
        )}
      </div>
      {adding && (
        <form className="card form-inline stack" onSubmit={submit}>
          <div className="row">
            <label className="field">
              <span>{ru.compliance.gate}</span>
              <select value={gateId} onChange={(e) => setGateId(e.target.value)} required>
                {track.gates.map((g) => (
                  <option key={g.id} value={g.id}>
                    {g.name}
                  </option>
                ))}
              </select>
            </label>
            <label className="field grow">
              <span>{ru.common.url}</span>
              <input type="url" value={url} onChange={(e) => setUrl(e.target.value)} required />
            </label>
            <label className="field grow">
              <span>{ru.common.sha256}</span>
              <input className="mono" value={sha} onChange={(e) => setSha(e.target.value)} pattern="[0-9a-fA-F]{64}" required />
            </label>
            <label className="field grow">
              <span>{ru.common.comment}</span>
              <input value={comment} onChange={(e) => setComment(e.target.value)} />
            </label>
          </div>
          {append.isError && <div className="alert alert-error">{errorMessage(append.error)}</div>}
          <div className="row">
            <button type="submit" className="btn btn-primary btn-sm" disabled={append.isPending}>
              {ru.common.add}
            </button>
            <button type="button" className="btn btn-sm" onClick={() => setAdding(false)}>
              {ru.app.cancel}
            </button>
          </div>
        </form>
      )}
      {loading && <Loading />}
      {error ? <ErrorBox error={error} /> : null}
      {setStatus.isError && <ErrorBox error={setStatus.error} />}
      {items && items.length === 0 && <Empty text={ru.compliance.evidenceEmpty} />}
      {items && items.length > 0 && (
        <div className="table-wrap">
          <table className="table table-compact">
            <thead>
              <tr>
                <th>{ru.compliance.seq}</th>
                <th>{ru.compliance.gate}</th>
                <th>{ru.common.url}</th>
                <th>{ru.common.sha256}</th>
                <th>{ru.common.status}</th>
                <th>{ru.common.comment}</th>
                <th>{ru.roadmap.actor}</th>
                <th>{ru.roadmap.at}</th>
                <th />
              </tr>
            </thead>
            <tbody>
              {items.map((e) => (
                <tr key={e.seq}>
                  <td className="num">{e.seq}</td>
                  <td>{gateName.get(e.gate_id) ?? e.gate_id}</td>
                  <td className="wrap">
                    <a href={e.url} target="_blank" rel="noreferrer">
                      {e.url}
                    </a>
                  </td>
                  <td className="mono" title={e.sha256}>
                    {e.sha256.slice(0, 12)}…
                  </td>
                  <td>
                    <Badge tone={EV_TONE[e.status]}>{ru.compliance.evidenceStatuses[e.status]}</Badge>
                    {e.supersedes !== undefined && (
                      <div className="muted">
                        {ru.compliance.supersedes} #{e.supersedes}
                      </div>
                    )}
                  </td>
                  <td className="wrap">{e.comment ?? ru.app.dash}</td>
                  <td>{e.actor}</td>
                  <td>{fmtDateTime(e.at)}</td>
                  <td>
                    {canWrite && e.status === 'submitted' && (
                      <div className="row">
                        <button
                          type="button"
                          className="btn btn-sm"
                          disabled={setStatus.isPending}
                          onClick={() => setStatus.mutate({ evidenceId: e.id, status: 'accepted' })}
                        >
                          {ru.compliance.evidenceStatuses.accepted}
                        </button>
                        <button
                          type="button"
                          className="btn btn-sm btn-danger"
                          disabled={setStatus.isPending}
                          onClick={() => {
                            const c = window.prompt(ru.common.comment) ?? ''
                            setStatus.mutate({ evidenceId: e.id, status: 'rejected', comment: c || undefined })
                          }}
                        >
                          {ru.compliance.evidenceStatuses.rejected}
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
  )
}

// ---- Дашборд ---------------------------------------------------------------

export function ComplianceDashboardPage() {
  const me = useMe()
  const products = useProducts()
  const ids = useMemo(() => (products.data ?? []).map((p) => p.id), [products.data])
  const tracks = useTracksOfProducts(ids)
  const verify = useVerifyEvidenceLog()

  if (me.isPending || products.isPending) return <Loading />
  if (me.isError) return <ErrorBox error={me.error} />
  if (!canSeeCompliance(me.data)) return <p className="muted">{ru.app.forbidden}</p>
  if (products.isError) return <ErrorBox error={products.error} onRetry={() => void products.refetch()} />

  const canVerify = hasRole(me.data, 'admin', 'compliance')
  const productById = new Map(products.data.map((p) => [p.id, p]))
  const rows = tracks.flatMap((q, i) => (q.data ?? []).map((t) => ({ t, product: productById.get(ids[i]) })))
  const noAccess = tracks.filter((q) => q.isError && q.error instanceof ApiError && q.error.status === 403).length
  const active = rows.filter((r) => r.t.status === 'active').length
  const certified = rows.filter((r) => r.t.status === 'certified').length
  const failed = rows.filter((r) => r.t.status === 'failed').length

  return (
    <section className="stack">
      <div className="page-head">
        <div>
          <h1>{ru.compliance.dashboard}</h1>
          <div className="muted">{ru.compliance.dashboardHint}</div>
        </div>
      </div>
      <div className="stats">
        <div className="stat stat-neutral">
          <div className="stat-value">{active}</div>
          <div className="stat-label">{ru.compliance.trackStatuses.active}</div>
        </div>
        <div className="stat stat-ok">
          <div className="stat-value">{certified}</div>
          <div className="stat-label">{ru.compliance.trackStatuses.certified}</div>
        </div>
        <div className="stat stat-danger">
          <div className="stat-value">{failed}</div>
          <div className="stat-label">{ru.compliance.trackStatuses.failed}</div>
        </div>
      </div>
      {canVerify && (
        <div className="card stack">
          <h2>{ru.compliance.verify}</h2>
          <p className="muted">{ru.compliance.verifyHint}</p>
          <div className="row">
            <button type="button" className="btn btn-primary" disabled={verify.isPending} onClick={() => verify.mutate()}>
              {verify.isPending ? ru.admin.auditRunning : ru.compliance.verify}
            </button>
          </div>
          {verify.isError && <ErrorBox error={verify.error} />}
          {verify.data && (
            <dl className="kv">
              <dt>{ru.admin.checked}</dt>
              <dd>{verify.data.checked}</dd>
              <dt>{ru.common.status}</dt>
              <dd>{verify.data.ok ? <Badge tone="ok">{ru.admin.ok}</Badge> : <Badge tone="danger">{ru.admin.broken}</Badge>}</dd>
              {!verify.data.ok && (
                <>
                  <dt>{ru.admin.brokenSeq}</dt>
                  <dd>{verify.data.broken_seq ?? ru.app.dash}</dd>
                  <dt>{ru.admin.reason}</dt>
                  <dd>{verify.data.reason ?? ru.app.dash}</dd>
                </>
              )}
            </dl>
          )}
        </div>
      )}
      {tracks.some((q) => q.isPending) && <Loading />}
      {noAccess > 0 && <p className="muted">{ru.delivery.hiddenProducts(noAccess)}</p>}
      {rows.length === 0 && !tracks.some((q) => q.isPending) && <Empty text={ru.compliance.tracksEmpty} />}
      {rows.map(({ t, product }) => (
        <div key={t.id} className="stack">
          <div className="muted">
            {ru.common.product}:{' '}
            {product ? <Link to={`/products/${product.id}/compliance`}>{product.name}</Link> : t.product_id}
          </div>
          <TrackCard track={t} canWrite={canWriteCompliance(me.data, accessLevel(me.data, t.product_id))} compact />
        </div>
      ))}
    </section>
  )
}
