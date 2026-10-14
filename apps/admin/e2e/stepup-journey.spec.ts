// Step-up journeys (spec #25 Testing Decisions, issue #64): every
// action on the ADR-0012 Step-up list demands a fresh TOTP code of the
// CALLER and enforces it server-side — a missing code is refused with
// the required-code sentence, a stale code with the invalid-code
// sentence, and only a fresh code passes the gate (the request then
// proceeds to its own validation, which is exactly what the
// past-the-gate assertions pin). All calls ride the admin HTTP
// surface; nothing inspects middleware internals.
//
// The Step-up list (spec #25): manual balance adjust, member TOTP
// reset, withdrawal approval, failed broadcast/sweep retry, hot→cold
// transfer, code-channel credential saves, Employee create, and
// password/TOTP resets of other Employees.
import { expect, test } from '@playwright/test'

import {
  loginHttp,
  pageApi,
  provisionEmployee,
  readAdminState,
  registerMember,
  totpCode,
  type StaffAccount,
} from './helpers'
/** What happens once the Step-up gate passes: the request's own
 * validation or upstream answer — never a Step-up 401. `message` is
 * the exact sentence; `messageContains` a stable fragment. */
interface PastGate {
  status: number
  message?: string
  messageContains?: string
}

interface StepUpAction {
  /** The spec's name for the action. */
  name: string
  /** The Employee whose Role the matrix grants the action. */
  as: 'root' | 'finance' | 'compliance'
  path: string
  /** The full request body minus the code (spread with each variant). */
  body: Record<string, unknown>
  pastGate: PastGate
}

/** A well-formed code from ten windows ago: no server clock accepts
 * it, so it isolates the stale-code refusal from typos. */
async function staleCode(secret: string): Promise<string> {
  return totpCode(secret, Date.now() - 10 * 60_000)
}

