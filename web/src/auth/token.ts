import type { AuthProvider } from './types'

const KEY = 'metis.access_token'

function read(): string | null {
  try {
    return window.sessionStorage.getItem(KEY)
  } catch {
    return null
  }
}

/** Стендовый режим: токен вставляется вручную и живёт в sessionStorage вкладки. */
export const tokenAuth: AuthProvider = {
  mode: 'token',
  getAccessToken: () => Promise.resolve(read()),
  login: (token) => {
    const value = token?.trim() ?? ''
    if (!value) return Promise.reject(new Error('token_required'))
    window.sessionStorage.setItem(KEY, value)
    return Promise.resolve()
  },
  handleCallback: () => Promise.resolve(),
  logout: () => {
    window.sessionStorage.removeItem(KEY)
    return Promise.resolve()
  },
}
