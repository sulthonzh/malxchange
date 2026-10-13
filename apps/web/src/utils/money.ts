import Big from 'big.js'

// Decimal-string money and amount helpers (ADR-0005).
//
// Every money value the api serves is a decimal string; these helpers are
// the only way source files turn those strings into display text. They
// use big.js (pinned in package.json) — no value ever routes through a
// float, and display always floors so rounding can never show funds that
// do not exist.

/**
 * Raised when a value is not a decimal string. Every caller renders money
 * from api data; a non-decimal string there is a contract break worth
 * failing loudly rather than coercing.
 */
export class InvalidMoneyError extends Error {
  constructor(value: unknown) {
    super(`The value is not a decimal string: ${String(value)}`)
    this.name = 'InvalidMoneyError'
  }
}

/**
 * Parses a decimal string exactly. Accepts only strings — a number here
 * means a float slipped in upstream, and floats are banned (ADR-0005).
 */
export function parseDecimal(value: string): Big {
  if (typeof value !== 'string' || value.trim() === '') {
    throw new InvalidMoneyError(value)
  }
  try {
    return new Big(value)
  } catch {
    throw new InvalidMoneyError(value)
  }
}

/**
 * Formats a decimal string at exactly `scale` decimal places, truncating
 * toward zero (floor for the non-negative money the api serves) and
 * preserving trailing zeros: "0.1" at scale 8 renders "0.10000000".
 */
export function formatMoney(value: string, scale: number): string {
  return parseDecimal(value).round(scale, Big.roundDown).toFixed(scale)
}

/**
 * Compares two decimal strings by value: -1, 0, or 1. Use this for
 * sorting market tables — never a locale string comparison.
 */
export function compareMoney(a: string, b: string): -1 | 0 | 1 {
  const result = parseDecimal(a).cmp(parseDecimal(b))
  return result as -1 | 0 | 1
}
