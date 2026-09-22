import { oidcAuth } from './oidc'
import { tokenAuth } from './token'
import type { AuthMode, AuthProvider } from './types'
import { runtimeConfig } from './runtime'

export type { AuthMode, AuthProvider } from './types'

export function resolveAuthMode(): AuthMode {
  return runtimeConfig().authMode
}

export const auth: AuthProvider = resolveAuthMode() === 'oidc' ? oidcAuth : tokenAuth
