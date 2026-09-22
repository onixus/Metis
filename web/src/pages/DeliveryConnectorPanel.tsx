import { useState, type FormEvent } from 'react'
import { useDeliveryConnector, useSaveDeliveryConnector, type DeliveryFieldMapping } from '../api/delivery'
import { useProducts } from '../api/hooks'
import type { Product } from '../api/types'
import { Badge, ErrorBox, Loading } from '../components/Status'
import { DeliverySyncStatus } from './DeliveryPage'

export function DeliveryConnectorPanel() {
  const connector = useDeliveryConnector()
  const products = useProducts()
  if (connector.isPending || products.isPending) return <Loading />
  if (connector.isError) return <ErrorBox error={connector.error} onRetry={() => void connector.refetch()} />
  if (products.isError) return <ErrorBox error={products.error} />
  const status = connector.data
  return <div className="stack">
    <div className="card stack"><div className="row wrap-row"><h2>Трекер поставки</h2><Badge tone={status.name === 'none' ? 'neutral' : 'info'}>{status.name === 'none' ? 'Отключён' : status.name}</Badge></div>
      <p>Привязано эпиков: {status.mapped_epics}. Недоставленных событий: <strong>{status.dlq_count}</strong>.</p>
      {status.dlq_count > 0 && <p className="alert alert-error">Есть события, исчерпавшие повторы. Проверьте worker и доступность внешних систем. Счётчик включает всю очередь приложения.</p>}
      <p className="muted">Адреса, секреты и поля исходной системы задаются в конфигурации подключения. Здесь назначаются доски продуктов; сохранение применяется при следующей фоновой сверке.</p>
    </div>
    <DeliverySyncStatus sync={status.sync} />
    <ConnectorForm key={JSON.stringify(status.mapping)} initial={status.mapping} products={products.data} />
  </div>
}

function ConnectorForm({ initial, products }: { initial: DeliveryFieldMapping; products: Product[] }) {
  const save = useSaveDeliveryConnector()
  const [project, setProject] = useState(initial.project)
  const [boards, setBoards] = useState<Record<string, string>>({ ...initial.boards })
  const listed = new Set(products.map((p) => p.id))
  const rows = [...products.map((p) => ({ id: p.id, name: p.name })), ...Object.keys(initial.boards).filter((id) => !listed.has(id)).map((id) => ({ id, name: `Продукт ${id}` }))]
  const submit = (e: FormEvent) => {
    e.preventDefault()
    // Preserve source fields/status mapping received from the server: this form only owns project/boards.
    save.mutate({ ...initial, project: project.trim(), boards: Object.fromEntries(Object.entries(boards).map(([id, board]) => [id, board.trim()]).filter(([, board]) => !!board)) })
  }
  return <form className="card stack" onSubmit={submit}>
    <h2>Проект и доски</h2>
    <label className="field"><span>Проект по умолчанию для форм администратора</span><input value={project} onChange={(e) => setProject(e.target.value)} maxLength={200} placeholder="PROJECT" /></label>
    <p className="muted">Пустая доска выключает её следующую сверку для продукта. Ранее сохранённые спринты остаются в последней проекции.</p>
    <div className="table-wrap"><table className="table"><thead><tr><th>Продукт</th><th>ID доски трекера</th></tr></thead><tbody>{rows.map((p) => <tr key={p.id}><td>{p.name}</td><td><input aria-label={`Доска: ${p.name}`} value={boards[p.id] ?? ''} onChange={(e) => setBoards((state) => ({ ...state, [p.id]: e.target.value }))} maxLength={200} placeholder="Например, 7" /></td></tr>)}</tbody></table></div>
    {save.isError && <ErrorBox error={save.error} />}
    {save.isSuccess && <p className="alert alert-ok">Настройки сохранены.</p>}
    <div><button className="btn btn-primary" disabled={save.isPending}>{save.isPending ? 'Сохраняется…' : 'Сохранить доски'}</button></div>
  </form>
}
