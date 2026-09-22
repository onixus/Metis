import { useState, type FormEvent } from 'react'
import { useCreateScoringModel, useSaveScoreInputs } from '../api/workflow'
import type { Feature, ScoreResult, ScoringModel } from '../api/types'
import { ru } from '../i18n/ru'
import { ErrorBox } from './Status'

export function ScoringModelForm({ productId, onDone }: { productId: string; onDone: (id?: string) => void }) {
  const save = useCreateScoringModel()
  const [name, setName] = useState('')
  const [type, setType] = useState<'rice' | 'wsjf'>('rice')
  const [validation, setValidation] = useState('')
  return <form className="card form-inline stack" onSubmit={(event) => {
    event.preventDefault()
    if (!name.trim()) { setValidation(ru.workflow.nameRequired); return }
    setValidation('')
    save.mutate({ product_id: productId, name: name.trim(), type }, { onSuccess: (model) => onDone(model.id) })
  }}>
    <h3>{ru.workflow.createModel}</h3><p className="muted">{ru.workflow.modelHelp}</p>
    <div className="row">
      <label className="field grow"><span>{ru.workflow.modelName}</span><input autoFocus required value={name} onChange={(e) => setName(e.target.value)} /></label>
      <label className="field"><span>{ru.workflow.modelType}</span><select value={type} onChange={(e) => setType(e.target.value as typeof type)}><option value="rice">RICE</option><option value="wsjf">WSJF</option></select></label>
    </div>
    {validation && <div className="alert alert-error">{validation}</div>}{save.isError && <ErrorBox error={save.error} />}
    <div className="row"><button className="btn btn-primary" disabled={save.isPending}>{ru.app.save}</button><button type="button" className="btn" onClick={() => onDone()}>{ru.app.cancel}</button></div>
  </form>
}

export function ScoreEditor({ productId, model, features, scores }: { productId: string; model: ScoringModel; features: Feature[]; scores: ScoreResult[] }) {
  const [chosen, setChosen] = useState('')
  const featureId = chosen || features[0]?.id || ''
  return <div className="card form-inline stack">
    <h3>{ru.workflow.scoreFeature}</h3>
    <label className="field"><span>{ru.feature.name}</span><select value={featureId} onChange={(e) => setChosen(e.target.value)}>
      {!features.length && <option value="">{ru.workflow.noScoreFeatures}</option>}
      {features.map((feature) => <option key={feature.id} value={feature.id}>{feature.name}</option>)}
    </select></label>
    {featureId && <ScoreInputsForm key={`${model.id}-${featureId}`} productId={productId} featureId={featureId} model={model} score={scores.find((score) => score.feature_id === featureId)} />}
  </div>
}

function ScoreInputsForm({ productId, featureId, model, score }: { productId: string; featureId: string; model: ScoringModel; score?: ScoreResult }) {
  const save = useSaveScoreInputs(productId, model.id)
  const [values, setValues] = useState<Record<string, string>>(() => Object.fromEntries(model.inputs.map((name) => [name, score?.components.find((component) => component.name === name && !component.system)?.value ?? ''])))
  const [validation, setValidation] = useState('')
  const submit = (event: FormEvent) => {
    event.preventDefault()
    const normalized = Object.fromEntries(model.inputs.map((name) => [name, (values[name] ?? '').trim().replace(',', '.')]))
    if (Object.entries(normalized).some(([name, value]) => !/^-?\d+(?:\.\d+)?$/.test(value) || !Number.isFinite(Number(value)) ||
      (model.type !== 'custom' && Number(value) < 0) || ((name === 'effort' || name === 'job_size') && Number(value) <= 0) ||
      (name === 'confidence' && (Number(value) < 0 || Number(value) > 1)))) { setValidation(ru.workflow.decimalInvalid); return }
    setValidation('')
    save.mutate({ featureId, values: normalized })
  }
  return <form className="stack" onSubmit={submit}>
    <p className="muted">{ru.workflow.scoreHelp}</p>
    <code>{model.formula}</code>
    <div className="columns">{model.inputs.map((name) => <label className="field" key={name}>
      <span>{ru.workflow.scoreLabels[name as keyof typeof ru.workflow.scoreLabels] ?? name}</span>
      <input required inputMode="decimal" value={values[name] ?? ''} onChange={(e) => { setValues((current) => ({ ...current, [name]: e.target.value })); save.reset() }} />
    </label>)}</div>
    {validation && <div className="alert alert-error" role="alert">{validation}</div>}{save.isError && <ErrorBox error={save.error} />}
    {save.isSuccess && <div className="alert alert-ok" role="status">{ru.workflow.scoreSaved}: {save.data.score}</div>}
    <div><button className="btn btn-primary" disabled={save.isPending}>{ru.workflow.scoreSave}</button></div>
  </form>
}
