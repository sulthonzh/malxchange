import { describe, expect, it } from 'vitest'

import { adminSections, visibleRoutes } from '../src/utils/routes'

// The route table is the legacy findSlideArr port (spec #25, issue #56):
// static, keyed by the admin-api registry's permission strings — the
// sidebar is this table intersected with the login response's live
// permission set. The strings mirror internal/admin/registry.go; no
// UI-invented strings exist.

describe('visibleRoutes', () => {
  it('keeps every section for the full route-table permission set', () => {
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
      'view:announcement',
      'view:help-category',
      'view:help-article',
      // #58's read side: super-admin sees everything the table offers.
      'search:member',
      'view:member',
      'view:kyc',
      'view:kyc-evidence',
      // #62: the Code channel surface is super-admin's alone.
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

  it('offers support the member surface only (the ADR-0012 matrix)', () => {
    // Support's #58 grants: member view/lock — never the KYC queue.
    const sections = visibleRoutes(adminSections, ['search:member', 'view:member', 'lock:member'])
    expect(sections.map((s) => s.title)).toEqual(['Members'])
    expect(sections[0]?.routes.map((r) => r.path)).toEqual(['/members'])
  })

  it('offers compliance the KYC queue only (the ADR-0012 matrix)', () => {
    const sections = visibleRoutes(adminSections, [
      'view:kyc',
      'view:kyc-evidence',
      'review:kyc',
      'reset:member-totp',
    ])
    expect(sections.map((s) => s.title)).toEqual(['Compliance'])
    expect(sections[0]?.routes.map((r) => r.path)).toEqual(['/kyc'])
  })

  it('renders nothing for a permission set outside the table', () => {
    // A set no entry keys on (lock and review exist in the registry,
    // but the route table has no entry keyed on them) intersects the
    // table emptily — nothing renders.
    expect(visibleRoutes(adminSections, ['lock:member', 'review:kyc'])).toEqual([])
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
