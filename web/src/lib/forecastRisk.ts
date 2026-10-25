export function compareForecastDate(forecast: string | null | undefined, deadline: string | null | undefined, stale: boolean) {
  const parse = (value: string | null | undefined) => {
    if (!value || !/^\d{4}-\d{2}-\d{2}$/.test(value)) return NaN
    const ms = Date.parse(`${value}T00:00:00Z`)
    return Number.isFinite(ms) && new Date(ms).toISOString().slice(0, 10) === value ? ms : NaN
  }
  const estimate = parse(forecast), due = parse(deadline)
  if (!Number.isFinite(estimate) || !Number.isFinite(due)) return { state: 'unknown' as const, daysLate: null }
  const daysLate = Math.max(0, Math.round((estimate - due) / 86_400_000))
  if (stale) return { state: 'stale' as const, daysLate }
  return { state: daysLate > 0 ? 'at_risk' as const : 'within' as const, daysLate }
}
