import { useMutation, useQueries, useQuery, useQueryClient } from '@tanstack/react-query'
import { api, ApiError, unwrap } from './client'
import type { components } from './schema'
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

export function useProduct(id: string, enabled = true) {
  return useQuery({
    queryKey: keys.product(id),
    enabled,
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

export type LinkInput = components['schemas']['LinkInput']

/** Создание связи из графа: продуктовой или между фичами. При цикле сервер отвечает 409 с путём. */
export function useCreateLink() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: async (input: LinkInput) => unwrap(await api.POST('/links', { body: input })),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: keys.links })
      void qc.invalidateQueries({ queryKey: keys.hubs })
      void qc.invalidateQueries({ queryKey: ['products'] })
    },
  })
}

export function useDeleteLink() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: async (linkId: string) =>
      unwrap(await api.DELETE('/links/{linkId}', { params: { path: { linkId } } })),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: keys.links })
      void qc.invalidateQueries({ queryKey: keys.hubs })
      void qc.invalidateQueries({ queryKey: ['products'] })
    },
  })
}

/** Компактная фича для раскрытого узла графа: из бэклога, а без приватного доступа — из стратегического среза. */
export interface GraphFeature {
  id: string
  name: string
  status: string
  planned_date?: string | null
  affected: boolean
  privateAccess: boolean
}

export interface ExpandedFeatures {
  data: GraphFeature[] | undefined
  isPending: boolean
}

/** Стабильная функция combine: результат мемоизируется TanStack, пока данные запросов не меняются. */
const combineExpanded = (results: { data: GraphFeature[] | undefined; isPending: boolean }[]): ExpandedFeatures[] =>
  results.map((r) => ({ data: r.data, isPending: r.isPending }))

export function useExpandedFeatures(ids: string[]) {
  return useQueries({
    combine: combineExpanded,
    queries: ids.map((id) => ({
      queryKey: ['products', id, 'graph-features'] as const,
      queryFn: async (): Promise<GraphFeature[]> => {
        const res = await api.GET('/products/{productId}/features', { params: { path: { productId: id } } })
        if (res.response.status !== 403) {
          return unwrap(res).map((f) => ({
            id: f.id, name: f.name, status: f.status, planned_date: f.planned_date, affected: f.affected, privateAccess: true,
          }))
        }
        const slice = unwrap(await api.GET('/products/{productId}/strategic', { params: { path: { productId: id } } }))
        return slice.features.map((f) => ({
          id: f.id, name: f.name, status: f.status, planned_date: f.planned_date, affected: f.affected, privateAccess: false,
        }))
      },
      retry: (count: number, err: unknown) => !(err instanceof ApiError && err.status === 403) && count < 2,
    })),
  })
}

export type ProductInput = components['schemas']['ProductInput']
export type FeatureInput = components['schemas']['FeatureInput']

/** Конструктор продукта (PG-01): создание продукта, затем стартовые фичи и связи. */
export function useCreateProduct() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: async (input: ProductInput) => unwrap(await api.POST('/products', { body: input })),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: keys.products })
      void qc.invalidateQueries({ queryKey: keys.hubs })
    },
  })
}

export function useCreateFeature() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: async (input: { productId: string; body: FeatureInput }) =>
      unwrap(await api.POST('/products/{productId}/features', { params: { path: { productId: input.productId } }, body: input.body })),
    onSuccess: (_f, input) => {
      void qc.invalidateQueries({ queryKey: keys.features(input.productId) })
      void qc.invalidateQueries({ queryKey: ['products', input.productId, 'graph-features'] })
    },
  })
}

export function useUpdateProduct() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: async (input: { id: string; body: ProductInput }) =>
      unwrap(await api.PUT('/products/{productId}', { params: { path: { productId: input.id } }, body: input.body })),
    onSuccess: (_p, input) => {
      void qc.invalidateQueries({ queryKey: keys.products })
      void qc.invalidateQueries({ queryKey: keys.product(input.id) })
      void qc.invalidateQueries({ queryKey: keys.strategic(input.id) })
      void qc.invalidateQueries({ queryKey: keys.hubs })
    },
  })
}

export function useDeleteProduct() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: async (id: string) => unwrap(await api.DELETE('/products/{productId}', { params: { path: { productId: id } } })),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ['products'] })
      void qc.invalidateQueries({ queryKey: keys.links })
      void qc.invalidateQueries({ queryKey: keys.hubs })
    },
  })
}

