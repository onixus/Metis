/// <reference types="node" />
import assert from 'node:assert/strict'
import { test } from 'node:test'
import { compareForecastDate } from './forecastRisk.ts'

test('TestDL04_DeadlineComparisonUsesCalendarDaysAndExplicitUnknown', () => {
  assert.deepEqual(compareForecastDate('2028-03-01', '2028-02-28', false), { state: 'at_risk', daysLate: 2 })
  assert.deepEqual(compareForecastDate('2026-12-31', '2027-01-01', false), { state: 'within', daysLate: 0 })
  assert.deepEqual(compareForecastDate('2027-01-01', '2027-01-01', false), { state: 'within', daysLate: 0 })
  for (const date of [null, undefined, '', '2027-02-29', '2027-01-01T00:00:00Z']) {
    assert.deepEqual(compareForecastDate(date, '2027-01-01', false), { state: 'unknown', daysLate: null })
  }
})

test('TestDL04_StaleProjectionCannotClaimDeadlineConfidence', () => {
  assert.equal(compareForecastDate('2027-01-01', '2027-01-01', true).state, 'stale')
  assert.equal(compareForecastDate('2027-02-01', '2027-01-01', true).state, 'stale')
})
