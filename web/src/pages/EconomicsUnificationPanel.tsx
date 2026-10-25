import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useProducts } from '../api/hooks'
import { api, unwrap } from '../api/client'
import type { components } from '../api/schema'
import { fmtMoney } from '../lib/format'
import { Empty, ErrorBox, Loading } from '../components/Status'

type S = components['schemas']
const money = (value?: S['Money']) => !value ? '—' : Number.isSafeInteger(value.amount) ? fmtMoney(value) : 'Точное значение в плане JSON'

type Plan = { input: S['HistoryUnificationRequest']; result: S['HistoryUnificationPreview'] }

export function EconomicsUnificationPanel({ productId, period }: { productId: string; period: string }) {
  const qc = useQueryClient(), path = { productId, period }, products = useProducts()
  const names = new Map(products.data?.map(p => [p.id, p.name]) ?? [])
  const [plan, setPlan] = useState<Plan | null>(null), [confirmed, setConfirmed] = useState(false)
  const books = useQuery({ queryKey: ['economics', productId, period, 'history'], queryFn: async () => unwrap(await api.GET('/products/{productId}/economics/{period}/versions', { params: { path } })) })
  const imports = useQuery({ queryKey: ['economics-model', 'imports', period], queryFn: async () => unwrap(await api.GET('/economics/imports', { params: { query: { period } } })) })
  const latestBook = books.data?.at(-1)?.version ?? 0
  const modelVersion = imports.data?.filter(b => b.status === 'applied').reduce((max, b) => Math.max(max, b.data_version ?? 0), 0) ?? 0
  const preview = useMutation({ mutationFn: async () => {
    setPlan(null); setConfirmed(false)
    const input = { expected_book_version: latestBook, expected_model_version: modelVersion }
    const result = unwrap(await api.POST('/products/{productId}/economics/{period}/unification/preview', { params: { path }, body: input }))
    return { input: { ...input, digest: result.digest }, result }
  }, onSuccess: setPlan })
  const apply = useMutation({ mutationFn: async () => {
    if (!plan || !confirmed) throw new Error('Сначала проверьте и подтвердите план объединения')
    return unwrap(await api.POST('/products/{productId}/economics/{period}/unification/apply', { params: { path }, body: plan.input }))
  }, onSuccess: async () => { await qc.invalidateQueries() } })
  const downloadPlan = useMutation({ mutationFn: async () => {
    if (!plan) throw new Error('Сначала получите план объединения')
    const blob = unwrap(await api.POST('/products/{productId}/economics/{period}/unification/preview', { params: { path }, body: plan.input, parseAs: 'blob' }))
    const metadata = JSON.parse(await blob.text()) as { digest?: string }
    if (metadata.digest !== plan.result.digest) throw new Error('Данные изменились. Повторите предпросмотр объединения.')
    const url = URL.createObjectURL(blob), link = document.createElement('a')
    link.href = url; link.download = `metis-unification-${period}.json`; link.click(); URL.revokeObjectURL(url)
  } })
  const archive = useMutation({ mutationFn: async () => {
    const blob = unwrap(await api.GET('/products/{productId}/economics/{period}/unification/archive', { params: { path }, parseAs: 'blob' }))
    const url = URL.createObjectURL(blob), link = document.createElement('a')
    link.href = url; link.download = `metis-history-${period}.json`; link.click(); URL.revokeObjectURL(url)
  } })
  return <div className="card stack"><h2>Объединение финансовой истории</h2>
    <p>Выбранная книга станет единственным источником текущих фактов за {period}. Отчёты модели будут читать её данные и распределение. Исторические версии обоих контуров сохранятся в архиве.</p>
    <p className="muted">Объединение выполняется в PostgreSQL. Для него нужны finance/full и доступ ко всем продуктам. После переключения отдельный импорт модели за этот месяц отключён, а ручной ввод общих полей записывается в книгу; будущие изменения книги сразу видны в общих расчётах. Факты разных источников не складываются автоматически.</p>
    {(books.isPending || imports.isPending) && <Loading />}{books.isError && <ErrorBox error={books.error} />}{imports.isError && <ErrorBox error={imports.error} />}
    {!books.isPending && !latestBook && <Empty text="Сначала создайте финансовую книгу за выбранный месяц." />}
    <div className="row"><button className="btn" disabled={!latestBook || books.isPending || imports.isPending || books.isError || imports.isError || preview.isPending || apply.isPending} onClick={() => { apply.reset(); preview.mutate() }}>Проверить объединение</button><button className="btn" disabled={archive.isPending || apply.isPending} onClick={() => archive.mutate()}>Скачать архив объединения</button></div>
    {preview.isError && <ErrorBox error={preview.error} />}{apply.isError && <ErrorBox error={apply.error} />}{archive.isError && <ErrorBox error={archive.error} />}{downloadPlan.isError && <ErrorBox error={downloadPlan.error} />}
    {plan && <div className="stack"><p>Версий книги: {plan.result.book_versions}. Версий данных модели: {plan.result.model_versions}. Каноническая книга: v{plan.result.canonical_version}.</p>
      {!!plan.result.excluded_model_fields?.length && <p className="error-box">В выбранной книге нет отдельных входов прежней модели: {plan.result.excluded_model_fields.join(', ')}. Они сохранятся в архиве и не войдут в текущий расчёт.</p>}
      <div className="table-wrap"><table className="table"><caption>Сверка прибыли с хабом</caption><thead><tr><th>Продукт</th><th>Прежняя модель</th><th>Каноническая книга</th></tr></thead><tbody>{Array.from(new Set([...(plan.result.previous_pnl ?? []).map(p => p.product_id), ...(plan.result.canonical_pnl ?? []).map(p => p.product_id)])).map(id => <tr key={id}><td>{names.get(id) ?? id}</td><td>{money(plan.result.previous_pnl?.find(p => p.product_id === id)?.loaded_profit)}</td><td>{money(plan.result.canonical_pnl?.find(p => p.product_id === id)?.loaded_profit)}</td></tr>)}</tbody></table></div>
      <div><button className="btn" disabled={downloadPlan.isPending} onClick={() => downloadPlan.mutate()}>Скачать точный план JSON</button></div>
      {plan.result.warnings.map((warning, i) => <p className="muted" key={i}>{{ 'book becomes the sole current fact source; original model versions remain immutable': 'Книга становится единственным текущим источником фактов. Прежние версии модели остаются неизменяемыми.', 'legacy model writes for this month are disabled': 'Отдельный импорт прежней модели за этот месяц отключён. Ручной ввод общих полей записывается в книгу.', 'canonical revenue includes the bundle component; do not add bundle_revenue again in common formulas': 'Каноническая выручка уже включает выручку пакетов. Повторное добавление bundle_revenue в общей формуле удвоит эту часть.', 'canonical book data requires finance/full; aggregate-only legacy access is retained only for unbound months': 'Для данных общей книги нужны права finance/full. Прежний доступ только к агрегатам действует для необъединённых месяцев.', 'canonical owner already selected; archived originals remain immutable': 'Канонический источник уже выбран; архив исходных данных сохранён.' }[warning] ?? warning}</p>)}
      {!!Object.keys(plan.result.definition_mappings ?? {}).length && <div className="table-wrap"><table className="table"><caption>Определения книги в общем конструкторе</caption><thead><tr><th>Исходный ключ</th><th>Ключ общего определения</th></tr></thead><tbody>{Object.entries(plan.result.definition_mappings ?? {}).map(([key, value]) => <tr key={key}><td>{key}</td><td className="mono">{value}</td></tr>)}</tbody></table></div>}
      <p className="muted">Контрольная сумма плана: <code style={{ overflowWrap: 'anywhere' }}>{plan.result.digest}</code></p>
      {!plan.result.applied && !apply.isSuccess && <><label><input type="checkbox" checked={confirmed} disabled={apply.isPending} onChange={e => setConfirmed(e.target.checked)} /> Подтверждаю выбор книги единственным текущим источником. Проверил расхождения и сохранение исходной истории.</label><div><button className="btn btn-primary" disabled={!confirmed || apply.isPending} onClick={() => apply.mutate()}>Объединить историю</button></div></>}
      {(plan.result.applied || apply.isSuccess) && <p role="status">История объединена. Текущие расчёты используют выбранную книгу.</p>}
    </div>}
  </div>
}
