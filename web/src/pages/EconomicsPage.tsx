import { useState, type FormEvent } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { api, unwrap, errorMessage } from '../api/client'
import { useMe, useProducts } from '../api/hooks'
import type { components } from '../api/schema'
import { Badge, Empty, ErrorBox, Loading } from '../components/Status'
import { fmtDateTime, fmtMoney } from '../lib/format'
import { parseMinorUnits } from '../lib/moneyInput'

type Schema = components['schemas']
type Template = Schema['FinanceTemplate']
type Snapshot = Schema['EconomicsSnapshot']
type Report = Schema['EconomicsReport']
type Field = Schema['EconomicsField']
type Rule = Schema['EconomicsRowRule']
const categories: Record<string, string> = { revenue: 'Выручка', payroll: 'ФОТ', direct_cost: 'Прямые затраты', marketing: 'Маркетинг', hub_cost: 'Затраты хаба', certification_cost: 'Сертификация', maintenance_cost: 'Поддержка веток' }
const columns: Record<string, string> = { product_id: 'UUID продукта', period: 'Период YYYY-MM', category: 'Статья', amount: 'Сумма в основных единицах', amount_minor: 'Сумма в минорных единицах (вместо amount)', currency: 'Валюта', team_id: 'Команда', headcount: 'Численность', feature_id: 'UUID фичи', certification_track_id: 'UUID трека', branch: 'Ветка', bundle_id: 'Бандл', description: 'Описание' }
const initialTemplate: Template = { sheet: '', header_row: 1, delimiter: ',', columns: {} }
const majorCurrencies = new Set(['RUB', 'USD', 'EUR', 'GBP', 'CNY'])
const money = (amount: number, currency: string) => !Number.isSafeInteger(amount) ? 'Точная сумма доступна в CSV' : majorCurrencies.has(currency) ? fmtMoney({ amount, currency }) : `${amount} мин. ед. ${currency}`
const financeMoney = (m: Schema['Money']) => money(m.amount, m.currency)

export function EconomicsPage() {
  const me = useMe(), products = useProducts()
  const [chosen, setChosen] = useState('')
  const [period, setPeriod] = useState(new Date().toISOString().slice(0, 7))
  const productId = chosen || products.data?.[0]?.id || ''
  const allowed = me.data?.roles.includes('finance') && me.data?.finance === 'full'
  if (me.isPending || products.isPending) return <Loading />
  if (me.isError) return <ErrorBox error={me.error} />
  if (products.isError) return <ErrorBox error={products.error} />
  return <section className="stack">
    <div className="page-head"><div><h1>Экономика портфеля</h1><p className="muted">Версии финансовых данных, распределение затрат и воспроизводимый P&amp;L</p></div></div>
    {!allowed ? <Empty text="Для финансовых данных нужны роль «finance» и финансовый уровень «full». Доступ выдаётся отдельно от административных прав." /> : <>
      <div className="card row"><label className="field"><span>Владелец финансовой книги</span><select value={productId} onChange={e => setChosen(e.target.value)}>{products.data?.map(p => <option key={p.id} value={p.id}>{p.name}</option>)}</select></label><label className="field"><span>Период</span><input type="month" value={period} onChange={e => setPeriod(e.target.value)} required /></label></div>
      {productId && period && <EconomicsBook key={`${productId}/${period}`} productId={productId} period={period} products={products.data ?? []} />}
    </>}
  </section>
}

