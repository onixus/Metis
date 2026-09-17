import createClient, { type Middleware } from 'openapi-fetch'
import { auth } from '../auth'
import { ru } from '../i18n/ru'
import type { components, paths } from './schema'

export type Problem = components['schemas']['Problem']
export type CycleProblem = components['schemas']['CycleProblem']

/** Ошибка API в понятном виде. Не содержит токенов и заголовков. */
export class ApiError extends Error {
  readonly status: number
  readonly problem: Problem | null
  readonly cycle: string[] | null
  readonly field: string | null

  constructor(status: number, problem: Problem | null, message: string) {
    super(message)
    this.name = 'ApiError'
    this.status = status
    this.problem = problem
    this.cycle = problem && 'cycle' in problem && Array.isArray((problem as CycleProblem).cycle)
      ? (problem as CycleProblem).cycle
      : null
    this.field = problem?.field ?? null
  }
}

function isProblem(x: unknown): x is Problem {
  return typeof x === 'object' && x !== null && 'title' in x && 'status' in x
}

/** Превращает problem+json (в том числе 409 с cycle) в сообщение для пользователя. */
export function describeProblem(status: number, body: unknown): string {
  if (status === 401) return ru.app.unauthorized
  if (status === 403) return ru.app.forbidden
  if (!isProblem(body)) return `${ru.app.error} ${status}`
  const parts: string[] = [body.title]
  if (body.detail) parts.push(body.detail)
  if (body.field) parts.push(`(${body.field})`)
  if ('cycle' in body && Array.isArray((body as CycleProblem).cycle)) {
    const cycle = (body as CycleProblem).cycle
    parts.push(`${ru.app.cycle}: ${cycle.join(' → ')}`)
  }
  return parts.join('. ')
}

export function errorMessage(err: unknown): string {
  if (err instanceof ApiError) return err.message
  if (err instanceof TypeError) return ru.app.networkError
  if (err instanceof Error && err.message) return err.message
  return ru.app.unknownError
}

const authMiddleware: Middleware = {
  async onRequest({ request }) {
    const token = await auth.getAccessToken()
    if (token) request.headers.set('Authorization', `Bearer ${token}`)
    return request
  },
}

export const api = createClient<paths>({ baseUrl: '/api/v1' })
api.use(authMiddleware)

interface FetchResult<T> {
  data?: T
  error?: unknown
  response: Response
}

/** Разворачивает ответ openapi-fetch: данные либо ApiError. */
export function unwrap<T>(res: FetchResult<T>): T {
  if (res.error !== undefined || !res.response.ok) {
    const status = res.response.status
    const problem = isProblem(res.error) ? res.error : null
    throw new ApiError(status, problem, describeProblem(status, res.error))
  }
  return res.data as T
}
