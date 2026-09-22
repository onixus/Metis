import { QueryClient } from '@tanstack/react-query'

/** A client belongs to exactly one authenticated session. Never reuse it for another identity. */
export function createSessionClient(): QueryClient {
  return new QueryClient({
    defaultOptions: {
      queries: {
        retry: (count, error) => {
          const status = (error as { status?: number }).status
          return !(typeof status === 'number' && status < 500) && count < 2
        },
        refetchOnWindowFocus: false,
      },
    },
  })
}

export function discardSessionClient(client: QueryClient): void {
  void client.cancelQueries()
  client.clear()
}