function EconomicsBook({ productId, period, products }: { productId: string; period: string; products: Schema['Product'][] }) {
  const qc = useQueryClient(), path = { productId, period }
  const [version, setVersion] = useState(0), [compare, setCompare] = useState(0), [filterProduct, setFilterProduct] = useState(''), [team, setTeam] = useState('')
  const [tab, setTab] = useState<'report' | 'import' | 'rules' | 'source'>('report')
  const names = new Map(products.map(p => [p.id, p.name]))
  const history = useQuery({ queryKey: ['economics', productId, period, 'history'], queryFn: async () => unwrap(await api.GET('/products/{productId}/economics/{period}/versions', { params: { path } })) })
  const selected = version || history.data?.at(-1)?.version || 0
  const latest = history.data?.at(-1)?.version || 0
  const snapshot = useQuery({ queryKey: ['economics', productId, period, selected, 'snapshot'], enabled: selected > 0, queryFn: async () => unwrap(await api.GET('/products/{productId}/economics/{period}', { params: { path, query: { version: selected } } })) })
  const report = useQuery({ queryKey: ['economics', productId, period, selected, filterProduct, team, 'report'], enabled: selected > 0, queryFn: async () => unwrap(await api.GET('/products/{productId}/economics/{period}/report', { params: { path, query: { version: selected, filter_product_id: filterProduct || undefined, team: team || undefined } } })) })
  const previous = useQuery({ queryKey: ['economics', productId, period, compare, filterProduct, team, 'compare'], enabled: compare > 0, queryFn: async () => unwrap(await api.GET('/products/{productId}/economics/{period}/report', { params: { path, query: { version: compare, filter_product_id: filterProduct || undefined, team: team || undefined } } })) })
  const changed = async () => { setVersion(0); await qc.invalidateQueries({ queryKey: ['economics', productId, period] }) }
  const close = useMutation({ mutationFn: async () => unwrap(await api.POST('/products/{productId}/economics/{period}/close', { params: { path }, body: { expected_version: latest } })), onSuccess: changed })
  const exportCSV = useMutation({ mutationFn: async () => {
    const blob = unwrap(await api.GET('/products/{productId}/economics/{period}/export', { params: { path, query: { version: selected, filter_product_id: filterProduct || undefined, team: team || undefined } }, parseAs: 'blob' }))
    download(blob, `metis-pnl-${period}-v${selected}.csv`)
  } })
  return <>
    {history.isPending && <Loading />}{history.isError && <ErrorBox error={history.error} />}
    <div className="row"><button className={`btn ${tab === 'report' ? 'btn-primary' : ''}`} onClick={() => setTab('report')}>P&amp;L и сценарии</button><button className={`btn ${tab === 'import' ? 'btn-primary' : ''}`} onClick={() => setTab('import')}>Загрузить данные</button><button className={`btn ${tab === 'rules' ? 'btn-primary' : ''}`} disabled={!selected} onClick={() => setTab('rules')}>Правила и показатели</button><button className={`btn ${tab === 'source' ? 'btn-primary' : ''}`} disabled={!selected} onClick={() => setTab('source')}>Исходные строки</button></div>
    {latest > 0 && <div className="card row"><label className="field"><span>Версия данных</span><select value={version} onChange={e => setVersion(Number(e.target.value))}><option value={0}>Последняя · v{latest}</option>{history.data?.map(v => <option key={v.id} value={v.version}>v{v.version} · {v.closed ? 'закрыта' : 'открыта'} · {fmtDateTime(v.created_at)}</option>)}</select></label><label className="field"><span>Сравнить с версией</span><select value={compare} onChange={e => setCompare(Number(e.target.value))}><option value={0}>Без сравнения</option>{history.data?.filter(v => v.version !== selected).map(v => <option key={v.id} value={v.version}>v{v.version}</option>)}</select></label><Badge tone={snapshot.data?.closed ? 'ok' : 'warn'}>{snapshot.data?.closed ? 'Период закрыт' : 'Период открыт'}</Badge>{!snapshot.data?.closed && selected === latest && <button className="btn" disabled={close.isPending} onClick={() => close.mutate()}>Закрыть период</button>}</div>}
    {close.isError && <ErrorBox error={close.error} />}{snapshot.isError && <ErrorBox error={snapshot.error} />}
    {tab === 'import' && <ImportForm productId={productId} period={period} expectedVersion={latest} closed={history.data?.at(-1)?.closed ?? false} onDone={async () => { await changed(); setTab('report') }} />}
    {tab === 'rules' && snapshot.data && <RulesForm key={snapshot.data.version} snapshot={snapshot.data} products={products} latest={latest} onDone={changed} />}
    {tab === 'source' && snapshot.data && <SourceTable snapshot={snapshot.data} names={names} />}
    {tab === 'report' && <>
      {!latest && !history.isPending && <Empty text="Финансовых данных за этот месяц ещё нет. Загрузите CSV/XLSX или подключите загрузку из 1С." />}
      {latest > 0 && <div className="row"><label className="field"><span>Срез продукта</span><select value={filterProduct} onChange={e => setFilterProduct(e.target.value)}><option value="">Все продукты книги</option>{products.map(p => <option key={p.id} value={p.id}>{p.name}</option>)}</select></label><label className="field"><span>Команда</span><select value={team} onChange={e => setTeam(e.target.value)}><option value="">Все команды</option>{[...new Set(snapshot.data?.rows.map(r => r.team_id).filter(Boolean) ?? [])].map(t => <option key={t} value={t}>{t}</option>)}</select></label><button className="btn" disabled={!report.data || exportCSV.isPending} onClick={() => exportCSV.mutate()}>Экспорт CSV</button></div>}
      {report.isFetching && <Loading />}{report.isError && <ErrorBox error={report.error} />}{exportCSV.isError && <ErrorBox error={exportCSV.error} />}{previous.isError && <ErrorBox error={previous.error} />}
      {report.data && <ReportView report={report.data} names={names} comparison={compare ? previous.data : undefined} />}
      {report.data && <ScenarioForm key={`${selected}/${filterProduct}/${team}`} productId={productId} period={period} report={report.data} filterProduct={filterProduct} team={team} fields={snapshot.data?.fields ?? []} names={names} />}
    </>}
  </>
}

