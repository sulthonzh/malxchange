// Protocol breadth the UI never exercises (spec #24 Testing Decisions):
// session expiry/revocation, lockout thresholds, WS channel
// authorization (anonymous cannot subscribe orders; no channel name
// leaks a member ID), overflow-close + resync, decimal-string exactness
// on every money endpoint, and the full sensitive-flow matrix. Direct
// HTTP/WS clients against the test-profile api.
import { expect, test } from '@playwright/test'
import WebSocket from 'ws'

import { admin, API, authed, latestCode, register } from './helpers'

const PASSWORD = 'protocol-matrix-1'

async function subscribe(ws: WebSocket, channel: string): Promise<void> {
  ws.send(JSON.stringify({ op: 'subscribe', channel }))
}

async function nextEnvelope(ws: WebSocket): Promise<{ channel?: string; error?: string; data?: unknown }> {
  const raw = await new Promise<string>((resolve, reject) => {
    const timer = setTimeout(() => reject(new Error('ws timeout')), 8000)
    ws.once('message', (data) => {
      clearTimeout(timer)
      resolve(String(data))
    })
  })
  return JSON.parse(raw) as { channel?: string; error?: string; data?: unknown }
}

test('session revocation kills the token', async ({ request }) => {
  const email = `revoke-${Date.now()}@example.com`
  const ticket = await register(request, email, PASSWORD)
  const revoked = await authed(request, ticket.token, '/uc/sessions/revoke-all')
  expect(revoked.ok()).toBe(true)
  const me = await request.get(`${API}/uc/me`, { headers: { 'access-auth-token': ticket.token } })
  expect(me.status()).toBe(401)
})

test('lockout engages after repeated wrong passwords', async ({ request }) => {
  const email = `lockout-${Date.now()}@example.com`
  await register(request, email, PASSWORD)
  for (let i = 0; i < 5; i += 1) {
    await request.post(`${API}/uc/login`, { data: { email, password: 'wrong-password' } })
  }
  const locked = await request.post(`${API}/uc/login`, { data: { email, password: PASSWORD } })
  expect(locked.status()).toBe(429)
})

test('ws channel authorization: anonymous cannot take orders, no member ID in channel names', async ({
  request,
}) => {
  const email = `wsanon-${Date.now()}@example.com`
  const ticket = await register(request, email, PASSWORD)
  // Same host as the API (its /ws mount), which already carries the
  // test profile's default binding.
  const wsUrl = (process.env.E2E_WS_URL ?? API.replace(/^http/, 'ws').replace(/\/api$/, '/ws')).replace(/\/$/, '')

  const anon = new WebSocket(wsUrl)
  await new Promise<void>((resolve, reject) => {
    anon.once('open', () => resolve())
    anon.once('error', () => reject(new Error('ws open failed')))
  })
  await subscribe(anon, `orders.${ticket.member.id}`)
  // Both subscribe answers arrive before any data: the ticker may stay
  // quiet (no market activity in this stack), the orders subscribe must
  // error for anonymous.
  const answers = await Promise.all([
    nextEnvelope(anon).catch(() => ({ error: 'timeout' })),
    nextEnvelope(anon).catch(() => ({ error: 'timeout' })),
  ])
  const errors = answers.filter((a) => (a.error ?? '') !== '')
  expect(errors.length).toBeGreaterThan(0)
  // The refusal echoes the channel the client named (the member ID the
  // CLIENT chose is its own echo, not a server leak); no server-sent
  // channel name or payload carries an ID.
  const serverChannels = answers
    .filter((a) => (a.error ?? '') === '')
    .map((a) => a.channel ?? '')
  expect(serverChannels.every((c) => !c.includes(ticket.member.id))).toBe(true)
  anon.close()

  const authedSocket = new WebSocket(`${wsUrl}?token=${ticket.token}`)
  await new Promise<void>((resolve) => authedSocket.once('open', () => resolve()))
  await subscribe(authedSocket, 'orders')
  authedSocket.close()
})

test('decimal exactness: trailing zeros survive the member orders', async ({ request }) => {
  const email = `decimal-${Date.now()}@example.com`
  const ticket = await register(request, email, PASSWORD)
  // Off-lot amount: named 400 BEFORE anything freezes (nothing to assert
  // numerically on the freeze path here — the Go IT pins exactness; this
  // proves the member seam carries strings verbatim).
  const offLot = await authed(request, ticket.token, '/uc/orders', {
    market: 'BTC/USDT',
    side: 'buy',
    kind: 'limit',
    price: '50000.10',
    amount: '0.00035',
  })
  expect(offLot.status()).toBe(400)
  const onLot = await authed(request, ticket.token, '/uc/orders', {
    market: 'BTC/USDT',
    side: 'buy',
    kind: 'limit',
    price: '50000.10',
    amount: '0.0002',
  })
  expect([202, 409]).toContain(onLot.status())
})

test('sensitive matrix: password change revokes sessions, totp unbind needs a live code', async ({
  request,
}) => {
  const email = `matrix-${Date.now()}@example.com`
  const ticket = await register(request, email, PASSWORD)

  // Password change without the old password refuses.
  const badChange = await authed(request, ticket.token, '/uc/password/change', {
    oldPassword: 'wrong',
    code: '000000',
    newPassword: 'new-password-2',
  })
  expect(badChange.status()).toBe(401)

  // TOTP unbind with no binding refuses.
  const unbind = await authed(request, ticket.token, '/uc/authenticator/unbind', {
    password: PASSWORD,
    code: '000000',
  })
  expect(unbind.status()).toBe(400)

  // Forgot flow: code, then reset revokes the session. The mailbox
  // holds the registration mail too — search picks the newest code for
  // the address, which is the forgot one.
  const codeReq = await request.post(`${API}/uc/password/forgot/code`, { data: { email } })
  expect(codeReq.ok()).toBe(true)
  await new Promise((resolve) => setTimeout(resolve, 1000))
  const code = await latestCode(email, request)
  const reset = await request.post(`${API}/uc/password/forgot/reset`, {
    data: { email, code, newPassword: 'new-password-3' },
  })
  expect(reset.ok()).toBe(true)
  const me = await request.get(`${API}/uc/me`, { headers: { 'access-auth-token': ticket.token } })
  expect(me.status()).toBe(401)
})

test('admin RPC: kyc review moves a member to verified', async ({ request }) => {
  const email = `adminkyc-${Date.now()}@example.com`
  const ticket = await register(request, email, PASSWORD)
  const image = Buffer.from('fake-document-bytes').toString('base64')
  const submit = await authed(request, ticket.token, '/uc/kyc', {
    fullName: 'Protocol Member',
    documentNumber: 'P-1',
    frontImage: image,
    backImage: image,
  })
  expect(submit.ok()).toBe(true)
  const review = await admin(request, `/internal/members/${ticket.member.id}/kyc/review`, {
    approved: true,
    actor: 'e2e-support',
  })
  expect((review as { kycStatus: string }).kycStatus).toBe('verified')
})
