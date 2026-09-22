import type { AuthMode } from './types'

type PublicConfig = {
  authMode: AuthMode
  oidcIssuer?: string
  oidcClientId?: string
  oidcScope?: string
}

declare global {
  interface Window {
    __METIS_CONFIG__?: PublicConfig
  }
}

// API injects this public configuration before the application bundle. Vite development
// keeps the existing .env workflow when the static server is not in use.
export function runtimeConfig(): PublicConfig {
  if (window.__METIS_CONFIG__) return window.__METIS_CONFIG__
  return {
    authMode: import.meta.env.VITE_AUTH_MODE === 'oidc' ? 'oidc' : 'token',
    oidcIssuer: import.meta.env.VITE_OIDC_ISSUER as string | undefined,
    oidcClientId: import.meta.env.VITE_OIDC_CLIENT_ID as string | undefined,
    oidcScope: import.meta.env.VITE_OIDC_SCOPE as string | undefined,
  }
}
