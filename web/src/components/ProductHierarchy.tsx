import { useMemo, useState, type FormEvent } from 'react'
import { useFeatures } from '../api/hooks'
import { useCapabilities, useCreateCapability, useCreateRequirement, useRequirements } from '../api/portfolio'
import { groupPortfolioFeatures } from '../lib/portfolio'
import { ru } from '../i18n/ru'
import { Empty, ErrorBox, Loading } from './Status'

export function ProductHierarchy({ productId, canWrite }: { productId: string; canWrite: boolean }) {
  const capabilities = useCapabilities(productId)
  const features = useFeatures(productId)
  const create = useCreateCapability(productId)
  const [name, setName] = useState('')
  const [expanded, setExpanded] = useState<string | null>(null)
  const groups = useMemo(() => groupPortfolioFeatures(capabilities.data ?? [], features.data ?? []), [capabilities.data, features.data])
  const submit = (event: FormEvent) => {
    event.preventDefault()
    if (!name.trim()) return
    create.mutate(name.trim(), { onSuccess: () => setName('') })
  }
  return <div className="card stack" id="hierarchy">
    <h2>Возможности → фичи → требования</h2>
    <p className="muted">Возможности группируют фичи внутри продукта. Требования доступны в раскрытой фиче; возможность фичи задаётся при её создании или редактировании в бэклоге.</p>
    {(capabilities.isPending || features.isPending) && <Loading />}
    {capabilities.isError && <ErrorBox error={capabilities.error} onRetry={() => void capabilities.refetch()} />}
    {features.isError && <ErrorBox error={features.error} onRetry={() => void features.refetch()} />}
    {canWrite && <form className="row wrap-row" onSubmit={submit}>
      <label className="field grow"><span>Название возможности</span><input required maxLength={300} value={name} onChange={event => setName(event.target.value)} /></label>
      <button className="btn" disabled={create.isPending}>Создать возможность</button>
      {create.isError && <ErrorBox error={create.error} />}
    </form>}
    {capabilities.isSuccess && features.isSuccess && !groups.length && <Empty text="Возможностей и фич пока нет" />}
    {groups.map(group => <div className="stack" key={group.id}>
      <h3>{group.name}</h3>
      {!group.features.length && <Empty text="Фич пока нет" />}
      {group.features.map(feature => <div key={feature.id} className="stack">
        <div className="row wrap-row"><strong>{feature.name}</strong><span className="muted">{ru.feature.statuses[feature.status]}</span><button className="btn btn-sm" type="button" aria-expanded={expanded === feature.id} onClick={() => setExpanded(expanded === feature.id ? null : feature.id)}>Требования: {feature.name}</button></div>
        {expanded === feature.id && <FeatureRequirements featureId={feature.id} canWrite={canWrite} />}
      </div>)}
    </div>)}
  </div>
}

function FeatureRequirements({ featureId, canWrite }: { featureId: string; canWrite: boolean }) {
  const requirements = useRequirements(featureId)
  const create = useCreateRequirement(featureId)
  const [text, setText] = useState('')
  const submit = (event: FormEvent) => {
    event.preventDefault()
    if (!text.trim()) return
    create.mutate(text.trim(), { onSuccess: () => setText('') })
  }
  return <div className="stack">
    {requirements.isPending && <Loading />}
    {requirements.isError && <ErrorBox error={requirements.error} onRetry={() => void requirements.refetch()} />}
    {requirements.data && (!requirements.data.length ? <Empty text="Требований пока нет" /> : <ul>{requirements.data.map(requirement => <li key={requirement.id} style={{ whiteSpace: 'pre-wrap', overflowWrap: 'anywhere' }}>{requirement.text}</li>)}</ul>)}
    {canWrite && <form className="stack" onSubmit={submit}>
      <label className="field"><span>Текст требования</span><textarea required rows={3} value={text} onChange={event => setText(event.target.value)} /></label>
      {create.isError && <ErrorBox error={create.error} />}
      <div><button className="btn btn-sm" disabled={create.isPending}>Добавить требование</button></div>
    </form>}
  </div>
}
