import Big from 'big.js'

import { formatMoney, parseDecimal } from './money'

// en-US display formatters (ADR-0013 rule 3).
//
// Dates, numbers, and money render through these helpers only. Each one
// pins en-US and UTC explicitly, so a future locale switch is a change to
// this file — not a template sweep — and server and client renders agree
// because no formatter depends on the machine timezone.

/** Oct 13, 2026 — dates render en-US, pinned to UTC. */
export function formatDate(value: string | number | Date): string {
  return new Intl.DateTimeFormat('en-US', {
    timeZone: 'UTC',
    month: 'short',
    day: 'numeric',
    year: 'numeric',
  }).format(new Date(value))
}

/** Oct 13, 2026, 8:15 AM — date and 12-hour time, en-US, pinned to UTC. */
export function formatDateTime(value: string | number | Date): string {
  return new Intl.DateTimeFormat('en-US', {
    timeZone: 'UTC',
    month: 'short',
    day: 'numeric',
    year: 'numeric',
    hour: 'numeric',
    minute: '2-digit',
  }).format(new Date(value))
}

/** 1,234,567 — integer counts render with en-US grouping. */
export function formatNumber(value: number | string): string {
  const n = typeof value === 'string' ? parseDecimal(value) : new Big(value)
  return new Intl.NumberFormat('en-US', { maximumFractionDigits: 0 }).format(
    // BigInt keeps every integer digit exact; grouping never needs the
    // fraction part, which the maximumFractionDigits 0 above drops.
    BigInt(n.round(0, Big.roundDown).toFixed(0)),
  )
}

/**
 * 1,234,567.89 — money for table display: en-US grouping on the integer
 * part, floored to `scale`, trailing zeros preserved. Values arrive as
 * decimal strings and never pass through a float.
 */
export function formatMoneyEnUs(value: string, scale: number): string {
  const fixed = formatMoney(value, scale)
  const negated = fixed.startsWith('-')
  const unsigned = negated ? fixed.slice(1) : fixed
  const [integerPart, fractionPart] = unsigned.split('.')
  const grouped = integerPart.replace(/\B(?=(\d{3})+(?!\d))/g, ',')
  const fraction = fractionPart === undefined ? '' : `.${fractionPart}`
  return (negated ? '-' : '') + grouped + fraction
}
