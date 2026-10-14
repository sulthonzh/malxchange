// Employee-management guard journeys (spec #25 Testing Decisions,
// issue #64) over the real stack: self-disable and self-role-removal
// are refused by the API (the UI's disabled buttons are presentation
// only), the LAST enabled super-admin can be neither disabled nor
// stripped of the Role — proven server-side by an Employee whose grant
// set was widened through the grant editor, so the caller is not the
// target — and the seeded Roles refuse deletion. Grant edits are
// restored at the end: the seeded matrix must survive the journey.
import { expect, test } from '@playwright/test'

import {
  auditRows,
  employeeIdOf,
  loginHttp,
  pageApi,
  provisionEmployee,
  readAdminState,
  roleIdOf,
  totpCode,
  seatPage,
} from './helpers'

test('employee-management guards refuse the lockout shapes server-side', async ({ page }) => {
  const root = readAdminState()
  test.skip(!root?.totpSecret, 'needs the provisioned super-admin from the first journey')
  await seatPage(page)

  await loginHttp(page, root!.email, root!.password, await totpCode(root!.totpSecret))
  const rootID = await employeeIdOf(page, root!.email)
  const financeRoleID = await roleIdOf(page, 'finance')

  // Self-disable and self-role-removal: the API refuses them even
  // though the caller holds the permission.
  const selfDisable = await pageApi(page, `/api/employees/${rootID}/disable`, 'POST')
  expect(selfDisable.status).toBe(409)
  expect(selfDisable.body).toEqual({ message: 'You cannot disable your own account.' })

  const selfRole = await pageApi(page, `/api/employees/${rootID}/role`, 'POST', {
    role_id: financeRoleID,
  })
  expect(selfRole.status).toBe(409)
  expect(selfRole.body).toEqual({ message: 'You cannot change your own Role.' })

  // Seeded Roles never delete.
  const deleteSeeded = await pageApi(page, `/api/roles/${financeRoleID}`, 'DELETE')
  expect(deleteSeeded.status).toBe(409)
  expect(deleteSeeded.body).toEqual({ message: 'Seeded roles cannot be deleted.' })

  // A disable/enable round-trip on a fresh Employee works and audits;
  // provisioning ends as the Employee session, so root signs back in.
  const employee = await provisionEmployee(page, root!, 'support')
  await loginHttp(page, root!.email, root!.password, await totpCode(root!.totpSecret))
  const employeeID = await employeeIdOf(page, employee.email)
  const disabled = await pageApi(page, `/api/employees/${employeeID}/disable`, 'POST')
  expect(disabled.status).toBe(200)
  const enabled = await pageApi(page, `/api/employees/${employeeID}/enable`, 'POST')
  expect(enabled.status).toBe(200)
  const disableAudit = await auditRows(page, { actor: root!.email, action: 'disable:employee' })
  expect(disableAudit.some((row) => row.target === employeeID)).toBe(true)
  const enableAudit = await auditRows(page, { actor: root!.email, action: 'enable:employee' })
  expect(enableAudit.some((row) => row.target === employeeID)).toBe(true)

  // The last-super-admin shapes need a caller who is NOT the target:
  // widen finance's grants through the grant editor (itself audited),
  // let the live finance session exercise the guards, then restore.
  // This is also the grant editor biting on the very next request.
  await test.step('the last enabled super-admin is untouchable', async () => {
    await loginHttp(page, root!.email, root!.password, await totpCode(root!.totpSecret))
    for (const permission of ['disable:employee', 'assign:employee-role']) {
      const granted = await pageApi(page, `/api/roles/${financeRoleID}/grants`, 'POST', {
        permission,
        granted: true,
      })
      expect(granted.status, permission).toBe(200)
    }
    await pageApi(page, '/api/logout', 'POST')

    const finance = await provisionEmployee(page, root!, 'finance')
    const lastDisable = await pageApi(page, `/api/employees/${rootID}/disable`, 'POST')
    expect(lastDisable.status).toBe(409)
    expect(lastDisable.body).toEqual({ message: 'The last enabled super-admin cannot be disabled.' })

    const lastRole = await pageApi(page, `/api/employees/${rootID}/role`, 'POST', {
      role_id: financeRoleID,
    })
    expect(lastRole.status).toBe(409)
    expect(lastRole.body).toEqual({ message: 'The last enabled super-admin cannot lose the Role.' })
    await pageApi(page, '/api/logout', 'POST')

    // Restore the seeded matrix: revoke the widened grants.
    await loginHttp(page, root!.email, root!.password, await totpCode(root!.totpSecret))
    for (const permission of ['disable:employee', 'assign:employee-role']) {
      const revoked = await pageApi(page, `/api/roles/${financeRoleID}/grants`, 'POST', {
        permission,
        granted: false,
      })
      expect(revoked.status, permission).toBe(200)
    }

    // The revocation bites the finance session's next request — a
    // re-login keeps the cookie fresh while the grant stays gone.
    await pageApi(page, '/api/logout', 'POST')
    await loginHttp(page, finance.email, finance.password, await totpCode(finance.totpSecret))
    const refused = await pageApi(page, `/api/employees/${rootID}/disable`, 'POST')
    expect(refused.status).toBe(403)

    // The finance session cannot search the audit log — the
    // super-admin re-authenticates for the audit assertions.
    await pageApi(page, '/api/logout', 'POST')
    await loginHttp(page, root!.email, root!.password, await totpCode(root!.totpSecret))
    // The audit log is append-only across reruns, so assert this
    // run's rows by target + permission, not a global count.
    const grantAudit = await auditRows(page, { actor: root!.email, action: 'grant:role' })
    const granted = grantAudit.filter((row) => row.target === financeRoleID)
    expect(granted.length).toBeGreaterThanOrEqual(2)
    for (const permission of ['disable:employee', 'assign:employee-role']) {
      expect(
        granted.some((row) => JSON.stringify(row.after).includes(`"${permission}"`)),
        `this run granted ${permission}`,
      ).toBe(true)
    }
    const revokeAudit = await auditRows(page, { actor: root!.email, action: 'revoke:role' })
    const revoked = revokeAudit.filter((row) => row.target === financeRoleID)
    expect(revoked.length).toBeGreaterThanOrEqual(2)
    for (const permission of ['disable:employee', 'assign:employee-role']) {
      expect(
        revoked.some((row) => JSON.stringify(row.after).includes(`"${permission}"`)),
        `this run revoked ${permission}`,
      ).toBe(true)
    }
    await pageApi(page, '/api/logout', 'POST')
  })

  // The sole super-admin is still intact and signing in — the guards
  // protected the back office from locking itself out — and finance's
  // grants read back exactly as seeded (the widened grants restored).
  const stillRoot = await loginHttp(page, root!.email, root!.password, await totpCode(root!.totpSecret))
  expect(stillRoot.status).toBe(200)
  const roles = await pageApi(page, '/api/roles')
  expect(roles.status).toBe(200)
  const financeRole = (Array.isArray(roles.body) ? roles.body : []).find(
    (role) =>
      typeof role === 'object' &&
      role !== null &&
      'name' in role &&
      'permissions' in role &&
      (role as { name: string }).name === 'finance',
  ) as { permissions: string[] } | undefined
  expect(financeRole, 'the seeded Roles survive the journey').toBeTruthy()
  expect(financeRole!.permissions).not.toContain('disable:employee')
  expect(financeRole!.permissions).not.toContain('assign:employee-role')
})
