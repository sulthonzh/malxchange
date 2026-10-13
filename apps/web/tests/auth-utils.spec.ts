import { describe, expect, it } from 'vitest'

import { confirmPasswordMatches, validateEmail } from '../src/utils/authValidation'
import { deviceLabel } from '../src/utils/device'
import { safeRedirectTarget } from '../src/utils/redirect'

// Pure auth helpers (ticket #47): the redirect guard is a security
// boundary (open-redirect), the validators carry the copy contract, and
// the device label names the Member's sessions.

describe('safeRedirectTarget', () => {
  it('honors same-site paths', () => {
    expect(safeRedirectTarget('/uc/money')).toBe('/uc/money')
    expect(safeRedirectTarget('/exchange/BTC%2FUSDT')).toBe('/exchange/BTC%2FUSDT')
  })

  it('rejects absolute and protocol-relative URLs', () => {
    expect(safeRedirectTarget('https://evil.example/phish')).toBe('/exchange')
    expect(safeRedirectTarget('//evil.example/phish')).toBe('/exchange')
    expect(safeRedirectTarget('/\\evil.example')).toBe('/exchange')
  })

  it('falls back for non-strings and empty values', () => {
    expect(safeRedirectTarget(undefined)).toBe('/exchange')
    expect(safeRedirectTarget('')).toBe('/exchange')
    expect(safeRedirectTarget(['/uc'])).toBe('/exchange')
    expect(safeRedirectTarget('exchange', '/login')).toBe('/login')
  })
})

describe('deviceLabel', () => {
  it('names browser and system', () => {
    expect(
      deviceLabel(
        'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0 Safari/537.36',
      ),
    ).toBe('Chrome on macOS')
    expect(
      deviceLabel('Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X) AppleWebKit/605.1.15 Safari/604.1'),
    ).toBe('Safari on iOS')
    expect(deviceLabel('Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:127.0) Gecko/20100101 Firefox/127.0')).toBe(
      'Firefox on Windows',
    )
  })

  it('falls back to a plain label', () => {
    expect(deviceLabel(undefined)).toBe('Web browser')
    expect(deviceLabel('')).toBe('Web browser')
    expect(deviceLabel('curl/8.0')).toBe('Web browser')
  })
})

describe('auth validators', () => {
  function run(validator: (rule: unknown, value: string, cb: (e?: Error) => void) => void, value: string) {
    let error: Error | undefined
    validator(null, value, (e) => {
      error = e
    })
    return error
  }

  it('validateEmail accepts empty (required rule owns it) and valid addresses', () => {
    expect(run(validateEmail, '')).toBeUndefined()
    expect(run(validateEmail, 'member@example.com')).toBeUndefined()
    expect(run(validateEmail, 'not-an-email')?.message).toBe('Enter a valid email address.')
    expect(run(validateEmail, 'a@b')?.message).toBe('Enter a valid email address.')
  })

  it('confirmPasswordMatches compares against the live password', () => {
    let password = 'first-secret'
    const validator = confirmPasswordMatches(() => password)
    expect(run(validator, '')?.message).toBe('Enter your password again.')
    expect(run(validator, 'other-secret')?.message).toBe('Enter the same password twice.')
    expect(run(validator, 'first-secret')).toBeUndefined()
    password = 'changed-secret'
    expect(run(validator, 'first-secret')?.message).toBe('Enter the same password twice.')
  })
})
