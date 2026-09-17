import { useMemo, useState, type FormEvent } from 'react'
import { Link, useParams } from 'react-router-dom'
import { errorMessage } from '../api/client'
import {
  accessLevel,
  useChangeHypothesisStatus,
  useCreateEvidence,
  useCreateHypothesis,
  useCreateInsight,
  useCreateInterview,
  useCustomFields,
  useCustomStatuses,
  useEvidence,
  useFeatures,
  useHypotheses,
  useInsights,
  useInterviews,
  useMe,
  useProduct,
  useUpdateEvidence,
} from '../api/hooks'
import type { CustomFieldDef, Evidence, EvidenceInput, Hypothesis, Interview } from '../api/types'
import { Badge, Empty, ErrorBox, Loading } from '../components/Status'
import { Tabs } from '../components/Tabs'
import { ru } from '../i18n/ru'
import { fmtDate, pick } from '../lib/format'
import { canWriteDiscovery } from '../lib/roles'

type Tab = 'hypotheses' | 'interviews' | 'insights' | 'evidence'
const TABS: { key: Tab; label: string }[] = (['hypotheses', 'interviews', 'insights', 'evidence'] as const).map((k) => ({ key: k, label: ru.discovery.tabs[k] }))
const BUILTIN_STATUSES = ['draft', 'testing', 'confirmed', 'rejected'] as const
const LEVELS = ['low', 'medium', 'high'] as const
const VERIFICATIONS = ['unverified', 'verified', 'rejected'] as const

const STATUS_TONE: Record<string, 'neutral' | 'ok' | 'warn' | 'danger' | 'info'> = {
  draft: 'neutral',
  testing: 'info',
  confirmed: 'ok',
  rejected: 'danger',
}

const splitLines = (s: string) => s.split('\n').map((x) => x.trim()).filter(Boolean)
const splitCsv = (s: string) => s.split(',').map((x) => x.trim()).filter(Boolean)

export function DiscoveryPage() {
  const { id = '' } = useParams()
  const me = useMe()
  const product = useProduct(id)
  const [tab, setTab] = useState<Tab>('hypotheses')
  const level = accessLevel(me.data, id)
  const canWrite = canWriteDiscovery(me.data, level)

  if (me.isPending || product.isPending) return <Loading />
  if (product.isError) return <ErrorBox error={product.error} onRetry={() => void product.refetch()} />

  return (
    <section className="stack">
      <div className="page-head">
        <div>
          <h1>
            {ru.discovery.title}: {product.data.name}
          </h1>
          <Link to={`/products/${id}`}>{ru.product.open}</Link>
        </div>
        <Tabs<Tab> tabs={TABS} value={tab} onChange={setTab} />
      </div>
      {!canWrite && <p className="muted">{ru.common.forbiddenWrite}</p>}
      {tab === 'hypotheses' && <Hypotheses productId={id} canWrite={canWrite} />}
      {tab === 'interviews' && <Interviews productId={id} canWrite={canWrite} />}
      {tab === 'insights' && <Insights productId={id} canWrite={canWrite} />}
      {tab === 'evidence' && <EvidenceTab productId={id} canWrite={canWrite} />}
    </section>
  )
}

// ---- Гипотезы --------------------------------------------------------------

