import { useState, type FormEvent } from 'react'
import { Link } from 'react-router-dom'
import { accessLevel, useContracts, useFeatures, useHypotheses } from '../api/hooks'
import { useIngestSignal, useSetSignalTarget, useSignals, useTriageSignal } from '../api/workflow'
import type { Me, Signal } from '../api/types'
import { ru } from '../i18n/ru'
import { fmtDate, fmtMoney, pick } from '../lib/format'
import { parseMinorUnits } from '../lib/moneyInput'
import { canWriteDiscovery } from '../lib/roles'
import { Badge, Empty, ErrorBox, Loading } from './Status'
import { SimilarSignals } from './SignalSimilarity'

export function SignalsPanel({ productId, me }: { productId: string; me?: Me }) {
  const signals = useSignals(productId)
  const features = useFeatures(productId)
  const hypotheses = useHypotheses(productId)
  const contracts = useContracts()
  const canWrite = canWriteDiscovery(me, accessLevel(me, productId))
  const [filter, setFilter] = useState<'queue' | 'all'>('queue')
  const [creating, setCreating] = useState(false)
  const [editingId, setEditingId] = useState('')
  const [similarId, setSimilarId] = useState('')
  const active = signals.data?.find((s) => s.id === editingId)
  const similar = signals.data?.find((s) => s.id === similarId)
  const relevantContracts = (contracts.data ?? []).filter((c) => c.provider_product_id === productId || c.consumer_product_id === productId)
  const targets = {
    feature_id: (features.data ?? []).map((f) => ({ id: f.id, name: f.name })),
    contract_id: relevantContracts.map((c) => ({ id: c.id, name: c.name })),
    hypothesis_id: (hypotheses.data ?? []).map((h) => ({ id: h.id, name: h.title })),
  }
  const names = new Map(Object.values(targets).flat().map((x) => [x.id, x.name]))
  const rows = (signals.data ?? []).filter((s) => filter === 'all' || s.status === 'new' || s.status === 'in_review')
    .sort((a, b) => (a.due_date ?? '9999').localeCompare(b.due_date ?? '9999') || a.created_at.localeCompare(b.created_at))
  return <section className="card stack" id="signals">
    <div className="page-head"><h2>{ru.workflow.signals}</h2>{canWrite && <button type="button" className="btn btn-primary" onClick={() => setCreating(!creating)}>{ru.workflow.createSignal}</button>}</div>
    {creating && canWrite && <SignalForm productId={productId} onDone={() => setCreating(false)} />}
    <div className="segmented" role="group" aria-label={ru.workflow.signals}>
      <button type="button" className={filter === 'queue' ? 'active' : ''} onClick={() => setFilter('queue')}>{ru.workflow.queue}</button>
      <button type="button" className={filter === 'all' ? 'active' : ''} onClick={() => setFilter('all')}>{ru.workflow.allSignals}</button>
    </div>
    {signals.isPending && <Loading />}
    {signals.isError && <ErrorBox error={signals.error} onRetry={() => void signals.refetch()} />}
    {!signals.isPending && !signals.isError && rows.length === 0 && <Empty text={filter === 'queue' ? ru.workflow.noQueue : ru.workflow.noSignals} />}
    {rows.length > 0 && <div className="table-wrap"><table className="table">
      <thead><tr><th>{ru.signal.text}</th><th>{ru.workflow.account}</th><th>{ru.signal.status}</th><th>{ru.signal.weight}</th><th>{ru.signal.dueDate}</th><th>{ru.workflow.linkedTo}</th><th>{ru.signal2.actions}</th></tr></thead>
      <tbody>{rows.map((s) => <tr key={s.id}>
        <td className="wrap">{s.text}<div className="muted">{pick(ru.signal.sources, s.source)}{s.segment && ` · ${s.segment}`}</div></td>
        <td>{s.account_id || ru.app.dash}<div className="muted">{s.deal_id}</div></td>
        <td>{pick(ru.signal.statuses, s.status)}{s.blocks_deal && <div><Badge tone="danger">{ru.signal.blocksDeal}</Badge></div>}</td>
        <td className="num">{fmtMoney(s.weight)}</td><td>{fmtDate(s.due_date)}</td>
        <td>{[s.feature_id, s.contract_id, s.hypothesis_id].filter(Boolean).map((id) => names.get(id!) ?? id).join(', ') || ru.app.dash}</td>
        <td><div className="row">
          {canWrite && s.status !== 'merged' && <button type="button" className="btn btn-sm" onClick={() => setEditingId(editingId === s.id ? '' : s.id)}>{ru.workflow.triage}</button>}
          <Link className="btn btn-sm" to={`/trace/signal/${s.id}`}>{ru.discovery.hypothesis.trace}</Link>
          {canWrite && s.status !== 'merged' && <button type="button" className="btn btn-sm" onClick={() => setSimilarId(similarId === s.id ? '' : s.id)}>{ru.signal2.similar}</button>}
        </div></td>
      </tr>)}</tbody>
    </table></div>}
    {canWrite && active && <>
      {features.isError && <ErrorBox error={features.error} />}
      {hypotheses.isError && <ErrorBox error={hypotheses.error} />}
      {contracts.isError && <ErrorBox error={contracts.error} />}
      <SignalReview key={`${active.id}-${active.updated_at}`} signal={active} targets={targets} onDone={() => setEditingId('')} />
    </>}
    {canWrite && similar && <SimilarSignals key={similar.id} productId={productId} signal={similar} onClose={() => setSimilarId('')} />}
  </section>
}