function ImportForm({ productId, period, expectedVersion, closed, onDone }: { productId: string; period: string; expectedVersion: number; closed: boolean; onDone: () => Promise<void> }) {
  const [template, setTemplate] = useState<Template>(initialTemplate), [file, setFile] = useState<File | null>(null), [name, setName] = useState('Основной'), [recalculate, setRecalculate] = useState(false)
  const [prepared, setPrepared] = useState<Schema['FinanceFileInput'] | null>(null)
  const qc = useQueryClient(), path = { productId, period }
  const templates = useQuery({ queryKey: ['finance-templates', productId], queryFn: async () => unwrap(await api.GET('/products/{productId}/finance-templates', { params: { path: { productId } } })) })
  const preview = useMutation({ mutationFn: async () => {
    if (!file) throw new Error('Выберите файл CSV или XLSX')
    if (file.size > 8 * 1024 * 1024) throw new Error('Максимальный размер файла — 8 МиБ')
    const data = await fileBase64(file)
    const body = { filename: file.name, content_base64: data, template: { ...template, columns: Object.fromEntries(Object.entries(template.columns).filter(([, v]) => v.trim())) }, expected_version: expectedVersion, recalculate }
    const result = unwrap(await api.POST('/products/{productId}/economics/{period}/preview', { params: { path }, body }))
    setPrepared(body)
    return result
  } })
  const save = useMutation({ mutationFn: async () => { if (!prepared) throw new Error('Сначала выполните предпросмотр'); return unwrap(await api.POST('/products/{productId}/economics/{period}/import', { params: { path }, body: { ...prepared, expected_version: expectedVersion, recalculate } })) }, onSuccess: onDone })
  const saveTemplate = useMutation({ mutationFn: async () => unwrap(await api.PUT('/products/{productId}/finance-templates', { params: { path: { productId } }, body: { product_id: productId, name, template: { ...template, columns: Object.fromEntries(Object.entries(template.columns).filter(([, v]) => v.trim())) }, updated_at: new Date().toISOString() } })), onSuccess: async () => { await qc.invalidateQueries({ queryKey: ['finance-templates', productId] }) } })
  const updateTemplate = (next: Template) => { setTemplate(next); setPrepared(null); preview.reset() }
  const updateColumn = (key: string, value: string) => {
    const mapped = Object.keys(template.columns).length ? { ...template.columns } : { product_id: 'product_id', period: 'period', category: 'category', amount: 'amount', currency: 'currency' } as Record<string, string>
    mapped[key] = value
    if (value.trim() && key === 'amount_minor') delete mapped.amount
    if (value.trim() && key === 'amount') delete mapped.amount_minor
    updateTemplate({ ...template, columns: mapped })
  }
  return <div className="card stack"><h2>Импорт финансового периода</h2><p className="muted">CSV или XLSX до 8 МиБ. Выберите один период и валюту. amount использует RUB/USD/EUR/GBP/CNY с двумя знаками; для других валют задайте целое amount_minor. Конвертации валют нет. Формулы XLSX читаются по сохранённым значениям. Загрузка с ошибками не фиксируется.</p>
    <div className="row"><label className="field"><span>Сохранённый шаблон</span><select onChange={e => { const t = templates.data?.find(x => x.name === e.target.value); if (t) { updateTemplate(t.template); setName(t.name) } }} defaultValue=""><option value="">Новый шаблон</option>{templates.data?.map(t => <option key={t.name}>{t.name}</option>)}</select></label><label className="field"><span>Название шаблона</span><input value={name} onChange={e => setName(e.target.value)} maxLength={128} /></label><button className="btn" disabled={!name.trim() || saveTemplate.isPending} onClick={() => saveTemplate.mutate()}>Сохранить шаблон</button><button className="btn" onClick={() => download(new Blob([`product_id,period,category,amount,currency,team_id,headcount\n${productId},${period},revenue,1000000.00,RUB,,\n${productId},${period},payroll,400000.00,RUB,team-example,6\n`], { type: 'text/csv' }), 'finance-example.csv')}>Пример CSV</button></div>
    {templates.isError && <ErrorBox error={templates.error} />}{saveTemplate.isError && <ErrorBox error={saveTemplate.error} />}{saveTemplate.isSuccess && <p className="muted">Шаблон сохранён</p>}
    <div className="row"><label className="field"><span>Файл</span><input type="file" accept=".csv,.xlsx" onChange={e => { setFile(e.target.files?.[0] ?? null); setPrepared(null); preview.reset() }} /></label><label className="field"><span>Лист XLSX (пусто — первый)</span><input value={template.sheet} onChange={e => updateTemplate({ ...template, sheet: e.target.value })} /></label><label className="field"><span>Строка заголовка</span><input type="number" min={1} max={10000} value={template.header_row} onChange={e => updateTemplate({ ...template, header_row: Number(e.target.value) })} /></label><label className="field"><span>Разделитель CSV</span><select value={template.delimiter} onChange={e => updateTemplate({ ...template, delimiter: e.target.value })}><option value=",">Запятая</option><option value=";">Точка с запятой</option><option value={'\t'}>Табуляция</option></select></label></div>
    <details><summary>Соответствие колонок</summary><p className="muted">Пустой маппинг автоматически читает стандартные заголовки — подходит для примера CSV. При изменении задайте обязательные колонки продукта, периода, статьи, суммы и валюты; ненужные колонки оставьте пустыми. ФОТ требует team_id и headcount. Продукт задаётся UUID из карточки продукта.</p><div className="finance-grid">{Object.entries(columns).map(([key, label]) => <label className="field" key={key}><span>{label}</span><input value={template.columns[key] ?? ''} placeholder={key} onChange={e => updateColumn(key, e.target.value)} /></label>)}</div></details>
    {closed && <label><input type="checkbox" checked={recalculate} onChange={e => setRecalculate(e.target.checked)} /> Явно пересчитать закрытый период новой версией</label>}
    <div className="row"><button className="btn" disabled={!file || preview.isPending} onClick={() => preview.mutate()}>Предпросмотр</button><button className="btn btn-primary" disabled={!prepared || !preview.data?.rows.length || !!preview.data?.errors.length || save.isPending || (closed && !recalculate)} onClick={() => save.mutate()}>Сохранить версию {expectedVersion + 1}</button></div>
    {preview.isError && <ErrorBox error={preview.error} />}{save.isError && <ErrorBox error={save.error} />}
    {preview.data && <><p>Корректных строк: {preview.data.rows.length}. Ошибок: {preview.data.errors.length}.</p>{preview.data.errors.slice(0, 50).map((e, i) => <div key={i} className="error-box">Строка {e.row}, {e.field}: {e.message}</div>)}<div className="table-wrap"><table className="table"><thead><tr><th>Строка</th><th>Статья</th><th>Команда</th><th>Сумма</th></tr></thead><tbody>{preview.data.rows.slice(0, 50).map((r, i) => <tr key={i}><td>{r.source.row}</td><td>{categories[r.category] ?? r.category}</td><td>{r.team_id || '—'}</td><td>{financeMoney(r.amount)}</td></tr>)}</tbody></table></div><p className="muted">Показаны первые 50 строк. История прежних загрузок сохранится. После повторного импорта проверьте распределение и ручные значения новых строк.</p></>}
  </div>
}

