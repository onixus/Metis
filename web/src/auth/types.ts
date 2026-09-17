export type AuthMode = 'oidc' | 'token'

export interface AuthProvider {
  readonly mode: AuthMode
  /** Текущий токен доступа или null. Никогда не логировать. */
  getAccessToken(): Promise<string | null>
  /** Начать вход. В token-режиме принимает вставленный токен. */
  login(token?: string): Promise<void>
  /** Завершить вход после редиректа (только OIDC). */
  handleCallback(): Promise<void>
  logout(): Promise<void>
}