// ---------------------------------------------------------------------------
// Этап 2: discovery (DS), обязательства (CT), compliance (CM), решения (DA), релизы (RM-04/05), PR-04/05.
// ---------------------------------------------------------------------------

import type {
  CommitmentInput,
  CustomEntity,
  CustomFieldDefInput,
  DecisionInput,
  DecisionStatus,
  EvidenceInput,
  EvidenceItemInput,
  GateUpdate,
  HypothesisInput,
  ImpactClass,
  InsightInput,
  InterviewInput,
  Money,
  ProductType,
  ReleaseInput,
  RequirementSetInput,
  TraceKind,
  TrackInput,
  TrackTemplateInput,
} from './types'

export const keys2 = {
  hypotheses: (p: string) => ['products', p, 'hypotheses'] as const,
  interviews: (p: string) => ['products', p, 'interviews'] as const,
  insights: (p: string) => ['products', p, 'insights'] as const,
  evidence: (p: string) => ['products', p, 'evidence'] as const,
  trace: (kind: string, id: string) => ['trace', kind, id] as const,
  similar: (signalId: string) => ['signals', signalId, 'similar'] as const,
  customFields: (entity: string) => ['admin', 'custom-fields', entity] as const,
  customStatuses: (entity: string) => ['admin', 'custom-statuses', entity] as const,
  commitments: (p: string) => ['products', p, 'commitments'] as const,
  alerts: (p: string) => ['products', p, 'commitment-alerts'] as const,
  requirementSets: ['admin', 'requirement-sets'] as const,
  trackTemplates: ['admin', 'track-templates'] as const,
  tracks: (p: string) => ['products', p, 'tracks'] as const,
  trackEvidence: (t: string) => ['tracks', t, 'evidence'] as const,
  baselines: (p: string) => ['products', p, 'baselines'] as const,
  impact: (f: string) => ['features', f, 'impact'] as const,
  impactHistory: (f: string) => ['features', f, 'impact', 'history'] as const,
  affectedBaselines: (f: string) => ['features', f, 'affected-baselines'] as const,
  flags: (f: string) => ['features', f, 'flags'] as const,
  cost: (f: string) => ['features', f, 'cost'] as const,
  decisions: (p: string | undefined, status: string | undefined) => ['decisions', p ?? '', status ?? ''] as const,
  releases: (p: string) => ['products', p, 'releases'] as const,
  readiness: (r: string) => ['releases', r, 'readiness'] as const,
  scoringModels: ['scoring-models'] as const,
  rank: (m: string, p: string) => ['scoring-models', m, 'products', p, 'rank'] as const,
}

// ---- Discovery -------------------------------------------------------------

export function useHypotheses(productId: string, enabled = true) {
  return useQuery({
    queryKey: keys2.hypotheses(productId),
    enabled,
    queryFn: async () => unwrap(await api.GET('/products/{productId}/hypotheses', { params: { path: { productId } } })),
  })
}

export function useCreateHypothesis(productId: string) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: async (body: HypothesisInput) =>
      unwrap(await api.POST('/products/{productId}/hypotheses', { params: { path: { productId } }, body })),
    onSuccess: () => void qc.invalidateQueries({ queryKey: keys2.hypotheses(productId) }),
  })
}

export function useUpdateHypothesis(productId: string) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: async (input: { id: string; body: HypothesisInput }) =>
      unwrap(await api.PUT('/hypotheses/{hypothesisId}', { params: { path: { hypothesisId: input.id } }, body: input.body })),
    onSuccess: () => void qc.invalidateQueries({ queryKey: keys2.hypotheses(productId) }),
  })
}

export function useChangeHypothesisStatus(productId: string) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: async (input: { id: string; status: string; resolution?: string }) =>
      unwrap(
        await api.POST('/hypotheses/{hypothesisId}/status', {
          params: { path: { hypothesisId: input.id } },
          body: { status: input.status, resolution: input.resolution },
        }),
      ),
    onSuccess: () => void qc.invalidateQueries({ queryKey: keys2.hypotheses(productId) }),
  })
}

export function useInterviews(productId: string, enabled = true) {
  return useQuery({
    queryKey: keys2.interviews(productId),
    enabled,
    queryFn: async () => unwrap(await api.GET('/products/{productId}/interviews', { params: { path: { productId } } })),
  })
}