function RulesForm({ snapshot, products, latest, onDone }: { snapshot: Snapshot; products: Schema['Product'][]; latest: number; onDone: () => Promise<void> }) {
  const [fields, setFields] = useState<Field[]>(snapshot.fields ?? []), [rules, setRules] = useState<Record<string, Rule>>({}), [selected, setSelected] = useState(snapshot.rows[0]?.id ?? ''), [recalculate, setRecalculate] = useState(false)
  const row = snapshot.rows.find(r => r.id === selected)
  const allocations = rules[selected]?.allocations ?? row?.allocations ?? []
  const setAllocations = (next: Schema['EconomicsAllocation'][]) => setRules(old => ({ ...old, [selected]: { ...old[selected], row_id: selected, allocations: next } }))
  const save = useMutation({ mutationFn: async () => unwrap(await api.PUT('/products/{productId}/economics/{period}/configuration', { params: { path: { productId: snapshot.product_id, period: snapshot.period } }, body: { expected_version: snapshot.version, recalculate, fields, rows: Object.values(rules) } })), onSuccess: onDone })
  const worklogs = useMutation({ mutationFn: async () => unwrap(await api.POST('/products/{productId}/economics/{period}/worklog-shares', { params: { path: { productId: snapshot.product_id, period: snapshot.period } }, body: { expected_version: snapshot.version, recalculate } })), onSuccess: onDone })
  return <div className="card stack"><h2>Правила и показатели · v{snapshot.version}</h2><p className="muted">Правила действуют с выбранного периода. Источник сохраняет исходные суммы; формулы и доли записываются новой версией. Сумма долей должна равняться 1.</p>
    <label className="field"><span>Строка для распределения</span><select value={selected} onChange={e => setSelected(e.target.value)}>{snapshot.rows.map((r, i) => <option key={r.id} value={r.id}>{i + 1}. {categories[r.category]} · {r.team_id || r.description || 'Без команды'} · {financeMoney(r.amount)}</option>)}</select></label>
    {allocations.map((a, i) => <div className="row" key={i}><label className="field"><span>Продукт</span><select value={a.product_id} onChange={e => setAllocations(allocations.map((v, n) => n === i ? { ...v, product_id: e.target.value } : v))}>{products.map(p => <option key={p.id} value={p.id}>{p.name}</option>)}</select></label><label className="field"><span>Доля (например 0.6)</span><input value={a.share} onChange={e => setAllocations(allocations.map((v, n) => n === i ? { ...v, share: e.target.value } : v))} /></label><button className="btn" onClick={() => setAllocations(allocations.filter((_, n) => n !== i))}>Убрать</button></div>)}
    <button className="btn" onClick={() => setAllocations([...allocations, { product_id: products[0]?.id ?? snapshot.product_id, share: allocations.length ? '0' : '1' }])}>Добавить долю</button>
    <div className="row"><button className="btn" disabled={worklogs.isPending || snapshot.version !== latest || (snapshot.closed && !recalculate)} onClick={() => worklogs.mutate()}>Распределить по Jira worklogs</button><span className="muted">Требуются актуальная проекция Jira и соответствие сотрудников командам. Все командные затраты получат доли за выбранный месяц.</span></div>{worklogs.isError && <ErrorBox error={worklogs.error} />}
    <h3>Поля и формулы</h3><p className="muted">Доступны revenue, payroll, direct_cost, marketing, hub_cost, certification_cost, maintenance_cost, direct_profit, loaded_profit и ключи других полей. Арифметика, min/max и условия ifgt/ifge/ifeq. Денежные поля и результаты формул — в копейках.</p>
    {fields.map((field, i) => <div className="card stack" key={i}><div className="row"><label className="field"><span>Ключ</span><input value={field.key} onChange={e => setFields(fields.map((f, n) => n === i ? { ...f, key: e.target.value } : f))} /></label><label className="field"><span>Название</span><input value={field.name} onChange={e => setFields(fields.map((f, n) => n === i ? { ...f, name: e.target.value } : f))} /></label><label className="field"><span>Тип</span><select value={field.type} onChange={e => setFields(fields.map((f, n) => n === i ? { ...f, type: e.target.value as Field['type'] } : f))}>{Object.entries({ money: 'Деньги', number: 'Число', percent: 'Процент', date: 'Дата', catalog: 'Справочник' }).map(([k, v]) => <option key={k} value={k}>{v}</option>)}</select></label><label className="field"><span>Источник</span><select value={field.source} onChange={e => setFields(fields.map((f, n) => n === i ? { ...f, source: e.target.value as Field['source'], formula: '' } : f))}><option value="manual">Ручной ввод</option><option value="calculated">Расчёт</option></select></label><button className="btn" onClick={() => setFields(fields.filter((_, n) => n !== i))}>Удалить поле</button></div>{field.source === 'calculated' ? <label className="field"><span>Формула</span><input className="mono" value={field.formula ?? ''} onChange={e => setFields(fields.map((f, n) => n === i ? { ...f, formula: e.target.value } : f))} /></label> : field.source === 'manual' && row && <label className="field"><span>Значение для выбранной строки</span><input value={rules[selected]?.values?.[field.key] ?? row.values?.[field.key] ?? ''} onChange={e => setRules(old => ({ ...old, [selected]: { row_id: selected, allocations, values: { ...old[selected]?.values, [field.key]: e.target.value } } }))} /></label>}</div>)}
    <button className="btn" onClick={() => setFields([...fields, { key: '', name: '', type: 'money', source: 'calculated', formula: '' }])}>Добавить показатель</button>
    {snapshot.closed && <label><input type="checkbox" checked={recalculate} onChange={e => setRecalculate(e.target.checked)} /> Пересчитать закрытый период новой версией</label>}
    {snapshot.version !== latest && <p className="muted">Для изменения откройте последнюю версию.</p>}
    <button className="btn btn-primary" disabled={save.isPending || snapshot.version !== latest || (snapshot.closed && !recalculate)} onClick={() => save.mutate()}>Сохранить новую версию</button>{save.isError && <ErrorBox error={save.error} />}
  </div>
}

