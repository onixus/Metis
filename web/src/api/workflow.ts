import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { api, unwrap } from './client'
import { keys, keys2 } from './hooks'
import type { components } from './schema'

export type SignalInput = components['schemas']['SignalInput']
export type RoadmapItemInput = components['schemas']['RoadmapItemInput']
export type ScoringModelInput = components['schemas']['ScoringModelInput']

export function useSaveFeature(productId: string) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: async ({ id, body }: { id?: string; body: components['schemas']['FeatureInput'] }) => id
      ? unwrap(await api.PATCH('/features/{featureId}', { params: { path: { featureId: id } }, body }))
      : unwrap(await api.POST('/products/{productId}/features', { params: { path: { productId } }, body })),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: keys.product(productId) })
      void qc.invalidateQueries({ queryKey: keys2.scoringModels })
    },
  })
}

export function useSignals(productId: string) {
  return useQuery({
    queryKey: [...keys.product(productId), 'signals'],
    queryFn: async ({ signal }) => unwrap(await api.GET('/products/{productId}/signals', { params: { path: { productId } }, signal })),
  })
}

export function useIngestSignal(productId: string) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: async (body: SignalInput) => unwrap(await api.POST('/products/{productId}/signals', { params: { path: { productId } }, body })),
    onSuccess: () => void qc.invalidateQueries({ queryKey: keys.product(productId) }),
  })
}

export function useTriageSignal() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: async (input: { id: string; status: 'new' | 'in_review' | 'rejected'; due_date?: string }) =>
      unwrap(await api.POST('/signals/{signalId}/triage', { params: { path: { signalId: input.id } }, body: { status: input.status, due_date: input.due_date } })),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: keys.products })
      void qc.invalidateQueries({ queryKey: keys.contracts })
      void qc.invalidateQueries({ queryKey: keys2.scoringModels })
      void qc.invalidateQueries({ queryKey: ['trace'] })
    },
  })
}

export function useSetSignalTarget() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: async (input: { id: string; feature_id?: string; contract_id?: string; hypothesis_id?: string }) => {
      const { id, ...body } = input
      return unwrap(await api.POST('/signals/{signalId}/link', { params: { path: { signalId: id } }, body }))
    },
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: keys.products })
      void qc.invalidateQueries({ queryKey: keys.contracts })
      void qc.invalidateQueries({ queryKey: keys2.scoringModels })
      void qc.invalidateQueries({ queryKey: ['trace'] })
    },
  })
}

export function useSaveRoadmapItem(productId: string) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: async ({ id, body }: { id?: string; body: RoadmapItemInput }) => id
      ? unwrap(await api.PATCH('/roadmap/items/{itemId}', { params: { path: { itemId: id } }, body }))
      : unwrap(await api.POST('/products/{productId}/roadmap/items', { params: { path: { productId } }, body })),
    onSuccess: () => void qc.invalidateQueries({ queryKey: [...keys.product(productId), 'roadmap'] }),
  })
}

export function useCreateScoringModel() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: async (body: ScoringModelInput) => unwrap(await api.POST('/scoring-models', { body })),
    onSuccess: () => void qc.invalidateQueries({ queryKey: keys2.scoringModels }),
  })
}

export function useSaveScoreInputs(productId: string, modelId: string) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: async (input: { featureId: string; values: Record<string, string> }) => unwrap(await api.PUT('/scoring-models/{modelId}/features/{featureId}/inputs', {
      params: { path: { modelId, featureId: input.featureId } }, body: { values: input.values },
    })),
    onSuccess: () => void qc.invalidateQueries({ queryKey: keys2.rank(modelId, productId) }),
  })
}
