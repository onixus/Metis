import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { api, unwrap } from './client'
import type { components } from './schema'

export type DeliveryOverview = components['schemas']['DeliveryOverview']
export type DeliverySync = components['schemas']['DeliverySync']
export type DeliveryFieldMapping = components['schemas']['DeliveryFieldMapping']
export const deliveryKeys = { all: ['delivery'] as const, product: (id: string) => ['delivery', 'product', id] as const, connector: ['delivery', 'connector'] as const }

export function useDelivery(productId: string, enabled = true) {
  return useQuery({ queryKey: deliveryKeys.product(productId), enabled: enabled && !!productId, queryFn: async () => unwrap(await api.GET('/products/{productId}/delivery', { params: { path: { productId } } })), refetchInterval: 15_000 })
}
export function useDeliveryConnector(enabled = true) {
  return useQuery({ queryKey: deliveryKeys.connector, enabled, queryFn: async () => unwrap(await api.GET('/admin/delivery')), refetchInterval: 30_000 })
}
export function useSaveDeliveryConnector() {
  const client = useQueryClient()
  return useMutation({ mutationFn: async (body: DeliveryFieldMapping) => unwrap(await api.PUT('/admin/delivery/mapping', { body })), onSuccess: () => client.invalidateQueries({ queryKey: deliveryKeys.all }) })
}
export function useLinkDeliveryFeature(productId: string) {
  const client = useQueryClient()
  return useMutation({
    mutationFn: async ({ featureId, epicKey, project }: { featureId: string; epicKey: string; project: string }) => unwrap(await api.PUT('/features/{featureId}/delivery-mapping', { params: { path: { featureId } }, body: { epic_key: epicKey, project } })),
    onSuccess: async () => { await client.invalidateQueries({ queryKey: deliveryKeys.all }); await client.invalidateQueries({ queryKey: ['products', productId, 'features'] }) },
  })
}
export function useRequestDeliveryEpic(productId: string) {
  const client = useQueryClient()
  return useMutation({
    mutationFn: async ({ featureId, project }: { featureId: string; project: string }) => unwrap(await api.POST('/features/{featureId}/request-epic', { params: { path: { featureId } }, body: { project } })),
    onSuccess: async () => { await client.invalidateQueries({ queryKey: deliveryKeys.all }); await client.invalidateQueries({ queryKey: ['products', productId, 'features'] }) },
  })
}
