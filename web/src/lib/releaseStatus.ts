export type ReleaseStatus = 'planned' | 'ready_for_certification' | 'released' | 'eol'

/** An explicit lifecycle transition is valid only for the status the user saw. */
export interface ReleaseStatusChoice {
  from: ReleaseStatus
  to: ReleaseStatus
}

// Readiness is a separate checked command; the editor only exposes forward actions.
export function releaseStatuses(current: ReleaseStatus): ReleaseStatus[] {
  if (current === 'eol') return ['eol']
  if (current === 'released') return ['released', 'eol']
  return [current, 'released']
}

/** Resolve both the displayed value and optional API transition from one source. */
export function resolveReleaseStatus(current: ReleaseStatus, choice: ReleaseStatusChoice | null): {
  value: ReleaseStatus
  update: ReleaseStatus | undefined
} {
  const selected = choice?.from === current && releaseStatuses(current).includes(choice.to)
    ? choice.to
    : current
  return { value: selected, update: selected === current ? undefined : selected }
}
