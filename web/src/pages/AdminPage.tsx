import { useState, type FormEvent } from 'react'
import { Navigate } from 'react-router-dom'
import { errorMessage } from '../api/client'
import {
  useCreateRequirementSet,
  useCustomFields,
  useDefineCustomField,
  useMe,
  useRequirementSets,
  useSaveTrackTemplate,
  useSetRequirementSetStatus,
  useTrackTemplates,
  useVerifyAudit,
} from '../api/hooks'
import type { CustomEntity, CustomFieldDefInput, GateTemplate, ProductType, RequirementSet, TrackTemplate } from '../api/types'
import { Badge, Empty, ErrorBox, Loading } from '../components/Status'
import { Tabs } from '../components/Tabs'
import { ru } from '../i18n/ru'
import { fmtDateTime } from '../lib/format'
import { hasRole, isAdmin } from '../lib/roles'
import { DeliveryConnectorPanel } from './DeliveryConnectorPanel'

type Tab = 'audit' | 'requirementSets' | 'trackTemplates' | 'customFields' | 'connectors'
const TABS: { key: Tab; label: string }[] = (['audit', 'requirementSets', 'trackTemplates', 'customFields'] as const).map((k) => ({
  key: k,
  label: ru.admin2.tabs[k],
}))
TABS.push({ key: 'connectors', label: 'Интеграции' })
const PRODUCT_TYPES: ProductType[] = ['security', 'infrastructure', 'platform', 'other']
const GATE_KINDS: GateTemplate['kind'][] = ['ssdlc', 'registry', 'fstec', 'support']
const ENTITIES: CustomEntity[] = ['feature', 'signal', 'hypothesis']
const FIELD_TYPES: CustomFieldDefInput['type'][] = ['string', 'number', 'date', 'enum']

export function AdminPage() {
  const me = useMe()
  const [tab, setTab] = useState<Tab>('audit')

  if (me.isPending) return <Loading />
  if (me.isError) return <ErrorBox error={me.error} />
  if (!isAdmin(me.data.roles)) return <Navigate to="/" replace />
  const canWrite = hasRole(me.data, 'admin', 'compliance')

  return (
    <section className="stack">
      <div className="page-head">
        <h1>{ru.admin.title}</h1>
        <Tabs<Tab> tabs={TABS.filter((item) => item.key !== 'connectors' || hasRole(me.data, 'admin'))} value={tab} onChange={setTab} />
      </div>
      {tab === 'audit' && <AuditTab />}
      {tab === 'requirementSets' && <RequirementSetsTab canWrite={canWrite} />}
      {tab === 'trackTemplates' && <TrackTemplatesTab canWrite={canWrite} />}
      {tab === 'customFields' && <CustomFieldsTab canWrite={hasRole(me.data, 'admin')} />}
      {tab === 'connectors' && hasRole(me.data, 'admin') && <DeliveryConnectorPanel />}
    </section>
  )
}

