import { describe, expect, it } from 'vitest'

import { adminSections, visibleRoutes } from '../src/utils/routes'

// The route table is the legacy findSlideArr port (spec #25, issue #56):
// static, keyed by the admin-api registry's permission strings — the
// sidebar is this table intersected with the login response's live
// permission set. The strings mirror internal/admin/registry.go; no
// UI-invented strings exist.

describe('visibleRoutes', () => {
  it('keeps every section for the full super-admin permission set', () => {
    const permissions = [
      'view:employee',
      'create:employee',
      'disable:employee',
      'enable:employee',
      'reset:employee-password',
      'reset:employee-totp',
      'assign:employee-role',
      'view:role',
      'edit:role',
      'delete:role',
      // Wallet Ops (#60): the Operations section rides the same strings
      // the admin-api registry gates its endpoints on.
      'view:ops',
      'view:hot-transfer',
      'view:incident',
      'view:reconcile',
      'view:channel',
      'activate:channel',
      'deactivate:channel',
    ]
    expect(visibleRoutes(adminSections, permissions)).toEqual(adminSections)
  })

  it('drops the entries an Employee lacks and drops emptied sections', () => {
    const sections = visibleRoutes(adminSections, ['view:employee'])
    expect(sections).toHaveLength(1)
    expect(sections[0]?.routes.map((r) => r.path)).toEqual(['/system/employees'])
  })

  it('renders nothing for a permission set outside the table', () => {
    // The support Role today: its ADR-0012 matrix grants (member
    // view/lock, content) belong to endpoints later slices own — none
    // exist in the registry yet, so its live set intersects emptily.
    expect(visibleRoutes(adminSections, [])).toEqual([])
  })

  it('derives every entry from a registry permission string', () => {
    // verb:resource — the registry's shape (registry_test.go enforces
    // the same on the server side).
    for (const section of adminSections) {
      for (const route of section.routes) {
        expect(route.permission).toMatch(/^[a-z]+:[a-z-]+$/)
        expect(route.path.startsWith('/')).toBe(true)
      }
    }
  })
})
