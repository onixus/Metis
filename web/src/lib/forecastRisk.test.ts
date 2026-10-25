/// <reference types="node" />
import assert from 'node:assert/strict'
import { test } from 'node:test'
import { compareForecastDate, forecastIsStale } from './forecastRisk.ts'

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

test('TestDL04_ActiveForecastLosesConfidenceWhenItsSourceAgesOrChanges', () => {
  const snapshot = { last_success_at: '2026-10-25T12:00:00Z', stale: false }
  assert.equal(forecastIsStale(snapshot, { ...snapshot }), false)
  assert.equal(forecastIsStale(snapshot, { ...snapshot, stale: true }), true)
  assert.equal(forecastIsStale(snapshot, { ...snapshot, last_success_at: '2026-10-25T12:15:00Z' }), true)
  assert.equal(compareForecastDate('2027-01-01', '2027-01-02', forecastIsStale(snapshot, { ...snapshot, stale: true })).state, 'stale')
})

test('TestDL04_UnknownSourceNeverConfirmsDeadline', () => {
  const snapshot = { last_success_at: '2026-10-25T12:00:00Z', stale: false }
  assert.equal(forecastIsStale(undefined, snapshot), true)
  assert.equal(forecastIsStale(snapshot, undefined), true)
  assert.equal(forecastIsStale(snapshot, { last_success_at: snapshot.last_success_at }), true)
  assert.equal(forecastIsStale({ last_success_at: '0001-01-01T00:00:00Z', stale: false }, { last_success_at: '0001-01-01T00:00:00Z', stale: false }), true)
})

test('TestDL04_CachedSourceRequiresConfirmationAfterNavigationOrDuringRefresh', () => {
  const cached = { last_success_at: '2026-10-25T12:00:00Z', stale: false }
  assert.equal(forecastIsStale(cached, cached, false), true)
  assert.equal(compareForecastDate('2027-01-01', '2027-01-02', forecastIsStale(cached, cached, false)).state, 'stale')
  assert.equal(forecastIsStale(cached, cached, true), false)
})
