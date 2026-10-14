// The deferred channel proof (ticket #62, discharged by #64): a
// newly activated Code channel actually delivers — a member requests
// a phone Verification code, the sms driver POSTs it to the endpoint
// the admin configured (with the saved API key as the bearer), and
// the member completes the phone bind with the code the channel
// delivered. The credential save itself is the Step-up positive: a
// fresh code at action time. Credentials never render back on any
// response, and deactivation clears them. Runs over the real stack:
// the api container reaches the host-side sink through
// host.docker.internal.
import { expect, test } from '@playwright/test'
import type { Page } from '@playwright/test'

import {
  auditRows,
  loginHttp,
  memberCall,
  pageApi,
  readAdminState,
  registerMember,
  startSmsSink,
  totpCode,
  seatPage,
} from './helpers'

interface ChannelState {
  channel: string
  active: boolean
}

function isChannelState(value: unknown): value is ChannelState {
  if (typeof value !== 'object' || value === null || !('channel' in value) || !('active' in value)) {
    return false
  }
  return typeof value.channel === 'string' && typeof value.active === 'boolean'
}

/** Whether the sms channel reads active right now. */
async function smsIsActive(page: Page): Promise<boolean> {
  const answer = await pageApi(page, '/api/channels')
  expect(answer.status).toBe(200)
  const states = (Array.isArray(answer.body) ? answer.body : []).filter(isChannelState)
  const sms = states.find((state) => state.channel === 'sms')
  return sms?.active === true
}

test('a newly activated channel delivers a member Verification code end to end', async ({
  page,
  request,
}) => {
  const root = readAdminState()
  test.skip(!root?.totpSecret, 'needs the provisioned super-admin from the first journey')
  await seatPage(page)

  const sink = await startSmsSink()
  const apiKey = `e2e-key-${Date.now()}`
  const credentials = { endpoint: `http://host.docker.internal:${sink.port}/sms`, apiKey }

  await loginHttp(page, root!.email, root!.password, await totpCode(root!.totpSecret))

  // A previous run may have left sms active; deactivate first so the
  // journey starts from the dormant state.
  if (await smsIsActive(page)) {
    const off = await pageApi(page, '/api/channels/sms/deactivate', 'POST')
    expect(off.status).toBe(204)
  }

  // The credential save demands Step-up: without a fresh code the
  // activation is refused before the api is ever called.
  const refused = await pageApi(page, '/api/channels/sms/activate', 'POST', {
    credentials,
    code: '',
  })
  expect(refused.status).toBe(401)
  expect(refused.body).toEqual({
    message: 'A fresh authenticator code is required for this action.',
  })

  const activated = await pageApi(page, '/api/channels/sms/activate', 'POST', {
    credentials,
    code: await totpCode(root!.totpSecret),
  })
  expect(activated.status, JSON.stringify(activated.body)).toBe(204)
  expect(await smsIsActive(page)).toBe(true)

  // The status list never serves the credentials — on any response.
  const after = await pageApi(page, '/api/channels')
  const raw = JSON.stringify(after.body)
  expect(raw.includes(apiKey) || raw.includes('apiKey') || raw.includes('endpoint')).toBe(false)

  const activateAudit = await auditRows(page, { actor: root!.email, action: 'activate:channel' })
  expect(activateAudit.some((row) => row.target === 'sms')).toBe(true)
  await pageApi(page, '/api/logout', 'POST')

  // The member leg: request a phone code, receive it on the channel
  // the admin activated, and bind the phone with the delivered code.
  const memberEmail = `e2e-sms-${Date.now()}@example.com`
  const memberPassword = 'a-fine-e2e-password'
  const ticket = await registerMember(request, memberEmail, memberPassword)
  const phone = `+1555${String(Date.now()).slice(-7)}`

  const sent = await memberCall(request, ticket.token, '/api/uc/phone/code', { phone })
  expect(sent.status(), await sent.text()).toBe(200)
  expect(await sent.json()).toEqual({ status: 'code sent' })

  // The sink received the delivery the credentials describe: the
  // member's phone, the bearer the admin saved, and the code itself.
  const deadline = Date.now() + 10000
  while (sink.deliveries.length === 0 && Date.now() < deadline) {
    await new Promise((resolve) => setTimeout(resolve, 250))
  }
  expect(sink.deliveries, 'the sms driver delivered through the configured endpoint').toHaveLength(1)
  const delivery = sink.deliveries[0]!
  expect(delivery.authorization).toBe(`Bearer ${apiKey}`)
  expect(delivery.to).toBe(phone)
  const codeMatch = delivery.message.match(/\b(\d{6})\b/)
  expect(codeMatch, 'the delivered message carries the Verification code').toBeTruthy()

  // The code the channel delivered completes the bind — proof the
  // receipt is real, not just an outbound POST.
  const bound = await memberCall(request, ticket.token, '/api/uc/phone/bind', {
    phone,
    password: memberPassword,
    code: codeMatch![1],
  })
  expect(bound.status(), await bound.text()).toBe(200)
  expect(await bound.json()).toEqual({ status: 'phone bound', phone })

  // Deactivation flips the channel off (audited, no Step-up — no
  // credential moves) and closes the journey's footprint.
  await loginHttp(page, root!.email, root!.password, await totpCode(root!.totpSecret))
  const deactivated = await pageApi(page, '/api/channels/sms/deactivate', 'POST')
  expect(deactivated.status).toBe(204)
  expect(await smsIsActive(page)).toBe(false)
  const deactivateAudit = await auditRows(page, { actor: root!.email, action: 'deactivate:channel' })
  expect(deactivateAudit.some((row) => row.target === 'sms')).toBe(true)

  await sink.close()
})
