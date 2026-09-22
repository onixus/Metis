/// <reference types="node" />
import assert from 'node:assert/strict'
import { test } from 'node:test'
import { parseMinorUnits } from './moneyInput.ts'

test('TestSG01_MoneyEntryPreservesKopecksAndOptionalAmount', () => {
  assert.equal(parseMinorUnits(''), undefined)
  assert.equal(parseMinorUnits('0'), 0)
  assert.equal(parseMinorUnits(' 12000000,01 '), 1200000001)
  assert.equal(parseMinorUnits('0.29'), 29)
  assert.equal(parseMinorUnits('90071992547409.91'), Number.MAX_SAFE_INTEGER)
})

test('TestSG01_MoneyEntryRejectsRoundingOverflowAndExecutableInput', () => {
  for (const value of ['0.001', '-1', '1e5', 'NaN', 'Infinity', '=1+2', '90071992547409.92']) {
    assert.throws(() => parseMinorUnits(value), /invalid_money/)
  }
})
