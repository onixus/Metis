import { useState } from 'react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { api, errorMessage, unwrap } from '../api/client'
import { accessLevel, keys, useContracts, useExpandedFeatures, useMe, useProducts } from '../api/hooks'
import type { components } from '../api/schema'
import type { Contract, Me, Product } from '../api/types'
import { ErrorBox, Loading } from './Status'
import { canWriteGraph } from '../lib/graph'
import { fmtDate } from '../lib/format'
import { ru } from '../i18n/ru'

type Input = components['schemas']['ContractInput']
type Pair = components['schemas']['VersionPair']
const empty: Input = { name: '', provider_product_id: '', consumer_product_id: '', criticality: 'blocks', status: 'draft', compatibility: [] }

function canManage(me: Me | undefined, provider: string, consumer: string) {
  return (canWriteGraph(me, provider) && accessLevel(me, consumer) !== 'none') ||
    (canWriteGraph(me, consumer) && accessLevel(me, provider) !== 'none')
}

/** PG-04: редактирование платформенных контрактов. Разрешения окончательно проверяет API. */
export function ContractsPanel({ productId }: { productId?: string }) {
  const contracts = useContracts()
  const products = useProducts()
  const me = useMe()
  const [editing, setEditing] = useState<Contract | 'new' | null>(null)
  if (contracts.isPending || products.isPending || me.isPending) return <Loading />
  if (contracts.isError || products.isError || me.isError) return <ErrorBox error={contracts.error ?? products.error ?? me.error} />
  const list = contracts.data.filter(c => !productId || c.provider_product_id === productId || c.consumer_product_id === productId)
  const name = (id: string) => products.data.find(p => p.id === id)?.name ?? id
  const canCreate = products.data.some(p => canWriteGraph(me.data, p.id))
  return <div className="card stack" id="integration-contracts">
    <div className="page-head"><h2>Контракты интеграции</h2>{canCreate && <button className="btn btn-sm" type="button" onClick={() => setEditing('new')}>Создать контракт</button>}</div>
    <p className="muted">Потребитель зависит от поставщика. Контракт связывает фичи обеих сторон и задаёт совместимость версий.</p>
    {editing && <ContractForm key={editing === 'new' ? 'new' : editing.id} initial={editing === 'new' ? undefined : editing} products={products.data} me={me.data} productId={productId} onClose={() => setEditing(null)} />}
    {!list.length && <p className="muted">Контрактов пока нет.</p>}
    {list.map(c => <details key={c.id} className="card">
      <summary>{c.name} · {ru.contract.statuses[c.status ?? 'draft']}</summary>
      <div className="stack">
        <p>{name(c.consumer_product_id)} → {name(c.provider_product_id)} · {ru.link.criticality[c.criticality]}</p>
        <p>Владелец: {c.owner || '—'} · Версия интерфейса: {c.interface_version || '—'} · Готовность: {fmtDate(c.ready_date)}</p>
        <p>Фич поставщика: {c.provider_feature_ids?.length ?? 0} · Фич потребителя: {c.consumer_feature_ids?.length ?? 0}</p>
        <div className="table-wrap"><table className="table"><thead><tr><th>Версия поставщика</th><th>Версия потребителя</th><th>Совместимы</th></tr></thead><tbody>
          {(c.compatibility ?? []).map((pair, i) => <tr key={i}><td>{pair.provider_version}</td><td>{pair.consumer_version}</td><td>{pair.compatible ? 'Да' : 'Нет'}</td></tr>)}
        </tbody></table></div>
        {canManage(me.data, c.provider_product_id, c.consumer_product_id) && <button className="btn btn-sm" type="button" onClick={() => setEditing(c)}>Изменить контракт</button>}
      </div>
    </details>)}
  </div>
}

