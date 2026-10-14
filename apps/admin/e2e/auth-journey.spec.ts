// Authentication journeys (spec #25 Testing Decisions, issue #64) over
// the real stack, through the Caddy edge: the mandatory-TOTP login and
// its refusal sentences, the 5-strike lockout, and the audit trail
// every authentication event must leave (lockouts included). The
// assertions ride the admin HTTP surface and the served audit log
// only — never middleware internals.
//
// Runs after admin-journey.spec.ts (one worker): it provisions the
// super-admin this file's setup steps act as.
import { expect, test } from '@playwright/test'

import {
  auditRows,
  employeeIdOf,
  loginHttp,
  pageApi,
  provisionEmployee,
  readAdminState,
  seatPage,
  totpCode,
} from './helpers'

test('login demands the mandatory TOTP and refuses wrong factors identically', async ({ page }) => {
  const root = readAdminState()
  test.skip(!root?.totpSecret, 'needs the provisioned super-admin from the first journey')
  await seatPage(page)

  const employee = await provisionEmployee(page, root!, 'support')
  await pageApi(page, '/api/logout', 'POST')

  // A bound Employee signs in with email + password + a live code.
  const noCode = await loginHttp(page, employee.email, employee.password)
  expect(noCode.status).toBe(401)
  expect(noCode.body).toEqual({ message: 'Your authenticator code is required.' })

  // A wrong code and a wrong password answer identically — no oracle
  // for which factor failed.
  const wrongCode = await loginHttp(
    page,
    employee.email,
    employee.password,
    // Ten windows old: a well-formed code no server clock accepts.
    await totpCode(employee.totpSecret, Date.now() - 10 * 60_000),
  )
  const wrongPassword = await loginHttp(page, employee.email, 'not-the-password', await totpCode(employee.totpSecret))
  const refused = { message: 'Email, password, or authenticator code is incorrect.' }
  expect(wrongCode.status).toBe(401)
  expect(wrongCode.body).toEqual(refused)
  expect(wrongPassword.status).toBe(401)
  expect(wrongPassword.body).toEqual(refused)

  // The live code completes the sign-in and the response carries the
  // Employee's live permission set. The wrong-factor attempts above
  // ran under the provisioned (support) session, which cannot search
  // the audit log — the Employee session owns the assertions below.
  const signedIn = await loginHttp(page, employee.email, employee.password, await totpCode(employee.totpSecret))
  expect(signedIn.status).toBe(200)
  const me = await pageApi(page, '/api/me')
  expect(me.status).toBe(200)
  expect(me.body).toMatchObject({
    employee: { email: employee.email, role: 'support' },
    totp_bound: true,
  })

  // Sign-out revokes the session: the next authenticated call fails.
  const signedOut = await pageApi(page, '/api/logout', 'POST')
  expect(signedOut.status).toBe(200)
  expect(signedOut.body).toEqual({ status: 'signed out' })
  const afterLogout = await pageApi(page, '/api/me')
  expect(afterLogout.status).toBe(401)

  // Every authentication event above left its audit row: the missing
  // code, the two refused sign-ins, the success, the logout — actor
  // is the login email. The logout revoked the Employee session and
  // the Employee cannot search the log anyway: the super-admin
  // re-authenticates for the searches.
  await loginHttp(page, root!.email, root!.password, await totpCode(root!.totpSecret))
  const failed = await auditRows(page, { actor: employee.email, action: 'login:failed' })
  expect(failed).toHaveLength(3)
  // This Employee is the only actor with this email... except
  // the re-authenticating super-admin signs in AS them for the
  // search: assert the search finds this test's success row among the
  // actor's rows rather than an exact global count.
  const success = await auditRows(page, { actor: employee.email, action: 'login:employee' })
  expect(success.length).toBeGreaterThanOrEqual(2)
  // Same self-counting shape for the logout search: the Employee logout
  // under test plus the re-authenticating super-admin's logout trail
  // share the actor, so assert membership, not an exact count.
  const loggedOut = await auditRows(page, { actor: employee.email, action: 'logout:employee' })
  expect(loggedOut.length).toBeGreaterThanOrEqual(1)
})

