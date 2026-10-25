import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { api, unwrap } from './client'
import { keys } from './hooks'

export function useCapabilities(productId: string) {
  return useQuery({
    queryKey: [...keys.product(productId), 'capabilities'],
    queryFn: async ({ signal }) => unwrap(await api.GET('/products/{productId}/capabilities', { params: { path: { productId } }, signal })),
  })
}

export function useCreateCapability(productId: string) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: async (name: string) => unwrap(await api.POST('/products/{productId}/capabilities', { params: { path: { productId } }, body: { name } })),
    onSuccess: () => void qc.invalidateQueries({ queryKey: [...keys.product(productId), 'capabilities'] }),
  })
}

export function useRequirements(featureId: string) {
  return useQuery({
    queryKey: ['features', featureId, 'requirements'],
    queryFn: async ({ signal }) => unwrap(await api.GET('/features/{featureId}/requirements', { params: { path: { featureId } }, signal })),
  })
}

export function useCreateRequirement(featureId: string) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: async (text: string) => unwrap(await api.POST('/features/{featureId}/requirements', { params: { path: { featureId } }, body: { text } })),
    onSuccess: () => void qc.invalidateQueries({ queryKey: ['features', featureId, 'requirements'] }),
  })
}
