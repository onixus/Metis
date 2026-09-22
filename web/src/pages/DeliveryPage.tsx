import { useState, type FormEvent } from 'react'
import { Link } from 'react-router-dom'
import { useDelivery, useDeliveryConnector, useLinkDeliveryFeature, useRequestDeliveryEpic, type DeliveryOverview, type DeliverySync } from '../api/delivery'
import { accessLevel, useFeatures, useMe, useProducts } from '../api/hooks'
import type { Feature } from '../api/types'
import { Badge, Empty, ErrorBox, Loading } from '../components/Status'
import { fmtDate, fmtDateTime } from '../lib/format'
import { canWriteRoadmap, hasRole } from '../lib/roles'

export function DeliveryPage() {
  const me = useMe()
  const products = useProducts()
  const [selected, setSelected] = useState('')
  if (me.isPending || products.isPending) return <Loading />
  if (me.isError) return <ErrorBox error={me.error} />
  if (products.isError) return <ErrorBox error={products.error} onRetry={() => void products.refetch()} />
  const available = products.data.filter((p) => accessLevel(me.data, p.id) === 'private')
  const productId = available.some((p) => p.id === selected) ? selected : available[0]?.id ?? ''
  return <section className="stack">
    <div className="page-head"><div><h1>Delivery</h1><p className="muted">Эпики, готовность фич и спринты из трекера поставки.</p></div>
      {available.length > 0 && <label className="field"><span>Продукт</span><select value={productId} onChange={(e) => setSelected(e.target.value)}>{available.map((p) => <option key={p.id} value={p.id}>{p.name}</option>)}</select></label>}
    </div>
    {available.length === 0 ? <Empty text="Для данных поставки нужен доступ к приватному контуру продукта." /> : <ProductDelivery key={productId} productId={productId} canWrite={canWriteRoadmap(me.data, accessLevel(me.data, productId))} admin={hasRole(me.data, 'admin')} />}
  </section>
}

function ProductDelivery({ productId, canWrite, admin }: { productId: string; canWrite: boolean; admin: boolean }) {
  const delivery = useDelivery(productId)
  const features = useFeatures(productId)
  const connector = useDeliveryConnector(admin)
  if (delivery.isPending || features.isPending) return <Loading />
  if (delivery.isError) return <ErrorBox error={delivery.error} onRetry={() => void delivery.refetch()} />
  if (features.isError) return <ErrorBox error={features.error} onRetry={() => void features.refetch()} />
  const data = delivery.data
  const names = new Map(features.data.map((f) => [f.id, f.name]))
  const metrics = new Map(data.metrics.map((m) => [m.feature_id, m]))
  return <>
    {!data.enabled && <div className="alert">Трекер не подключён. Сохранённые привязки и последние данные остаются доступны. Подключение настраивает администратор.</div>}
    <DeliverySyncStatus sync={data.sync} />
    <div className="stats"><Stat label="Привязанных эпиков" value={data.mappings.length} /><Stat label="Готовых фич" value={data.metrics.filter((m) => m.readiness.total > 0 && m.readiness.done === m.readiness.total).length} /><Stat label="Позже плана" value={data.metrics.filter((m) => m.plan_fact.late).length} /><Stat label="Активных спринтов" value={data.sprints.filter((s) => s.state === 'active').length} /></div>
    <div className="card stack">
      <div className="row wrap-row"><h2>Фичи и эпики</h2><button type="button" className="btn btn-sm" onClick={() => { void delivery.refetch(); void features.refetch() }}>Обновить</button><Link to={`/products/${productId}`}>Карточка продукта</Link></div>
      {data.mappings.length === 0 ? <Empty text="Привяжите фичу к эпику, чтобы получать готовность и изменения сроков." /> : <div className="table-wrap"><table className="table"><thead><tr><th>Фича</th><th>Эпик</th><th>Готовность</th><th>Изменение объёма</th><th>План фичи</th><th>Дата трекера</th><th>Отклонение</th></tr></thead><tbody>
        {data.mappings.map((mapping) => { const m = metrics.get(mapping.feature_id); return <tr key={mapping.feature_id}><td>{names.get(mapping.feature_id) ?? mapping.feature_id}</td><td className="mono">{mapping.epic_key}</td>{m ? <><td>{m.readiness.done} / {m.readiness.total} ({m.readiness.percent.toFixed(0)}%)</td><td>+{(m.scope_creep.added ?? []).length} / −{(m.scope_creep.removed ?? []).length}</td><td>{fmtDate(m.plan_fact.planned_date)}</td><td>{fmtDate(m.plan_fact.due_date)}</td><td><Badge tone={m.plan_fact.late ? 'danger' : 'ok'}>{m.plan_fact.delta_days > 0 ? '+' : ''}{m.plan_fact.delta_days} дн.</Badge></td></> : <td colSpan={5} className="muted">Ожидается первая сверка с трекером</td>}</tr> })}
      </tbody></table></div>}
      {canWrite && <DeliveryFeatureForm productId={productId} features={features.data} delivery={data} defaultProject={connector.data?.mapping.project ?? ''} />}
    </div>
    <div className="card stack"><h2>Спринты</h2>
      {data.sprints.length === 0 ? <Empty text="Спринты появятся после настройки доски продукта и первой сверки." /> : data.sprints.map((s) => <article className="item stack" key={`${s.board}:${s.sprint_id}`}>
        <div className="row wrap-row"><h3>{s.name}</h3><Badge tone={s.state === 'active' ? 'info' : 'neutral'}>{({ active: 'Активный', closed: 'Завершён', future: 'Запланирован' } as Record<string, string>)[s.state] ?? s.state}</Badge><span>{fmtDate(s.start_date)} — {fmtDate(s.end_date)}</span></div>
        {s.goal && <p>{s.goal}</p>}<p>Готово {s.done} из {s.total} задач. Перенесено из предыдущего спринта: {(s.carried_over ?? []).length}.</p>
        <details><summary>Состав спринта</summary><ul>{(s.issues ?? []).map((issue) => <li key={issue.key}><span className="mono">{issue.key}</span> — {issue.summary} <Badge tone={issue.done ? 'ok' : 'neutral'}>{issue.status}</Badge></li>)}</ul></details>
      </article>)}
    </div>
  </>
}

