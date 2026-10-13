import { describe, expect, it } from 'vitest'
import { formatDate, formatDateTime, formatMoneyEnUs, formatNumber } from '../src/utils/format'


// en-US display formatters (ADR-0013 rule 3): every expected literal is
// hand-written against the en-US convention these helpers pin — a locale
// or timezone regression shows up as a literal mismatch here.

describe('formatDate', () => {
  it('renders an en-US date pinned to UTC', () => {
    // 2026-10-13T23:30:00Z is already Oct 14 in every zone east of UTC;
    // pinning UTC is what keeps server and client renders identical.
    expect(formatDate('2026-10-13T23:30:00Z')).toBe('Oct 13, 2026')
    expect(formatDate('2026-01-01T00:00:00Z')).toBe('Jan 1, 2026')
  })
})

describe('formatDateTime', () => {
  it('renders an en-US date and 12-hour time pinned to UTC', () => {
    expect(formatDateTime('2026-10-13T08:15:30Z')).toBe('Oct 13, 2026, 8:15 AM')
    expect(formatDateTime('2026-10-13T20:05:00Z')).toBe('Oct 13, 2026, 8:05 PM')
    expect(formatDateTime('2026-10-13T12:00:00Z')).toBe('Oct 13, 2026, 12:00 PM')
  })
})

describe('formatNumber', () => {
  it('groups integers with en-US separators', () => {
    expect(formatNumber(1234567)).toBe('1,234,567')
    expect(formatNumber('999')).toBe('999')
    expect(formatNumber(0)).toBe('0')
  })
})

describe('formatMoneyEnUs', () => {
  it('groups the integer part and floors to the scale', () => {
    expect(formatMoneyEnUs('1234567.891', 2)).toBe('1,234,567.89')
    expect(formatMoneyEnUs('999999999.999999999', 8)).toBe('999,999,999.99999999')
  })

  it('keeps trailing zeros at the requested scale', () => {
    expect(formatMoneyEnUs('0.5', 2)).toBe('0.50')
    expect(formatMoneyEnUs('105', 6)).toBe('105.000000')
  })

  it('does not group a plain fractional amount', () => {
    expect(formatMoneyEnUs('7.25', 2)).toBe('7.25')
  })
})
