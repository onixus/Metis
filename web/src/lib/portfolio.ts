import type { components } from '../api/schema'

type Capability = components['schemas']['Capability']
type Feature = components['schemas']['Feature']

/** Keep unassigned/legacy features visible while grouping the persisted hierarchy. */
export function groupPortfolioFeatures(capabilities: Capability[], features: Feature[]) {
  const byId = new Set(capabilities.map(capability => capability.id))
  return [
    ...capabilities.map(capability => ({ id: capability.id, name: capability.name, features: features.filter(feature => feature.capability_id === capability.id) })),
    { id: '', name: 'Без возможности', features: features.filter(feature => !feature.capability_id || !byId.has(feature.capability_id)) },
  ].filter(group => group.id || group.features.length)
}

/** Mirrors portfolio WinLoss: ActionReadMarketing plus SeesAllProducts, independent of finance. */
export function canReadPortfolioWinLoss(me: components['schemas']['Me'] | undefined): boolean {
  if (!me || me.all_products === 'none') return false
  return me.roles.some(role => ['admin', 'cpo', 'marketing', 'service'].includes(role)) ||
    (me.roles.includes('pm') && me.all_products === 'private')
}
