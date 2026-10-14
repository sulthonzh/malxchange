// The four Roles' matrices (spec #25 Testing Decisions, issue #64)
// over the real stack: finance cannot review KYC or manage Employees,
// compliance cannot move funds, support cannot touch money or KYC, and
// even the super-admin is refused the single-Role endpoints (KYC
// review belongs to compliance alone, withdrawal approval to finance,
// content to support). Every positive is a real orchestration — the
// finance adjustment lands in the wallet's Journal, the compliance KYC
// review moves the member's status through api, the compliance member
// TOTP reset honors the KYC-verified gate — and every mutation leaves
// its audit row. Assertions ride the admin HTTP surface, the member
// HTTP surface, and the served audit log only.
import { expect, test } from '@playwright/test'

import {
  auditRows,
  loginHttp,
  mailedCode,
  memberCall,
  pageApi,
  provisionEmployee,
  readAdminState,
  registerMember,
  roleIdOf,
  submitMemberKyc,
  totpCode,
  seatPage,
} from './helpers'

const noPermission = 'You do not have permission to do that.'

test('the four Roles act inside their matrices and are refused outside', async ({ page, request }) => {
  const root = readAdminState()
  test.skip(!root?.totpSecret, 'needs the provisioned super-admin from the first journey')
  await seatPage(page)

  // One member walks the real path: register → KYC submit → TOTP bind.
  const memberEmail = `e2e-matrix-${Date.now()}@example.com`
  const memberPassword = 'a-fine-e2e-password'
  const ticket = await registerMember(request, memberEmail, memberPassword)
  await submitMemberKyc(request, ticket.token)

  // A second member stays KYC-unverified: the compliance TOTP reset
  // gate must refuse them, and the super-admin step reuses the id for
  // its own refusal (an unknown id would answer 404 before 403).
  const strangerEmail = `e2e-matrix-unverified-${Date.now()}@example.com`
  const stranger = await registerMember(request, strangerEmail, memberPassword)

  const bindMailedAt = Date.now()
  const codeSent = await memberCall(request, ticket.token, '/uc/authenticator/code', {}, 'POST')
  expect(codeSent.status(), await codeSent.text()).toBe(200)
  const emailCode = await mailedCode(memberEmail, request, bindMailedAt)
  const begun = await memberCall(request, ticket.token, '/uc/authenticator/bind/begin', { code: emailCode })
  expect(begun.status()).toBe(200)
  const begunBody = (await begun.json()) as { secret?: string }
  expect(begunBody.secret, 'the member TOTP enrollment stages a secret').toBeTruthy()
  const memberSecret = begunBody.secret!
  const confirmed = await memberCall(request, ticket.token, '/uc/authenticator/bind/confirm', {
    code: await totpCode(memberSecret),
  })
  expect(confirmed.status()).toBe(200)

  const finance = await provisionEmployee(page, root!, 'finance')
  const compliance = await provisionEmployee(page, root!, 'compliance')

  // ---- finance: funds yes, identity and management no ----
  await test.step('finance: funds yes, identity no', async () => {
    // provisionEmployee leaves the LAST provisioned session in the
    // cookie jar (compliance here) — finance signs in explicitly.
    await loginHttp(page, finance.email, finance.password, await totpCode(finance.totpSecret))
    const queue = await pageApi(page, '/api/withdrawals')
    expect(queue.status).toBe(200)

    // The allowed fund move, end to end: a manual adjustment with a
    // reason lands as an auditable Journal entry in the real wallet.
    const adjustKey = `e2e-matrix-${Date.now()}`
    const adjusted = await pageApi(page, '/api/adjustments', 'POST', {
      member: ticket.member.id,
      coin: 'USDT',
      amount: '25',
      reason: 'e2e matrix journey',
      key: adjustKey,
      code: await totpCode(finance.totpSecret),
    })
    expect(adjusted.status, JSON.stringify(adjusted.body)).toBe(200)
    expect(adjusted.body).toEqual({ ok: true, applied: true })

    // Idempotency: replaying the same key moves nothing twice.
    const replayed = await pageApi(page, '/api/adjustments', 'POST', {
      member: ticket.member.id,
      coin: 'USDT',
      amount: '25',
      reason: 'e2e matrix journey',
      key: adjustKey,
      code: await totpCode(finance.totpSecret),
    })
    expect(replayed.body).toEqual({ ok: true, applied: false })

    const kyc = await pageApi(page, '/api/kyc/applications')
    expect(kyc.status).toBe(403)
    expect(kyc.body).toEqual({ message: noPermission })
    const review = await pageApi(page, `/api/kyc/applications/${ticket.member.id}/review`, 'POST', {
      approved: true,
    })
    expect(review.status).toBe(403)
    const totpReset = await pageApi(page, `/api/members/${ticket.member.id}/totp/reset`, 'POST', {
      code: await totpCode(finance.totpSecret),
    })
    expect(totpReset.status).toBe(403)
    const employees = await pageApi(page, '/api/employees')
    expect(employees.status).toBe(403)
    const channels = await pageApi(page, '/api/channels')
    expect(channels.status).toBe(403)

    await pageApi(page, '/api/logout', 'POST')
    // Finance cannot search the audit log — re-authenticate as the
    // super-admin for the audit assertion.
    await loginHttp(page, root!.email, root!.password, await totpCode(root!.totpSecret))
    const audit = await auditRows(page, { actor: finance.email, action: 'adjust:balance' })
    expect(audit).toHaveLength(2) // the adjustment and its idempotent replay
    expect(audit.every((row) => row.target === ticket.member.id)).toBe(true)
  })
  await pageApi(page, '/api/logout', 'POST')

  // ---- compliance: identity yes, funds and management no ----
  await test.step('compliance: identity yes, funds no', async () => {
    // The finance step's cleanup logged the page out.
    await loginHttp(page, compliance.email, compliance.password, await totpCode(compliance.totpSecret))
    const queue = await pageApi(page, '/api/kyc/applications')
    expect(queue.status).toBe(200)
    const applications =
      isQueueBody(queue.body) && queue.body.applications.some((item) => item.memberId === ticket.member.id)
    expect(applications, 'the pending application waits in the queue').toBe(true)

    // The human decision, through the real api: approval verifies.
    const reviewed = await pageApi(
      page,
      `/api/kyc/applications/${ticket.member.id}/review`,
      'POST',
      { approved: true },
    )
    expect(reviewed.status, JSON.stringify(reviewed.body)).toBe(200)
    expect(reviewed.body).toEqual({ memberId: ticket.member.id, kycStatus: 'verified' })

    // Member TOTP reset honors the KYC-verified gate: this member is
    // verified now, so the reset revokes the binding (Step-up fresh).
    const reset = await pageApi(page, `/api/members/${ticket.member.id}/totp/reset`, 'POST', {
      code: await totpCode(compliance.totpSecret),
    })
    expect(reset.status, JSON.stringify(reset.body)).toBe(200)
    expect(reset.body).toEqual({ memberId: ticket.member.id, totpBound: false })

    // An unverified member's reset is refused by the gate.
    const refused = await pageApi(page, `/api/members/${stranger.member.id}/totp/reset`, 'POST', {
      code: await totpCode(compliance.totpSecret),
    })
    expect(refused.status).toBe(409)
    expect(refused.body).toEqual({
      message: 'authenticator reset requires a KYC-verified member',
    })

    const adjust = await pageApi(page, '/api/adjustments', 'POST', {
      member: ticket.member.id,
      coin: 'USDT',
      amount: '1',
      reason: 'e2e must refuse',
      key: `e2e-compliance-${Date.now()}`,
      code: await totpCode(compliance.totpSecret),
    })
    expect(adjust.status).toBe(403)
    const approve = await pageApi(page, '/api/withdrawals/00000000-0000-0000-0000-000000000000/approve', 'POST', {
      code: await totpCode(compliance.totpSecret),
    })
    expect(approve.status).toBe(403)
    const ops = await pageApi(page, '/api/ops')
    expect(ops.status).toBe(403)

    await pageApi(page, '/api/logout', 'POST')
    await loginHttp(page, root!.email, root!.password, await totpCode(root!.totpSecret))
    const reviewAudit = await auditRows(page, { actor: compliance.email, action: 'review:kyc' })
    expect(reviewAudit).toHaveLength(1)
    expect(reviewAudit[0]!.target).toBe(ticket.member.id)
    const resetAudit = await auditRows(page, { actor: compliance.email, action: 'reset:member-totp' })
    expect(resetAudit).toHaveLength(1)
    expect(resetAudit[0]!.target).toBe(ticket.member.id)
  })
  await pageApi(page, '/api/logout', 'POST')

  // ---- support: members and content yes, money and KYC no ----
  await test.step('support: members yes, money and KYC no', async () => {
    const support = await provisionEmployee(page, root!, 'support')
    // admin-journey's grants-free test strips support to zero grants on
    // this shared stack — restore the member strings this step proves.
    await loginHttp(page, root!.email, root!.password, await totpCode(root!.totpSecret))
    const supportRole = await roleIdOf(page, 'support')
    for (const permission of ['search:member', 'view:member', 'lock:member', 'unlock:member']) {
      const granted = await pageApi(page, `/api/roles/${supportRole}/grants`, 'POST', { permission, granted: true })
      expect(granted.status, permission).toBe(200)
    }
    await loginHttp(page, support.email, support.password, await totpCode(support.totpSecret))

    const viewed = await pageApi(page, `/api/members/${ticket.member.id}`)
    expect(viewed.status).toBe(200)
    expect(viewed.body).toMatchObject({ kycStatus: 'verified' })

    const locked = await pageApi(page, `/api/members/${ticket.member.id}/lock`, 'POST')
    expect(locked.status, JSON.stringify(locked.body)).toBe(200)
    const unlocked = await pageApi(page, `/api/members/${ticket.member.id}/unlock`, 'POST')
    expect(unlocked.status).toBe(200)

    const adjust = await pageApi(page, '/api/adjustments', 'POST', {
      member: ticket.member.id,
      coin: 'USDT',
      amount: '1',
      reason: 'e2e must refuse',
      key: `e2e-support-${Date.now()}`,
      code: await totpCode(support.totpSecret),
    })
    expect(adjust.status).toBe(403)
    const queue = await pageApi(page, '/api/kyc/applications')
    expect(queue.status).toBe(403)
    const withdrawals = await pageApi(page, '/api/withdrawals')
    expect(withdrawals.status).toBe(403)
    const employees = await pageApi(page, '/api/employees')
    expect(employees.status).toBe(403)
    const channels = await pageApi(page, '/api/channels')
    expect(channels.status).toBe(403)

    await pageApi(page, '/api/logout', 'POST')
    await loginHttp(page, root!.email, root!.password, await totpCode(root!.totpSecret))
    const lockAudit = await auditRows(page, { actor: support.email, action: 'lock:member' })
    expect(lockAudit).toHaveLength(1)
    expect(lockAudit[0]!.target).toBe(ticket.member.id)
    const unlockAudit = await auditRows(page, { actor: support.email, action: 'unlock:member' })
    expect(unlockAudit).toHaveLength(1)
    await pageApi(page, '/api/logout', 'POST')
  })

  // ---- super-admin: management yes, but the single-Role endpoints
  // refuse even the highest Role ----
  await test.step('super-admin: management yes, single-Role endpoints no', async () => {
    await loginHttp(page, root!.email, root!.password, await totpCode(root!.totpSecret))

    const employees = await pageApi(page, '/api/employees')
    expect(employees.status).toBe(200)
    const permissions = await pageApi(page, '/api/permissions')
    expect(permissions.status).toBe(200)
    const auditLog = await pageApi(page, '/api/audit-log?action=login:employee')
    expect(auditLog.status).toBe(200)

    // KYC review belongs to compliance alone; the super-admin reading
    // the queue is allowed but the decision is refused.
    const queue = await pageApi(page, '/api/kyc/applications')
    expect(queue.status).toBe(200)
    const review = await pageApi(page, `/api/kyc/applications/${stranger.member.id}/review`, 'POST', {
      approved: false,
      reason: 'e2e super-admin must not review',
    })
    expect(review.status).toBe(403)
    expect(review.body).toEqual({ message: noPermission })

    const approve = await pageApi(page, '/api/withdrawals/00000000-0000-0000-0000-000000000000/approve', 'POST', {
      code: await totpCode(root!.totpSecret),
    })
    expect(approve.status).toBe(403)
    const adjust = await pageApi(page, '/api/adjustments', 'POST', {
      member: ticket.member.id,
      coin: 'USDT',
      amount: '1',
      reason: 'e2e must refuse',
      key: `e2e-root-${Date.now()}`,
      code: await totpCode(root!.totpSecret),
    })
    expect(adjust.status).toBe(403)
    const announcement = await pageApi(page, '/api/announcements', 'POST', {
      title: 'super-admin must not publish',
      content: '<p>support owns content</p>',
      published: false,
    })
    expect(announcement.status).toBe(403)
    await pageApi(page, '/api/logout', 'POST')
  })
})

interface QueueItem {
  memberId: string
}

function isQueueBody(body: unknown): body is { applications: QueueItem[] } {
  if (typeof body !== 'object' || body === null || !('applications' in body)) {
    return false
  }
  return Array.isArray(body.applications)
}
