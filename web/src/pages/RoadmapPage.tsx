import { useMemo, useState, type FormEvent } from 'react'
import { Link, useParams, useSearchParams } from 'react-router-dom'
import { errorMessage } from '../api/client'
import {
  accessLevel,
  useChangeItemDates,
  useItemHistory,
  useMe,
  useProduct,
  useRoadmapByRelease,
  useRoadmapNnl,
  useRoadmapTimeline,
  type RoadmapView,
} from '../api/hooks'
import type { RoadmapItem, SalesSafeItem } from '../api/types'
import { RoadmapEditor } from '../components/RoadmapEditor'
import { Badge, Empty, ErrorBox, Loading } from '../components/Status'
import { ru } from '../i18n/ru'
import { daysBetween, fmtDate, fmtDateTime, pick } from '../lib/format'
import { canWriteRoadmap } from '../lib/roles'
import { CompatMatrix, ReleaseBadges, ReleasesPanel } from './ReleasesPanel'

type View = RoadmapView | 'releases'
const VIEWS: { key: View; label: string; internalOnly?: boolean }[] = [
  { key: 'timeline', label: ru.roadmap.viewTimeline },
  { key: 'now-next-later', label: ru.roadmap.viewNnl },
  { key: 'by-release', label: ru.roadmap.viewByRelease },
  { key: 'releases', label: ru.release.title, internalOnly: true },
]

/** Единое представление элемента для обеих аудиторий. */
interface Row {
  id: string
  title: string
  bucket: RoadmapItem['bucket']
  start: string | null
  end: string | null
  status?: RoadmapItem['status']
  audience?: RoadmapItem['audience']
  kind?: RoadmapItem['kind']
  internal: boolean
  item?: RoadmapItem
}

function rows(audience: 'internal' | 'sales_safe', items?: RoadmapItem[], safe?: SalesSafeItem[]): Row[] {
  if (audience === 'sales_safe') {
    return (safe ?? []).map((s) => ({
      id: s.id,
      title: s.title,
      bucket: s.bucket,
      start: s.start_date ?? null,
      end: s.end_date ?? null,
      internal: false,
    }))
  }
  return (items ?? []).map((i) => ({
    id: i.id,
    title: i.title,
    bucket: i.bucket,
    start: i.start_date ?? null,
    end: i.end_date ?? null,
    status: i.status,
    audience: i.audience,
    kind: i.kind,
    internal: true,
    item: i,
  }))
}

export function RoadmapPage() {
  const { id = '' } = useParams()
  const [view, setView] = useState<View>('now-next-later')
  const [search, setSearch] = useSearchParams()
  const [creating, setCreating] = useState(search.has('feature'))
  const [saved, setSaved] = useState(false)
  const me = useMe()
  const product = useProduct(id)
  const canWrite = canWriteRoadmap(me.data, accessLevel(me.data, id))
  const timeline = useRoadmapTimeline(id, view === 'timeline')
  const nnl = useRoadmapNnl(id, view === 'now-next-later')
  const byRelease = useRoadmapByRelease(id, view === 'by-release')
  const active = view === 'timeline' ? timeline : view === 'now-next-later' ? nnl : byRelease
  const audience = active.data?.audience ?? me.data?.audience
  const visibleViews = VIEWS.filter((v) => !v.internalOnly || me.data?.audience === 'internal')

  return (
    <section className="stack">
      <div className="page-head">
        <div>
          <h1>
            {ru.roadmap.title}: {product.data?.name ?? ru.app.loading}
          </h1>
          <Link to={`/products/${id}`}>{ru.product.open}</Link>
        </div>
        <div className="segmented" role="tablist">
          {visibleViews.map((v) => (
            <button
              key={v.key}
              type="button"
              role="tab"
              aria-selected={view === v.key}
              className={view === v.key ? 'active' : undefined}
              onClick={() => setView(v.key)}
            >
              {v.label}
            </button>
          ))}
        </div>
      </div>
      {canWrite && <div><button type="button" className="btn btn-primary" onClick={() => { setCreating(!creating); setSaved(false) }}>{ru.workflow.createItem}</button></div>}
      {canWrite && creating && <RoadmapEditor productId={id} featureId={search.get('feature') ?? undefined} onDone={(success) => { setCreating(false); setSearch({}, { replace: true }); if (success) { setSaved(true); setView('now-next-later') } }} />}
      {saved && <div className="alert alert-ok" role="status">{ru.workflow.itemSaved}</div>}
      {audience === 'sales_safe' && <div className="alert alert-warn">{ru.roadmap.salesSafeBanner}</div>}
      {view !== 'releases' && active.isPending && <Loading />}
      {view !== 'releases' && active.isError && <ErrorBox error={active.error} onRetry={() => void active.refetch()} />}
      {view === 'releases' && <ReleasesPanel productId={id} canWrite={canWrite} />}
      {view === 'timeline' && timeline.data && (
        <Timeline productId={id} canWrite={canWrite} data={rows(timeline.data.audience, timeline.data.items, timeline.data.sales_safe)} />
      )}
      {view === 'now-next-later' && nnl.data && (
        <div className="columns">
          {(['now', 'next', 'later'] as const).map((b) => (
            <div key={b} className="card stack">
              <h2>{ru.roadmap.bucket[b]}</h2>
              <ItemList productId={id} canWrite={canWrite} data={rows(nnl.data.audience, nnl.data[b].items, nnl.data[b].sales_safe)} />
            </div>
          ))}
        </div>
      )}
      {view === 'by-release' && byRelease.data && (
        <div className="stack">
          {byRelease.data.releases.map((g) => (
            <div key={g.release.id} className="card stack">
              <div className="row wrap-row">
                <h2>
                  {g.release.name} <span className="mono muted">{g.release.version}</span>
                </h2>
                <ReleaseBadges r={g.sales_safe_release ?? g.release} />
                <span className="muted">{fmtDate(g.release.planned_date)}</span>
              </div>
              <ItemList productId={id} canWrite={canWrite} data={rows(byRelease.data.audience, g.items, g.sales_safe)} />
              {byRelease.data.audience === 'sales_safe' && g.sales_safe_release && (
                <>
                  <h3>{ru.release.compat}</h3>
                  <CompatMatrix rows={g.sales_safe_release.compatibility_matrix} />
                </>
              )}
            </div>
          ))}
          <div className="card stack">
            <h2>{ru.roadmap.unassigned}</h2>
            <ItemList
              productId={id}
              canWrite={canWrite}
              data={rows(byRelease.data.audience, byRelease.data.unassigned.items, byRelease.data.unassigned.sales_safe)}
            />
          </div>
        </div>
      )}
    </section>
  )
}

