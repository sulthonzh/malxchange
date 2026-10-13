import { describe, expect, it } from 'vitest'
import { compareMoney, formatMoney, parseDecimal } from '../src/utils/money'


// Decimal-string money helpers (ADR-0005): every expected value below is a
// hand-worked literal, never recomputed with the same library the
// implementation uses — these tests exist to catch float drift and
// round-up regressions, so they must not borrow the implementation's math.

describe('parseDecimal', () => {
  it('parses an exact decimal string', () => {
    expect(parseDecimal('0.1').toString()).toBe('0.1')
    expect(parseDecimal('41234').toString()).toBe('41234')
  })

  it('keeps more scale than a float can represent', () => {
    // 0.1 + 0.2 as floats is 0.30000000000000004; a decimal parse of the
    // string "0.30000000000000004" must keep every digit.
    expect(parseDecimal('0.30000000000000004').toString()).toBe('0.30000000000000004')
  })

  it('rejects values that are not decimal strings', () => {
    expect(() => parseDecimal('')).toThrow()
    expect(() => parseDecimal('abc')).toThrow()
    expect(() => parseDecimal('1.2.3')).toThrow()
    expect(() => parseDecimal('12 px')).toThrow()
  })
})

describe('formatMoney', () => {
  it('preserves trailing zeros out to the requested scale', () => {
    expect(formatMoney('0.1', 8)).toBe('0.10000000')
    expect(formatMoney('1.1', 2)).toBe('1.10')
    expect(formatMoney('5', 6)).toBe('5.000000')
  })

  it('floors (truncates toward zero), never rounds up', () => {
    // Rounding would display funds that do not exist (ADR-0005).
    expect(formatMoney('1.005', 2)).toBe('1.00')
    expect(formatMoney('0.999999999', 8)).toBe('0.99999999')
    expect(formatMoney('2.7', 0)).toBe('2')
  })

  it('pads short values with zeros instead of failing', () => {
    expect(formatMoney('3', 2)).toBe('3.00')
  })

  it('rejects non-decimal-string input', () => {
    expect(() => formatMoney('oops', 2)).toThrow()
  })
})


describe('compareMoney', () => {
  it('compares by decimal value, not string order', () => {
    expect(compareMoney('9.9', '10.1')).toBe(-1)
    expect(compareMoney('0.10', '0.1')).toBe(0)
    expect(compareMoney('2.0000000000000001', '2')).toBe(1)
  })
})
