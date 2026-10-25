import { Explanation } from './EconomicsExplanation'
export { Explanation } from './EconomicsExplanation'
import { EconomicsScenariosPanel } from './EconomicsScenariosPanel'
import { useState, type FormEvent } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { api, unwrap } from '../api/client'
import { useMe } from '../api/hooks'
import type { components } from '../api/schema'
import { Empty, ErrorBox, Loading } from '../components/Status'
import { Tabs } from '../components/Tabs'
import { fmtDateTime } from '../lib/format'
import { effectiveVersion, fieldDraft, formulaSources, latestVersion, metricDraft } from '../lib/economicsModel'

type S = components['schemas']
const dimensions = { product: 'Продукт', team: 'Команда', period: 'Период', item: 'Статья' }
const types = { money: 'Деньги', number: 'Число', percent: 'Процент', date: 'Дата', reference: 'Справочник' }
const sources = { import: 'Импорт', manual: 'Ручной ввод', calculated: 'Расчёт' }
const emptyField: S['FinancialFieldInput'] = { key: '', name: '', type: 'money', source: 'import', currency: 'RUB', dimensions: ['product', 'period'] }
const emptyMetric: S['MetricInput'] = { key: '', name: '', expression: '', currency: '' }

export function EconomicsModelPanel(props: { productId: string; period: string }) {
  const me = useMe()
  if (me.isPending) return <Loading />
  if (me.isError) return <ErrorBox error={me.error} />
  if (me.data.all_products === 'none') return <Empty text="Общие определения финансовой модели требуют доступа ко всем продуктам. Книга выбранного продукта доступна в отдельной вкладке." />
  return <ModelEditor {...props} />
}