function ReportView({ report: r, names, comparison }: { report: Report; names: Map<string, string>; comparison?: Report }) {
  return <div className="stack"><div className="stats">{[['Выручка', r.total.revenue], ['Прямые затраты', r.total.direct_cost], ['Нагрузка хаба', r.total.hub_cost], ['Прибыль с хабом', r.total.loaded_profit]].map(([label, value]) => <div className="stat" key={String(label)}><div className="stat-value">{money(Number(value), r.currency)}</div><div className="stat-label">{label}</div></div>)}</div>
    {comparison && <p className="card">Изменение прибыли относительно v{comparison.version}: <strong>{money(r.total.loaded_profit - comparison.total.loaded_profit, r.currency)}</strong></p>}
    <div className="card stack"><h2>{r.scenario ? 'P&L сценария' : 'P&L по продуктам'}</h2><div className="table-wrap"><table className="table"><thead><tr><th>Продукт</th><th>Выручка</th><th>Прямые затраты</th><th>Прямая прибыль</th><th>Нагрузка хаба</th><th>Прибыль с хабом</th></tr></thead><tbody>{r.products.map(p => <tr key={p.product_id}><td>{names.get(p.product_id) ?? p.product_id}</td>{[p.revenue, p.direct_cost, p.direct_profit, p.hub_cost, p.loaded_profit].map((v, i) => <td className="num" key={i}>{money(v, r.currency)}</td>)}</tr>)}</tbody></table></div></div>
    {!!r.teams?.length && <div className="card"><h2>Команда × продукт</h2><div className="table-wrap"><table className="table"><thead><tr><th>Команда</th><th>Численность</th><th>Продукт</th><th>Затраты</th></tr></thead><tbody>{r.teams.map((t, i) => <tr key={i}><td>{t.team_id}</td><td>{t.headcount}</td><td>{names.get(t.product_id) ?? t.product_id}</td><td>{money(t.cost, r.currency)}</td></tr>)}</tbody></table></div></div>}
    {!!r.investments?.length && <div className="card"><h2>Фичи, сертификация и ветки</h2><div className="table-wrap"><table className="table"><thead><tr><th>Измерение</th><th>Ссылка</th><th>Продукт</th><th>Выручка</th><th>Затраты</th><th>Баланс</th></tr></thead><tbody>{r.investments.map((v, i) => <tr key={i}><td>{{ feature: 'Фича', certification: 'Трек сертификации', branch: 'Ветка' }[v.kind] ?? v.kind}</td><td>{v.key}</td><td>{names.get(v.product_id) ?? v.product_id}</td><td>{money(v.revenue, r.currency)}</td><td>{money(v.cost, r.currency)}</td><td>{money(v.balance, r.currency)}</td></tr>)}</tbody></table></div></div>}
    {r.products.some(p => Object.keys(p.metrics ?? {}).length > 0) && <div className="card stack"><h2>Расчётные показатели и источники</h2>{r.products.map(p => <div key={p.product_id}><h3>{names.get(p.product_id) ?? p.product_id}</h3>{Object.entries(p.metrics ?? {}).map(([k, v]) => <details key={k}><summary><strong>{k}: {v}</strong></summary><p className="mono">{p.lineage?.[k]?.formula || 'Агрегат входных значений'}</p><p>Зависимости: {p.lineage?.[k]?.dependencies?.join(', ') || '—'}</p>{p.lineage?.[k]?.sources?.slice(0, 30).map((s, i) => <div key={i}>{s.file} · {s.sheet || 'CSV'} · строка {s.row} <span className="mono muted">{s.hash.slice(0, 12)}</span></div>)}</details>)}</div>)}</div>}
  </div>
}