export function useCreateInterview(productId: string) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: async (body: InterviewInput) =>
      unwrap(await api.POST('/products/{productId}/interviews', { params: { path: { productId } }, body })),
    onSuccess: () => void qc.invalidateQueries({ queryKey: keys2.interviews(productId) }),
  })
}

export function useInsights(productId: string, enabled = true) {
  return useQuery({
    queryKey: keys2.insights(productId),
    enabled,
    queryFn: async () => unwrap(await api.GET('/products/{productId}/insights', { params: { path: { productId } } })),
  })
}

export function useCreateInsight(productId: string) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: async (body: InsightInput) =>
      unwrap(await api.POST('/products/{productId}/insights', { params: { path: { productId } }, body })),
    onSuccess: () => void qc.invalidateQueries({ queryKey: keys2.insights(productId) }),
  })
}

export function useEvidence(productId: string, enabled = true) {
  return useQuery({
    queryKey: keys2.evidence(productId),
    enabled,
    queryFn: async () => unwrap(await api.GET('/products/{productId}/evidence', { params: { path: { productId } } })),
  })
}

export function useCreateEvidence(productId: string) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: async (body: EvidenceInput) =>
      unwrap(await api.POST('/products/{productId}/evidence', { params: { path: { productId } }, body })),
    onSuccess: () => void qc.invalidateQueries({ queryKey: keys2.evidence(productId) }),
  })
}

export function useUpdateEvidence(productId: string) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: async (input: { id: string; body: EvidenceInput }) =>
      unwrap(await api.PUT('/evidence/{evidenceId}', { params: { path: { evidenceId: input.id } }, body: input.body })),
    onSuccess: () => void qc.invalidateQueries({ queryKey: keys2.evidence(productId) }),
  })
}

export function useTrace(kind: Exclude<TraceKind, 'decision'>, id: string) {
  return useQuery({
    queryKey: keys2.trace(kind, id),
    queryFn: async () => unwrap(await api.GET('/trace/{kind}/{id}', { params: { path: { kind, id } } })),
  })
}

export function useSimilarSignals(signalId: string, enabled: boolean) {
  return useQuery({
    queryKey: keys2.similar(signalId),
    enabled,
    queryFn: async () =>
      unwrap(await api.GET('/signals/{signalId}/similar', { params: { path: { signalId }, query: { limit: 10 } } })),
  })
}

export function useMergeSignals(productId: string) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: async (input: { signalId: string; duplicate_ids: string[] }) =>
      unwrap(
        await api.POST('/signals/{signalId}/merge', {
          params: { path: { signalId: input.signalId } },
          body: { duplicate_ids: input.duplicate_ids },
        }),
      ),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: keys.triage(productId) })
      void qc.invalidateQueries({ queryKey: ['signals'] })
    },
  })
}

export function useLinkSignalToHypothesis(productId: string) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: async (input: { signalId: string; hypothesisId: string }) =>
      unwrap(
        await api.POST('/signals/{signalId}/link', {
          params: { path: { signalId: input.signalId } },
          body: { hypothesis_id: input.hypothesisId },
        }),
      ),
    onSuccess: () => void qc.invalidateQueries({ queryKey: keys.triage(productId) }),
  })
}

export function useCustomFields(entity: CustomEntity, enabled = true) {
  return useQuery({
    queryKey: keys2.customFields(entity),
    enabled,
    queryFn: async () => unwrap(await api.GET('/admin/custom-fields', { params: { query: { entity } } })),
  })
}

export function useCustomStatuses(entity: CustomEntity, enabled = true) {
  return useQuery({
    queryKey: keys2.customStatuses(entity),
    enabled,
    retry: false,
    queryFn: async () => unwrap(await api.GET('/admin/custom-statuses', { params: { query: { entity } } })),
  })
}

export function useDefineCustomField() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: async (body: CustomFieldDefInput) => unwrap(await api.POST('/admin/custom-fields', { body })),
    onSuccess: (_d, body) => void qc.invalidateQueries({ queryKey: keys2.customFields(body.entity) }),
  })
}

// ---- Обязательства ---------------------------------------------------------

