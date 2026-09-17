import { useContext } from 'react'
import { AuthCtx, type AuthState } from './context'

export function useAuth(): AuthState {
  const ctx = useContext(AuthCtx)
  if (!ctx) throw new Error('AuthProvider missing')
  return ctx
}