function SignalForm({ productId, onDone }: { productId: string; onDone: () => void }) {
  const save = useIngestSignal(productId)
  const [text, setText] = useState('')
  const [account, setAccount] = useState('')
  const [deal, setDeal] = useState('')
  const [segment, setSegment] = useState('')
  const [version, setVersion] = useState('')
  const [amount, setAmount] = useState('')
  const [arr, setArr] = useState('')
  const [blocks, setBlocks] = useState(false)
  const [validation, setValidation] = useState('')
  const submit = (event: FormEvent) => {
    event.preventDefault()
    if (!text.trim()) { setValidation(ru.workflow.textRequired); return }
    let dealAmount: number | undefined, accountArr: number | undefined
    try { dealAmount = parseMinorUnits(amount); accountArr = parseMinorUnits(arr) } catch { setValidation(ru.workflow.invalidMoney); return }
    setValidation('')
    save.mutate({ source: 'manual', text: text.trim(), account_id: account.trim() || undefined, deal_id: deal.trim() || undefined, segment: segment.trim() || undefined, version: version.trim() || undefined,
      deal_amount: dealAmount === undefined ? undefined : { amount: dealAmount, currency: 'RUB' }, account_arr: accountArr === undefined ? undefined : { amount: accountArr, currency: 'RUB' }, blocks_deal: blocks }, { onSuccess: onDone })
  }
  return <form className="card form-inline stack" onSubmit={submit}>
    <h3>{ru.workflow.createSignal}</h3><p className="muted">{ru.workflow.signalHelp}</p>
    <label className="field"><span>{ru.signal.text}</span><textarea autoFocus required rows={3} value={text} onChange={(e) => setText(e.target.value)} /></label>
    <div className="columns">
      <label className="field"><span>{ru.workflow.account}</span><input value={account} onChange={(e) => setAccount(e.target.value)} /></label>
      <label className="field"><span>{ru.workflow.deal}</span><input value={deal} onChange={(e) => setDeal(e.target.value)} /></label>
      <label className="field"><span>{ru.workflow.segment}</span><input value={segment} onChange={(e) => setSegment(e.target.value)} /></label>
      <label className="field"><span>{ru.workflow.version}</span><input value={version} onChange={(e) => setVersion(e.target.value)} /></label>
      <label className="field"><span>{ru.workflow.dealAmount}</span><input inputMode="decimal" value={amount} onChange={(e) => setAmount(e.target.value)} /></label>
      <label className="field"><span>{ru.workflow.accountArr}</span><input inputMode="decimal" value={arr} onChange={(e) => setArr(e.target.value)} /></label>
    </div>
    <p className="muted">{ru.workflow.moneyHint}</p>
    <label className="check"><input type="checkbox" checked={blocks} onChange={(e) => setBlocks(e.target.checked)} />{ru.signal.blocksDeal}</label>
    {validation && <div className="alert alert-error" role="alert">{validation}</div>}{save.isError && <ErrorBox error={save.error} />}
    <div className="row"><button className="btn btn-primary" disabled={save.isPending}>{ru.app.save}</button><button type="button" className="btn" onClick={onDone}>{ru.app.cancel}</button></div>
  </form>
}