export function useCommitments(productId: string, enabled = true) {
  return useQuery({
    queryKey: keys2.commitments(productId),
    enabled,
    queryFn: async () => unwrap(await api.GET('/products/{productId}/commitments', { params: { path: { productId } } })),
  })
}

export function useCommitmentAlerts(productId: string, open: boolean, enabled = true) {
  return useQuery({
    queryKey: [...keys2.alerts(productId), open] as const,
    enabled,
    queryFn: async () =>
      unwrap(await api.GET('/products/{productId}/commitment-alerts', { params: { path: { productId }, query: { open } } })),
  })
}

function useCommitmentMutation<TInput, TOut>(productId: string, fn: (input: TInput) => Promise<TOut>) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: fn,
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: keys2.commitments(productId) })
      void qc.invalidateQueries({ queryKey: keys2.alerts(productId) })
      void qc.invalidateQueries({ queryKey: ['products', productId, 'roadmap'] })
    },
  })
}

export function useCreateCommitment(productId: string) {
  return useCommitmentMutation(productId, async (body: CommitmentInput) =>
    unwrap(await api.POST('/products/{productId}/commitments', { params: { path: { productId } }, body })),
  )
}

export function useUpdateCommitment(productId: string) {
  return useCommitmentMutation(productId, async (input: { id: string; body: CommitmentInput }) =>
    unwrap(await api.PUT('/commitments/{commitmentId}', { params: { path: { commitmentId: input.id } }, body: input.body })),
  )
}

export function useFulfilCommitment(productId: string) {
  return useCommitmentMutation(productId, async (id: string) =>
    unwrap(await api.POST('/commitments/{commitmentId}/fulfil', { params: { path: { commitmentId: id } } })),
  )
}

export function useCancelCommitment(productId: string) {
  return useCommitmentMutation(productId, async (id: string) =>
    unwrap(await api.POST('/commitments/{commitmentId}/cancel', { params: { path: { commitmentId: id } } })),
  )
}

export function useAckAlert(productId: string) {
  return useCommitmentMutation(productId, async (alertId: string) =>
    unwrap(await api.POST('/commitment-alerts/{alertId}/ack', { params: { path: { alertId } } })),
  )
}

export function useEnsureRenewals(productId: string) {
  return useCommitmentMutation(productId, async () => unwrap(await api.POST('/commitments/ensure-renewals', { body: {} })))
}

// ---- Compliance ------------------------------------------------------------

export function useRequirementSets(enabled = true) {
  return useQuery({
    queryKey: keys2.requirementSets,
    enabled,
    queryFn: async () => unwrap(await api.GET('/admin/requirement-sets')),
  })
}

export function useCreateRequirementSet() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: async (body: RequirementSetInput) => unwrap(await api.POST('/admin/requirement-sets', { body })),
    onSuccess: () => void qc.invalidateQueries({ queryKey: keys2.requirementSets }),
  })
}

export function useSetRequirementSetStatus() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: async (input: { id: string; status: 'draft' | 'published' | 'retired' }) =>
      unwrap(
        await api.POST('/requirement-sets/{setId}/status', { params: { path: { setId: input.id } }, body: { status: input.status } }),
      ),
    onSuccess: () => void qc.invalidateQueries({ queryKey: keys2.requirementSets }),
  })
}

export function useTrackTemplates(productType?: ProductType, enabled = true) {
  return useQuery({
    queryKey: [...keys2.trackTemplates, productType ?? ''] as const,
    enabled,
    queryFn: async () =>
      unwrap(await api.GET('/admin/track-templates', { params: { query: productType ? { productType } : {} } })),
  })
}

export function useSaveTrackTemplate() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: async (body: TrackTemplateInput) => unwrap(await api.POST('/admin/track-templates', { body })),
    onSuccess: () => void qc.invalidateQueries({ queryKey: keys2.trackTemplates }),
  })
}

export function useTracks(productId: string, enabled = true) {
  return useQuery({
    queryKey: keys2.tracks(productId),
    enabled,
    queryFn: async () => unwrap(await api.GET('/products/{productId}/tracks', { params: { path: { productId } } })),
  })
}

/** Треки нескольких продуктов для compliance-дашборда; продукты без доступа помечаются. */
export function useTracksOfProducts(ids: string[]) {
  return useQueries({
    queries: ids.map((productId) => ({
      queryKey: keys2.tracks(productId),
      queryFn: async () => unwrap(await api.GET('/products/{productId}/tracks', { params: { path: { productId } } })),
      retry: (count: number, err: unknown) => !(err instanceof ApiError && err.status < 500) && count < 2,
    })),
  })
}

