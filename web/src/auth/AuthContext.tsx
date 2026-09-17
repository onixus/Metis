import { useCallback, useEffect, useMemo, useState, type ReactNode } from 'react'
import { auth } from './index'

import { AuthCtx, type AuthState } from './context'

export function AuthProvider({ children }: { children: ReactNode }) {
  const [ready, setReady] = useState(false)
  const [authenticated, setAuthenticated] = useState(false)

  const refresh = useCallback(async () => {
    const token = await auth.getAccessToken()
    setAuthenticated(token !== null)
    setReady(true)
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
      login: async (token) => {
        await auth.login(token)
        await refresh()
      },
      logout: async () => {
        await auth.logout()
        setAuthenticated(false)
      },
      refresh,
    }),
    [ready, authenticated, refresh],
  )

  return <AuthCtx.Provider value={value}>{children}</AuthCtx.Provider>
}
