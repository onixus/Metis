import { oidcAuth } from './oidc'
import { tokenAuth } from './token'
import type { AuthMode, AuthProvider } from './types'

export type { AuthMode, AuthProvider } from './types'

export function resolveAuthMode(): AuthMode {
  return import.meta.env.VITE_AUTH_MODE === 'oidc' ? 'oidc' : 'token'
}

export const auth: AuthProvider = resolveAuthMode() === 'oidc' ? oidcAuth : tokenAuth
