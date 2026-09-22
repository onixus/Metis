/** Parse major currency units without floating point multiplication or rounding. */
export function parseMinorUnits(value: string): number | undefined {
  const normalized = value.trim().replace(',', '.')
  if (normalized === '') return undefined
  if (!/^\d+(?:\.\d{1,2})?$/.test(normalized)) throw new Error('invalid_money')
  const [major, fraction = ''] = normalized.split('.')
  const minor = BigInt(major) * 100n + BigInt(fraction.padEnd(2, '0'))
  if (minor > BigInt(Number.MAX_SAFE_INTEGER)) throw new Error('invalid_money')
  return Number(minor)
}