function ScenarioForm({ productId, period, report, filterProduct, team, fields, names }: { productId: string; period: string; report: Report; filterProduct: string; team: string; fields: Field[]; names: Map<string, string> }) {
  const [field, setField] = useState('revenue'), [value, setValue] = useState(''), [inputError, setInputError] = useState('')
  const selectedProduct = filterProduct || (report.products.length === 1 ? report.products[0].product_id : '')
  const numericFields = fields.filter(f => f.source !== 'calculated' && ['money', 'number', 'percent'].includes(f.type))
  const isMoney = field in categories || numericFields.find(f => f.key === field)?.type === 'money'
  const scenario = useMutation({ mutationFn: async (override: string) => unwrap(await api.POST('/products/{productId}/economics/{period}/scenario', { params: { path: { productId, period } }, body: { version: report.version, filter_product_id: selectedProduct, team: team || undefined, overrides: { [field]: override } } })) })
  const submit = (e: FormEvent) => { e.preventDefault(); try { const v = isMoney && majorCurrencies.has(report.currency) ? String(parseMinorUnits(value)) : value; setInputError(''); scenario.mutate(v) } catch (err) { setInputError(errorMessage(err)) } }
  return <div className="card stack"><h2>Сценарий «что если»</h2><p className="muted">Подменяет вход выбранного продукта в том же вычислителе. Фактическая версия {report.version} остаётся прежней.</p>{!selectedProduct ? <p>Выберите один продукт в фильтре для расчёта сценария.</p> : <form className="row" onSubmit={submit}><label className="field"><span>Входной показатель</span><select value={field} onChange={e => { setField(e.target.value); scenario.reset() }}>{Object.entries(categories).map(([k, v]) => <option key={k} value={k}>{v}</option>)}{numericFields.map(f => <option key={f.key} value={f.key}>{f.name}</option>)}</select></label><label className="field"><span>Новое значение {isMoney ? majorCurrencies.has(report.currency) ? `(${report.currency})` : `(мин. ед. ${report.currency})` : ''}</span><input value={value} onChange={e => setValue(e.target.value)} required /></label><button className="btn" disabled={scenario.isPending}>Рассчитать</button></form>}{inputError && <p className="error-box">{inputError}</p>}{scenario.isError && <ErrorBox error={scenario.error} />}{scenario.data && <ReportView report={scenario.data} names={names} comparison={report} />}</div>
}

