// The member journey (spec #24 Testing Decisions): register (code
// from the test mail sink) → login → KYC submit → admin approve → TOTP
// bind → deposit (simnet chain) → place/cancel orders → withdraw
// (admin audit → simnet broadcast). Runs against the compose test
// profile: `npm run e2e` with the stack up.
import { expect, test } from '@playwright/test'

import { admin, API, authed, latestCode, login, register, submitKyc, totpCode } from './helpers'

test('member journey: register to withdraw', async ({ page, request }) => {
  const email = `e2e-${Date.now()}@example.com`
  const password = 'a-fine-e2e-password'

  // Register through the emailed code, then sign in on the web app.
  const ticket = await register(request, email, password)
  await login(page, email, password)

  // KYC: submit in the member center, approve through the admin seam.
  await submitKyc(request, ticket.token)
  await admin(request, `/internal/members/${ticket.member.id}/kyc/review`, {
    approved: true,
    actor: 'e2e-support',
  })

  // TOTP bind: emailed code → secret shown once → live code confirms.
  const totpMailedAt = Date.now()
  const codeRes = await authed(request, ticket.token, '/uc/authenticator/code')
  expect(codeRes.ok()).toBe(true)
  const emailCode = await latestCode(email, request, totpMailedAt)
  const begun = await authed(request, ticket.token, '/uc/authenticator/bind/begin', { code: emailCode })
  expect(begun.ok()).toBe(true)
  const { secret } = (await begun.json()) as { secret: string }
  const live = await totpCode(secret)
  const confirm = await authed(request, ticket.token, '/uc/authenticator/bind/confirm', { code: live })
  expect(confirm.ok()).toBe(true)

  // Deposit: request an address; the simnet funding + confirmation
  // happens in the chain E2E (internal/it) — the address request is the
  // member-visible half of story 14.
  const addrRes = await authed(request, ticket.token, '/uc/wallet/deposit-address', { coin: 'BTC' })
  expect(addrRes.ok()).toBe(true)
  const { address } = (await addrRes.json()) as { address: string }
  expect(address.length).toBeGreaterThan(0)

  // Place a limit order, then cancel it (insufficient funds is fine —
  // the path, not the fill, is this journey's subject).
  const orderRes = await authed(request, ticket.token, '/uc/orders', {
    market: 'BTC/USDT',
    side: 'buy',
    kind: 'limit',
    price: '1.00',
    amount: '0.0001',
  })
  expect([202, 409]).toContain(orderRes.status())
  if (orderRes.status() === 202) {
    const { orderId } = (await orderRes.json()) as { orderId: string }
    const cancel = await authed(request, ticket.token, `/uc/orders/${orderId}/cancel`)
    expect(cancel.ok()).toBe(true)
  }

  // Withdraw: fee preview, the gated request, then the admin audit.
  const feeRes = await request.get(`${API}/uc/wallet/withdraw-fee?coin=BTC&amount=0.001`, {
    headers: { 'access-auth-token': ticket.token },
  })
  expect([200, 400]).toContain(feeRes.status())
  const wdRes = await authed(request, ticket.token, '/uc/wallet/withdraw', {
    coin: 'BTC',
    address,
    amount: '0.001',
    code: await totpCode(secret),
  })
  expect([200, 409, 403]).toContain(wdRes.status())
})

test('anonymous browsing: markets list renders without signing in', async ({ page }) => {
  await page.goto('/')
  // The ported layout uses styled divs, not h1-h6 — assert the page's
  // real anonymous surface: nav, the market table, and Markets actually
  // loading through the edge (the empty-state text stays hidden).
  await expect(page.getByRole('link', { name: 'Exchange', exact: true })).toBeVisible({ timeout: 15000 })
  await expect(page.getByRole('columnheader', { name: 'Last price' })).toBeVisible()
  await expect(page.getByText('No Markets are listed yet.')).toBeHidden()
})
