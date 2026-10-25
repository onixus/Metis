import test from 'node:test'
import assert from 'node:assert/strict'
import { canReadPortfolioWinLoss, groupPortfolioFeatures } from './portfolio.ts'

test('PG02_hierarchy_retains_empty_capabilities_and_unassigned_features', () => {
  const capabilities = [{ id: 'cap-a', product_id: 'p', name: 'A' }, { id: 'cap-b', product_id: 'p', name: 'B' }]
  const make = (id: string, capability_id?: string) => ({ id, product_id: 'p', name: id, status: 'idea' as const, affected: false, own_value: { amount: 0, currency: 'RUB' }, capability_id })
  const groups = groupPortfolioFeatures(capabilities, [make('linked', 'cap-a'), make('loose'), make('legacy', 'missing')])
  assert.deepEqual(groups.map(group => [group.id, group.features.map(feature => feature.id)]), [['cap-a', ['linked']], ['cap-b', []], ['', ['loose', 'legacy']]])
  assert.equal(new Set(groups.flatMap(group => group.features.map(feature => feature.id))).size, 3)
})

test('DA04_AD02_portfolio_win_loss_is_independent_of_finance_and_denies_missing_role_or_scope', () => {
  const me = { subject: 'synthetic', finance: 'none' as const, audience: 'internal' as const, roles: ['cpo'], all_products: 'strategic' as const, products: {} }
  assert.equal(canReadPortfolioWinLoss(undefined), false)
  for (const role of ['cpo', 'admin', 'marketing', 'service']) {
    assert.equal(canReadPortfolioWinLoss({ ...me, roles: [role] }), true, `${role} without finance`)
    assert.equal(canReadPortfolioWinLoss({ ...me, roles: [role], all_products: 'none' }), false, `${role} without portfolio scope`)
  }
  for (const role of ['finance', 'presale', 'dev_lead', 'compliance', 'pm']) {
    assert.equal(canReadPortfolioWinLoss({ ...me, roles: [role], finance: 'full' }), false, `${role} cannot read portfolio marketing with strategic scope`)
  }
  assert.equal(canReadPortfolioWinLoss({ ...me, roles: ['pm'], all_products: 'private' }), true)
  assert.equal(canReadPortfolioWinLoss({ ...me, roles: ['pm'], all_products: 'none', products: { p: 'private' } }), false)
})
