import type { components } from '../api/schema'
import { fmtDateTime } from '../lib/format'
import { formulaSources } from '../lib/economicsModel'

type S = components['schemas']

export function Explanation({ value }: { value: S['Explanation'] }) {
  const kind = value.kind === 'field' ? 'Поле' : value.kind === 'source' ? 'Прямой источник' : 'Показатель'
  return <details open><summary>{kind} <strong className="mono">{value.key}: {value.value}</strong></summary>
    {value.expression && <p className="mono">{value.expression}</p>}
    {value.inputs?.map((input, i) => <Explanation key={`${input.kind}/${input.key}/${i}`} value={input} />)}
    {value.sources?.map((source, i) => <div className="card stack" key={`${source.snapshot_id}/${source.kind}/${i}`}>
      <p><strong>{formulaSources[source.kind].name}</strong> · {source.currency ? `мин. ед. ${source.currency}` : formulaSources[source.kind].unit} · {source.period}</p>
      <p>Фактическое значение снимка: <strong className="mono">{source.captured_value}</strong>{source.overridden && <> · сценарная подмена: <strong className="mono">{value.value}</strong></>}</p>
      <p className="muted">Продукт: {source.product_id || 'все разрешённые'} · команда: {source.team_id || 'все'}{source.item ? ` · статья: ${source.item}` : ''}</p>
      <p className="muted">Снимок <code className="mono">{source.snapshot_id}</code> · зафиксирован {fmtDateTime(source.as_of)}</p>
      <details><summary>Происхождение снимка</summary><div className="table-wrap"><table className="table"><thead><tr><th>Источник</th><th>Снимок / агрегат</th><th>Версия</th></tr></thead><tbody>{source.provenance.map((p, n) => <tr key={`${p.source}/${p.record_id}/${n}`}><td>{p.source}</td><td className="mono">{p.record_id}</td><td className="mono">{p.version || '—'}</td></tr>)}</tbody></table></div></details>
    </div>)}
    {!!value.rows?.length && <div className="table-wrap"><table className="table"><thead><tr><th>Поле</th><th>Продукт / команда</th><th>Период</th><th>Значение</th><th>Источник</th></tr></thead><tbody>{value.rows.map((r, i) => <tr key={`${r.id}/${r.product_id}/${r.field_key}/${i}`}><td>{r.field_key}</td><td>{r.product_id || '—'} / {r.team_id || '—'}</td><td>{r.period}</td><td>{r.value}</td><td>{r.sheet || '—'} · строка {r.row ?? '—'} · загрузка {r.batch_id || '—'} · v{r.data_version ?? '—'}</td></tr>)}</tbody></table></div>}
  </details>
}