function useTrackMutation<TInput, TOut>(productId: string, fn: (input: TInput) => Promise<TOut>) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: fn,
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: keys2.tracks(productId) })
      void qc.invalidateQueries({ queryKey: ['tracks'] })
      void qc.invalidateQueries({ queryKey: keys2.baselines(productId) })
      void qc.invalidateQueries({ queryKey: ['releases'] })
    },
  })
}

export function useStartTrack(productId: string) {
  return useTrackMutation(productId, async (body: TrackInput) =>
    unwrap(await api.POST('/products/{productId}/tracks', { params: { path: { productId } }, body })),
  )
}

export function useUpdateGate(productId: string) {
  return useTrackMutation(productId, async (input: { trackId: string; gateId: string; body: GateUpdate }) =>
    unwrap(
      await api.PATCH('/tracks/{trackId}/gates/{gateId}', {
        params: { path: { trackId: input.trackId, gateId: input.gateId } },
        body: input.body,
      }),
    ),
  )
}

export function useCheckGateItem(productId: string) {
  return useTrackMutation(productId, async (input: { trackId: string; gateId: string; key: string; evidence_id: string }) =>
    unwrap(
      await api.POST('/tracks/{trackId}/gates/{gateId}/check', {
        params: { path: { trackId: input.trackId, gateId: input.gateId } },
        body: { key: input.key, evidence_id: input.evidence_id },
      }),
    ),
  )
}

export function usePassGate(productId: string) {
  return useTrackMutation(productId, async (input: { trackId: string; gateId: string }) =>
    unwrap(
      await api.POST('/tracks/{trackId}/gates/{gateId}/pass', {
        params: { path: { trackId: input.trackId, gateId: input.gateId } },
      }),
    ),
  )
}

export function useFailGate(productId: string) {
  return useTrackMutation(productId, async (input: { trackId: string; gateId: string; reason: string }) =>
    unwrap(
      await api.POST('/tracks/{trackId}/gates/{gateId}/fail', {
        params: { path: { trackId: input.trackId, gateId: input.gateId } },
        body: { reason: input.reason },
      }),
    ),
  )
}

export function useTrackEvidence(trackId: string, enabled = true) {
  return useQuery({
    queryKey: keys2.trackEvidence(trackId),
    enabled,
    queryFn: async () => unwrap(await api.GET('/tracks/{trackId}/evidence', { params: { path: { trackId } } })),
  })
}

export function useAppendTrackEvidence(trackId: string) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: async (body: EvidenceItemInput) =>
      unwrap(await api.POST('/tracks/{trackId}/evidence', { params: { path: { trackId } }, body })),
    onSuccess: () => void qc.invalidateQueries({ queryKey: keys2.trackEvidence(trackId) }),
  })
}

export function useSetEvidenceItemStatus(trackId: string) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: async (input: { evidenceId: string; status: 'submitted' | 'accepted' | 'rejected'; comment?: string }) =>
      unwrap(
        await api.POST('/evidence-items/{evidenceId}/status', {
          params: { path: { evidenceId: input.evidenceId } },
          body: { status: input.status, comment: input.comment },
        }),
      ),
    onSuccess: () => void qc.invalidateQueries({ queryKey: keys2.trackEvidence(trackId) }),
  })
}

export function useVerifyEvidenceLog() {
  return useMutation({ mutationFn: async () => unwrap(await api.POST('/admin/evidence/verify')) })
}

export function useBaselines(productId: string, enabled = true) {
  return useQuery({
    queryKey: keys2.baselines(productId),
    enabled,
    queryFn: async () => unwrap(await api.GET('/products/{productId}/baselines', { params: { path: { productId } } })),
  })
}

/** Класс влияния: 404 означает «оценки ещё нет», это не ошибка экрана. */
export function useFeatureImpact(featureId: string, enabled = true) {
  return useQuery({
    queryKey: keys2.impact(featureId),
    enabled,
    retry: false,
    queryFn: async () => {
      const res = await api.GET('/features/{featureId}/impact', { params: { path: { featureId } } })
      if (res.response.status === 404) return null
      return unwrap(res)
    },
  })
}

