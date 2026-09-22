import { UserManager, WebStorageStateStore } from 'oidc-client-ts'
import type { AuthProvider } from './types'
import { runtimeConfig } from './runtime'

let manager: UserManager | null = null

function getManager(): UserManager {
  if (manager) return manager
  const config = runtimeConfig()
  const authority = config.oidcIssuer
  const clientId = config.oidcClientId
  if (!authority || !clientId) {
    throw new Error('oidc_not_configured')
  }
  manager = new UserManager({
    authority,
    client_id: clientId,
    redirect_uri: `${window.location.origin}/callback`,
    post_logout_redirect_uri: `${window.location.origin}/login`,
    response_type: 'code',
    scope: config.oidcScope ?? 'openid profile email',
    userStore: new WebStorageStateStore({ store: window.sessionStorage }),
    automaticSilentRenew: true,
  })
  return manager
}

export const oidcAuth: AuthProvider = {
  mode: 'oidc',
  getAccessToken: async () => {
    const user = await getManager().getUser()
    if (!user || user.expired) return null
    return user.access_token
  },
  login: async () => {
    await getManager().signinRedirect()
  },
  handleCallback: async () => {
    await getManager().signinCallback()
  },
  logout: async () => {
    const m = getManager()
    await m.removeUser()
    try {
      await m.signoutRedirect()
    } catch {
      // Провайдер без end_session_endpoint: локального выхода достаточно.
    }
  },
}
