// The permission-catalog invariant (spec #25 Testing Decisions,
// issue #64): the seeded catalog served by GET /api/permissions equals
// exactly the endpoint list's checked strings — transcribed below from
// internal/admin/registry.go, the single seam every permissioned
// endpoint registers through (one endpoint per string, catalog seeded
// idempotently from that list). A new endpoint without a registry
// entry, or an entry without its endpoint, fails here; the grant
// editor refuses any string the catalog does not carry, so no
// UI-invented permission can silently gate nothing. Assertions ride
// the served HTTP surface only.
import { expect, test } from '@playwright/test'

import { loginHttp, pageApi, readAdminState, roleIdOf, seatPage, totpCode } from './helpers'

/** The 59 permission strings internal/admin/registry.go checks — the
 * endpoint list itself, sorted as the catalog serves it. */
const CATALOG: string[] = [
  'activate:channel',
  'adjust:balance',
  'approve:withdrawal',
  'assign:employee-role',
  'create:announcement',
  'create:employee',
  'create:help-article',
  'create:help-category',
  'create:hot-transfer',
  'deactivate:channel',
  'delete:announcement',
  'delete:help-article',
  'delete:help-category',
  'delete:role',
  'disable:employee',
  'edit:announcement',
  'edit:coin',
  'edit:help-article',
  'edit:help-category',
  'edit:market',
  'edit:role',
  'enable:employee',
  'list:withdrawal',
  'lock:member',
  'pause:sweep',
  'record:cold-transfer',
  'refund:withdrawal',
  'reject:withdrawal',
  'reset:employee-password',
  'reset:employee-totp',
  'reset:member-totp',
  'resolve:incident',
  'retry:hot-transfer',
  'retry:sweep',
  'retry:withdrawal',
  'review:kyc',
  'search:member',
  'unlock:member',
  'view:announcement',
  'view:audit-log',
  'view:channel',
  'view:coin',
  'view:cold-transfer',
  'view:employee',
  'view:engine',
  'view:help-article',
  'view:help-category',
  'view:hot-transfer',
  'view:incident',
  'view:kyc',
  'view:kyc-evidence',
  'view:market',
  'view:member',
  'view:ops',
  'view:permission',
  'view:reconcile',
  'view:role',
  'view:sweep',
  'view:withdrawal',
]

interface RoleView {
  name: string
  permissions: unknown
}

function isRoleView(value: unknown): value is RoleView {
  return typeof value === 'object' && value !== null && 'name' in value && 'permissions' in value
}

test('the seeded catalog equals the endpoint list and gates the grant editor', async ({ page }) => {
  const root = readAdminState()
  test.skip(!root?.totpSecret, 'needs the provisioned super-admin from the first journey')
  await seatPage(page)
  await loginHttp(page, root!.email, root!.password, await totpCode(root!.totpSecret))

  // The catalog read answers exactly the endpoint list's strings —
  // nothing more (an invented string would gate nothing), nothing
  // less (an unchecked string would be a silent hole).
  const catalog = await pageApi(page, '/api/permissions')
  expect(catalog.status).toBe(200)
  expect(Array.isArray(catalog.body)).toBe(true)
  const served = (Array.isArray(catalog.body) ? catalog.body : []).filter(
    (item): item is string => typeof item === 'string',
  )
  expect(served, 'the served catalog equals the endpoint list').toEqual(CATALOG)

  // Every string is a well-formed verb:resource pair — the vocabulary
  // the audit actions share.
  for (const permission of served) {
    expect(permission, `${permission} is verb:resource`).toMatch(/^[a-z]+:[a-z-]+$/)
  }

  // Every Role's granted set is a subset of the catalog: a grant can
  // only ever name a catalog string.
  const roles = await pageApi(page, '/api/roles')
  expect(roles.status).toBe(200)
  const roleViews = (Array.isArray(roles.body) ? roles.body : []).filter(isRoleView)
  for (const role of roleViews) {
    if (!Array.isArray(role.permissions)) {
      throw new Error(`Role ${role.name} served no permission list`)
    }
    for (const permission of role.permissions) {
      expect(CATALOG, `Role ${role.name} grants ${permission}`).toContain(permission)
    }
  }

  // The seeded matrix carries the deciding grants: finance owns the
  // funds, compliance the identity decisions, support the members and
  // content, the super-admin the management surface.
  const grantsOf = (name: string): string[] => {
    const role = roleViews.find((candidate) => candidate.name === name)
    if (!role || !Array.isArray(role.permissions)) {
      throw new Error(`Role ${name} served no permission list`)
    }
    return role.permissions
  }
  expect(grantsOf('finance')).toContain('adjust:balance')
  expect(grantsOf('finance')).toContain('approve:withdrawal')
  expect(grantsOf('finance')).not.toContain('review:kyc')
  expect(grantsOf('compliance')).toContain('review:kyc')
  expect(grantsOf('compliance')).toContain('reset:member-totp')
  expect(grantsOf('compliance')).not.toContain('adjust:balance')
  expect(grantsOf('support')).toContain('lock:member')
  expect(grantsOf('support')).toContain('create:announcement')
  expect(grantsOf('support')).not.toContain('approve:withdrawal')
  expect(grantsOf('super-admin')).toContain('create:employee')
  expect(grantsOf('super-admin')).not.toContain('review:kyc')
  expect(grantsOf('super-admin')).not.toContain('approve:withdrawal')

  // The grant editor refuses a UI-invented string: the catalog is the
  // only source of grantable permissions.
  const financeRoleID = await roleIdOf(page, 'finance')
  const invented = await pageApi(page, `/api/roles/${financeRoleID}/grants`, 'POST', {
    permission: 'invent:nothing',
    granted: true,
  })
  expect(invented.status).toBe(400)
  expect(invented.body).toEqual({ message: 'That permission does not exist.' })

  // A refused grant lands in no Role's set — the matrix is unchanged.
  const after = await pageApi(page, '/api/roles')
  const afterViews = (Array.isArray(after.body) ? after.body : []).filter(isRoleView)
  const financeAfter = afterViews.find((role) => role.name === 'finance')
  expect(financeAfter && Array.isArray(financeAfter.permissions)).toBe(true)
  expect((financeAfter!.permissions as string[])).not.toContain('invent:nothing')
})
