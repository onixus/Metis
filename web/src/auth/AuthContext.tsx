import { useCallback, useEffect, useMemo, useState, type ReactNode } from 'react'
import { auth } from './index'

import { AuthCtx, type AuthState } from './context'

export function AuthProvider({ children }: { children: ReactNode }) {
  const [ready, setReady] = useState(false)
  const [authenticated, setAuthenticated] = useState(false)
  const [sessionVersion, setSessionVersion] = useState(0)

  const expireSession = useCallback(async () => {
    setAuthenticated(false)
    setSessionVersion((version) => version + 1)
    // An API rejection should lead to the local sign-in screen, not an OIDC redirect loop.
    if (auth.mode === 'token') await auth.logout()
  }, [])

  const refresh = useCallback(async () => {
    setAuthenticated(false)
    setReady(false)
    try {
      const token = await auth.getAccessToken()
      setSessionVersion((version) => version + 1)
      setAuthenticated(token !== null)
    } finally {
      setReady(true)
    }
  }, [])

  useEffect(() => {
    let alive = true
    auth
      .getAccessToken()
      .then((token) => {
        if (!alive) return
        setAuthenticated(token !== null)
        setReady(true)
      })
      .catch(() => {
        if (alive) setReady(true)
      })
    return () => {
      alive = false
    }
  }, [])

  const value = useMemo<AuthState>(
    () => ({
      mode: auth.mode,
      ready,
      authenticated,
      sessionVersion,
      expireSession,
      login: async (token) => {
        setAuthenticated(false)
        setSessionVersion((version) => version + 1)
        await auth.login(token)
        await refresh()
      },
      logout: async () => {
        setAuthenticated(false)
        setSessionVersion((version) => version + 1)
        await auth.logout()
      },
      refresh,
    }),
    [ready, authenticated, sessionVersion, refresh, expireSession],
  )

  return <AuthCtx.Provider value={value}>{children}</AuthCtx.Provider>
}
