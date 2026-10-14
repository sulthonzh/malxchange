import { describe, expect, it } from 'vitest'

import { loginTarget, sessionGate } from '../src/utils/sessionGate'

// The routing half of the Employee session gate (issue #56): a pure
// decision the route middleware applies — null means the path is
// allowed, a string is the redirect target. The server's session gate
// (internal/admin/server.go withSession) remains the real authority;
// this only keeps the UI out of states it cannot use.

const provisioned = { employee: { id: 'e-1', email: 'root@x', role: 'super-admin' }, mustRotate: false, totpBound: true, permissions: ['view:employee'] }

describe('sessionGate', () => {
  it('sends an anonymous visitor to the login page', () => {
    expect(sessionGate(null, '/')).toBe('/login')
    expect(sessionGate(null, '/system/employees')).toBe('/login')
  })

  it('lets an anonymous visitor see the login page', () => {
    expect(sessionGate(null, '/login')).toBeNull()
  })

  it('forces an unprovisioned Employee through setup, password first', () => {
    const fresh = { ...provisioned, mustRotate: true, totpBound: false }
    expect(sessionGate(fresh, '/')).toBe('/setup/password')
    expect(sessionGate(fresh, '/login')).toBe('/setup/password')
    expect(sessionGate(fresh, '/setup/totp')).toBe('/setup/password')
    expect(sessionGate(fresh, '/setup/password')).toBeNull()
  })

  it('routes a rotated-but-unbound Employee to TOTP enrollment', () => {
    const rotated = { ...provisioned, mustRotate: false, totpBound: false }
    expect(sessionGate(rotated, '/setup/password')).toBe('/setup/totp')
    expect(sessionGate(rotated, '/')).toBe('/setup/totp')
    expect(sessionGate(rotated, '/setup/totp')).toBeNull()
  })

  it('keeps a provisioned Employee out of login and setup', () => {
    expect(sessionGate(provisioned, '/login')).toBe('/')
    expect(sessionGate(provisioned, '/setup/totp')).toBe('/')
  })

  it('lets a provisioned Employee browse', () => {
    expect(sessionGate(provisioned, '/')).toBeNull()
    expect(sessionGate(provisioned, '/system/employees')).toBeNull()
  })
})

describe('loginTarget', () => {
  it('routes a fresh one-time credential to the password rotation first', () => {
    expect(loginTarget({ ...provisioned, mustRotate: true, totpBound: false })).toBe('/setup/password')
  })

  it('routes a rotated Employee to TOTP enrollment, a provisioned one home', () => {
    expect(loginTarget({ ...provisioned, mustRotate: false, totpBound: false })).toBe('/setup/totp')
    expect(loginTarget(provisioned)).toBe('/')
  })
})