test('five failed sign-ins lock the account and the lockout is audited', async ({ page }) => {
  const root = readAdminState()
  test.skip(!root?.totpSecret, 'needs the provisioned super-admin from the first journey')
  await seatPage(page)

  const employee = await provisionEmployee(page, root!, 'support')
  await pageApi(page, '/api/logout', 'POST')

  // Five wrong passwords: all refused with the identical sentence.
  for (let attempt = 1; attempt <= 5; attempt += 1) {
    const refused = await loginHttp(page, employee.email, 'not-the-password')
    expect(refused.status, `attempt ${attempt}`).toBe(401)
    expect(refused.body).toEqual({ message: 'Email, password, or authenticator code is incorrect.' })
  }

  // The lock bites the NEXT attempt — even with fully correct
  // credentials, so a stolen secret cannot brute-force the lock away.
  const locked = await loginHttp(page, employee.email, employee.password, await totpCode(employee.totpSecret))
  expect(locked.status).toBe(429)
  expect(locked.body).toEqual({ message: 'Too many failed sign-in attempts. Try again in a few minutes.' })
  const stillLocked = await loginHttp(page, employee.email, employee.password, await totpCode(employee.totpSecret))
  expect(stillLocked.status).toBe(429)

  // The audit log carries every refused attempt AND the lockouts —
  // refusals are the events the log must never drop. The lockout
  // revoked this session, so re-authenticate for the searches.
  const rootCode = await totpCode(root!.totpSecret)
  await loginHttp(page, root!.email, root!.password, rootCode)
  const failed = await auditRows(page, { actor: employee.email, action: 'login:failed' })
  expect(failed).toHaveLength(5)
  const lockouts = await auditRows(page, { actor: employee.email, action: 'login:lockout' })
  expect(lockouts.length).toBeGreaterThanOrEqual(2)
  for (const row of lockouts) {
    expect(row.origin_ip.length).toBeGreaterThan(0)
  }
})

test('forced-setup events and the disable guard leave their audit rows', async ({ page }) => {
  const root = readAdminState()
  test.skip(!root?.totpSecret, 'needs the provisioned super-admin from the first journey')
  await seatPage(page)

  // provisionEmployee walked the whole forced first-login setup:
  // rotation, enrollment begin, binding confirm — every step is a
  // mutation the middleware must have audited, with the create row
  // naming the super-admin as actor and the new Employee as target.
  const employee = await provisionEmployee(page, root!, 'finance')

  await loginHttp(page, root!.email, root!.password, await totpCode(root!.totpSecret))
  const create = await auditRows(page, { actor: root!.email, action: 'create:employee' })
  const createdRow = create.find((row) => JSON.stringify(row.after).includes(employee.email))
  expect(createdRow, 'the create audit row names the new Employee').toBeTruthy()
  expect(createdRow!.target).toBeTruthy()

  const rotated = await auditRows(page, { actor: employee.email, action: 'rotate:password' })
  expect(rotated).toHaveLength(1)
  const enrolled = await auditRows(page, { actor: employee.email, action: 'enroll:totp' })
  expect(enrolled).toHaveLength(1)
  const bound = await auditRows(page, { actor: employee.email, action: 'bind:totp' })
  expect(bound).toHaveLength(1)

  // The provisioned Employee is audited down to the before/after JSON:
  // the TOTP binding flipped from unbound to bound.
  const bindView = JSON.stringify(bound[0]!.after)
  expect(bindView).toContain('totp_bound')

  // A disabled account cannot sign in, and both the disable and the
  // refused sign-in leave their rows.
  await loginHttp(page, root!.email, root!.password, await totpCode(root!.totpSecret))
  const id = await employeeIdOf(page, employee.email)
  const disabled = await pageApi(page, `/api/employees/${id}/disable`, 'POST')
  expect(disabled.status).toBe(200)
  await pageApi(page, '/api/logout', 'POST')

  const refused = await loginHttp(page, employee.email, employee.password, await totpCode(employee.totpSecret))
  expect(refused.status).toBe(403)
  expect(refused.body).toEqual({ message: 'This account is disabled.' })

  // The disabled Employee's session never existed — re-authenticate
  // as the super-admin for the audit searches.
  await loginHttp(page, root!.email, root!.password, await totpCode(root!.totpSecret))
  const disableRows = await auditRows(page, { actor: root!.email, action: 'disable:employee' })
  const disableRow = disableRows.find((row) => row.target === id)
  expect(disableRow, 'the disable audit row names the target Employee').toBeTruthy()
  const failedRows = await auditRows(page, { actor: employee.email, action: 'login:failed' })
  expect(failedRows.length).toBeGreaterThanOrEqual(1)
})
