import { useState } from 'react'
import { useMutation, useQuery } from '@tanstack/react-query'
import { api, unwrap } from '../api/client'
import { ErrorBox } from '../components/Status'
import { Explanation } from './EconomicsModelPanel'

export function BookMetricsPanel({ productId, period, version, filterProduct, team }: { productId: string; period: string; version: number; filterProduct: string; team: string }) {
  const [chosen, setChosen] = useState('')
  const metrics = useQuery({ queryKey: ['economics-model', 'metrics'], queryFn: async () => unwrap(await api.GET('/economics/metrics')) })
  const key = chosen || metrics.data?.[0]?.key || ''
  const calculate = useMutation({ mutationFn: async () => unwrap(await api.GET('/products/{productId}/economics/{period}/metrics/{metricKey}', { params: { path: { productId, period, metricKey: key }, query: { version, filter_product_id: filterProduct || undefined, team: team || undefined } } })) })
  return <div className="card stack"><h2>Формула модели на данных книги</h2>
    <p className="muted">Входы — выбранная версия книги v{version}, с её распределением затрат. Расчёт использует формулы конструктора, действующие на конец месяца. Исходные данные модели и книга сохраняются без изменений.</p>
    {metrics.isError && <ErrorBox error={metrics.error} />}
    {!metrics.isPending && !metrics.isError && !metrics.data?.length && <p>Создайте формулу в «Конструкторе модели», затем рассчитайте её на данных книги.</p>}
    {!!metrics.data?.length && <div className="row"><label className="field"><span>Формула конструктора</span><select value={key} onChange={e => { setChosen(e.target.value); calculate.reset() }}>{metrics.data.map(m => <option key={m.id} value={m.key}>{m.key}</option>)}</select></label><button className="btn" disabled={!key || calculate.isPending} onClick={() => calculate.mutate()}>Рассчитать на книге v{version}</button></div>}
    {calculate.isError && <ErrorBox error={calculate.error} />}{calculate.data && <><p>Источник: книга v{calculate.data.snapshot.version} · {calculate.data.snapshot.period} · {calculate.data.snapshot.currency}</p><Explanation value={calculate.data.metric} /></>}
  </div>
}
