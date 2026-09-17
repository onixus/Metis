import { useState, type FormEvent } from 'react'
import { Link } from 'react-router-dom'
import { errorMessage } from '../api/client'
import {
  useAffectedBaselines,
  useFeatureCost,
  useFeatureFlags,
  useFeatureImpact,
  useSetFeatureDevCost,
  useSetFeatureFlags,
  useSetFeatureImpact,
} from '../api/hooks'
import type { ImpactClass } from '../api/types'
import { ru } from '../i18n/ru'
import { fmtDate, fmtDateTime, fmtMoney } from '../lib/format'
import { Badge, ErrorBox, Loading } from './Status'

const CLASSES: ImpactClass[] = ['none', 'analysis_required', 'security_functions']
const CLASS_TONE: Record<ImpactClass, 'neutral' | 'ok' | 'warn' | 'danger' | 'info'> = {
  none: 'ok',
  analysis_required: 'warn',
  security_functions: 'danger',
}

/** Карточка фичи этапа 2: класс влияния (CM-06), затронутые baseline (CM-07), флаг PR-04, стоимость PR-05. */
export function FeatureDetails({
  featureId,
  productId,
  canCompliance,
  canPriority,
}: {
  featureId: string
  productId: string
  canCompliance: boolean
  canPriority: boolean
}) {
  const impact = useFeatureImpact(featureId)
  const affected = useAffectedBaselines(featureId)
  const flags = useFeatureFlags(featureId)
  const cost = useFeatureCost(featureId)
  const setImpact = useSetFeatureImpact(productId)
  const setFlags = useSetFeatureFlags()
  const setDev = useSetFeatureDevCost()
  const [cls, setCls] = useState<ImpactClass>('none')
  const [just, setJust] = useState('')
  const [flagReason, setFlagReason] = useState('')
  const [dev, setDevAmount] = useState('')

  const submitImpact = (e: FormEvent) => {
    e.preventDefault()
    if (!just.trim()) return
    setImpact.mutate({ featureId, class: cls, justification: just.trim() }, { onSuccess: () => setJust('') })
  }

  return (
    <div className="columns">
      <div className="stack">
        <h3>{ru.compliance.impact.title}</h3>
        {impact.isPending && <Loading />}
        {impact.isError && <ErrorBox error={impact.error} />}
        {impact.data === null && <span className="muted">{ru.compliance.impact.none}</span>}
        {impact.data && (
          <div>
            <Badge tone={CLASS_TONE[impact.data.class]}>{ru.compliance.impact.classes[impact.data.class]}</Badge>
            <div className="wrap">{impact.data.justification}</div>
            <div className="muted">
              {impact.data.author} · {fmtDateTime(impact.data.at)}
            </div>
          </div>
        )}
        {canCompliance && (
          <form className="row" onSubmit={submitImpact}>
            <label className="field">
              <span>{ru.compliance.impact.class}</span>
              <select value={cls} onChange={(e) => setCls(e.target.value as ImpactClass)}>
                {CLASSES.map((c) => (
                  <option key={c} value={c}>
                    {ru.compliance.impact.classes[c]}
                  </option>
                ))}
              </select>
            </label>
            <label className="field grow">
              <span>{ru.compliance.impact.justification}</span>
              <input value={just} onChange={(e) => setJust(e.target.value)} required />
            </label>
            <button type="submit" className="btn btn-sm btn-primary" disabled={setImpact.isPending}>
              {ru.compliance.impact.set}
            </button>
          </form>
        )}
        {setImpact.isError && <div className="alert alert-error">{errorMessage(setImpact.error)}</div>}

        <h3>{ru.compliance.affected.title}</h3>
        {affected.isPending && <Loading />}
        {affected.isError && <ErrorBox error={affected.error} />}
        {affected.data && affected.data.length === 0 && <span className="muted">{ru.compliance.affected.empty}</span>}
        {affected.data && affected.data.length > 0 && (
          <table className="table table-compact">
            <thead>
              <tr>
                <th>{ru.common.product}</th>
                <th>{ru.common.version}</th>
                <th>{ru.compliance.certificate}</th>
                <th>{ru.compliance.eol}</th>
                <th>{ru.compliance.affected.path}</th>
                <th>{ru.compliance.affected.procedure}</th>
              </tr>
            </thead>
            <tbody>
              {affected.data.map((a) => (
                <tr key={a.baseline.id}>
                  <td>
                    <Link to={`/products/${a.baseline.product_id}/compliance`}>{a.baseline.product_id.slice(0, 8)}…</Link>
                  </td>
                  <td className="mono">{a.baseline.version}</td>
                  <td>{a.baseline.certificate_no}</td>
                  <td>{fmtDate(a.baseline.eol)}</td>
                  <td className="wrap">{a.path.join(' → ')}</td>
                  <td>
                    <Badge tone={a.procedure === 'full_procedure' ? 'danger' : 'warn'}>{ru.compliance.affected.procedures[a.procedure]}</Badge>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>
      <div className="stack">
        <h3>{ru.compliance.flags.mandatory}</h3>
        {flags.isPending && <Loading />}
        {flags.isError && <ErrorBox error={flags.error} />}
        {flags.data && (
          <div className="row wrap-row">
            <label className="check">
              <input
                type="checkbox"
                checked={flags.data.regulatory_mandatory}
                disabled={!canPriority || setFlags.isPending}
                onChange={(e) =>
                  setFlags.mutate({ featureId, regulatory_mandatory: e.target.checked, reason: flagReason.trim() || undefined })
                }
              />
              {ru.compliance.flags.mandatory}
            </label>
            {canPriority && (
              <label className="field grow">
                <span>{ru.compliance.flags.reason}</span>
                <input value={flagReason} onChange={(e) => setFlagReason(e.target.value)} placeholder={flags.data.reason ?? ''} />
              </label>
            )}
            {flags.data.regulatory_mandatory && (
              <span className="muted">
                {flags.data.reason} {flags.data.set_by && `· ${ru.compliance.flags.setBy}: ${flags.data.set_by}`}
              </span>
            )}
          </div>
        )}
        {setFlags.isError && <div className="alert alert-error">{errorMessage(setFlags.error)}</div>}

        <h3>{ru.compliance.cost.title}</h3>
        {cost.isPending && <Loading />}
        {cost.isError && <ErrorBox error={cost.error} />}
        {cost.data && (
          <dl className="kv">
            <dt>{ru.compliance.cost.dev}</dt>
            <dd>{fmtMoney(cost.data.dev_cost)}</dd>
            <dt>{ru.compliance.cost.confirmation}</dt>
            <dd>{fmtMoney(cost.data.confirmation_cost)}</dd>
            <dt>{ru.compliance.cost.total}</dt>
            <dd>
              <strong>{fmtMoney(cost.data.total)}</strong>
            </dd>
          </dl>
        )}
        {canPriority && cost.data && (
          <form
            className="row"
            onSubmit={(e) => {
              e.preventDefault()
              setDev.mutate({ featureId, dev_cost: { amount: Math.round(Number(dev || '0') * 100), currency: cost.data.dev_cost.currency || 'RUB' } })
            }}
          >
            <label className="field">
              <span>
                {ru.compliance.cost.dev}, {cost.data.dev_cost.currency || 'RUB'}
              </span>
              <input type="number" min={0} step={1} value={dev} onChange={(e) => setDevAmount(e.target.value)} required />
            </label>
            <button type="submit" className="btn btn-sm" disabled={setDev.isPending}>
              {ru.compliance.cost.setDev}
            </button>
          </form>
        )}
        {setDev.isError && <div className="alert alert-error">{errorMessage(setDev.error)}</div>}
      </div>
    </div>
  )
}