function Hypotheses({ productId, canWrite }: { productId: string; canWrite: boolean }) {
  const list = useHypotheses(productId)
  const features = useFeatures(productId)
  const fields = useCustomFields('hypothesis', canWrite)
  const statuses = useCustomStatuses('hypothesis', canWrite)
  const [creating, setCreating] = useState(false)
  const [changing, setChanging] = useState<Hypothesis | null>(null)
  const featureName = useMemo(() => new Map((features.data ?? []).map((f) => [f.id, f.name])), [features.data])
  const statusOptions = useMemo(() => {
    const custom = (statuses.data ?? []).map((s) => ({ key: s.key, label: s.label }))
    return [
      ...BUILTIN_STATUSES.map((k) => ({ key: k as string, label: ru.discovery.hypothesis.statuses[k] })),
      ...custom.filter((c) => !(BUILTIN_STATUSES as readonly string[]).includes(c.key)),
    ]
  }, [statuses.data])
  const statusLabel = (key: string) => statusOptions.find((s) => s.key === key)?.label ?? key

  if (list.isPending) return <Loading />
  if (list.isError) return <ErrorBox error={list.error} onRetry={() => void list.refetch()} />

  return (
    <div className="card stack">
      <div className="row wrap-row">
        <h2>{ru.discovery.tabs.hypotheses}</h2>
        {canWrite && (
          <button type="button" className="btn btn-sm" onClick={() => setCreating((v) => !v)}>
            {ru.discovery.hypothesis.create}
          </button>
        )}
      </div>
      {creating && (
        <HypothesisForm
          productId={productId}
          fields={fields.data ?? []}
          features={(features.data ?? []).map((f) => ({ id: f.id, name: f.name }))}
          onDone={() => setCreating(false)}
        />
      )}
      {list.data.length === 0 ? (
        <Empty text={ru.discovery.hypothesis.empty} />
      ) : (
        <div className="table-wrap">
          <table className="table">
            <thead>
              <tr>
                <th>{ru.discovery.hypothesis.title}</th>
                <th>{ru.discovery.hypothesis.status}</th>
                <th>{ru.discovery.hypothesis.criterion}</th>
                <th>{ru.discovery.hypothesis.feature}</th>
                <th>{ru.discovery.hypothesis.customFields}</th>
                <th />
              </tr>
            </thead>
            <tbody>
              {list.data.map((h) => (
                <tr key={h.id}>
                  <td className="wrap">
                    <strong>{h.title}</strong>
                    <div className="muted">{h.statement}</div>
                    {(h.assumptions ?? []).length > 0 && (
                      <ul className="muted">
                        {(h.assumptions ?? []).map((a) => (
                          <li key={a}>{a}</li>
                        ))}
                      </ul>
                    )}
                  </td>
                  <td>
                    <Badge tone={STATUS_TONE[h.status] ?? 'neutral'}>{statusLabel(h.status)}</Badge>
                    {h.resolution && <div className="muted">{h.resolution}</div>}
                  </td>
                  <td className="wrap">{h.confirmation_criterion}</td>
                  <td>{h.feature_id ? (featureName.get(h.feature_id) ?? <span className="mono">{h.feature_id}</span>) : ru.app.dash}</td>
                  <td>
                    {h.custom_fields && Object.keys(h.custom_fields).length > 0 ? (
                      <dl className="kv">
                        {Object.entries(h.custom_fields).map(([k, v]) => (
                          <CustomValue key={k} k={k} v={v} defs={fields.data ?? []} />
                        ))}
                      </dl>
                    ) : (
                      ru.app.dash
                    )}
                  </td>
                  <td>
                    <div className="row">
                      <Link className="btn btn-sm" to={`/trace/hypothesis/${h.id}`}>
                        {ru.discovery.hypothesis.trace}
                      </Link>
                      {canWrite && (
                        <button type="button" className="btn btn-sm" onClick={() => setChanging(changing?.id === h.id ? null : h)}>
                          {ru.discovery.hypothesis.changeStatus}
                        </button>
                      )}
                    </div>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      {changing && (
        <StatusForm productId={productId} hypothesis={changing} options={statusOptions} onDone={() => setChanging(null)} />
      )}
    </div>
  )
}

function CustomValue({ k, v, defs }: { k: string; v: unknown; defs: CustomFieldDef[] }) {
  const def = defs.find((d) => d.key === k)
  return (
    <>
      <dt>{def?.label ?? k}</dt>
      <dd>{typeof v === 'string' || typeof v === 'number' ? String(v) : JSON.stringify(v)}</dd>
    </>
  )
}

function HypothesisForm({
  productId,
  fields,
  features,
  onDone,
}: {
  productId: string
  fields: CustomFieldDef[]
  features: { id: string; name: string }[]
  onDone: () => void
}) {
  const create = useCreateHypothesis(productId)
  const [title, setTitle] = useState('')
  const [statement, setStatement] = useState('')
  const [assumptions, setAssumptions] = useState('')
  const [criterion, setCriterion] = useState('')
  const [featureId, setFeatureId] = useState('')
  const [custom, setCustom] = useState<Record<string, string>>({})
  const [validation, setValidation] = useState<string | null>(null)

  const submit = (e: FormEvent) => {
    e.preventDefault()
    if (!title.trim()) return setValidation(ru.common.required(ru.discovery.hypothesis.title))
    if (!statement.trim()) return setValidation(ru.common.required(ru.discovery.hypothesis.statement))
    if (!criterion.trim()) return setValidation(ru.common.required(ru.discovery.hypothesis.criterion))
    for (const f of fields) {
      if (f.required && !custom[f.key]?.trim()) return setValidation(ru.common.required(f.label))
    }
    setValidation(null)
    const custom_fields: Record<string, unknown> = {}
    for (const f of fields) {
      const raw = custom[f.key]?.trim()
      if (!raw) continue
      custom_fields[f.key] = f.type === 'number' ? Number(raw) : raw
    }
    create.mutate(
      {
        title: title.trim(),
        statement: statement.trim(),
        assumptions: splitLines(assumptions),
        confirmation_criterion: criterion.trim(),
        feature_id: featureId || undefined,
        custom_fields: Object.keys(custom_fields).length ? custom_fields : undefined,
      },
      { onSuccess: onDone },
    )
  }

  return (
    <form className="card form-inline stack" onSubmit={submit}>
      <h3>{ru.discovery.hypothesis.create}</h3>
      <div className="row">
        <label className="field grow">
          <span>{ru.discovery.hypothesis.title}</span>
          <input value={title} onChange={(e) => setTitle(e.target.value)} required />
        </label>
        <label className="field">
          <span>{ru.discovery.hypothesis.feature}</span>
          <select value={featureId} onChange={(e) => setFeatureId(e.target.value)}>
            <option value="">{ru.app.dash}</option>
            {features.map((f) => (
              <option key={f.id} value={f.id}>
                {f.name}
              </option>
            ))}
          </select>
        </label>
      </div>
      <label className="field">
        <span>{ru.discovery.hypothesis.statement}</span>
        <textarea rows={2} value={statement} onChange={(e) => setStatement(e.target.value)} required />
      </label>
      <div className="row">
        <label className="field grow">
          <span>
            {ru.discovery.hypothesis.assumptions} ({ru.common.lines})
          </span>
          <textarea rows={2} value={assumptions} onChange={(e) => setAssumptions(e.target.value)} />
        </label>
        <label className="field grow">
          <span>{ru.discovery.hypothesis.criterion}</span>
          <textarea rows={2} value={criterion} onChange={(e) => setCriterion(e.target.value)} required />
        </label>
      </div>
      {fields.length > 0 && (
        <div className="row">
          {fields.map((f) => (
            <label key={f.id} className="field">
              <span>
                {f.label}
                {f.required ? ' *' : ''}
              </span>
              {f.type === 'enum' ? (
                <select value={custom[f.key] ?? ''} onChange={(e) => setCustom((c) => ({ ...c, [f.key]: e.target.value }))}>
                  <option value="">{ru.app.dash}</option>
                  {(f.options ?? []).map((o) => (
                    <option key={o} value={o}>
                      {o}
                    </option>
                  ))}
                </select>
              ) : (
                <input
                  type={f.type === 'number' ? 'number' : f.type === 'date' ? 'date' : 'text'}
                  value={custom[f.key] ?? ''}
                  onChange={(e) => setCustom((c) => ({ ...c, [f.key]: e.target.value }))}
                />
              )}
            </label>
          ))}
        </div>
      )}
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

function StatusForm({
  productId,
  hypothesis,
  options,
  onDone,
}: {
  productId: string
  hypothesis: Hypothesis
  options: { key: string; label: string }[]
  onDone: () => void
}) {
  const change = useChangeHypothesisStatus(productId)
  const [status, setStatus] = useState(hypothesis.status)
  const [resolution, setResolution] = useState('')
  const [validation, setValidation] = useState<string | null>(null)

  const submit = (e: FormEvent) => {
    e.preventDefault()
    if (!resolution.trim()) return setValidation(ru.feature.reasonRequired)
    setValidation(null)
    change.mutate({ id: hypothesis.id, status, resolution: resolution.trim() }, { onSuccess: onDone })
  }

  return (
    <form className="card form-inline stack" onSubmit={submit}>
      <h3>
        {ru.discovery.hypothesis.changeStatus}: {hypothesis.title}
      </h3>
      <div className="row">
        <label className="field">
          <span>{ru.discovery.hypothesis.newStatus}</span>
          <select value={status} onChange={(e) => setStatus(e.target.value)}>
            {options.map((o) => (
              <option key={o.key} value={o.key}>
                {o.label}
              </option>
            ))}
          </select>
        </label>
        <label className="field grow">
          <span>{ru.discovery.hypothesis.resolution}</span>
          <input value={resolution} onChange={(e) => setResolution(e.target.value)} required />
        </label>
      </div>
      {validation && <div className="alert alert-error">{validation}</div>}
      {change.isError && <div className="alert alert-error">{errorMessage(change.error)}</div>}
      <div className="row">
        <button type="submit" className="btn btn-primary" disabled={change.isPending}>
          {ru.app.save}
        </button>
        <button type="button" className="btn" onClick={onDone}>
          {ru.app.cancel}
        </button>
      </div>
    </form>
  )
}

// ---- Интервью --------------------------------------------------------------

function Interviews({ productId, canWrite }: { productId: string; canWrite: boolean }) {
  const list = useInterviews(productId)
  const hyps = useHypotheses(productId)
  const create = useCreateInterview(productId)
  const [creating, setCreating] = useState(false)
  const [account, setAccount] = useState('')
  const [segment, setSegment] = useState('')
  const [date, setDate] = useState('')
  const [participants, setParticipants] = useState('')
  const [notes, setNotes] = useState('')
  const [hypIds, setHypIds] = useState<string[]>([])
  const hypTitle = useMemo(() => new Map((hyps.data ?? []).map((h) => [h.id, h.title])), [hyps.data])

  if (list.isPending) return <Loading />
  if (list.isError) return <ErrorBox error={list.error} onRetry={() => void list.refetch()} />

  const submit = (e: FormEvent) => {
    e.preventDefault()
    if (!date) return
    create.mutate(
      {
        account_id: account.trim() || undefined,
        segment: segment.trim() || undefined,
        date,
        participants: splitCsv(participants),
        notes: notes.trim() || undefined,
        hypothesis_ids: hypIds,
      },
      { onSuccess: () => setCreating(false) },
    )
  }

  return (
    <div className="card stack">
      <div className="row wrap-row">
        <h2>{ru.discovery.tabs.interviews}</h2>
        {canWrite && (
          <button type="button" className="btn btn-sm" onClick={() => setCreating((v) => !v)}>
            {ru.discovery.interview.create}
          </button>
        )}
      </div>
      {creating && (
        <form className="card form-inline stack" onSubmit={submit}>
          <div className="row">
            <label className="field">
              <span>{ru.discovery.interview.date}</span>
              <input type="date" value={date} onChange={(e) => setDate(e.target.value)} required />
            </label>
            <label className="field">
              <span>{ru.discovery.interview.account}</span>
              <input value={account} onChange={(e) => setAccount(e.target.value)} />
            </label>
            <label className="field">
              <span>{ru.discovery.interview.segment}</span>
              <input value={segment} onChange={(e) => setSegment(e.target.value)} />
            </label>
            <label className="field grow">
              <span>{ru.discovery.interview.participants}</span>
              <input value={participants} onChange={(e) => setParticipants(e.target.value)} />
            </label>
          </div>
          <label className="field">
            <span>{ru.discovery.interview.notes}</span>
            <textarea rows={2} value={notes} onChange={(e) => setNotes(e.target.value)} />
          </label>
          <MultiSelect
            label={ru.discovery.interview.hypotheses}
            options={(hyps.data ?? []).map((h) => ({ id: h.id, label: h.title }))}
            value={hypIds}
            onChange={setHypIds}
          />
          {create.isError && <div className="alert alert-error">{errorMessage(create.error)}</div>}
          <div className="row">
            <button type="submit" className="btn btn-primary" disabled={create.isPending}>
              {ru.common.create}
            </button>
            <button type="button" className="btn" onClick={() => setCreating(false)}>
              {ru.app.cancel}
            </button>
          </div>
        </form>
      )}
      {list.data.length === 0 ? (
        <Empty text={ru.discovery.interview.empty} />
      ) : (
        <div className="table-wrap">
          <table className="table">
            <thead>
              <tr>
                <th>{ru.discovery.interview.date}</th>
                <th>{ru.discovery.interview.account}</th>
                <th>{ru.discovery.interview.segment}</th>
                <th>{ru.discovery.interview.participants}</th>
                <th>{ru.discovery.interview.hypotheses}</th>
                <th>{ru.discovery.interview.notes}</th>
              </tr>
            </thead>
            <tbody>
              {list.data.map((i: Interview) => (
                <tr key={i.id}>
                  <td>{fmtDate(i.date)}</td>
                  <td>{i.account_id ?? ru.app.dash}</td>
                  <td>{i.segment ?? ru.app.dash}</td>
                  <td>{(i.participants ?? []).join(', ') || ru.app.dash}</td>
                  <td>{(i.hypothesis_ids ?? []).map((h) => hypTitle.get(h) ?? h).join(', ') || ru.app.dash}</td>
                  <td className="wrap">{i.notes ?? ru.app.dash}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  )
}

/** Мультивыбор чекбоксами: без новых зависимостей. */
export function MultiSelect({
  label,
  options,
  value,
  onChange,
}: {
  label: string
  options: { id: string; label: string }[]
  value: string[]
  onChange: (v: string[]) => void
}) {
  return (
    <fieldset className="filter-group">
      <legend>{label}</legend>
      {options.length === 0 && <span className="muted">{ru.app.empty}</span>}
      {options.map((o) => (
        <label key={o.id} className="check">
          <input
            type="checkbox"
            checked={value.includes(o.id)}
            onChange={(e) => onChange(e.target.checked ? [...value, o.id] : value.filter((x) => x !== o.id))}
          />
          {o.label}
        </label>
      ))}
    </fieldset>
  )
}

// ---- Инсайты ---------------------------------------------------------------

function Insights({ productId, canWrite }: { productId: string; canWrite: boolean }) {
  const list = useInsights(productId)
  const hyps = useHypotheses(productId)
  const interviews = useInterviews(productId)
  const create = useCreateInsight(productId)
  const [creating, setCreating] = useState(false)
  const [text, setText] = useState('')
  const [interviewId, setInterviewId] = useState('')
  const [confidence, setConfidence] = useState<'low' | 'medium' | 'high'>('medium')
  const [hypIds, setHypIds] = useState<string[]>([])
  const [signalIds, setSignalIds] = useState('')
  const hypTitle = useMemo(() => new Map((hyps.data ?? []).map((h) => [h.id, h.title])), [hyps.data])

  if (list.isPending) return <Loading />
  if (list.isError) return <ErrorBox error={list.error} onRetry={() => void list.refetch()} />

  const submit = (e: FormEvent) => {
    e.preventDefault()
    if (!text.trim()) return
    create.mutate(
      {
        text: text.trim(),
        interview_id: interviewId || undefined,
        confidence,
        hypothesis_ids: hypIds,
        signal_ids: splitCsv(signalIds),
      },
      { onSuccess: () => setCreating(false) },
    )
  }

  return (
    <div className="card stack">
      <div className="row wrap-row">
        <h2>{ru.discovery.tabs.insights}</h2>
        {canWrite && (
          <button type="button" className="btn btn-sm" onClick={() => setCreating((v) => !v)}>
            {ru.discovery.insight.create}
          </button>
        )}
      </div>
      {creating && (
        <form className="card form-inline stack" onSubmit={submit}>
          <label className="field">
            <span>{ru.discovery.insight.text}</span>
            <textarea rows={2} value={text} onChange={(e) => setText(e.target.value)} required />
          </label>
          <div className="row">
            <label className="field">
              <span>{ru.discovery.insight.confidence}</span>
              <select value={confidence} onChange={(e) => setConfidence(e.target.value as 'low' | 'medium' | 'high')}>
                {LEVELS.map((l) => (
                  <option key={l} value={l}>
                    {ru.discovery.insight.levels[l]}
                  </option>
                ))}
              </select>
            </label>
            <label className="field">
              <span>{ru.discovery.insight.interview}</span>
              <select value={interviewId} onChange={(e) => setInterviewId(e.target.value)}>
                <option value="">{ru.app.dash}</option>
                {(interviews.data ?? []).map((i) => (
                  <option key={i.id} value={i.id}>
                    {fmtDate(i.date)} {i.account_id ?? ''}
                  </option>
                ))}
              </select>
            </label>
            <label className="field grow">
              <span>{ru.discovery.insight.signals}</span>
              <input value={signalIds} onChange={(e) => setSignalIds(e.target.value)} className="mono" />
            </label>
          </div>
          <MultiSelect
            label={ru.discovery.insight.hypotheses}
            options={(hyps.data ?? []).map((h) => ({ id: h.id, label: h.title }))}
            value={hypIds}
            onChange={setHypIds}
          />
          {create.isError && <div className="alert alert-error">{errorMessage(create.error)}</div>}
          <div className="row">
            <button type="submit" className="btn btn-primary" disabled={create.isPending}>
              {ru.common.create}
            </button>
            <button type="button" className="btn" onClick={() => setCreating(false)}>
              {ru.app.cancel}
            </button>
          </div>
        </form>
      )}
      {list.data.length === 0 ? (
        <Empty text={ru.discovery.insight.empty} />
      ) : (
        <div className="table-wrap">
          <table className="table">
            <thead>
              <tr>
                <th>{ru.discovery.insight.text}</th>
                <th>{ru.discovery.insight.confidence}</th>
                <th>{ru.discovery.insight.hypotheses}</th>
                <th>{ru.signal.text}</th>
                <th />
              </tr>
            </thead>
            <tbody>
              {list.data.map((i) => (
                <tr key={i.id}>
                  <td className="wrap">{i.text}</td>
                  <td>{ru.discovery.insight.levels[i.confidence]}</td>
                  <td>{(i.hypothesis_ids ?? []).map((h) => hypTitle.get(h) ?? h).join(', ') || ru.app.dash}</td>
                  <td className="mono">{(i.signal_ids ?? []).length || ru.app.dash}</td>
                  <td>
                    <Link className="btn btn-sm" to={`/trace/insight/${i.id}`}>
                      {ru.discovery.hypothesis.trace}
                    </Link>
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

// ---- Evidence --------------------------------------------------------------

function EvidenceTab({ productId, canWrite }: { productId: string; canWrite: boolean }) {
  const list = useEvidence(productId)
  const hyps = useHypotheses(productId)
  const insights = useInsights(productId)
  const features = useFeatures(productId)
  const create = useCreateEvidence(productId)
  const update = useUpdateEvidence(productId)
  const [creating, setCreating] = useState(false)
  const [form, setForm] = useState<EvidenceInput>({ source: '', date: '', trust: 'medium', verification: 'unverified' })
  const hypTitle = useMemo(() => new Map((hyps.data ?? []).map((h) => [h.id, h.title])), [hyps.data])

  if (list.isPending) return <Loading />
  if (list.isError) return <ErrorBox error={list.error} onRetry={() => void list.refetch()} />

  const set = <K extends keyof EvidenceInput>(k: K, v: EvidenceInput[K]) => setForm((f) => ({ ...f, [k]: v }))
  const submit = (e: FormEvent) => {
    e.preventDefault()
    if (!form.source.trim() || !form.date) return
    create.mutate(
      {
        ...form,
        source: form.source.trim(),
        source_ref: form.source_ref?.trim() || undefined,
        sha256: form.sha256?.trim() || undefined,
        hypothesis_id: form.hypothesis_id || undefined,
        insight_id: form.insight_id || undefined,
        feature_id: form.feature_id || undefined,
      },
      { onSuccess: () => setCreating(false) },
    )
  }
  const setVerification = (ev: Evidence, verification: EvidenceInput['verification']) => {
    const { id: _id, product_id: _p, created_by: _c, created_at: _ca, updated_at: _ua, ...body } = ev
    update.mutate({ id: ev.id, body: { ...body, verification } })
  }

  return (
    <div className="card stack">
      <div className="row wrap-row">
        <h2>{ru.discovery.tabs.evidence}</h2>
        {canWrite && (
          <button type="button" className="btn btn-sm" onClick={() => setCreating((v) => !v)}>
            {ru.discovery.evidence.create}
          </button>
        )}
      </div>
      {creating && (
        <form className="card form-inline stack" onSubmit={submit}>
          <div className="row">
            <label className="field grow">
              <span>{ru.discovery.evidence.source}</span>
              <input value={form.source} onChange={(e) => set('source', e.target.value)} required />
            </label>
            <label className="field grow">
              <span>{ru.discovery.evidence.sourceRef}</span>
              <input value={form.source_ref ?? ''} onChange={(e) => set('source_ref', e.target.value)} />
            </label>
            <label className="field">
              <span>{ru.discovery.evidence.date}</span>
              <input type="date" value={form.date} onChange={(e) => set('date', e.target.value)} required />
            </label>
            <label className="field">
              <span>{ru.discovery.evidence.trust}</span>
              <select value={form.trust} onChange={(e) => set('trust', e.target.value as EvidenceInput['trust'])}>
                {LEVELS.map((l) => (
                  <option key={l} value={l}>
                    {ru.discovery.insight.levels[l]}
                  </option>
                ))}
              </select>
            </label>
          </div>
          <div className="row">
            <label className="field grow">
              <span>{ru.discovery.evidence.sha256}</span>
              <input className="mono" value={form.sha256 ?? ''} onChange={(e) => set('sha256', e.target.value)} pattern="[0-9a-fA-F]{64}" />
            </label>
            <label className="field">
              <span>{ru.discovery.evidence.hypothesis}</span>
              <select value={form.hypothesis_id ?? ''} onChange={(e) => set('hypothesis_id', e.target.value)}>
                <option value="">{ru.app.dash}</option>
                {(hyps.data ?? []).map((h) => (
                  <option key={h.id} value={h.id}>
                    {h.title}
                  </option>
                ))}
              </select>
            </label>
            <label className="field">
              <span>{ru.discovery.evidence.insight}</span>
              <select value={form.insight_id ?? ''} onChange={(e) => set('insight_id', e.target.value)}>
                <option value="">{ru.app.dash}</option>
                {(insights.data ?? []).map((i) => (
                  <option key={i.id} value={i.id}>
                    {i.text.slice(0, 60)}
                  </option>
                ))}
              </select>
            </label>
            <label className="field">
              <span>{ru.discovery.evidence.feature}</span>
              <select value={form.feature_id ?? ''} onChange={(e) => set('feature_id', e.target.value)}>
                <option value="">{ru.app.dash}</option>
                {(features.data ?? []).map((f) => (
                  <option key={f.id} value={f.id}>
                    {f.name}
                  </option>
                ))}
              </select>
            </label>
          </div>
          {create.isError && <div className="alert alert-error">{errorMessage(create.error)}</div>}
          <div className="row">
            <button type="submit" className="btn btn-primary" disabled={create.isPending}>
              {ru.common.add}
            </button>
            <button type="button" className="btn" onClick={() => setCreating(false)}>
              {ru.app.cancel}
            </button>
          </div>
        </form>
      )}
      {update.isError && <ErrorBox error={update.error} />}
      {list.data.length === 0 ? (
        <Empty text={ru.discovery.evidence.empty} />
      ) : (
        <div className="table-wrap">
          <table className="table">
            <thead>
              <tr>
                <th>{ru.discovery.evidence.source}</th>
                <th>{ru.discovery.evidence.date}</th>
                <th>{ru.discovery.evidence.trust}</th>
                <th>{ru.discovery.evidence.verification}</th>
                <th>{ru.discovery.evidence.sha256}</th>
                <th>{ru.discovery.evidence.hypothesis}</th>
              </tr>
            </thead>
            <tbody>
              {list.data.map((ev) => (
                <tr key={ev.id}>
                  <td className="wrap">
                    {ev.source}
                    {ev.source_ref && (
                      <div className="muted mono">
                        {/^https?:\/\//.test(ev.source_ref) ? (
                          <a href={ev.source_ref} target="_blank" rel="noreferrer">
                            {ev.source_ref}
                          </a>
                        ) : (
                          ev.source_ref
                        )}
                      </div>
                    )}
                  </td>
                  <td>{fmtDate(ev.date)}</td>
                  <td>{ru.discovery.insight.levels[ev.trust]}</td>
                  <td>
                    <Badge tone={ev.verification === 'verified' ? 'ok' : ev.verification === 'rejected' ? 'danger' : 'neutral'}>
                      {pick(ru.discovery.evidence.verifications, ev.verification ?? 'unverified')}
                    </Badge>
                    {canWrite && (
                      <div>
                        <select
                          aria-label={ru.discovery.evidence.setVerification}
                          value={ev.verification ?? 'unverified'}
                          disabled={update.isPending}
                          onChange={(e) => setVerification(ev, e.target.value as EvidenceInput['verification'])}
                        >
                          {VERIFICATIONS.map((v) => (
                            <option key={v} value={v}>
                              {ru.discovery.evidence.verifications[v]}
                            </option>
                          ))}
                        </select>
                      </div>
                    )}
                  </td>
                  <td className="mono wrap" title={ev.sha256}>
                    {ev.sha256 ? `${ev.sha256.slice(0, 12)}…` : ru.app.dash}
                  </td>
                  <td>{ev.hypothesis_id ? (hypTitle.get(ev.hypothesis_id) ?? ev.hypothesis_id) : ru.app.dash}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  )
}