function SourceTable({ snapshot, names }: { snapshot: Snapshot; names: Map<string, string> }) {
  return <div className="card stack"><h2>Исходные строки · v{snapshot.version}</h2><p className="muted">{snapshot.source} · {snapshot.created_by} · {fmtDateTime(snapshot.created_at)}. Всего {snapshot.rows.length}; показаны первые 200.</p><div className="table-wrap"><table className="table"><thead><tr><th>Продукт</th><th>Статья</th><th>Команда</th><th>Сумма</th><th>Распределение</th><th>Источник</th></tr></thead><tbody>{snapshot.rows.slice(0, 200).map(r => <tr key={r.id}><td>{names.get(r.product_id) ?? r.product_id}</td><td>{categories[r.category]}</td><td>{r.team_id || '—'}</td><td>{financeMoney(r.amount)}</td><td>{r.allocations?.map(a => `${names.get(a.product_id) ?? a.product_id}: ${a.share}`).join('; ') || 'Прямое'}</td><td>{r.source.file} · {r.source.sheet || 'CSV'} · {r.source.row}</td></tr>)}</tbody></table></div><p className="mono muted">SHA-256: {snapshot.source_hash}</p></div>
}

function download(blob: Blob, filename: string) { const url = URL.createObjectURL(blob); const link = document.createElement('a'); link.href = url; link.download = filename; link.click(); URL.revokeObjectURL(url) }
function fileBase64(file: File): Promise<string> { return new Promise((resolve, reject) => { const reader = new FileReader(); reader.onload = () => resolve(String(reader.result).split(',')[1]); reader.onerror = () => reject(new Error('Не удалось прочитать файл')); reader.readAsDataURL(file) }) }
