import { useState, type FormEvent } from 'react'
import type { Feature } from '../api/types'
import { useSaveFeature } from '../api/workflow'
import { ru } from '../i18n/ru'
import { ErrorBox } from './Status'

export function FeatureEditor({ productId, feature, onDone }: { productId: string; feature?: Feature; onDone: () => void }) {
  const save = useSaveFeature(productId)
  const [name, setName] = useState(feature?.name ?? '')
  const [status, setStatus] = useState<Feature['status']>(feature?.status ?? 'idea')
  const [date, setDate] = useState('')
  const [validation, setValidation] = useState('')
  const submit = (event: FormEvent) => {
    event.preventDefault()
    if (!name.trim()) { setValidation(ru.workflow.nameRequired); return }
    setValidation('')
    save.mutate({ id: feature?.id, body: { name: name.trim(), status: feature?.external_key ? undefined : status, planned_date: feature ? undefined : date || undefined } }, { onSuccess: onDone })
  }
  return <form className="card form-inline stack" onSubmit={submit}>
    <h3>{feature ? ru.workflow.editFeature : ru.workflow.createFeature}</h3>
    <p className="muted">{feature ? ru.workflow.featureArchive : ru.workflow.featureHelp}</p>
    <div className="row">
      <label className="field grow"><span>{ru.feature.name}</span><input autoFocus required maxLength={300} value={name} onChange={(e) => setName(e.target.value)} /></label>
      <label className="field"><span>{ru.feature.status}</span><select value={status} disabled={!!feature?.external_key} onChange={(e) => setStatus(e.target.value as Feature['status'])}>
        {Object.entries(ru.feature.statuses).map(([value, label]) => <option key={value} value={value}>{label}</option>)}
      </select></label>
      {!feature && <label className="field"><span>{ru.feature.plannedDate}</span><input type="date" value={date} onChange={(e) => setDate(e.target.value)} /></label>}
    </div>
    {feature?.external_key && <p className="muted">{ru.workflow.externalStatus} <span className="mono">{feature.external_key}</span></p>}
    {validation && <div className="alert alert-error" role="alert">{validation}</div>}
    {save.isError && <ErrorBox error={save.error} />}
    <div className="row"><button className="btn btn-primary" disabled={save.isPending}>{ru.app.save}</button><button type="button" className="btn" onClick={onDone}>{ru.app.cancel}</button></div>
  </form>
}