function Timeline({ productId, canWrite, data }: { productId: string; canWrite: boolean; data: Row[] }) {
  const sorted = useMemo(
    () => [...data].sort((a, b) => (a.start ?? '9999').localeCompare(b.start ?? '9999')),
    [data],
  )
  const range = useMemo(() => {
    const dates = sorted.flatMap((r) => [r.start, r.end]).filter((d): d is string => !!d)
    if (dates.length === 0) return null
    const min = dates.reduce((a, b) => (a < b ? a : b))
    const max = dates.reduce((a, b) => (a > b ? a : b))
    const span = Math.max(daysBetween(min, max) ?? 1, 1)
    return { min, span }
  }, [sorted])

  if (sorted.length === 0) return <Empty text={ru.roadmap.noItems} />
  return (
    <div className="stack">
      {sorted.map((r) => {
        const left = range && r.start ? ((daysBetween(range.min, r.start) ?? 0) / range.span) * 100 : 0
        const width = range && r.start && r.end ? Math.max(((daysBetween(r.start, r.end) ?? 0) / range.span) * 100, 1) : 1
        return (
          <div key={r.id} className="tl-row">
            <div className="tl-label">
              <ItemHeader row={r} />
            </div>
            <div className="tl-track">
              {r.start ? (
                <div className="tl-bar" style={{ left: `${left}%`, width: `${width}%` }} title={`${fmtDate(r.start)} — ${fmtDate(r.end)}`} />
              ) : (
                <span className="muted">{ru.roadmap.noDates}</span>
              )}
            </div>
            <div className="tl-dates muted">
              {fmtDate(r.start)} — {fmtDate(r.end)}
            </div>
            <ItemActions productId={productId} canWrite={canWrite} row={r} />
          </div>
        )
      })}
    </div>
  )
}

function ItemList({ productId, canWrite, data }: { productId: string; canWrite: boolean; data: Row[] }) {
  if (data.length === 0) return <Empty text={ru.roadmap.noItems} />
  return (
    <ul className="items">
      {data.map((r) => (
        <li key={r.id} className="item">
          <ItemHeader row={r} />
          <div className="muted">
            {fmtDate(r.start)} — {fmtDate(r.end)}
          </div>
          <ItemActions productId={productId} canWrite={canWrite} row={r} />
        </li>
      ))}
    </ul>
  )
}

