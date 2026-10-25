import { CircleCheck, TriangleAlert, CircleAlert, Info, Circle } from 'lucide-react'
import { errorMessage } from '../api/client'
import { ru } from '../i18n/ru'

export function Loading({ label }: { label?: string }) {
  return <p className="muted">{label ?? ru.app.loading}</p>
}

export function ErrorBox({ error, onRetry }: { error: unknown; onRetry?: () => void }) {
  return (
    <div className="alert alert-error" role="alert">
      <span><CircleAlert aria-hidden="true" /> {errorMessage(error)}</span>
      {onRetry && (
        <button type="button" className="btn btn-sm" onClick={onRetry}>
          {ru.app.retry}
        </button>
      )}
    </div>
  )
}

export function Empty({ text }: { text?: string }) {
  return <p className="muted">{text ?? ru.app.empty}</p>
}

export function Badge({ children, tone = 'neutral' }: { children: React.ReactNode; tone?: 'neutral' | 'ok' | 'warn' | 'danger' | 'info' }) {
  const Icon = { neutral: Circle, ok: CircleCheck, warn: TriangleAlert, danger: CircleAlert, info: Info }[tone]
  return <span className={`badge badge-${tone}`}><Icon aria-hidden="true" />{children}</span>
}