export function useSetFeatureImpact(productId: string) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: async (input: { featureId: string; class: ImpactClass; justification: string }) =>
      unwrap(
        await api.PUT('/features/{featureId}/impact', {
          params: { path: { featureId: input.featureId } },
          body: { class: input.class, justification: input.justification },
        }),
      ),
    onSuccess: (_d, input) => {
      void qc.invalidateQueries({ queryKey: keys2.impact(input.featureId) })
      void qc.invalidateQueries({ queryKey: keys2.cost(input.featureId) })
      void qc.invalidateQueries({ queryKey: keys2.affectedBaselines(input.featureId) })
      void qc.invalidateQueries({ queryKey: ['scoring-models'] })
      void qc.invalidateQueries({ queryKey: keys.features(productId) })
    },
  })
}

export function useAffectedBaselines(featureId: string, enabled = true) {
  return useQuery({
    queryKey: keys2.affectedBaselines(featureId),
    enabled,
    queryFn: async () =>
      unwrap(await api.GET('/features/{featureId}/affected-baselines', { params: { path: { featureId } } })),
  })
}

export function useFeatureFlags(featureId: string, enabled = true) {
  return useQuery({
    queryKey: keys2.flags(featureId),
    enabled,
    queryFn: async () => unwrap(await api.GET('/features/{featureId}/flags', { params: { path: { featureId } } })),
  })
}

export function useSetFeatureFlags() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: async (input: { featureId: string; regulatory_mandatory: boolean; reason?: string }) =>
      unwrap(
        await api.PUT('/features/{featureId}/flags', {
          params: { path: { featureId: input.featureId } },
          body: { regulatory_mandatory: input.regulatory_mandatory, reason: input.reason },
        }),
      ),
    onSuccess: (_d, input) => {
      void qc.invalidateQueries({ queryKey: keys2.flags(input.featureId) })
      void qc.invalidateQueries({ queryKey: ['scoring-models'] })
    },
  })
}

export function useFeatureCost(featureId: string, enabled = true) {
  return useQuery({
    queryKey: keys2.cost(featureId),
    enabled,
    queryFn: async () => unwrap(await api.GET('/features/{featureId}/cost', { params: { path: { featureId } } })),
  })
}

export function useSetFeatureDevCost() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: async (input: { featureId: string; dev_cost: Money }) =>
      unwrap(
        await api.PUT('/features/{featureId}/cost', {
          params: { path: { featureId: input.featureId } },
          body: { dev_cost: input.dev_cost },
        }),
      ),
    onSuccess: (_d, input) => void qc.invalidateQueries({ queryKey: keys2.cost(input.featureId) }),
  })
}

// ---- Решения ---------------------------------------------------------------

export function useDecisions(productId: string | undefined, status: DecisionStatus | undefined, refetchInterval?: number) {
  return useQuery({
    queryKey: keys2.decisions(productId, status),
    refetchInterval,
    queryFn: async () =>
      unwrap(
        await api.GET('/decisions', {
          params: { query: { ...(productId ? { productId } : {}), ...(status ? { status } : {}) } },
        }),
      ),
  })
}

function useDecisionMutation<TInput, TOut>(fn: (input: TInput) => Promise<TOut>) {
  const qc = useQueryClient()
  return useMutation({ mutationFn: fn, onSuccess: () => void qc.invalidateQueries({ queryKey: ['decisions'] }) })
}

export function useCreateDecision() {
  return useDecisionMutation(async (body: DecisionInput) => unwrap(await api.POST('/decisions', { body })))
}

export function useUpdateDecision() {
  return useDecisionMutation(async (input: { id: string; body: DecisionInput }) =>
    unwrap(await api.PUT('/decisions/{decisionId}', { params: { path: { decisionId: input.id } }, body: input.body })),
  )
}

export function useAcceptDecision() {
  return useDecisionMutation(async (id: string) =>
    unwrap(await api.POST('/decisions/{decisionId}/accept', { params: { path: { decisionId: id } } })),
  )
}

export function useRejectDecision() {
  return useDecisionMutation(async (id: string) =>
    unwrap(await api.POST('/decisions/{decisionId}/reject', { params: { path: { decisionId: id } } })),
  )
}

