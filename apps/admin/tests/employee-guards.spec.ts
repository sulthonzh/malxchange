import { describe, expect, it } from 'vitest'

import { SUPER_ADMIN_ROLE, type EmployeeRow } from '../src/utils/apiTypes'
import { disableRefusal, isSoleEnabledSuperAdmin, roleChangeRefusal } from '../src/utils/employeeGuards'

// The client-side mirrors of the management guards (ADR-0012): the UI
// keeps itself out of refusable states, and the sentences it shows are
// the server's own — so a stale list that lets a refusal through still
// renders the api's message (the pages' job), never a silent no-op.

function employee(overrides: Partial<EmployeeRow> = {}): EmployeeRow {
  return {
    id: 'e-1',
    email: 'root@exchange.example.com',
    role_id: 'r-1',
    role: SUPER_ADMIN_ROLE,
    enabled: true,
    totp_bound: true,
    must_rotate: false,
    created_at: '2026-01-01T00:00:00Z',
    ...overrides,
  }
}

describe('isSoleEnabledSuperAdmin', () => {
  it('holds when the target is the only enabled super-admin', () => {
    const target = employee()
    expect(isSoleEnabledSuperAdmin(target, [target])).toBe(true)
  })

  it('releases when a second enabled super-admin exists', () => {
    const target = employee({ id: 'e-1' })
    const second = employee({ id: 'e-2', email: 'deputy@exchange.example.com' })
    expect(isSoleEnabledSuperAdmin(target, [target, second])).toBe(false)
  })

  it('releases when a disabled super-admin or another role holds the target', () => {
    const target = employee({ enabled: false })
    expect(isSoleEnabledSuperAdmin(target, [target])).toBe(false)
    const finance = employee({ role: 'finance', role_id: 'r-2' })
    expect(isSoleEnabledSuperAdmin(finance, [finance])).toBe(false)
  })
})

describe('disableRefusal', () => {
  it('refuses self-disable with the server sentence', () => {
    const target = employee()
    expect(disableRefusal(target, 'e-1', [target])).toBe('You cannot disable your own account.')
  })

  it('refuses the last enabled super-admin even for another actor', () => {
    const target = employee()
    expect(disableRefusal(target, 'e-9', [target])).toBe(
      'The last enabled super-admin cannot be disabled.',
    )
  })

  it('allows a plain target', () => {
    const actor = employee({ id: 'e-9', email: 'root@exchange.example.com' })
    const target = employee({ id: 'e-2', email: 'employee@exchange.example.com', role: 'support', role_id: 'r-3' })
    expect(disableRefusal(target, 'e-9', [actor, target])).toBeNull()
  })
})

describe('roleChangeRefusal', () => {
  it('refuses self-role-removal with the server sentence', () => {
    const target = employee()
    expect(roleChangeRefusal(target, 'e-1', 'finance', [target])).toBe(
      'You cannot change your own Role.',
    )
  })

  it('refuses stripping the last super-admin, but allows promoting INTO the Role', () => {
    const target = employee()
    expect(roleChangeRefusal(target, 'e-9', 'finance', [target])).toBe(
      'The last enabled super-admin cannot lose the Role.',
    )
    expect(roleChangeRefusal(target, 'e-9', SUPER_ADMIN_ROLE, [target])).toBeNull()
  })

  it('allows a plain reassignment', () => {
    const actor = employee({ id: 'e-9' })
    const target = employee({ id: 'e-2', email: 'employee@exchange.example.com', role: 'support', role_id: 'r-3' })
    expect(roleChangeRefusal(target, 'e-9', 'finance', [actor, target])).toBeNull()
  })
})
