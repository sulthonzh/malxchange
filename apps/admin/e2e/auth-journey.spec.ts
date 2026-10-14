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
  totpCode,
} from './helpers'

test('login demands the mandatory TOTP and refuses wrong factors identically', async ({ page }) => {
  const root = readAdminState()
  test.skip(!root?.totpSecret, 'needs the provisioned super-admin from the first journey')

  const staff = await provisionEmployee(page, root!, 'support')
  await pageApi(page, '/api/logout', 'POST')

  // A bound Employee signs in with email + password + a live code.
  const noCode = await loginHttp(page, staff.email, staff.password)
  expect(noCode.status).toBe(401)
  expect(noCode.body).toEqual({ message: 'Your authenticator code is required.' })

  // A wrong code and a wrong password answer identically — no oracle
  // for which factor failed.
  const wrongCode = await loginHttp(
    page,
    staff.email,
    staff.password,
    // Ten windows old: a well-formed code no server clock accepts.
    await totpCode(staff.totpSecret, Date.now() - 10 * 60_000),
  )
  const wrongPassword = await loginHttp(page, staff.email, 'not-the-password', await totpCode(staff.totpSecret))
  const refused = { message: 'Email, password, or authenticator code is incorrect.' }
  expect(wrongCode.status).toBe(401)
  expect(wrongCode.body).toEqual(refused)
  expect(wrongPassword.status).toBe(401)
  expect(wrongPassword.body).toEqual(refused)

  // The live code completes the sign-in and the response carries the
  // Employee's live permission set.
  const signedIn = await loginHttp(page, staff.email, staff.password, await totpCode(staff.totpSecret))
  expect(signedIn.status).toBe(200)
  const me = await pageApi(page, '/api/me')
  expect(me.status).toBe(200)
  expect(me.body).toMatchObject({
    employee: { email: staff.email, role: 'support' },
    totpBound: true,
  })

  // Sign-out revokes the session: the next authenticated call fails.
  const signedOut = await pageApi(page, '/api/logout', 'POST')
  expect(signedOut.status).toBe(200)
  expect(signedOut.body).toEqual({ status: 'signed out' })
  const afterLogout = await pageApi(page, '/api/me')
  expect(afterLogout.status).toBe(401)

  // Every authentication event above left its audit row: two refused
  // sign-ins, the success, the logout — actor is the login email.
  const failed = await auditRows(page, { actor: staff.email, action: 'login:failed' })
  expect(failed).toHaveLength(2)
  const success = await auditRows(page, { actor: staff.email, action: 'login:employee' })
  expect(success).toHaveLength(1)
  const loggedOut = await auditRows(page, { actor: staff.email, action: 'logout:employee' })
  expect(loggedOut).toHaveLength(1)
})

test('five failed sign-ins lock the account and the lockout is audited', async ({ page }) => {
  const root = readAdminState()
  test.skip(!root?.totpSecret, 'needs the provisioned super-admin from the first journey')

  const staff = await provisionEmployee(page, root!, 'support')
  await pageApi(page, '/api/logout', 'POST')

  // Five wrong passwords: all refused with the identical sentence.
  for (let attempt = 1; attempt <= 5; attempt += 1) {
    const refused = await loginHttp(page, staff.email, 'not-the-password')
    expect(refused.status, `attempt ${attempt}`).toBe(401)
    expect(refused.body).toEqual({ message: 'Email, password, or authenticator code is incorrect.' })
  }

  // The lock bites the NEXT attempt — even with fully correct
  // credentials, so a stolen secret cannot brute-force the lock away.
  const locked = await loginHttp(page, staff.email, staff.password, await totpCode(staff.totpSecret))
  expect(locked.status).toBe(429)
  expect(locked.body).toEqual({ message: 'Too many failed sign-in attempts. Try again in a few minutes.' })
  const stillLocked = await loginHttp(page, staff.email, staff.password, await totpCode(staff.totpSecret))
  expect(stillLocked.status).toBe(429)

  // The audit log carries every refused attempt AND the lockouts —
  // refusals are the events the log must never drop.
  const failed = await auditRows(page, { actor: staff.email, action: 'login:failed' })
  expect(failed).toHaveLength(5)
  const lockouts = await auditRows(page, { actor: staff.email, action: 'login:lockout' })
  expect(lockouts.length).toBeGreaterThanOrEqual(2)
  for (const row of lockouts) {
    expect(row.origin_ip.length).toBeGreaterThan(0)
  }
})

test('forced-setup events and the disable guard leave their audit rows', async ({ page }) => {
  const root = readAdminState()
  test.skip(!root?.totpSecret, 'needs the provisioned super-admin from the first journey')

  // provisionEmployee walked the whole forced first-login setup:
  // rotation, enrollment begin, binding confirm — every step is a
  // mutation the middleware must have audited, with the create row
  // naming the super-admin as actor and the new Employee as target.
  const staff = await provisionEmployee(page, root!, 'finance')

  const create = await auditRows(page, { actor: root!.email, action: 'create:employee' })
  const createdRow = create.find((row) => JSON.stringify(row.after).includes(staff.email))
  expect(createdRow, 'the create audit row names the new Employee').toBeTruthy()
  expect(createdRow!.target).toBeTruthy()

  const rotated = await auditRows(page, { actor: staff.email, action: 'rotate:password' })
  expect(rotated).toHaveLength(1)
  const enrolled = await auditRows(page, { actor: staff.email, action: 'enroll:totp' })
  expect(enrolled).toHaveLength(1)
  const bound = await auditRows(page, { actor: staff.email, action: 'bind:totp' })
  expect(bound).toHaveLength(1)

  // The provisioned Employee is audited down to the before/after JSON:
  // the TOTP binding flipped from unbound to bound.
  const bindView = JSON.stringify(bound[0]!.after)
  expect(bindView).toContain('totpBound')

  // A disabled account cannot sign in, and both the disable and the
  // refused sign-in leave their rows.
  await loginHttp(page, root!.email, root!.password, await totpCode(root!.totpSecret))
  const id = await employeeIdOf(page, staff.email)
  const disabled = await pageApi(page, `/api/employees/${id}/disable`, 'POST')
  expect(disabled.status).toBe(200)
  await pageApi(page, '/api/logout', 'POST')

  const refused = await loginHttp(page, staff.email, staff.password, await totpCode(staff.totpSecret))
  expect(refused.status).toBe(403)
  expect(refused.body).toEqual({ message: 'This account is disabled.' })

  const disableRows = await auditRows(page, { actor: root!.email, action: 'disable:employee' })
  const disableRow = disableRows.find((row) => row.target === id)
  expect(disableRow, 'the disable audit row names the target Employee').toBeTruthy()
  const failedRows = await auditRows(page, { actor: staff.email, action: 'login:failed' })
  expect(failedRows.length).toBeGreaterThanOrEqual(1)
})