export function useSupersedeDecision() {
  return useDecisionMutation(async (input: { id: string; by: string }) =>
    unwrap(
      await api.POST('/decisions/{decisionId}/supersede', { params: { path: { decisionId: input.id } }, body: { by: input.by } }),
    ),
  )
}

/** 202: запрос страницы ADR поставлен в outbox; page_id появится в решении позже. */
export function useRequestDecisionPage() {
  return useDecisionMutation(async (id: string) =>
    unwrap(await api.POST('/decisions/{decisionId}/request-page', { params: { path: { decisionId: id } }, body: {} })),
  )
}

// ---- Релизы (RM-04, RM-05) -------------------------------------------------

export function useReleases(productId: string, enabled = true) {
  return useQuery({
    queryKey: keys2.releases(productId),
    enabled,
    queryFn: async () => unwrap(await api.GET('/products/{productId}/releases', { params: { path: { productId } } })),
  })
}

function useReleaseMutation<TInput, TOut>(productId: string, fn: (input: TInput) => Promise<TOut>) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: fn,
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: keys2.releases(productId) })
      void qc.invalidateQueries({ queryKey: ['releases'] })
      void qc.invalidateQueries({ queryKey: ['products', productId, 'roadmap'] })
    },
  })
}

export function useCreateRelease(productId: string) {
  return useReleaseMutation(productId, async (body: ReleaseInput) =>
    unwrap(await api.POST('/products/{productId}/releases', { params: { path: { productId } }, body })),
  )
}

export function useUpdateRelease(productId: string) {
  return useReleaseMutation(productId, async (input: { id: string; body: ReleaseInput }) =>
    unwrap(await api.PUT('/releases/{releaseId}', { params: { path: { releaseId: input.id } }, body: input.body })),
  )
}

export function useSetReleaseFeatures(productId: string) {
  return useReleaseMutation(productId, async (input: { id: string; feature_ids: string[] }) =>
    unwrap(
      await api.PUT('/releases/{releaseId}/features', {
        params: { path: { releaseId: input.id } },
        body: { feature_ids: input.feature_ids },
      }),
    ),
  )
}

export function useSetReleaseNotes(productId: string) {
  return useReleaseMutation(productId, async (input: { id: string; release_notes: string }) =>
    unwrap(
      await api.PUT('/releases/{releaseId}/notes', {
        params: { path: { releaseId: input.id } },
        body: { release_notes: input.release_notes },
      }),
    ),
  )
}

export function useSetReleaseEol(productId: string) {
  return useReleaseMutation(productId, async (input: { id: string; eol: string }) =>
    unwrap(await api.PUT('/releases/{releaseId}/eol', { params: { path: { releaseId: input.id } }, body: { eol: input.eol } })),
  )
}

/** 409 с open_items приходит как ApiError; список незакрытых пунктов — в problem.detail либо в readiness. */
export function useMarkReleaseReady(productId: string) {
  return useReleaseMutation(productId, async (id: string) =>
    unwrap(await api.POST('/releases/{releaseId}/mark-ready', { params: { path: { releaseId: id } } })),
  )
}

export function useReleaseReadiness(releaseId: string, enabled: boolean) {
  return useQuery({
    queryKey: keys2.readiness(releaseId),
    enabled,
    queryFn: async () => unwrap(await api.GET('/releases/{releaseId}/readiness', { params: { path: { releaseId } } })),
  })
}

export function useUpdateRoadmapItemKind(productId: string) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: async (input: { itemId: string; kind: 'feature' | 'fix' }) =>
      unwrap(await api.PATCH('/roadmap/items/{itemId}', { params: { path: { itemId: input.itemId } }, body: { kind: input.kind } })),
    onSuccess: () => void qc.invalidateQueries({ queryKey: ['products', productId, 'roadmap'] }),
  })
}

// ---- Приоритизация (PR-04) -------------------------------------------------

export function useScoringModels() {
  return useQuery({ queryKey: keys2.scoringModels, queryFn: async () => unwrap(await api.GET('/scoring-models')) })
}

export function useRank(modelId: string, productId: string, enabled: boolean) {
  return useQuery({
    queryKey: keys2.rank(modelId, productId),
    enabled,
    queryFn: async () =>
      unwrap(
        await api.GET('/scoring-models/{modelId}/products/{productId}/rank', { params: { path: { modelId, productId } } }),
      ),
  })
}