function ModelEditor({ productId, period }: { productId: string; period: string }) {
  const qc = useQueryClient()
  const [tab, setTab] = useState<'fields' | 'metrics' | 'scenarios'>('fields')
  const [field, setField] = useState<S['FinancialFieldInput']>(emptyField)
  const [metric, setMetric] = useState<S['MetricInput']>(emptyMetric)
  const [editingField, setEditingField] = useState(false), [editingMetric, setEditingMetric] = useState(false)
  const fields = useQuery({ queryKey: ['economics-model', 'fields'], queryFn: async () => unwrap(await api.GET('/economics/fields')) })
  const metrics = useQuery({ queryKey: ['economics-model', 'metrics'], queryFn: async () => unwrap(await api.GET('/economics/metrics')) })
  const refresh = async () => { await qc.invalidateQueries({ queryKey: ['economics-model'] }); await qc.invalidateQueries({ queryKey: ['economics'] }) }
  const saveField = useMutation({ mutationFn: async () => unwrap(await api.POST('/economics/fields', { body: { ...field, currency: field.type === 'money' ? field.currency : undefined, effective_from: field.effective_from || undefined } })), onSuccess: async () => { setField(emptyField); setEditingField(false); await refresh() } })
  const saveMetric = useMutation({ mutationFn: async () => unwrap(await api.POST('/economics/metrics', { body: { ...metric, currency: metric.currency || undefined, effective_from: metric.effective_from || undefined } })), onSuccess: async () => { setMetric(emptyMetric); setEditingMetric(false); await refresh() } })
  const submitField = (e: FormEvent) => { e.preventDefault(); saveField.mutate() }
  const submitMetric = (e: FormEvent) => { e.preventDefault(); saveMetric.mutate() }
  return <div className="stack">
    <div className="card"><h2>Конструктор финансовой модели</h2><p className="muted">Поля и формулы для выбранного продукта и месяца. До объединения книга и модель используют свои входные данные. После объединения текущие значения и ручные правки читаются из общей книги, а исходные истории доступны в архиве. Прямые функции используют закреплённые снимки платформы.</p></div>
    <Tabs<'fields' | 'metrics' | 'scenarios'> label="Конструктор финансовой модели" tabs={[{ key: 'fields', label: 'Финансовые поля' }, { key: 'metrics', label: 'Формулы и версии' }, { key: 'scenarios', label: 'Сценарии бюджета и capacity' }]} value={tab} onChange={setTab} />
    {(fields.isPending || metrics.isPending) && <Loading />}{fields.isError && <ErrorBox error={fields.error} />}{metrics.isError && <ErrorBox error={metrics.error} />}
    {tab === 'scenarios' && <EconomicsScenariosPanel key={`${productId}/${period}`} productId={productId} period={period} fields={fields.data ?? []} metrics={metrics.data ?? []} />}
    {tab === 'fields' && <>
      <form className="card stack" onSubmit={submitField}><h3>{editingField ? `Новая версия поля ${field.key}` : 'Добавить финансовое поле'}</h3><div className="finance-grid">
        <label className="field"><span>Ключ поля</span><input value={field.key} disabled={editingField} required pattern="[a-z][a-z0-9_]*" onChange={e => setField({ ...field, key: e.target.value })} /></label>
        <label className="field"><span>Название поля</span><input value={field.name} required onChange={e => setField({ ...field, name: e.target.value })} /></label>
        <label className="field"><span>Тип поля</span><select aria-label="Тип поля" value={field.type} onChange={e => setField({ ...field, type: e.target.value as S['FinancialFieldInput']['type'] })}>{Object.entries(types).map(([k, v]) => <option key={k} value={k}>{v}</option>)}</select></label>
        <label className="field"><span>Источник поля</span><select aria-label="Источник поля" value={field.source} onChange={e => setField({ ...field, source: e.target.value as S['FinancialFieldInput']['source'] })}>{Object.entries(sources).map(([k, v]) => <option key={k} value={k}>{v}</option>)}</select></label>
        {field.type === 'money' && <label className="field"><span>Валюта поля</span><input value={field.currency ?? ''} required pattern="[A-Z]{3}" maxLength={3} onChange={e => setField({ ...field, currency: e.target.value.toUpperCase() })} /></label>}
        <label className="field"><span>Дата действия поля</span><input type="date" value={field.effective_from ?? ''} onChange={e => setField({ ...field, effective_from: e.target.value })} /></label>
      </div><fieldset><legend>Измерения поля</legend><div className="row">{Object.entries(dimensions).map(([k, v]) => <label key={k}><input type="checkbox" checked={field.dimensions?.includes(k as keyof typeof dimensions) ?? false} onChange={e => setField({ ...field, dimensions: e.target.checked ? [...field.dimensions ?? [], k as keyof typeof dimensions] : field.dimensions?.filter(d => d !== k) })} /> {v}</label>)}</div></fieldset>
        <p className="muted">Ключ неизменяемый. Изменение описания создаёт новую версию. Для расчётного значения создайте формулу на вкладке «Формулы и версии». Пустая дата использует серверное правило даты действия.</p>
        <div className="row"><button className="btn btn-primary" disabled={saveField.isPending}>Сохранить {editingField ? 'версию поля' : 'поле'}</button>{editingField && <button className="btn" type="button" onClick={() => { setField(emptyField); setEditingField(false); saveField.reset() }}>Отменить редактирование</button>}</div>
        {saveField.isError && <ErrorBox error={saveField.error} />}{saveField.isSuccess && <p role="status">Поле сохранено</p>}
      </form>
      {fields.data?.length === 0 && <Empty text="Полей модели пока нет. Добавьте поле и задайте его источник." />}
      {fields.data?.map(f => <div className="card stack" key={f.id}><div className="row"><h3>{latestVersion(f.versions)?.name} <code className="mono">{f.key}</code></h3><button className="btn" onClick={() => { setField(fieldDraft(f)); setEditingField(true); saveField.reset() }}>Изменить поле {f.key}</button></div><details><summary>История поля · {f.versions.length} версий</summary><div className="table-wrap"><table className="table"><thead><tr><th>Версия</th><th>Действует с</th><th>Название</th><th>Тип / источник</th><th>Измерения</th><th>Автор / время</th></tr></thead><tbody>{f.versions.map(v => <tr key={v.version}><td>v{v.version}</td><td>{v.effective_from || '—'}</td><td>{v.name}</td><td>{types[v.type as keyof typeof types] || v.type} {v.currency} / {sources[v.source as keyof typeof sources] || v.source}</td><td>{v.dimensions?.map(d => dimensions[d as keyof typeof dimensions] || d).join(', ') || '—'}</td><td>{v.actor || '—'} · {v.at ? fmtDateTime(v.at) : '—'}</td></tr>)}</tbody></table></div></details>{effectiveVersion(f.versions, period)?.source === 'manual' && <ManualValueForm field={f} productId={productId} period={period} onSaved={refresh} />}</div>)}
    </>}
    {tab === 'metrics' && <>
      <form className="card stack" onSubmit={submitMetric}><h3>{editingMetric ? `Новая версия формулы ${metric.key}` : 'Добавить расчётный показатель'}</h3><div className="finance-grid">
        <label className="field"><span>Ключ показателя</span><input value={metric.key} disabled={editingMetric} required pattern="[a-z][a-z0-9_]*" onChange={e => setMetric({ ...metric, key: e.target.value })} /></label>
        <label className="field"><span>Название показателя</span><input value={metric.name} required onChange={e => setMetric({ ...metric, name: e.target.value })} /></label>
        <label className="field"><span>Валюта результата (необязательно)</span><input value={metric.currency ?? ''} pattern="[A-Z]{3}" maxLength={3} onChange={e => setMetric({ ...metric, currency: e.target.value.toUpperCase() })} /></label>
        <label className="field"><span>Дата действия формулы</span><input type="date" value={metric.effective_from ?? ''} onChange={e => setMetric({ ...metric, effective_from: e.target.value })} /></label>
      </div><label className="field"><span>Выражение формулы</span><textarea aria-label="Выражение формулы" className="mono" rows={3} maxLength={8192} value={metric.expression} required onChange={e => setMetric({ ...metric, expression: e.target.value })} placeholder={'field("revenue") - field("payroll")'} /></label>
        <p className="muted">Ссылки: field("ключ"), metric("ключ"). Агрегаты: sum("ключ", "product", "*"). Условия: условие ? значение : значение. Точные дроби: d("0.15"). Сервер проверяет выражение и отклоняет циклы до сохранения.</p>
        <div className="row">{Object.entries(formulaSources).map(([key, source]) => <button type="button" className="btn" key={key} title={`${source.name}: ${source.unit}`} onClick={() => setMetric({ ...metric, expression: `${metric.expression.trim() ? `${metric.expression} + ` : ''}${key}()` })}>Вставить {key}()</button>)}</div>
        <p className="muted">worklogs() — секунды, crm_revenue() — целые минорные единицы валюты модели, headcount() — человек. Можно заменить измерение, например worklogs("team", "*"). Источник должен быть полным и доступным; его отсутствие не означает ноль. В закрытом месяце используется зафиксированный снимок. Происхождение раскрывается вместе с результатом.</p>
        {!!fields.data?.length && <p className="muted">Поля модели: {fields.data.map(f => f.key).join(', ')}</p>}
        <div className="row"><button className="btn btn-primary" disabled={saveMetric.isPending}>Сохранить {editingMetric ? 'версию формулы' : 'показатель'}</button>{editingMetric && <button className="btn" type="button" onClick={() => { setMetric(emptyMetric); setEditingMetric(false); saveMetric.reset() }}>Отменить редактирование</button>}</div>
        {saveMetric.isError && <ErrorBox error={saveMetric.error} />}{saveMetric.isSuccess && <p role="status">Формула сохранена</p>}
      </form>
      {metrics.data?.length === 0 && <Empty text="Расчётных показателей пока нет. Создайте формулу по полям модели." />}
      {metrics.data?.map(m => <MetricCard key={`${m.id}/${m.versions.length}/${metrics.dataUpdatedAt}/${productId}/${period}`} metric={m} productId={productId} period={period} onEdit={() => { setMetric(metricDraft(m)); setEditingMetric(true); saveMetric.reset() }} />)}
    </>}
  </div>
}

