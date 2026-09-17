import { useMemo, useState } from 'react'
import { useFeatures, useRank, useScoringModels } from '../api/hooks'
import type { ScoreResult } from '../api/types'
import { ru } from '../i18n/ru'
import { Empty, ErrorBox, Loading } from './Status'

/** Ранжирование продукта по модели оценки; регуляторно обязательные фичи — отдельным блоком (PR-04). */
export function RankingBlock({ productId }: { productId: string }) {
  const models = useScoringModels()
  const features = useFeatures(productId)
  const applicable = useMemo(
    () => (models.data ?? []).filter((m) => !m.product_id || m.product_id === productId),
    [models.data, productId],
  )
  const [choice, setChoice] = useState('')
  const modelId = choice || applicable[0]?.id || ''
  const rank = useRank(modelId, productId, modelId !== '')
  const nameById = useMemo(() => new Map((features.data ?? []).map((f) => [f.id, f.name])), [features.data])

  if (models.isPending) return <Loading />
  if (models.isError) return <ErrorBox error={models.error} />

  const table = (rows: ScoreResult[], empty: string) =>
    rows.length === 0 ? (
      <Empty text={empty} />
    ) : (
      <div className="table-wrap">
        <table className="table table-compact">
          <thead>
            <tr>
              <th>#</th>
              <th>{ru.feature.name}</th>
              <th>{ru.rank.score}</th>
              <th>{ru.rank.explanation}</th>
            </tr>
          </thead>
          <tbody>
            {rows.map((r, i) => (
              <tr key={r.feature_id}>
                <td className="num">{i + 1}</td>
                <td>{nameById.get(r.feature_id) ?? <span className="mono">{r.feature_id}</span>}</td>
                <td className="num mono">{r.score}</td>
                <td className="wrap muted">{r.explanation}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    )

  return (
    <div className="card stack">
      <div className="row wrap-row">
        <h2>{ru.rank.title}</h2>
        {applicable.length > 0 && (
          <label className="field">
            <span>{ru.rank.model}</span>
            <select value={modelId} onChange={(e) => setChoice(e.target.value)}>
              {applicable.map((m) => (
                <option key={m.id} value={m.id}>
                  {m.name} ({m.type})
                </option>
              ))}
            </select>
          </label>
        )}
      </div>
      {applicable.length === 0 && <Empty text={ru.rank.noModels} />}
      {rank.isPending && modelId && <Loading />}
      {rank.isError && <ErrorBox error={rank.error} />}
      {rank.data && (
        <>
          <h3>{ru.rank.ranked}</h3>
          {table(rank.data.ranked, ru.rank.empty)}
          <h3>{ru.rank.mandatory}</h3>
          <p className="muted">{ru.rank.mandatoryHint}</p>
          {table(rank.data.mandatory, ru.app.empty)}
        </>
      )}
    </div>
  )
}
