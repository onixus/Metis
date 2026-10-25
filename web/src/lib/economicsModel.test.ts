import { test } from 'node:test'
import assert from 'node:assert/strict'
import { fieldDraft, metricDraft, latestVersion } from './economicsModel.ts'

test('EC08_EC11_field_editor_keeps_type_source_dimensions_and_effective_date', () => {
  assert.deepEqual(fieldDraft({ id: 'field', key: 'salary', versions: [{ version: 2, name: 'ФОТ', type: 'money', source: 'import', currency: 'RUB', dimensions: ['product', 'team', 'period'], effective_from: '2026-10-01' }, { version: 1, name: 'Old', type: 'number', source: 'manual' }] }), { key: 'salary', name: 'ФОТ', type: 'money', source: 'import', currency: 'RUB', dimensions: ['product', 'team', 'period'], effective_from: '2026-10-01' })
})
test('EC09_EC11_formula_editor_preserves_decimal_literals_and_version_order', () => {
  const expression = 'field("revenue") * d("0.1234567890123456789")'
  const draft = metricDraft({ id: 'metric', key: 'profit', versions: [{ version: 1, name: 'Old', expression: '0' }, { version: 3, name: 'Profit', expression, currency: 'RUB', effective_from: '2026-10-01' }, { version: 2, name: 'Second', expression: '1' }] })
  assert.equal(draft.expression, expression)
  assert.equal(draft.effective_from, '2026-10-01')
  assert.equal(latestVersion([]), undefined)
})

test('EC13_DA02_signed_budget_delta_preserves_cents_and_rejects_rounding', async () => {
  const { signedMinorUnits } = await import('./economicsModel.ts')
  assert.equal(signedMinorUnits('-123,45'), -12345)
  assert.equal(signedMinorUnits('0.01'), 1)
  for (const value of ['1e3', '1.234', '900719925474099.01', '', '--2']) assert.throws(() => signedMinorUnits(value))
})

test('EC11_EC13_historic_scenario_uses_effective_field_definition', async () => {
  const { effectiveVersion } = await import('./economicsModel.ts')
  const versions = [{ version: 1, effective_from: null, source: 'manual' }, { version: 2, effective_from: '2026-10-01', source: 'calculated' }]
  assert.equal(effectiveVersion(versions, '2026-09')?.source, 'manual')
  assert.equal(effectiveVersion(versions, '2026-10')?.source, 'calculated')
  assert.equal(effectiveVersion([{ version: 1, effective_from: '2026-10-01' }], '2026-09'), undefined)
})

test('EC09_EC13_direct_source_override_serializes_exact_int64_and_team_without_field', async () => {
  const { scenarioOverride } = await import('./economicsModel.ts')
  const product = '00000000-0000-0000-0000-000000000001'
  const team = '00000000-0000-0000-0000-000000000002'
  assert.deepEqual(scenarioOverride('source:crm_revenue', '9007199254740993', product, '2026-10', team), { source: 'crm_revenue', product_id: product, period: '2026-10', team_id: team, value: '9007199254740993' })
  assert.deepEqual(scenarioOverride('source:headcount', '0', product, '2026-10'), { source: 'headcount', product_id: product, period: '2026-10', team_id: undefined, value: '0' })
  for (const [target, value, targetTeam] of [
    ['source:headcount', '1.5', ''], ['source:worklogs', '-1', ''], ['source:crm_revenue', '9223372036854775808', ''],
    ['source:worklogs', '1e9', ''], ['source:unknown', '1', ''], ['source:headcount', '2', 'raw-person-name'],
  ]) assert.throws(() => scenarioOverride(target, value, product, '2026-10', targetTeam))
})

test('EC08_EC13_field_override_keeps_precise_decimal_and_clears_source_team', async () => {
  const { scenarioOverride } = await import('./economicsModel.ts')
  assert.deepEqual(scenarioOverride('custom_input', '-9007199254740993.01', 'product', '2026-10'), { field_key: 'custom_input', product_id: 'product', period: '2026-10', value: '-9007199254740993.01' })
  assert.throws(() => scenarioOverride('custom_input', '1', 'product', '2026-10', 'team'))
  assert.throws(() => scenarioOverride('', '1', 'product', '2026-10'))
})