export function DeliverySyncStatus({ sync }: { sync: DeliverySync }) {
  const known = !!sync.last_success_at && !sync.last_success_at.startsWith('0001-')
  return <div className="card stack"><div className="row wrap-row"><Badge tone={!known || sync.stale ? 'warn' : 'ok'}>{!known ? 'Ещё не синхронизировано' : sync.stale ? 'Данные устарели' : 'Данные актуальны'}</Badge><span className="muted">Последняя успешная сверка: {known ? fmtDateTime(sync.last_success_at) : '—'}</span></div>{sync.last_error && <p className="alert alert-error">Ошибка последней сверки: {sync.last_error}</p>}</div>
}
function Stat({ label, value }: { label: string; value: number }) { return <div className="stat"><div className="stat-value">{value}</div><div className="stat-label">{label}</div></div> }

function DeliveryFeatureForm({ productId, features, delivery, defaultProject }: { productId: string; features: Feature[]; delivery: DeliveryOverview; defaultProject: string }) {
  const [open, setOpen] = useState(false)
  const [mode, setMode] = useState<'link' | 'create'>('link')
  const [featureId, setFeatureId] = useState('')
  const [project, setProject] = useState<string | null>(null)
  const [epicKey, setEpicKey] = useState('')
  const [queued, setQueued] = useState<string[]>([])
  const link = useLinkDeliveryFeature(productId)
  const create = useRequestDeliveryEpic(productId)
  const mapped = new Set(delivery.mappings.map((m) => m.feature_id))
  const choices = features.filter((f) => mode === 'link' || (!f.external_key && !mapped.has(f.id) && !queued.includes(f.id)))
  const chosen = choices.some((f) => f.id === featureId) ? featureId : choices[0]?.id ?? ''
  const submit = (e: FormEvent) => {
    e.preventDefault()
    const target = chosen
    const sourceProject = (project ?? defaultProject).trim()
    if (!target || !sourceProject) return
    if (mode === 'link') link.mutate({ featureId: target, epicKey: epicKey.trim(), project: sourceProject })
    else create.mutate({ featureId: target, project: sourceProject }, { onSuccess: () => setQueued((ids) => [...ids, target]) })
  }
  return <div className="stack"><div><button type="button" className="btn" onClick={() => setOpen((v) => !v)}>{open ? 'Скрыть форму' : 'Привязать или создать эпик'}</button></div>
    {open && <form className="card form-inline stack" onSubmit={submit}>
      <div className="row wrap-row"><label className="field"><span>Действие</span><select value={mode} onChange={(e) => { setMode(e.target.value as 'link' | 'create'); link.reset(); create.reset() }}><option value="link">Привязать существующий эпик</option>{delivery.enabled && <option value="create">Создать эпик в трекере</option>}</select></label>
      <label className="field grow"><span>Фича</span><select value={chosen} onChange={(e) => setFeatureId(e.target.value)} required>{choices.length === 0 && <option value="">Нет доступных фич</option>}{choices.map((f) => <option key={f.id} value={f.id}>{f.name}{f.external_key ? ` (${f.external_key})` : ''}</option>)}</select></label>
      <label className="field"><span>Проект трекера</span><input value={project ?? defaultProject} onChange={(e) => setProject(e.target.value)} maxLength={200} required placeholder="PROJECT" /></label>
      {mode === 'link' && <label className="field"><span>Ключ эпика</span><input value={epicKey} onChange={(e) => setEpicKey(e.target.value)} maxLength={200} required placeholder="PROJECT-123" /></label>}</div>
      {mode === 'create' && <p className="muted">Эпик создаст фоновый обработчик. Привязка появится после обработки запроса.</p>}
      {(link.isError || create.isError) && <ErrorBox error={link.error ?? create.error} />}
      {link.isSuccess && <p className="alert alert-ok">Привязка сохранена. Данные появятся после следующей сверки.</p>}
      {create.isSuccess && <p className="alert alert-ok">Запрос принят. Обновите данные после обработки или проверьте состояние коннектора у администратора.</p>}
      <div><button className="btn btn-primary" disabled={!chosen || link.isPending || create.isPending}>{link.isPending || create.isPending ? 'Сохраняется…' : mode === 'create' ? 'Запросить создание' : 'Сохранить привязку'}</button></div>
    </form>}
  </div>
}
