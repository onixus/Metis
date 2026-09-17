import { useEffect, useState } from 'react'
import { Link, useNavigate } from 'react-router-dom'
import { auth } from '../auth'
import { useAuth } from '../auth/useAuth'
import { ErrorBox, Loading } from '../components/Status'
import { ru } from '../i18n/ru'

export function CallbackPage() {
  const navigate = useNavigate()
  const { refresh } = useAuth()
  const [error, setError] = useState<unknown>(null)

  useEffect(() => {
    let cancelled = false
    auth
      .handleCallback()
      .then(refresh)
      .then(() => {
        if (!cancelled) navigate('/', { replace: true })
      })
      .catch((err: unknown) => {
        if (!cancelled) setError(err)
      })
    return () => {
      cancelled = true
    }
  }, [navigate, refresh])

  if (error !== null) {
    return (
      <div className="login">
        <div className="card login-card stack">
          <h2>{ru.auth.callbackError}</h2>
          <ErrorBox error={error} />
          <Link to="/login">{ru.auth.backToLogin}</Link>
        </div>
      </div>
    )
  }
  return (
    <div className="login">
      <Loading label={ru.auth.callbackProcessing} />
    </div>
  )
}