function MetricCard({ metric, productId, period, onEdit }: { metric: S['Metric']; productId: string; period: string; onEdit: () => void }) {
  const latest = latestVersion(metric.versions)
  const [a, setA] = useState(metric.versions[0]?.version ?? 1), [b, setB] = useState(latest?.version ?? 1)
  const query = { period, productId }
  const explain = useMutation({ mutationFn: async () => unwrap(await api.GET('/economics/metrics/{metricKey}/explain', { params: { path: { metricKey: metric.key }, query } })) })
  const compare = useMutation({ mutationFn: async () => unwrap(await api.GET('/economics/metrics/{metricKey}/compare', { params: { path: { metricKey: metric.key }, query: { ...query, a, b } } })) })
  return <div className="card stack"><div className="row"><h3>{latest?.name} <code className="mono">{metric.key}</code></h3><button className="btn" onClick={onEdit}>Изменить формулу <span className="mono">{metric.key}</span></button></div><p className="mono">{latest?.expression}</p>
    <details><summary>История формулы · {metric.versions.length} версий</summary>{metric.versions.map(v => <div key={v.version} className="card"><strong>v{v.version} · {v.name} · действует с {v.effective_from || '—'}</strong><p className="mono">{v.expression}</p><p className="muted">Зависимости: {v.refs?.map(r => `${r.kind}: ${r.key}`).join(', ') || 'нет'} · {v.actor || '—'} · {v.at ? fmtDateTime(v.at) : '—'}</p></div>)}</details>
    <div className="row"><p>Расчёт за {period} · продукт {productId}</p><button className="btn" disabled={!productId || !period || explain.isPending} onClick={() => explain.mutate()}>Рассчитать и объяснить <span className="mono">{metric.key}</span></button></div>
    {explain.isError && <ErrorBox error={explain.error} />}{explain.data && <Explanation value={explain.data} />}
    {metric.versions.length > 1 && <><div className="row"><label className="field"><span>Версия A · <code className="mono">{metric.key}</code></span><select value={a} onChange={e => { setA(Number(e.target.value)); compare.reset() }}>{metric.versions.map(v => <option key={v.version} value={v.version}>v{v.version}</option>)}</select></label><label className="field"><span>Версия B · <code className="mono">{metric.key}</code></span><select value={b} onChange={e => { setB(Number(e.target.value)); compare.reset() }}>{metric.versions.map(v => <option key={v.version} value={v.version}>v{v.version}</option>)}</select></label><button className="btn" disabled={a === b || !productId || !period || compare.isPending} onClick={() => compare.mutate()}>Сравнить формулы <span className="mono">{metric.key}</span></button></div>{compare.isError && <ErrorBox error={compare.error} />}{compare.data && <p role="status">На данных периода {period}: v{compare.data.a} = <strong>{compare.data.value_a}</strong>; v{compare.data.b} = <strong>{compare.data.value_b}</strong>. {latest?.result_type === 'percent' ? '%' : latest?.result_type === 'number' ? 'Число' : latest?.currency || 'В единицах формулы'}</p>}</>}
  </div>
}


