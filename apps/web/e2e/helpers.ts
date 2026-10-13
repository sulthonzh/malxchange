// E2E helpers (issue #51): the api client for admin steps, the mail
// sink for Verification codes, RFC 6238 TOTP for the withdrawal gate,
// and the session token threading every member call carries.
import { expect, request as baseRequest, type APIRequestContext } from '@playwright/test'
import type { Page } from '@playwright/test'

// Defaults are the compose test profile's published bindings.
export const API = process.env.E2E_API_URL ?? 'http://127.0.0.1:18080/api'
export const MAILSINK = process.env.E2E_MAIL_URL ?? 'http://127.0.0.1:18025'

export interface AuthTicket {
  token: string
  member: { id: string; email: string }
}

/** Register (via the emailed code) and log in, returning token + id. */
export async function register(request: APIRequestContext, email: string, password: string): Promise<AuthTicket> {
  await request.post(`${API}/uc/register/code`, { data: { email } })
  const code = await latestCode(email)
  const res = await request.post(`${API}/uc/register`, { data: { email, code, password } })
  if (!res.ok()) {
    throw new Error(`register ${res.status()}: ${await res.text()}`)
  }
  return (await res.json()) as AuthTicket
}

export async function authed(
  request: APIRequestContext,
  token: string,
  path: string,
  body?: unknown,
  method: 'POST' | 'PUT' = 'POST',
) {
  return request.fetch(`${API}${path}`, {
    method,
    data: body ?? {},
    headers: { 'access-auth-token': token },
  })
}

/** The newest 6-digit code mailed to an address after `notBefore` (ms
 *  epoch; mailpit returns newest-first). Waiting for a *new* code needs
 *  the cutoff — the newest message so far may be an older mail whose
 *  code was already consumed. */
export async function latestCode(email: string, request?: APIRequestContext, notBefore = 0): Promise<string> {
  const sink = request ?? (await baseRequest.newContext())
  const deadline = Date.now() + 15000
  while (Date.now() < deadline) {
    const res = await sink.get(`${MAILSINK}/api/v1/search?query=to:${encodeURIComponent(email)}`)
    if (res.ok()) {
      const body = (await res.json()) as { messages?: Array<{ ID: string; Created: string }> }
      for (const message of body.messages ?? []) {
        if (Date.parse(message.Created) < notBefore - 1000) {
          break // newest-first: everything older is irrelevant
        }
        const fetched = await sink.get(`${MAILSINK}/api/v1/message/${message.ID}`)
        if (!fetched.ok()) {
          continue
        }
        const messageBody = (await fetched.json()) as { Text?: string }
        const match = (messageBody.Text ?? '').match(/\b(\d{6})\b/)
        if (match) {
          return match[1]
        }
      }
    }
    await sleep(500)
  }
  throw new Error(`no code mailed to ${email}`)
}

function sleep(ms: number): Promise<void> {
  const { promise, resolve } = Promise.withResolvers<void>()
  setTimeout(resolve, ms)
  return promise
}

const BASE32 = 'ABCDEFGHIJKLMNOPQRSTUVWXYZ234567'

function base32Decode(secret: string): Uint8Array {
  const bits: number[] = []
  for (const char of secret.toUpperCase()) {
    const value = BASE32.indexOf(char)
    if (value < 0) {
      continue
    }
    for (let shift = 4; shift >= 0; shift -= 1) {
      bits.push((value >> shift) & 1)
    }
  }
  const bytes = new Uint8Array(Math.floor(bits.length / 8))
  bits.forEach((bit, index) => {
    if (bit === 1 && Math.floor(index / 8) < bytes.length) {
      bytes[Math.floor(index / 8)] |= 128 >> index % 8
    }
  })
  return bytes
}
/** RFC 6238 TOTP (SHA-1, 30s step, 6 digits) for the live-code steps. */
export async function totpCode(secret: string, at = Date.now()): Promise<string> {
  const key = Uint8Array.from(base32Decode(secret)).buffer as ArrayBuffer
  const counter = Math.floor(at / 30000)
  const message = new ArrayBuffer(8)
  new DataView(message).setBigUint64(0, BigInt(counter), false)
  const cryptoKey = await crypto.subtle.importKey('raw', key, { name: 'HMAC', hash: 'SHA-1' }, false, [
    'sign',
  ])
  const signature = new Uint8Array(await crypto.subtle.sign('HMAC', cryptoKey, message))
  const offset = signature[signature.length - 1]! & 0x0f
  const value =
    ((signature[offset]! & 0x7f) << 24) |
    ((signature[offset + 1]! & 0xff) << 16) |
    ((signature[offset + 2]! & 0xff) << 8) |
    (signature[offset + 3]! & 0xff)
  return String(value % 1000000).padStart(6, '0')
}

/** KYC submit for a member (base64 document images, no format checks). */
export async function submitKyc(request: APIRequestContext, token: string): Promise<void> {
  const image = Buffer.from('fake-document-bytes').toString('base64')
  const res = await authed(request, token, '/uc/kyc', {
    fullName: 'E2e Member',
    documentNumber: 'E2E-1',
    frontImage: image,
    backImage: image,
  })
  expect(res.ok()).toBe(true)
}

/** Internal admin RPCs (admin-api owns the UI in #25; E2E calls the seams). */
export async function admin(request: APIRequestContext, path: string, body?: unknown) {
  const base = (API.replace(/\/api$/, '') + ':18080').replace(/:8080:18080/, ':18080')
  const res = await request.post(`${process.env.E2E_ADMIN_URL ?? 'http://127.0.0.1:18080'}${path}`, { data: body ?? {} })
  expect(res.ok()).toBe(true)
  return res.json()
}

export async function login(page: Page, email: string, password: string): Promise<void> {
  await page.goto('/login')
  await page.getByPlaceholder('Email address').fill(email)
  await page.getByPlaceholder('Password', { exact: true }).fill(password)
  await page.getByRole('button', { name: 'Log in' }).click()
  await page.waitForURL(/\/exchange(\/|$)/, { timeout: 15000 })
}