test('every Step-up action demands and enforces a fresh code server-side', async ({
  page,
  request,
}) => {
  const root = readAdminState()
  test.skip(!root?.totpSecret, 'needs the provisioned super-admin from the first journey')

  // The balance adjust needs a real member row in the wallet ledger.
  const memberEmail = `e2e-stepup-${Date.now()}@example.com`
  const ticket = await registerMember(request, memberEmail, 'a-fine-e2e-password')

  const finance = await provisionEmployee(page, root!, 'finance')
  const compliance = await provisionEmployee(page, root!, 'compliance')

  const dummyID = '00000000-0000-0000-0000-000000000000'
  const actions: StepUpAction[] = [
    {
      name: 'manual balance adjust',
      as: 'finance',
      path: '/api/adjustments',
      body: {
        member: ticket.member.id,
        coin: 'USDT',
        amount: '1.5',
        reason: 'e2e step-up proof',
        key: `e2e-stepup-${Date.now()}`,
      },
      pastGate: { status: 200 },
    },
    {
      name: 'member TOTP reset',
      as: 'compliance',
      path: `/api/members/${dummyID}/totp/reset`,
      body: {},
      pastGate: { status: 404, message: 'member not found' },
    },
    {
      name: 'withdrawal approval',
      as: 'finance',
      path: `/api/withdrawals/${dummyID}/approve`,
      body: {},
      // The unknown id reaches the wallet's guarded state machine.
      pastGate: { status: 400, messageContains: 'invalid state' },
    },
    {
      name: 'failed broadcast retry',
      as: 'finance',
      path: `/api/withdrawals/${dummyID}/retry`,
      body: {},
      pastGate: { status: 400, messageContains: 'invalid state' },
    },
    {
      name: 'sweep retry',
      as: 'finance',
      path: `/api/sweeps/${dummyID}/retry`,
      body: {},
      pastGate: { status: 400, messageContains: 'invalid state' },
    },
    {
      name: 'hot→cold transfer',
      as: 'finance',
      path: '/api/hot-transfers',
      // Seeded BTC carries no cold address on a fresh install, so the
      // wallet refuses the record itself — past the Step-up gate.
      body: { coin: 'BTC', amount: '1' },
      pastGate: { status: 400, messageContains: 'cold address' },
    },
    {
      name: 'hot-transfer retry',
      as: 'finance',
      path: `/api/hot-transfers/${dummyID}/retry`,
      body: {},
      pastGate: { status: 400, messageContains: 'invalid state' },
    },
    {
      name: 'code-channel credential save',
      as: 'root',
      path: '/api/channels/nosuch/activate',
      body: { credentials: {} },
      pastGate: { status: 404, message: 'unknown code channel' },
    },
    {
      name: 'Employee create',
      as: 'root',
      path: '/api/employees',
      body: { email: 'not-an-email', password: 'E2e-one-time-x', role_id: 'none' },
      pastGate: { status: 400, message: 'Enter a valid email address.' },
    },
    {
      name: 'password reset of another Employee',
      as: 'root',
      path: `/api/employees/${dummyID}/reset-password`,
      body: {},
      pastGate: { status: 404, message: 'Employee not found.' },
    },
    {
      name: 'TOTP reset of another Employee',
      as: 'root',
      path: `/api/employees/${dummyID}/reset-totp`,
      body: {},
      pastGate: { status: 404, message: 'Employee not found.' },
    },
  ]

  const actors: Record<'root' | 'finance' | 'compliance', StaffAccount> = {
    root: { email: root!.email, password: root!.password, totpSecret: root!.totpSecret, role: 'super-admin' },
    finance,
    compliance,
  }

  for (const action of actions) {
    await test.step(`${action.name} (${action.as})`, async () => {
      const caller = actors[action.as]
      const signedIn = await loginHttp(page, caller.email, caller.password, await totpCode(caller.totpSecret))
      expect(signedIn.status, `${action.name}: sign in as ${action.as}`).toBe(200)

      // Missing code: refused before anything else happens.
      const missing = await pageApi(page, action.path, 'POST', { ...action.body, code: '' })
      expect(missing.status, `${action.name}: missing code status`).toBe(401)
      expect(missing.body, `${action.name}: missing code sentence`).toEqual({ message: stepUpRequired })

      // Stale code: still refused — the code must be fresh at action time.
      const stale = await pageApi(page, action.path, 'POST', {
        ...action.body,
        code: await staleCode(caller.totpSecret),
      })
      expect(stale.status, `${action.name}: stale code status`).toBe(401)
      expect(stale.body, `${action.name}: stale code sentence`).toEqual({ message: stepUpInvalid })

      // Another Employee's fresh code is not the caller's code: the
      // gate verifies the CALLER's binding.
      const other = action.as === 'finance' ? compliance : finance
      const borrowed = await pageApi(page, action.path, 'POST', {
        ...action.body,
        code: await totpCode(other.totpSecret),
      })
      expect(borrowed.status, `${action.name}: borrowed code status`).toBe(401)
      expect(borrowed.body, `${action.name}: borrowed code sentence`).toEqual({ message: stepUpInvalid })

      // The caller's own fresh code passes the gate; the request then
      // answers its own validation, never the Step-up refusal.
      const fresh = await pageApi(page, action.path, 'POST', {
        ...action.body,
        code: await totpCode(caller.totpSecret),
      })
      if (action.pastGate.message !== undefined) {
        expect(fresh.body, `${action.name}: past the gate`).toEqual({
          message: action.pastGate.message,
        })
      }
      if (action.pastGate.messageContains !== undefined) {
        const text = JSON.stringify(fresh.body)
        expect(text, `${action.name}: past the gate fragment`).toContain(action.pastGate.messageContains)
      }
      expect(fresh.status, `${action.name}: past the gate status`).toBe(action.pastGate.status)

      await pageApi(page, '/api/logout', 'POST')
    })
  }
})