function ManualValueForm({ field, productId, period, onSaved }: { field: S['FinancialField']; productId: string; period: string; onSaved: () => Promise<void> }) {
  const [value, setValue] = useState(''), [team, setTeam] = useState(''), [item, setItem] = useState('')
  const version = effectiveVersion(field.versions, period)
  const teams = useQuery({ queryKey: ['economics-model', 'teams'], queryFn: async () => unwrap(await api.GET('/economics/teams')) })
  const save = useMutation({ mutationFn: async () => unwrap(await api.POST('/economics/fields/{fieldKey}/manual-value', { params: { path: { fieldKey: field.key } }, body: { product_id: productId, period, value, team_id: team || undefined, item: item || undefined } })), onSuccess: onSaved })
  if (!version || !['money', 'number', 'percent'].includes(version.type)) return <p className="muted">Ручные значения даты и справочника пока не поддерживаются расчётным API.</p>
  return <form className="stack" onSubmit={e => { e.preventDefault(); save.mutate() }}><h4>Ручное значение · {field.key}</h4><p className="muted">Продукт {productId}, месяц {period}. Сохранение заменяет значение только этого среза новой версией данных; остальные поля сохраняются. Закрытый период изменять нельзя.</p><div className="finance-grid"><label className="field"><span>Значение {field.key} {version.type === 'money' ? `(мин. ед. ${version.currency || 'RUB'})` : '(точное десятичное)'}</span><input value={value} required inputMode="decimal" pattern="-?[0-9]+([.][0-9]+)?" onChange={e => { setValue(e.target.value); save.reset() }} /></label>{version.dimensions?.includes('team') && <label className="field"><span>Команда значения {field.key}</span><select value={team} onChange={e => { setTeam(e.target.value); save.reset() }}><option value="">Без команды</option>{teams.data?.map(t => <option key={t.id} value={t.id}>{t.name}</option>)}</select></label>}{version.dimensions?.includes('item') && <label className="field"><span>Статья значения {field.key}</span><input value={item} onChange={e => { setItem(e.target.value); save.reset() }} /></label>}</div><div><button className="btn" disabled={!productId || !period || save.isPending}>Сохранить значение {field.key}</button></div>{teams.isError && <ErrorBox error={teams.error} />}{save.isError && <ErrorBox error={save.error} />}{save.isSuccess && <p role="status">Значение сохранено · версия данных {save.data.data_version}</p>}</form>
}
