import { useMutation, useQueries, useQuery, useQueryClient } from '@tanstack/react-query'
import { api, ApiError, unwrap } from './client'
import type { AccessLevel, Feature, Me } from './types'

export const keys = {
  me: ['me'] as const,
  products: ['products'] as const,
  product: (id: string) => ['products', id] as const,
  strategic: (id: string) => ['products', id, 'strategic'] as const,
  features: (id: string) => ['products', id, 'features'] as const,
  featureValues: (id: string) => ['products', id, 'feature-values'] as const,
  links: ['links'] as const,
  hubs: ['hubs'] as const,
  contracts: ['contracts'] as const,
  triage: (id: string) => ['products', id, 'triage'] as const,
  roadmap: (id: string, view: string) => ['products', id, 'roadmap', view] as const,
  history: (itemId: string) => ['roadmap-items', itemId, 'history'] as const,
}

export function useMe() {
  return useQuery({ queryKey: keys.me, queryFn: async () => unwrap(await api.GET('/me')), staleTime: 60_000 })
}

export function accessLevel(me: Me | undefined, productId: string): AccessLevel {
  if (!me) return 'none'
  return me.products[productId] ?? me.all_products
}

export function useProducts() {
  return useQuery({ queryKey: keys.products, queryFn: async () => unwrap(await api.GET('/products')) })
}

export function useProduct(id: string) {
  return useQuery({
    queryKey: keys.product(id),
    queryFn: async () => unwrap(await api.GET('/products/{productId}', { params: { path: { productId: id } } })),
  })
}

export function useStrategic(id: string) {
  return useQuery({
    queryKey: keys.strategic(id),
    queryFn: async () =>
      unwrap(await api.GET('/products/{productId}/strategic', { params: { path: { productId: id } } })),
  })
}

export function useFeatures(id: string, enabled = true) {
  return useQuery({
    queryKey: keys.features(id),
    enabled,
    queryFn: async () =>
      unwrap(await api.GET('/products/{productId}/features', { params: { path: { productId: id } } })),
  })
}

/** Бэклоги нескольких продуктов; продукты без доступа помечаются, а не роняют экран. */
export function useFeaturesOfProducts(ids: string[]) {
  return useQueries({
    queries: ids.map((id) => ({
      queryKey: keys.features(id),
      queryFn: async (): Promise<Feature[]> =>
        unwrap(await api.GET('/products/{productId}/features', { params: { path: { productId: id } } })),
      retry: (count: number, err: unknown) => !(err instanceof ApiError && err.status === 403) && count < 2,
    })),
  })
}

export function useFeatureValues(id: string, enabled = true) {
  return useQuery({
    queryKey: keys.featureValues(id),
    enabled,
    queryFn: async () =>
      unwrap(await api.GET('/products/{productId}/feature-values', { params: { path: { productId: id } } })),
  })
}

export function useLinks() {
  return useQuery({ queryKey: keys.links, queryFn: async () => unwrap(await api.GET('/links')) })
}

export function useHubs() {
  return useQuery({ queryKey: keys.hubs, queryFn: async () => unwrap(await api.GET('/hubs')) })
}

export function useContracts() {
  return useQuery({ queryKey: keys.contracts, queryFn: async () => unwrap(await api.GET('/contracts')) })
}

export function useTriageQueue(id: string, enabled = true) {
  return useQuery({
    queryKey: keys.triage(id),
    enabled,
    queryFn: async () =>
      unwrap(await api.GET('/products/{productId}/signals/triage', { params: { path: { productId: id } } })),
  })
}

export function useShiftDate(productId: string) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: async (input: { featureId: string; planned_date: string; reason: string }) =>
      unwrap(
        await api.POST('/features/{featureId}/shift-date', {
          params: { path: { featureId: input.featureId } },
          body: { planned_date: input.planned_date, reason: input.reason },
        }),
      ),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: keys.features(productId) })
      void qc.invalidateQueries({ queryKey: keys.strategic(productId) })
      void qc.invalidateQueries({ queryKey: keys.contracts })
    },
  })
}

export function useLinkSignal(productId: string) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: async (input: { signalId: string; featureId: string }) =>
      unwrap(
        await api.POST('/signals/{signalId}/link', {
          params: { path: { signalId: input.signalId } },
          body: { feature_id: input.featureId },
        }),
      ),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: keys.triage(productId) })
      void qc.invalidateQueries({ queryKey: keys.featureValues(productId) })
    },
  })
}

export type RoadmapView = 'timeline' | 'now-next-later' | 'by-release'

export function useRoadmapTimeline(id: string, enabled: boolean) {
  return useQuery({
    queryKey: keys.roadmap(id, 'timeline'),
    enabled,
    queryFn: async () =>
      unwrap(await api.GET('/products/{productId}/roadmap/timeline', { params: { path: { productId: id } } })),
  })
}

export function useRoadmapNnl(id: string, enabled: boolean) {
  return useQuery({
    queryKey: keys.roadmap(id, 'now-next-later'),
    enabled,
    queryFn: async () =>
      unwrap(
        await api.GET('/products/{productId}/roadmap/now-next-later', { params: { path: { productId: id } } }),
      ),
  })
}

export function useRoadmapByRelease(id: string, enabled: boolean) {
  return useQuery({
    queryKey: keys.roadmap(id, 'by-release'),
    enabled,
    queryFn: async () =>
      unwrap(await api.GET('/products/{productId}/roadmap/by-release', { params: { path: { productId: id } } })),
  })
}

export function useChangeItemDates(productId: string) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: async (input: { itemId: string; start_date?: string; end_date?: string; reason: string }) =>
      unwrap(
        await api.POST('/roadmap/items/{itemId}/change-dates', {
          params: { path: { itemId: input.itemId } },
          body: { start_date: input.start_date, end_date: input.end_date, reason: input.reason },
        }),
      ),
    onSuccess: (_data, input) => {
      void qc.invalidateQueries({ queryKey: ['products', productId, 'roadmap'] })
      void qc.invalidateQueries({ queryKey: keys.history(input.itemId) })
    },
  })
}

export function useItemHistory(itemId: string, enabled: boolean) {
  return useQuery({
    queryKey: keys.history(itemId),
    enabled,
    queryFn: async () =>
      unwrap(await api.GET('/roadmap/items/{itemId}/history', { params: { path: { itemId } } })),
  })
}

export function useVerifyAudit() {
  return useMutation({ mutationFn: async () => unwrap(await api.POST('/admin/audit/verify')) })
}
