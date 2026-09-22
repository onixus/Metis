import { createContext } from 'react'
import type { AuthMode } from './types'

export interface AuthState {
  mode: AuthMode
  ready: boolean
  authenticated: boolean
  sessionVersion: number
  login(token?: string): Promise<void>
  logout(): Promise<void>
  expireSession(): Promise<void>
  refresh(): Promise<void>
}

export const AuthCtx = createContext<AuthState | null>(null)