type TargetKey = 'feature_id' | 'contract_id' | 'hypothesis_id'
function SignalReview({ signal, targets, onDone }: { signal: Signal; targets: Record<TargetKey, { id: string; name: string }[]>; onDone: () => void }) {
  const triage = useTriageSignal()
  const link = useSetSignalTarget()
  const [status, setStatus] = useState<'new' | 'in_review' | 'rejected'>(signal.status === 'rejected' ? 'rejected' : 'in_review')
  const [due, setDue] = useState(signal.due_date ?? '')
  const [kind, setKind] = useState<TargetKey>('feature_id')
  const [target, setTarget] = useState('')
  const labels: Record<TargetKey, string> = { feature_id: ru.workflow.featureTarget, contract_id: ru.workflow.contractTarget, hypothesis_id: ru.workflow.hypothesisTarget }
  return <div className="card form-inline stack">
    <div className="page-head"><h3>{signal.text}</h3><button type="button" className="btn btn-sm" onClick={onDone}>{ru.common.close}</button></div>
    <form className="row" onSubmit={(e) => { e.preventDefault(); triage.mutate({ id: signal.id, status, due_date: due || undefined }, { onSuccess: onDone }) }}>
      <label className="field"><span>{ru.signal.status}</span><select value={status} onChange={(e) => setStatus(e.target.value as typeof status)}>
        <option value="new">{ru.signal.statuses.new}</option><option value="in_review">{ru.signal.statuses.in_review}</option><option value="rejected">{ru.signal.statuses.rejected}</option>
      </select></label>
      <label className="field"><span>{ru.signal.dueDate}</span><input type="date" value={due} onChange={(e) => setDue(e.target.value)} /></label>
      <button className="btn" disabled={triage.isPending || link.isPending}>{ru.app.save}</button>
    </form>
    {status === 'rejected' && <p className="muted">{ru.workflow.rejectedLinkHint}</p>}
    <form className="row" onSubmit={(e) => { e.preventDefault(); link.mutate({ id: signal.id, [kind]: target }, { onSuccess: onDone }) }}>
      <label className="field"><span>{ru.workflow.linkTarget}</span><select value={kind} onChange={(e) => { setKind(e.target.value as TargetKey); setTarget('') }}>
        {Object.entries(labels).map(([value, label]) => <option value={value} key={value}>{label}</option>)}
      </select></label>
      <label className="field grow"><span>{labels[kind]}</span><select required value={target} onChange={(e) => setTarget(e.target.value)}><option value="">{ru.workflow.chooseTarget}</option>
        {targets[kind].map((t) => <option key={t.id} value={t.id}>{t.name}</option>)}
      </select></label>
      <button className="btn btn-primary" disabled={!target || link.isPending || triage.isPending}>{ru.signal.linkSubmit}</button>
    </form>
    {triage.isError && <ErrorBox error={triage.error} />}{link.isError && <ErrorBox error={link.error} />}
  </div>
}
