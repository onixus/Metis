import test from 'node:test'
import assert from 'node:assert/strict'
import { groupPortfolioFeatures } from './portfolio.ts'

test('PG02_hierarchy_retains_empty_capabilities_and_unassigned_features', () => {
  const capabilities = [{ id: 'cap-a', product_id: 'p', name: 'A' }, { id: 'cap-b', product_id: 'p', name: 'B' }]
  const make = (id: string, capability_id?: string) => ({ id, product_id: 'p', name: id, status: 'idea' as const, affected: false, own_value: { amount: 0, currency: 'RUB' }, capability_id })
  const groups = groupPortfolioFeatures(capabilities, [make('linked', 'cap-a'), make('loose'), make('legacy', 'missing')])
  assert.deepEqual(groups.map(group => [group.id, group.features.map(feature => feature.id)]), [['cap-a', ['linked']], ['cap-b', []], ['', ['loose', 'legacy']]])
  assert.equal(new Set(groups.flatMap(group => group.features.map(feature => feature.id))).size, 3)
})
