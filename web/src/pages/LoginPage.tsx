import { useState, type FormEvent } from 'react'
import { Navigate, useLocation, useNavigate } from 'react-router-dom'
import { useAuth } from '../auth/useAuth'
import { ru } from '../i18n/ru'
import { ErrorBox } from '../components/Status'

export function LoginPage() {
  const { mode, authenticated, login } = useAuth()
  const navigate = useNavigate()
  const location = useLocation()
  const [token, setToken] = useState('')
  const [error, setError] = useState<unknown>(null)
  const [busy, setBusy] = useState(false)
  const from = (location.state as { from?: string } | null)?.from ?? '/'

  if (authenticated) return <Navigate to={from} replace />

  const submitToken = async (e: FormEvent) => {
    e.preventDefault()
    if (!token.trim()) {
      setError(new Error(ru.auth.tokenRequired))
      return
    }
    setBusy(true)
    try {
      await login(token)
      setToken('')
      navigate(from, { replace: true })
    } catch (err) {
      setError(err)
    } finally {
      setBusy(false)
    }
  }

  const startOidc = async () => {
    setBusy(true)
    try {
      await login()
    } catch (err) {
      setError(err)
      setBusy(false)
    }
  }

  return (
    <div className="login">
      <div className="card login-card">
        <h1>{ru.app.title}</h1>
        <h2>{ru.auth.loginTitle}</h2>
        {error !== null && <ErrorBox error={error} />}
        {mode === 'token' ? (
          <form onSubmit={(e) => void submitToken(e)} className="stack">
            <p className="muted">{ru.auth.tokenModeHint}</p>
            <label className="field">
              <span>{ru.auth.tokenLabel}</span>
              <textarea
                value={token}
                onChange={(e) => setToken(e.target.value)}
                placeholder={ru.auth.tokenPlaceholder}
                rows={4}
                autoComplete="off"
                spellCheck={false}
              />
            </label>
            <button type="submit" className="btn btn-primary" disabled={busy}>
              {ru.auth.tokenSubmit}
            </button>
          </form>
        ) : (
          <div className="stack">
            <p className="muted">{ru.auth.oidcHint}</p>
            <button type="button" className="btn btn-primary" disabled={busy} onClick={() => void startOidc()}>
              {ru.auth.oidcSubmit}
            </button>
          </div>
        )}
      </div>
    </div>
  )
}