function AuditTab() {
  const verify = useVerifyAudit()
  return (
    <div className="card stack">
      <h2>{ru.admin.auditTitle}</h2>
      <p className="muted">{ru.admin.auditHint}</p>
      <div className="row">
        <button type="button" className="btn btn-primary" disabled={verify.isPending} onClick={() => verify.mutate()}>
          {verify.isPending ? ru.admin.auditRunning : ru.admin.auditRun}
        </button>
      </div>
      {verify.isError && <ErrorBox error={verify.error} />}
      {verify.data && (
        <dl className="kv">
          <dt>{ru.admin.checked}</dt>
          <dd>{verify.data.checked}</dd>
          <dt>{ru.app.error}</dt>
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
  )
}

// ---- Наборы требований (CM-01) --------------------------------------------

const RS_TONE: Record<RequirementSet['status'], 'neutral' | 'ok' | 'warn' | 'danger' | 'info'> = { draft: 'neutral', published: 'ok', retired: 'danger' }

function RequirementSetsTab({ canWrite }: { canWrite: boolean }) {
  const sets = useRequirementSets()
  const create = useCreateRequirementSet()
  const setStatus = useSetRequirementSetStatus()
  const [creating, setCreating] = useState(false)
  const [code, setCode] = useState('')
  const [type, setType] = useState<ProductType>('security')
  const [items, setItems] = useState('')

  if (sets.isPending) return <Loading />
  if (sets.isError) return <ErrorBox error={sets.error} onRetry={() => void sets.refetch()} />

  const submit = (e: FormEvent) => {
    e.preventDefault()
    const parsed = items
      .split('\n')
      .map((l) => l.trim())
      .filter(Boolean)
      .map((l) => {
        const [key, ...rest] = l.split(/\s+[—-]\s+/)
        return { key: key.trim(), text: rest.join(' — ').trim() || key.trim() }
      })
    create.mutate({ code: code.trim(), product_type: type, items: parsed }, { onSuccess: () => setCreating(false) })
  }

  return (
    <div className="card stack">
      <div className="row wrap-row">
        <h2>{ru.admin2.rs.title}</h2>
        {canWrite && (
          <button type="button" className="btn btn-sm" onClick={() => setCreating((v) => !v)}>
            {ru.admin2.rs.create}
          </button>
        )}
      </div>
      {creating && (
        <form className="card form-inline stack" onSubmit={submit}>
          <div className="row">
            <label className="field">
              <span>{ru.admin2.rs.code}</span>
              <input className="mono" value={code} onChange={(e) => setCode(e.target.value)} required />
            </label>
            <label className="field">
              <span>{ru.admin2.rs.productType}</span>
              <select value={type} onChange={(e) => setType(e.target.value as ProductType)}>
                {PRODUCT_TYPES.map((t) => (
                  <option key={t} value={t}>
                    {ru.product.types[t]}
                  </option>
                ))}
              </select>
            </label>
          </div>
          <label className="field">
            <span>
              {ru.admin2.rs.items} ({ru.admin2.rs.itemsHint}, {ru.common.lines})
            </span>
            <textarea rows={4} value={items} onChange={(e) => setItems(e.target.value)} required />
          </label>
          {create.isError && <div className="alert alert-error">{errorMessage(create.error)}</div>}
          <div className="row">
            <button type="submit" className="btn btn-primary btn-sm" disabled={create.isPending}>
              {ru.common.create}
            </button>
            <button type="button" className="btn btn-sm" onClick={() => setCreating(false)}>
              {ru.app.cancel}
            </button>
          </div>
        </form>
      )}
      {setStatus.isError && <ErrorBox error={setStatus.error} />}
      {sets.data.length === 0 ? (
        <Empty text={ru.admin2.rs.empty} />
      ) : (
        <div className="table-wrap">
          <table className="table">
            <thead>
              <tr>
                <th>{ru.admin2.rs.code}</th>
                <th>{ru.admin2.rs.version}</th>
                <th>{ru.admin2.rs.productType}</th>
                <th>{ru.common.status}</th>
                <th>{ru.admin2.rs.items}</th>
                <th>{ru.roadmap.at}</th>
                <th />
              </tr>
            </thead>
            <tbody>
              {sets.data.map((s) => (
                <tr key={s.id}>
                  <td className="mono">{s.code}</td>
                  <td className="num">{s.version}</td>
                  <td>{ru.product.types[s.product_type]}</td>
                  <td>
                    <Badge tone={RS_TONE[s.status]}>{ru.admin2.rs.statuses[s.status]}</Badge>
                  </td>
                  <td className="wrap">
                    <ul>
                      {s.items.map((it) => (
                        <li key={it.key}>
                          <span className="mono">{it.key}</span> — {it.text}
                        </li>
                      ))}
                    </ul>
                  </td>
                  <td>{fmtDateTime(s.updated_at)}</td>
                  <td>
                    {canWrite && (
                      <div className="row">
                        {s.status === 'draft' && (
                          <button type="button" className="btn btn-sm btn-primary" disabled={setStatus.isPending} onClick={() => setStatus.mutate({ id: s.id, status: 'published' })}>
                            {ru.admin2.rs.publish}
                          </button>
                        )}
                        {s.status === 'published' && (
                          <button type="button" className="btn btn-sm btn-danger" disabled={setStatus.isPending} onClick={() => setStatus.mutate({ id: s.id, status: 'retired' })}>
                            {ru.admin2.rs.retire}
                          </button>
                        )}
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

// ---- Шаблоны треков (CM-02) -----------------------------------------------

const emptyGate = (order: number): GateTemplate => ({ key: '', name: '', kind: 'ssdlc', order, checklist: [] })

function TrackTemplatesTab({ canWrite }: { canWrite: boolean }) {
  const templates = useTrackTemplates()
  const [editing, setEditing] = useState<TrackTemplate | 'new' | null>(null)

  if (templates.isPending) return <Loading />
  if (templates.isError) return <ErrorBox error={templates.error} onRetry={() => void templates.refetch()} />

  return (
    <div className="card stack">
      <div className="row wrap-row">
        <h2>{ru.admin2.tt.title}</h2>
        {canWrite && (
          <button type="button" className="btn btn-sm" onClick={() => setEditing(editing === 'new' ? null : 'new')}>
            {ru.admin2.tt.create}
          </button>
        )}
      </div>
      {editing && <TemplateForm key={editing === 'new' ? 'new' : editing.id} initial={editing === 'new' ? null : editing} onDone={() => setEditing(null)} />}
      {templates.data.length === 0 && <Empty text={ru.admin2.tt.empty} />}
      {templates.data.map((t) => (
        <div key={t.id} className="item">
          <div className="row wrap-row">
            <strong>{t.name}</strong>
            <Badge tone="neutral">{ru.product.types[t.product_type]}</Badge>
            <span className="mono muted">{t.id}</span>
            {canWrite && (
              <button type="button" className="btn btn-sm" onClick={() => setEditing(t)}>
                {ru.admin2.tt.edit}
              </button>
            )}
          </div>
          <table className="table table-compact">
            <thead>
              <tr>
                <th>{ru.admin2.tt.order}</th>
                <th>{ru.admin2.tt.key}</th>
                <th>{ru.admin2.tt.gate}</th>
                <th>{ru.admin2.tt.kind}</th>
                <th>{ru.admin2.tt.parallel}</th>
                <th>{ru.admin2.tt.rsCode}</th>
                <th>{ru.admin2.tt.checklist}</th>
              </tr>
            </thead>
            <tbody>
              {[...t.gates]
                .sort((a, b) => a.order - b.order)
                .map((g) => (
                  <tr key={g.key}>
                    <td className="num">{g.order}</td>
                    <td className="mono">{g.key}</td>
                    <td>{g.name}</td>
                    <td>{ru.compliance.gateKinds[g.kind]}</td>
                    <td>{g.parallel_group ?? ru.app.dash}</td>
                    <td className="mono">{g.requirement_set_code ?? ru.app.dash}</td>
                    <td className="wrap">{g.checklist.join('; ') || ru.app.dash}</td>
                  </tr>
                ))}
            </tbody>
          </table>
        </div>
      ))}
    </div>
  )
}

function TemplateForm({ initial, onDone }: { initial: TrackTemplate | null; onDone: () => void }) {
  const save = useSaveTrackTemplate()
  const [name, setName] = useState(initial?.name ?? '')
  const [type, setType] = useState<ProductType>(initial?.product_type ?? 'security')
  const [gates, setGates] = useState<GateTemplate[]>(initial?.gates.length ? initial.gates : [emptyGate(1)])
  const [checklists, setChecklists] = useState<string[]>((initial?.gates ?? [emptyGate(1)]).map((g) => g.checklist.join('\n')))
  const setGate = (i: number, patch: Partial<GateTemplate>) => setGates((gs) => gs.map((g, j) => (j === i ? { ...g, ...patch } : g)))

  const submit = (e: FormEvent) => {
    e.preventDefault()
    const body = {
      id: initial?.id,
      name: name.trim(),
      product_type: type,
      gates: gates.map((g, i) => ({
        ...g,
        key: g.key.trim(),
        name: g.name.trim(),
        parallel_group: g.parallel_group?.trim() || undefined,
        requirement_set_code: g.requirement_set_code?.trim() || undefined,
        checklist: (checklists[i] ?? '').split('\n').map((x) => x.trim()).filter(Boolean),
      })),
    }
    save.mutate(body, { onSuccess: onDone })
  }

  return (
    <form className="card form-inline stack" onSubmit={submit}>
      <h3>{initial ? ru.admin2.tt.edit : ru.admin2.tt.create}</h3>
      <div className="row">
        <label className="field grow">
          <span>{ru.admin2.tt.name}</span>
          <input value={name} onChange={(e) => setName(e.target.value)} required />
        </label>
        <label className="field">
          <span>{ru.admin2.tt.productType}</span>
          <select value={type} onChange={(e) => setType(e.target.value as ProductType)}>
            {PRODUCT_TYPES.map((t) => (
              <option key={t} value={t}>
                {ru.product.types[t]}
              </option>
            ))}
          </select>
        </label>
      </div>
      <h3>{ru.admin2.tt.gates}</h3>
      {gates.map((g, i) => (
        <div key={i} className="item">
          <div className="row">
            <label className="field">
              <span>{ru.admin2.tt.order}</span>
              <input type="number" min={1} value={g.order} onChange={(e) => setGate(i, { order: Number(e.target.value) })} required />
            </label>
            <label className="field">
              <span>{ru.admin2.tt.key}</span>
              <input className="mono" value={g.key} onChange={(e) => setGate(i, { key: e.target.value })} required />
            </label>
            <label className="field grow">
              <span>{ru.admin2.tt.gate}</span>
              <input value={g.name} onChange={(e) => setGate(i, { name: e.target.value })} required />
            </label>
            <label className="field">
              <span>{ru.admin2.tt.kind}</span>
              <select value={g.kind} onChange={(e) => setGate(i, { kind: e.target.value as GateTemplate['kind'] })}>
                {GATE_KINDS.map((k) => (
                  <option key={k} value={k}>
                    {ru.compliance.gateKinds[k]}
                  </option>
                ))}
              </select>
            </label>
            <label className="field">
              <span>{ru.admin2.tt.parallel}</span>
              <input value={g.parallel_group ?? ''} onChange={(e) => setGate(i, { parallel_group: e.target.value })} />
            </label>
            <label className="field">
              <span>{ru.admin2.tt.rsCode}</span>
              <input className="mono" value={g.requirement_set_code ?? ''} onChange={(e) => setGate(i, { requirement_set_code: e.target.value })} />
            </label>
            <button
              type="button"
              className="btn btn-sm"
              onClick={() => {
                setGates((gs) => gs.filter((_, j) => j !== i))
                setChecklists((cs) => cs.filter((_, j) => j !== i))
              }}
            >
              {ru.common.remove}
            </button>
          </div>
          <label className="field">
            <span>
              {ru.admin2.tt.checklist} ({ru.common.lines})
            </span>
            <textarea rows={2} value={checklists[i] ?? ''} onChange={(e) => setChecklists((cs) => cs.map((c, j) => (j === i ? e.target.value : c)))} />
          </label>
        </div>
      ))}
      <div className="row">
        <button
          type="button"
          className="btn btn-sm"
          onClick={() => {
            setGates((gs) => [...gs, emptyGate(gs.length + 1)])
            setChecklists((cs) => [...cs, ''])
          }}
        >
          {ru.admin2.tt.addGate}
        </button>
      </div>
      {save.isError && <div className="alert alert-error">{errorMessage(save.error)}</div>}
      <div className="row">
        <button type="submit" className="btn btn-primary btn-sm" disabled={save.isPending}>
          {ru.admin2.tt.save}
        </button>
        <button type="button" className="btn btn-sm" onClick={onDone}>
          {ru.app.cancel}
        </button>
      </div>
    </form>
  )
}

// ---- Кастомные поля (AD-03) -----------------------------------------------

function CustomFieldsTab({ canWrite }: { canWrite: boolean }) {
  const [entity, setEntity] = useState<CustomEntity>('hypothesis')
  const fields = useCustomFields(entity)
  const define = useDefineCustomField()
  const [form, setForm] = useState<CustomFieldDefInput>({ entity, key: '', label: '', type: 'string', required: false })
  const set = <K extends keyof CustomFieldDefInput>(k: K, v: CustomFieldDefInput[K]) => setForm((f) => ({ ...f, [k]: v }))
  const [options, setOptions] = useState('')

  const submit = (e: FormEvent) => {
    e.preventDefault()
    define.mutate(
      {
        ...form,
        entity,
        key: form.key.trim(),
        label: form.label.trim(),
        options: form.type === 'enum' ? options.split(',').map((x) => x.trim()).filter(Boolean) : undefined,
      },
      { onSuccess: () => setForm({ entity, key: '', label: '', type: 'string', required: false }) },
    )
  }

  return (
    <div className="card stack">
      <div className="row wrap-row">
        <h2>{ru.admin2.cf.title}</h2>
        <label className="field">
          <span>{ru.admin2.cf.entity}</span>
          <select value={entity} onChange={(e) => setEntity(e.target.value as CustomEntity)}>
            {ENTITIES.map((en) => (
              <option key={en} value={en}>
                {ru.admin2.cf.entities[en]}
              </option>
            ))}
          </select>
        </label>
      </div>
      {fields.isPending && <Loading />}
      {fields.isError && <ErrorBox error={fields.error} />}
      {fields.data && fields.data.length === 0 && <Empty text={ru.admin2.cf.empty} />}
      {fields.data && fields.data.length > 0 && (
        <div className="table-wrap">
          <table className="table table-compact">
            <thead>
              <tr>
                <th>{ru.admin2.cf.key}</th>
                <th>{ru.admin2.cf.label}</th>
                <th>{ru.admin2.cf.type}</th>
                <th>{ru.admin2.cf.options}</th>
                <th>{ru.admin2.cf.required}</th>
              </tr>
            </thead>
            <tbody>
              {fields.data.map((f) => (
                <tr key={f.id}>
                  <td className="mono">{f.key}</td>
                  <td>{f.label}</td>
                  <td>{ru.admin2.cf.types[f.type]}</td>
                  <td>{(f.options ?? []).join(', ') || ru.app.dash}</td>
                  <td>{f.required ? ru.app.yes : ru.app.no}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      {canWrite && (
        <form className="card form-inline stack" onSubmit={submit}>
          <h3>{ru.admin2.cf.define}</h3>
          <div className="row">
            <label className="field">
              <span>{ru.admin2.cf.key}</span>
              <input className="mono" value={form.key} onChange={(e) => set('key', e.target.value)} required />
            </label>
            <label className="field grow">
              <span>{ru.admin2.cf.label}</span>
              <input value={form.label} onChange={(e) => set('label', e.target.value)} required />
            </label>
            <label className="field">
              <span>{ru.admin2.cf.type}</span>
              <select value={form.type} onChange={(e) => set('type', e.target.value as CustomFieldDefInput['type'])}>
                {FIELD_TYPES.map((t) => (
                  <option key={t} value={t}>
                    {ru.admin2.cf.types[t]}
                  </option>
                ))}
              </select>
            </label>
            {form.type === 'enum' && (
              <label className="field grow">
                <span>{ru.admin2.cf.options}</span>
                <input value={options} onChange={(e) => setOptions(e.target.value)} />
              </label>
            )}
            <label className="check">
              <input type="checkbox" checked={form.required ?? false} onChange={(e) => set('required', e.target.checked)} />
              {ru.admin2.cf.required}
            </label>
          </div>
          {define.isError && <div className="alert alert-error">{errorMessage(define.error)}</div>}
          {define.isSuccess && <div className="alert alert-ok">{ru.common.saved}</div>}
          <div className="row">
            <button type="submit" className="btn btn-primary btn-sm" disabled={define.isPending}>
              {ru.admin2.cf.define}
            </button>
          </div>
        </form>
      )}
    </div>
  )
}