function ItemHeader({ row }: { row: Row }) {
  return (
    <div className="row wrap-row">
      <strong>{row.title}</strong>
      <Badge tone="neutral">{ru.roadmap.bucket[row.bucket]}</Badge>
      {row.status && <Badge tone={row.status === 'done' ? 'ok' : row.status === 'cancelled' ? 'danger' : 'info'}>{pick(ru.roadmap.statuses, row.status)}</Badge>}
      {row.kind && <Badge tone={row.kind === 'fix' ? 'warn' : 'neutral'}>{ru.release.itemKinds[row.kind]}</Badge>}
      {row.audience === 'sales_safe' && <Badge tone="warn">{ru.me.audienceSalesSafe}</Badge>}
    </div>
  )
}

function ItemActions({ productId, canWrite, row }: { productId: string; canWrite: boolean; row: Row }) {
  const [open, setOpen] = useState<'none' | 'edit' | 'details' | 'history'>('none')
  if (!row.internal) return null
  return (
    <div className="stack">
      <div className="row">
        {canWrite && (
          <>
            <button type="button" className="btn btn-sm" onClick={() => setOpen(open === 'details' ? 'none' : 'details')}>{ru.workflow.edit}</button>
            <button type="button" className="btn btn-sm" onClick={() => setOpen(open === 'edit' ? 'none' : 'edit')}>
              {ru.roadmap.changeDates}
            </button>
          </>
        )}
        <button type="button" className="btn btn-sm" onClick={() => setOpen(open === 'history' ? 'none' : 'history')}>
          {ru.roadmap.history}
        </button>
      </div>
      {canWrite && open === 'details' && row.item && <RoadmapEditor productId={productId} item={row.item} onDone={() => setOpen('none')} />}
      {canWrite && open === 'edit' && <ChangeDatesForm key={row.id} productId={productId} row={row} onDone={() => setOpen('history')} />}
      {open === 'history' && <History itemId={row.id} />}
    </div>
  )
}

function ChangeDatesForm({ productId, row, onDone }: { productId: string; row: Row; onDone: () => void }) {
  const change = useChangeItemDates(productId)
  const [start, setStart] = useState(row.start ?? '')
  const [end, setEnd] = useState(row.end ?? '')
  const [reason, setReason] = useState('')
  const [validation, setValidation] = useState<string | null>(null)

  const submit = (e: FormEvent) => {
    e.preventDefault()
    if (!reason.trim()) return setValidation(ru.feature.reasonRequired)
    if (start && end && end < start) return setValidation(ru.workflow.datesInvalid)
    setValidation(null)
    change.mutate(
      { itemId: row.id, start_date: start || undefined, end_date: end || undefined, reason: reason.trim() },
      { onSuccess: onDone },
    )
  }

  return (
    <form className="form-inline stack" onSubmit={submit}>
      <div className="row">
        <label className="field">
          <span>{ru.roadmap.start}</span>
          <input type="date" value={start} onChange={(e) => setStart(e.target.value)} />
        </label>
        <label className="field">
          <span>{ru.roadmap.end}</span>
          <input type="date" value={end} onChange={(e) => setEnd(e.target.value)} />
        </label>
        <label className="field grow">
          <span>{ru.feature.reason}</span>
          <input type="text" value={reason} onChange={(e) => setReason(e.target.value)} required />
        </label>
      </div>
      {validation && <div className="alert alert-error">{validation}</div>}
      {change.isError && <div className="alert alert-error">{errorMessage(change.error)}</div>}
      <div className="row">
        <button type="submit" className="btn btn-primary btn-sm" disabled={change.isPending}>
          {ru.app.save}
        </button>
      </div>
    </form>
  )
}

function History({ itemId }: { itemId: string }) {
  const history = useItemHistory(itemId, true)
  if (history.isPending) return <Loading />
  if (history.isError) return <ErrorBox error={history.error} />
  if (history.data.length === 0) return <Empty text={ru.roadmap.historyEmpty} />
  return (
    <div className="table-wrap">
      <table className="table table-compact">
        <thead>
          <tr>
            <th>{ru.roadmap.at}</th>
            <th>{ru.roadmap.actor}</th>
            <th>{ru.roadmap.was}</th>
            <th>{ru.roadmap.became}</th>
            <th>{ru.feature.reason}</th>
          </tr>
        </thead>
        <tbody>
          {history.data.map((h) => (
            <tr key={h.id}>
              <td>{fmtDateTime(h.at)}</td>
              <td>{h.actor}</td>
              <td>
                {fmtDate(h.old_start)} — {fmtDate(h.old_end)}
              </td>
              <td>
                {fmtDate(h.new_start)} — {fmtDate(h.new_end)}
              </td>
              <td className="wrap">{h.reason}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}
