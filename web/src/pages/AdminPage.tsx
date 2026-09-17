import { Navigate } from 'react-router-dom'
import { useMe, useVerifyAudit } from '../api/hooks'
import { isAdmin } from '../lib/roles'
import { Badge, ErrorBox, Loading } from '../components/Status'
import { ru } from '../i18n/ru'

export function AdminPage() {
  const me = useMe()
  const verify = useVerifyAudit()

  if (me.isPending) return <Loading />
  if (me.isError) return <ErrorBox error={me.error} />
  if (!isAdmin(me.data.roles)) return <Navigate to="/" replace />

  return (
    <section className="stack">
      <h1>{ru.admin.title}</h1>
      <div className="card stack">
        <h2>{ru.admin.auditTitle}</h2>
        <p className="muted">{ru.admin.auditHint}</p>
        <div className="row">
          <button type="button" className="btn btn-primary" disabled={verify.isPending} onClick={() => verify.mutate()}>
            {verify.isPending ? ru.admin.auditRunning : ru.admin.auditRun}
          </button>
        </div>
        {verify.isError && <ErrorBox error={verify.error} />}
        {verify.data && (
          <dl className="kv">
            <dt>{ru.admin.checked}</dt>
            <dd>{verify.data.checked}</dd>
            <dt>{ru.app.error}</dt>
            <dd>{verify.data.ok ? <Badge tone="ok">{ru.admin.ok}</Badge> : <Badge tone="danger">{ru.admin.broken}</Badge>}</dd>
            {!verify.data.ok && (
              <>
                <dt>{ru.admin.brokenSeq}</dt>
                <dd>{verify.data.broken_seq ?? ru.app.dash}</dd>
                <dt>{ru.admin.reason}</dt>
                <dd>{verify.data.reason ?? ru.app.dash}</dd>
              </>
            )}
          </dl>
        )}
      </div>
    </section>
  )
}
