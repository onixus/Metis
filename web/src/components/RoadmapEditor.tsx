import { useState, type FormEvent } from 'react'
import { useFeatures, useReleases } from '../api/hooks'
import { useSaveRoadmapItem } from '../api/workflow'
import type { Feature, Release, RoadmapItem } from '../api/types'
import { ru } from '../i18n/ru'
import { ErrorBox, Loading } from './Status'

export function RoadmapEditor({ productId, item, featureId, onDone }: { productId: string; item?: RoadmapItem; featureId?: string; onDone: (saved?: boolean) => void }) {
  const features = useFeatures(productId)
  const releases = useReleases(productId)
  if (features.isPending || releases.isPending) return <Loading />
  if (features.isError) return <ErrorBox error={features.error} onRetry={() => void features.refetch()} />
  if (releases.isError) return <ErrorBox error={releases.error} onRetry={() => void releases.refetch()} />
  return <RoadmapForm productId={productId} item={item} features={features.data} releases={releases.data} initialFeatureId={featureId} onDone={onDone} />
}

function RoadmapForm({ productId, item, features, releases, initialFeatureId, onDone }: { productId: string; item?: RoadmapItem; features: Feature[]; releases: Release[]; initialFeatureId?: string; onDone: (saved?: boolean) => void }) {
  const save = useSaveRoadmapItem(productId)
  const preselected = features.find((feature) => feature.id === initialFeatureId)
  const [title, setTitle] = useState(item?.title ?? preselected?.name ?? '')
  const [featureId, setFeatureId] = useState(item?.feature_id ?? preselected?.id ?? '')
  const [releaseId, setReleaseId] = useState(item?.release_id ?? '')
  const [bucket, setBucket] = useState<RoadmapItem['bucket']>(item?.bucket ?? 'next')
  const [audience, setAudience] = useState<RoadmapItem['audience']>(item?.audience ?? 'internal')
  const [status, setStatus] = useState<RoadmapItem['status']>(item?.status ?? 'planned')
  const [kind, setKind] = useState<RoadmapItem['kind']>(item?.kind ?? 'feature')
  const [start, setStart] = useState(item?.start_date ?? '')
  const [end, setEnd] = useState(item?.end_date ?? preselected?.planned_date ?? '')
  const [validation, setValidation] = useState('')
  const submit = (event: FormEvent) => {
    event.preventDefault()
    if (!title.trim()) { setValidation(ru.workflow.nameRequired); return }
    if (start && end && end < start) { setValidation(ru.workflow.datesInvalid); return }
    setValidation('')
    // PATCH is a complete attribute replacement in the current API. Preserve dates exactly;
    // date changes go through the separate audited operation with a reason.
    save.mutate({ id: item?.id, body: { title: title.trim(), feature_id: featureId || undefined, release_id: releaseId || undefined, bucket, audience, status, kind,
      start_date: item ? item.start_date || undefined : start || undefined, end_date: item ? item.end_date || undefined : end || undefined } }, { onSuccess: () => onDone(true) })
  }
  return <form className="card form-inline stack" onSubmit={submit}>
    <h3>{item ? ru.workflow.editItem : ru.workflow.createItem}</h3><p className="muted">{ru.workflow.roadmapHelp}</p>
    <label className="field"><span>{ru.workflow.title}</span><input autoFocus required value={title} onChange={(e) => setTitle(e.target.value)} /></label>
    <div className="columns">
      <label className="field"><span>{ru.feature.name}</span><select value={featureId} onChange={(e) => { setFeatureId(e.target.value); if (!title) setTitle(features.find((f) => f.id === e.target.value)?.name ?? '') }}>
        <option value="">{ru.workflow.noFeature}</option>{features.map((feature) => <option value={feature.id} key={feature.id}>{feature.name}</option>)}
      </select></label>
      <label className="field"><span>{ru.roadmap.release}</span><select value={releaseId} onChange={(e) => { setReleaseId(e.target.value); if (releases.find((r) => r.id === e.target.value)?.branch === 'certified') setKind('fix') }}>
        <option value="">{ru.workflow.noRelease}</option>{releases.map((release) => <option value={release.id} key={release.id}>{release.name} · {release.version}</option>)}
      </select></label>
      <label className="field"><span>{ru.workflow.bucket}</span><select value={bucket} onChange={(e) => setBucket(e.target.value as typeof bucket)}>{Object.entries(ru.roadmap.bucket).map(([value, label]) => <option key={value} value={value}>{label}</option>)}</select></label>
      <label className="field"><span>{ru.roadmap.audience}</span><select value={audience} onChange={(e) => setAudience(e.target.value as typeof audience)}><option value="internal">{ru.workflow.internal}</option><option value="sales_safe">{ru.workflow.salesSafe}</option></select></label>
      <label className="field"><span>{ru.roadmap.status}</span><select value={status} onChange={(e) => setStatus(e.target.value as typeof status)}>{Object.entries(ru.roadmap.statuses).map(([value, label]) => <option key={value} value={value}>{label}</option>)}</select></label>
      <label className="field"><span>{ru.release.itemKind}</span><select value={kind} disabled={releases.find((r) => r.id === releaseId)?.branch === 'certified'} onChange={(e) => setKind(e.target.value as typeof kind)}>{Object.entries(ru.release.itemKinds).map(([value, label]) => <option key={value} value={value}>{label}</option>)}</select></label>
      {!item && <><label className="field"><span>{ru.roadmap.start}</span><input type="date" value={start} onChange={(e) => setStart(e.target.value)} /></label><label className="field"><span>{ru.roadmap.end}</span><input type="date" min={start || undefined} value={end} onChange={(e) => setEnd(e.target.value)} /></label></>}
    </div>
    {validation && <div className="alert alert-error" role="alert">{validation}</div>}{save.isError && <ErrorBox error={save.error} />}
    <div className="row"><button className="btn btn-primary" disabled={save.isPending}>{ru.app.save}</button><button type="button" className="btn" onClick={() => onDone()}>{ru.app.cancel}</button></div>
  </form>
}
