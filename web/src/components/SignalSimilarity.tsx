import { useState } from 'react'
import { errorMessage } from '../api/client'
import { useMergeSignals, useSimilarSignals } from '../api/hooks'
import type { Signal } from '../api/types'
import { ru } from '../i18n/ru'
import { pick } from '../lib/format'
import { Empty, ErrorBox, Loading } from './Status'

/** Похожие сигналы (SG-04) с объединением дубликатов в текущий. */
export function SimilarSignals({ productId, signal, onClose }: { productId: string; signal: Signal; onClose: () => void }) {
  const similar = useSimilarSignals(signal.id, true)
  const merge = useMergeSignals(productId)
  const [picked, setPicked] = useState<string[]>([])
  // Объединять можно только отмеченные из текущего списка: чужие отметки в него не попадают.
  const mergeable = picked.filter((pid) => (similar.data ?? []).some(({ signal: s }) => s.id === pid && s.status !== 'merged'))
  return (
    <div className="card form-inline stack">
      <div className="row wrap-row">
        <h3>
          {ru.signal2.similar}: {signal.text.slice(0, 80)}
        </h3>
        <button type="button" className="btn btn-sm" onClick={onClose}>
          {ru.common.close}
        </button>
      </div>
      {similar.isPending && <Loading />}
      {similar.isError && <ErrorBox error={similar.error} />}
      {similar.data && similar.data.length === 0 && <Empty text={ru.signal2.similarEmpty} />}
      {similar.data && similar.data.length > 0 && (
        <>
          <div className="table-wrap">
            <table className="table table-compact">
              <thead>
                <tr>
                  <th />
                  <th>{ru.signal.text}</th>
                  <th>{ru.signal.status}</th>
                  <th>{ru.signal2.score}</th>
                </tr>
              </thead>
              <tbody>
                {similar.data.map(({ signal: s, score }) => (
                  <tr key={s.id}>
                    <td>
                      <input
                        type="checkbox"
                        aria-label={s.text}
                        checked={picked.includes(s.id)}
                        disabled={s.status === 'merged'}
                        onChange={(e) => setPicked((p) => (e.target.checked ? [...p, s.id] : p.filter((x) => x !== s.id)))}
                      />
                    </td>
                    <td className="wrap">{s.text}</td>
                    <td>{pick(ru.signal.statuses, s.status)}</td>
                    <td className="num mono">{score.toFixed(2)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
          <p className="muted">{ru.signal2.mergeHint}</p>
          <div className="row">
            <button
              type="button"
              className="btn btn-sm btn-primary"
              disabled={mergeable.length === 0 || merge.isPending}
              onClick={() => merge.mutate({ signalId: signal.id, duplicate_ids: mergeable }, { onSuccess: () => setPicked([]) })}
            >
              {ru.signal2.merge}
            </button>
            {merge.isSuccess && <span className="alert alert-ok">{ru.signal2.merged}</span>}
            {merge.isError && <span className="alert alert-error">{errorMessage(merge.error)}</span>}
          </div>
        </>
      )}
    </div>
  )
}
