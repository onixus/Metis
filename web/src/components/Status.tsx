import { errorMessage } from '../api/client'
import { ru } from '../i18n/ru'

export function Loading({ label }: { label?: string }) {
  return <p className="muted">{label ?? ru.app.loading}</p>
}

export function ErrorBox({ error, onRetry }: { error: unknown; onRetry?: () => void }) {
  return (
    <div className="alert alert-error" role="alert">
      <span>{errorMessage(error)}</span>
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
  return <span className={`badge badge-${tone}`}>{children}</span>
}
