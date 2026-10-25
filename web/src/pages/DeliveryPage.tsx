import { useState, type FormEvent } from 'react'
import { Link } from 'react-router-dom'
import { useDelivery, useDeliveryConnector, useFeatureForecast, useLinkDeliveryFeature, useRequestDeliveryEpic, type DeliveryOverview, type DeliverySync } from '../api/delivery'
import { accessLevel, useCommitments, useFeatures, useMe, useProducts } from '../api/hooks'
import type { Feature } from '../api/types'
import { Badge, Empty, ErrorBox, Loading } from '../components/Status'
import { fmtDate, fmtDateTime } from '../lib/format'
import { canWriteRoadmap, hasRole } from '../lib/roles'
import { compareForecastDate, forecastIsStale } from '../lib/forecastRisk'

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
  const [forecastFeature, setForecastFeature] = useState('')
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
    <div className="card stack"><h2>Прогноз доставки</h2>
      <p className="muted">Монте-Карло по истории закрытых спринтов. Прогноз предполагает прежний темп команды и неизменный объём задач; зависимости и будущие изменения состава он не учитывает.</p>
      {data.mappings.length === 0 ? <Empty text="Для прогноза нужны привязанный эпик, данные задач и история закрытых спринтов." /> : <>
        <label className="field"><span>Фича для прогноза</span><select value={forecastFeature} onChange={(e) => setForecastFeature(e.target.value)}>
          <option value="">Выберите фичу</option>{data.mappings.map((m) => <option key={m.feature_id} value={m.feature_id}>{names.get(m.feature_id) ?? m.feature_id}</option>)}
        </select></label>
        {forecastFeature && data.mappings.some((m) => m.feature_id === forecastFeature) && <FeatureForecast key={forecastFeature} featureId={forecastFeature} productId={productId} currentSync={data.sync} currentConfirmed={delivery.fetchStatus === 'idle' && delivery.isFetchedAfterMount} plannedDate={features.data.find((f) => f.id === forecastFeature)?.planned_date} />}
      </>}
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

function FeatureForecast({ featureId, productId, plannedDate, currentSync, currentConfirmed }: { featureId: string; productId: string; plannedDate?: string | null; currentSync: DeliverySync; currentConfirmed: boolean }) {
  const forecast = useFeatureForecast(featureId, currentSync.last_success_at)
  const commitments = useCommitments(productId)
  if (forecast.isPending) return <Loading />
  if (forecast.isError) return <div className="stack"><ErrorBox error={forecast.error} onRetry={() => void forecast.refetch()} /><p className="muted">Если истории throughput нет, сначала настройте доску и выполните сверку с трекером. Нулевой throughput не позволяет рассчитать дату.</p></div>
  const f = forecast.data
  const stale = forecastIsStale(f.sync, currentSync, currentConfirmed)
  return <div className="stack">
    {!currentConfirmed ? <p className="muted" role="status">Проверяем актуальность исходных данных прогноза…</p> : f.sync && <DeliverySyncStatus sync={{ ...f.sync, stale }} />}
    {currentConfirmed && stale && <p className="alert alert-warn">Исходные данные прогноза устарели или изменились. Подтвердите даты новым расчётом после успешной сверки с трекером.</p>}
    <p className="muted">Расчёт выполнен: {fmtDateTime(f.calculated_at)}.</p>
    {f.remaining === 0 ? <p className="alert alert-ok">Все задачи эпика завершены на дату расчёта {fmtDate(f.from)}.</p> : <>
      <div className="stats">{([['P50', f.p50], ['P85', f.p85], ['P95', f.p95]] as const).map(([label, date]) => <div className="stat" key={label}><div className="stat-value">{fmtDate(date)}</div><div className="stat-label">{label}</div></div>)}</div>
      <p>P85 означает: в 85% прогонов модели задачи завершены к этой дате. Это оценка при текущих допущениях, а не согласованный срок обязательства.</p>
      <p>Диапазон P10–P90: {fmtDate(f.interval_low)} — {fmtDate(f.interval_high)}. Чем шире диапазон, тем сильнее различается темп в исходной истории.</p>
    </>}
    <p>Эпик: {f.epic_key ?? '—'}. Осталось задач: {f.remaining}. Отсчёт: {fmtDate(f.from)}. Итерация: {f.iteration_days ?? '—'} дн. Прогонов: {f.samples ?? '—'}.</p>
    <p>Завершено задач за закрытые спринты: {(f.throughput ?? []).join(', ') || '—'} ({f.throughput?.length ?? 0} спринтов). Короткая история ограничивает надёжность оценки.</p>
    {f.remaining > 0 && <div className="stack"><h3>Сравнение со сроками</h3>
      <ForecastDeadline label="План фичи" p85={f.p85} due={plannedDate} stale={stale} checking={!currentConfirmed} />
      {commitments.isPending && <Loading />}
      {commitments.isError && <ErrorBox error={commitments.error} onRetry={() => void commitments.refetch()} />}
      {commitments.data?.filter((c) => c.status === 'active' && c.feature_id === featureId).map((c) => <ForecastDeadline key={c.id} label={`Обязательство: ${c.subject}`} p85={f.p85} due={c.due_date} stale={stale} checking={!currentConfirmed} />)}
      <p className="muted">Сравниваются план фичи и активные обязательства, явно связанные с этой фичей. Сроки и статусы обязательств при расчёте не меняются.</p>
    </div>}
    <div><button className="btn" type="button" disabled={forecast.isFetching} onClick={() => void forecast.refetch()}>Пересчитать прогноз</button></div>
  </div>
}

function ForecastDeadline({ label, p85, due, stale, checking }: { label: string; p85?: string | null; due?: string | null; stale: boolean; checking: boolean }) {
  const risk = compareForecastDate(p85, due, stale)
  const text = checking ? 'Проверяем актуальность данных' : risk.state === 'unknown' ? 'Недостаточно дат для сравнения' : risk.state === 'stale' ? 'Данные устарели — сравнение требует сверки' : risk.state === 'at_risk' ? `P85 позже срока на ${risk.daysLate} дн.` : 'P85 укладывается в срок при допущениях модели'
  return <p>{label}: {fmtDate(due)}. <Badge tone={risk.state === 'at_risk' ? 'danger' : risk.state === 'within' ? 'info' : 'warn'}>{text}</Badge></p>
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
