import type { components } from '../api/schema'
type S = components['schemas']

export function latestVersion<T extends { version: number }>(versions: T[]): T | undefined {
  return versions.reduce<T | undefined>((last, next) => !last || next.version > last.version ? next : last, undefined)
}

export function fieldDraft(field: S['FinancialField']): S['FinancialFieldInput'] {
  const v = latestVersion(field.versions)
  if (!v) throw new Error('У поля нет версии')
  return { key: field.key, name: v.name, type: v.type as S['FinancialFieldInput']['type'], source: v.source as S['FinancialFieldInput']['source'], currency: v.currency, dimensions: (v.dimensions ?? []) as S['FinancialFieldInput']['dimensions'], effective_from: v.effective_from }
}

export function metricDraft(metric: S['Metric']): S['MetricInput'] {
  const v = latestVersion(metric.versions)
  if (!v) throw new Error('У показателя нет версии')
  return { key: metric.key, name: v.name, expression: v.expression, currency: v.currency, effective_from: v.effective_from }
}

/** Signed major units with the same safe-integer boundary as ordinary money input. */
export function signedMinorUnits(value: string): number {
  const raw = value.trim()
  const negative = raw.startsWith('-')
  const normalized = negative ? raw.slice(1) : raw
  if (!/^\d+(?:[.,]\d{1,2})?$/.test(normalized)) throw new Error('Введите дельту бюджета числом с двумя десятичными знаками')
  const [major, fraction = ''] = normalized.replace(',', '.').split('.')
  const amount = BigInt(major) * 100n + BigInt(fraction.padEnd(2, '0'))
  if (amount > BigInt(Number.MAX_SAFE_INTEGER)) throw new Error('Сумма превышает точный диапазон браузера')
  return Number(amount) * (negative ? -1 : 1)
}

/** Versions take effect within a calendar month; ISO dates sort lexicographically. */
export function effectiveVersion<T extends { version: number; effective_from?: string | null }>(versions: T[], period: string): T | undefined {
  return latestVersion(versions.filter(v => !v.effective_from || v.effective_from <= `${period}-31`))
}

export const formulaSources = {
  worklogs: { name: 'Списанное время', unit: 'секунды' },
  crm_revenue: { name: 'Выручка CRM', unit: 'минорные единицы валюты модели' },
  headcount: { name: 'Численность', unit: 'человек' },
} as const

/** Keep the exact source scalar and serialize only one target kind. */
export function scenarioOverride(target: string, value: string, productId: string, period: string, teamId = ''): S['ScenarioOverride'] {
  const raw = value.trim()
  if (!raw || raw.length > 512 || !/^-?\d+(?:\.\d+)?$/.test(raw)) throw new Error('Введите точное десятичное значение, не более 512 символов')
  if (target.startsWith('source:')) {
    const source = target.slice(7)
    if (!Object.hasOwn(formulaSources, source)) throw new Error('Выберите известный прямой источник')
    if (!/^\d+$/.test(raw) || BigInt(raw) > 9223372036854775807n) throw new Error('Для источника нужно неотрицательное целое int64 в указанных единицах')
    if (teamId && !/^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i.test(teamId)) throw new Error('Введите UUID команды из снимка источника')
    return { source: source as keyof typeof formulaSources, product_id: productId, period, team_id: teamId || undefined, value: raw }
  }
  if (!target || teamId) throw new Error('Выберите поле; командная подмена доступна прямому источнику')
  return { field_key: target, product_id: productId, period, value: raw }
}