function ContractForm({ initial, products, me, productId, onClose }: { initial?: Contract; products: Product[]; me?: Me; productId?: string; onClose: () => void }) {
  const [value, setValue] = useState<Input>(() => initial ? {
    name: initial.name, provider_product_id: initial.provider_product_id, consumer_product_id: initial.consumer_product_id,
    provider_feature_ids: [...(initial.provider_feature_ids ?? [])], consumer_feature_ids: [...(initial.consumer_feature_ids ?? [])],
    interface_version: initial.interface_version ?? '', owner: initial.owner ?? '', status: initial.status ?? 'draft',
    criticality: initial.criticality, compatibility: [...(initial.compatibility ?? [])],
  } : { ...empty, consumer_product_id: productId ?? '' })
  const [failure, setFailure] = useState('')
  const qc = useQueryClient()
  const save = useMutation({
    mutationFn: async (body: Input) => initial
      ? unwrap(await api.PUT('/contracts/{contractId}', { params: { path: { contractId: initial.id } }, body }))
      : unwrap(await api.POST('/contracts', { body })),
    onSuccess: async () => {
      await Promise.all([keys.contracts, keys.links, keys.hubs, keys.products].map(queryKey => qc.invalidateQueries({ queryKey })))
      onClose()
    },
  })
  const ids = [...new Set([value.provider_product_id, value.consumer_product_id].filter(Boolean))]
  const queries = useExpandedFeatures(ids)
  const fields = <K extends keyof Input>(key: K, next: Input[K]) => setValue(old => ({ ...old, [key]: next }))
  const pairChange = (index: number, patch: Partial<Pair>) => fields('compatibility', (value.compatibility ?? []).map((pair, i) => i === index ? { ...pair, ...patch } : pair))
  const allowed = value.provider_product_id !== value.consumer_product_id && canManage(me, value.provider_product_id, value.consumer_product_id)
  return <form className="stack" aria-label={initial ? 'Изменение контракта' : 'Новый контракт'} onSubmit={event => {
    event.preventDefault(); setFailure('')
    if (!allowed) { setFailure('Выберите разные продукты с правом записи хотя бы на одну сторону.'); return }
    void save.mutateAsync(value).catch(error => setFailure(errorMessage(error)))
  }}>
    <fieldset disabled={save.isPending} className="stack" style={{ minWidth: 0, border: 0, padding: 0, margin: 0 }}>
    <h3>{initial ? 'Изменение контракта' : 'Новый контракт'}</h3>
    <label className="field">Название контракта<input required value={value.name} onChange={e => fields('name', e.target.value)} /></label>
    {(['provider', 'consumer'] as const).map(side => {
      const key = `${side}_product_id` as const
      const featureKey = `${side}_feature_ids` as const
      const query = queries[ids.indexOf(value[key])]
      return <fieldset key={side} className="stack"><legend>{side === 'provider' ? 'Поставщик' : 'Потребитель'}</legend>
        <label className="field">{side === 'provider' ? 'Продукт-поставщик' : 'Продукт-потребитель'}<select required disabled={!!initial} value={value[key]} onChange={e => setValue(old => ({ ...old, [key]: e.target.value, [featureKey]: [] }))}>
          <option value="">Выберите продукт</option>{products.map(p => <option key={p.id} value={p.id}>{p.name}</option>)}
        </select></label>
        {query?.isPending && <Loading />}
        {query?.error && <ErrorBox error={query.error} />}
        {query?.data?.map(feature => <label className="check" key={feature.id}><input type="checkbox" checked={(value[featureKey] ?? []).includes(feature.id)} onChange={e => fields(featureKey, e.target.checked ? [...(value[featureKey] ?? []), feature.id] : (value[featureKey] ?? []).filter(id => id !== feature.id))} />{feature.name}</label>)}
      </fieldset>
    })}
    <label className="field">Версия интерфейса<input value={value.interface_version ?? ''} onChange={e => fields('interface_version', e.target.value)} /></label>
    <label className="field">Владелец контракта<input value={value.owner ?? ''} onChange={e => fields('owner', e.target.value)} /></label>
    <label className="field">Статус контракта<select value={value.status} onChange={e => fields('status', e.target.value as Input['status'])}>{(['draft', 'active', 'deprecated'] as const).map(s => <option key={s} value={s}>{ru.contract.statuses[s]}</option>)}</select></label>
    <label className="field">Критичность контракта<select value={value.criticality} onChange={e => fields('criticality', e.target.value as Input['criticality'])}>{(['blocks', 'accelerates', 'desirable'] as const).map(c => <option key={c} value={c}>{ru.link.criticality[c]}</option>)}</select></label>
    <fieldset className="stack"><legend>Матрица совместимости</legend>
      {(value.compatibility ?? []).map((pair, index) => <div className="row wrap-row" key={index}>
        <label className="field">Версия поставщика<input required value={pair.provider_version} onChange={e => pairChange(index, { provider_version: e.target.value })} /></label>
        <label className="field">Версия потребителя<input required value={pair.consumer_version} onChange={e => pairChange(index, { consumer_version: e.target.value })} /></label>
        <label className="check"><input type="checkbox" checked={pair.compatible} onChange={e => pairChange(index, { compatible: e.target.checked })} />Совместимы</label>
        <button className="btn btn-sm" type="button" aria-label={`Удалить пару версий ${index + 1}`} onClick={() => fields('compatibility', value.compatibility?.filter((_, i) => i !== index))}>Удалить пару</button>
      </div>)}
      <button className="btn btn-sm" type="button" onClick={() => fields('compatibility', [...(value.compatibility ?? []), { provider_version: '', consumer_version: '', compatible: true }])}>Добавить пару версий</button>
    </fieldset>
    {failure && <div className="alert error" role="alert">{failure}</div>}
    <div className="row"><button className="btn btn-primary" disabled={save.isPending || !allowed || queries.some(q => q.isPending || q.error)} type="submit">Сохранить контракт</button><button className="btn" type="button" disabled={save.isPending} onClick={onClose}>Отмена</button></div>
    </fieldset>
  </form>
}
