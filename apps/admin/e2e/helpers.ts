// E2E helpers (issue #56): the admin-api client through the Caddy edge
// (same-origin with the browser), RFC 6238 TOTP for the mandatory code,
// and the super-admin credential store that makes the journey
// repeatable — the first Employee can only ever be bootstrapped once
// (the internal RPC refuses forever after), so the first run rotates
// the one-time credential in the browser and persists the rotated
// credentials here (gitignored); later runs sign in with them.
import { createServer, type Server } from 'node:http'
import type { APIRequestContext, Page } from '@playwright/test'
import { readFileSync, writeFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'



export interface AdminState {
  email: string
  password: string
  totpSecret: string
}

const STATE_FILE = fileURLToPath(new URL('./.admin-state.json', import.meta.url))

export function readAdminState(): AdminState | null {
  try {
    return JSON.parse(readFileSync(STATE_FILE, 'utf8')) as AdminState
  } catch {
    return null
  }
}

export function writeAdminState(state: AdminState): void {
  writeFileSync(STATE_FILE, JSON.stringify(state, null, 2) + '\n')
}
/** The one-time password `make admin-e2e` mints and persists (gitignored):
 * env first, then the file, so reruns and direct `npx playwright test`
 * invocations both work after a fresh bootstrap. */
export function bootstrapPassword(): string {
  if (process.env.E2E_BOOTSTRAP_PASSWORD) {
    return process.env.E2E_BOOTSTRAP_PASSWORD
  }
  try {
    return readFileSync(fileURLToPath(new URL('./.bootstrap-password', import.meta.url)), 'utf8').trim()
  } catch {
    return ''
  }
}

/** Seats the page on the admin origin before any pageApi call: the
 * in-page fetch is same-origin, so the page must hold the admin
 * hostname for fetch() to resolve and for cookies to apply. New pages
 * start at about:blank — every journey begins with this call. */
export async function seatPage(page: Page): Promise<void> {
  await page.goto('/')
}
export interface ApiAnswer {
  status: number
  body: unknown
}

/** Calls the admin-api FROM INSIDE the browser page: the fetch is
 * same-origin through the Caddy edge, so Chromium's host mapping for
 * the admin hostname and the session cookie both apply (request
 * contexts run Node-side and resolve DNS for real). The page must
 * already sit on the admin origin. */
export async function pageApi(
  page: Page,
  path: string,
  method: 'GET' | 'POST' = 'GET',
  body?: unknown,
): Promise<ApiAnswer> {
  return page.evaluate(
    async ({ path, method, body }) => {
      const response = await fetch(path, {
        method,
        headers: body === undefined ? {} : { 'Content-Type': 'application/json' },
        body: body === undefined ? undefined : JSON.stringify(body),
        credentials: 'same-origin',
      })
      const text = await response.text()
      try {
        return { status: response.status, body: JSON.parse(text) as unknown }
      } catch {
        return { status: response.status, body: text }
      }
    },
    { path, method, body },
  )
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

/** RFC 6238 TOTP (SHA-1, 30s step, 6 digits) — the mandatory login code. */
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

// ---- issue #64: the full-stack black-box proof helpers ----


/** The member-side seam (spec #24 stack): Node-side against the test
 * profile's published ports, because request contexts resolve DNS for
 * real and cannot use the Chromium host mapping pageApi relies on.
 * MEMBER_API already carries the /api prefix — memberCall paths are
 * the FULL public paths (/api/uc/...), exactly as the browser serves
 * them through the edge. */
export const MEMBER_API = process.env.E2E_API_URL ?? 'http://127.0.0.1:18080/api'
export const MAILSINK = process.env.E2E_MAIL_URL ?? 'http://127.0.0.1:18025'

export interface MemberTicket {
  token: string
  member: { id: string }
}

/** Registers one member through the real member surface (emailed code
 * from the compose mail sink), returning the session ticket. */
export async function registerMember(
  request: APIRequestContext,
  email: string,
  password: string,
): Promise<MemberTicket> {
  await request.post(`${MEMBER_API}/uc/register/code`, { data: { email } })
  const code = await mailedCode(email, request)
  const res = await request.post(`${MEMBER_API}/uc/register`, { data: { email, code, password } })
  if (!res.ok()) {
    throw new Error(`member register ${res.status()}: ${await res.text()}`)
  }
  return (await res.json()) as MemberTicket
}

/** One authenticated member call over HTTP. */
export async function memberCall(
  request: APIRequestContext,
  token: string,
  path: string,
  body?: unknown,
  method: 'GET' | 'POST' | 'PUT' = body === undefined ? 'GET' : 'POST',
) {
  return request.fetch(`${MEMBER_API}${path}`, {
    method,
    data: body ?? {},
    headers: { 'access-auth-token': token },
  })
}

/** The newest 6-digit code mailed to an address after `notBefore`
 * (ms epoch; mailpit answers newest-first). */
export async function mailedCode(
  email: string,
  request: APIRequestContext,
  notBefore = 0,
): Promise<string> {
  const deadline = Date.now() + 15000
  while (Date.now() < deadline) {
    const res = await request.get(`${MAILSINK}/api/v1/search?query=to:${encodeURIComponent(email)}`)
    if (res.ok()) {
      const body = (await res.json()) as { messages?: Array<{ ID: string; Created: string }> }
      for (const message of body.messages ?? []) {
        if (Date.parse(message.Created) < notBefore - 1000) {
          break // newest-first: everything older is irrelevant
        }
        const fetched = await request.get(`${MAILSINK}/api/v1/message/${message.ID}`)
        if (!fetched.ok()) {
          continue
        }
        const messageBody = (await fetched.json()) as { Text?: string }
        const match = (messageBody.Text ?? '').match(/\b(\d{6})\b/)
        if (match) {
          return match[1]!
        }
      }
    }
    await new Promise((resolve) => setTimeout(resolve, 500))
  }
  throw new Error(`no code mailed to ${email}`)
}

/** Submits one KYC application for a member (base64 images, as the
 * member app does). */
export async function submitMemberKyc(request: APIRequestContext, token: string): Promise<void> {
  const image = Buffer.from('e2e-document-bytes').toString('base64')
  const res = await memberCall(request, token, '/api/uc/kyc', {
    fullName: 'E2e Member',
    documentNumber: `E2E-${Date.now()}`,
    frontImage: image,
    backImage: image,
  })
  if (!res.ok()) {
    throw new Error(`member kyc ${res.status()}: ${await res.text()}`)
  }
}

/** Signs in over HTTP inside the browser page (same-origin through
 * the Caddy edge, cookie jar included). */
export async function loginHttp(
  page: Page,
  email: string,
  password: string,
  code = '',
): Promise<ApiAnswer> {
  return pageApi(page, '/api/login', 'POST', { email, password, code })
}

export interface StaffAccount {
  email: string
  password: string
  totpSecret: string
  role: string
}

function suffix(): string {
  return Math.random().toString(16).slice(2, 10)
}

/** A per-run unique staff email (audit rows filter on the actor, so
 * uniqueness makes every assertion exact across reruns). */
export function uniqueEmail(prefix: string): string {
  return `${prefix}-${suffix()}@exchange.example.com`
}

interface RoleListEntry {
  id: string
  name: string
}

function isRoleListEntry(value: unknown): value is RoleListEntry {
  return typeof value === 'object' && value !== null && 'id' in value && 'name' in value
}

/** The Role id of one seeded Role, over the served surface. */
export async function roleIdOf(page: Page, name: string): Promise<string> {
  const answer = await pageApi(page, '/api/roles')
  if (answer.status !== 200) {
    throw new Error(`roles ${answer.status}: ${JSON.stringify(answer.body)}`)
  }
  const roles = (Array.isArray(answer.body) ? answer.body : []).filter(isRoleListEntry)
  const role = roles.find((candidate) => candidate.name === name)
  if (!role) {
    throw new Error(`Role ${name} is not seeded`)
  }
  return role.id
}

interface EmployeeListEntry {
  id: string
  email: string
}

function isEmployeeListEntry(value: unknown): value is EmployeeListEntry {
  return typeof value === 'object' && value !== null && 'id' in value && 'email' in value
}

/** The Employee id of one email, over the served surface. */
export async function employeeIdOf(page: Page, email: string): Promise<string> {
  const answer = await pageApi(page, '/api/employees')
  if (answer.status !== 200) {
    throw new Error(`employees ${answer.status}: ${JSON.stringify(answer.body)}`)
  }
  const rows = (Array.isArray(answer.body) ? answer.body : []).filter(isEmployeeListEntry)
  const row = rows.find((candidate) => candidate.email === email)
  if (!row) {
    throw new Error(`Employee ${email} not found`)
  }
  return row.id
}

/** Creates one Employee as the super-admin (Employee create is itself
 * Step-up — the root's fresh code), then walks the forced first-login
 * setup over HTTP: rotate the one-time password, enroll TOTP, confirm.
 * The page ends signed in as the new Employee's normal session. */
export async function provisionEmployee(
  page: Page,
  root: { email: string; password: string; totpSecret: string },
  role: string,
): Promise<StaffAccount> {
  const login = await loginHttp(page, root.email, root.password, await totpCode(root.totpSecret))
  if (login.status !== 200) {
    throw new Error(`root login ${login.status}: ${JSON.stringify(login.body)}`)
  }
  const roleID = await roleIdOf(page, role)
  const email = uniqueEmail(`e2e-${role}`)
  const oneTime = `E2e-one-time-${suffix()}`
  const created = await pageApi(page, '/api/employees', 'POST', {
    email,
    password: oneTime,
    role_id: roleID,
    code: await totpCode(root.totpSecret),
  })
  if (created.status !== 200) {
    throw new Error(`employee create ${created.status}: ${JSON.stringify(created.body)}`)
  }
  // Back to the caller: the grant searches below run as whoever the
  // caller signs in next. (Cookie jar holds one session at a time.)
  await pageApi(page, '/api/logout', 'POST')

  // Forced setup: password-only login (the one-time credential carries
  // no TOTP yet), rotation, re-login, enrollment, confirmation.
  const first = await loginHttp(page, email, oneTime)
  if (first.status !== 200) {
    throw new Error(`first login ${first.status}: ${JSON.stringify(first.body)}`)
  }
  const password = `E2e-staff-${suffix()}`
  const rotated = await pageApi(page, '/api/password', 'POST', {
    current_password: oneTime,
    new_password: password,
  })
  if (rotated.status !== 200) {
    throw new Error(`rotation ${rotated.status}: ${JSON.stringify(rotated.body)}`)
  }
  const second = await loginHttp(page, email, password)
  if (second.status !== 200) {
    throw new Error(`second login ${second.status}: ${JSON.stringify(second.body)}`)
  }
  const enrolled = await pageApi(page, '/api/totp/enroll', 'POST')
  if (enrolled.status !== 200) {
    throw new Error(`enroll ${enrolled.status}: ${JSON.stringify(enrolled.body)}`)
  }
  const secret = enrolled.body !== null &&
    typeof enrolled.body === 'object' &&
    'secret' in enrolled.body &&
    typeof enrolled.body.secret === 'string'
    ? enrolled.body.secret
    : ''
  if (!secret) {
    throw new Error('enrollment served no secret')
  }
  const confirmed = await pageApi(page, '/api/totp/confirm', 'POST', {
    code: await totpCode(secret),
  })
  if (confirmed.status !== 200) {
    throw new Error(`confirm ${confirmed.status}: ${JSON.stringify(confirmed.body)}`)
  }
  return { email, password, totpSecret: secret, role }
}

export interface AuditRow {
  id: number
  actor: string
  action: string
  target: string
  before: unknown
  after: unknown
  origin_ip: string
  created_at: string
}

/** Searches the served audit log — the only read side of the
 * middleware-written trail the black-box proof may assert on. */
export async function auditRows(
  page: Page,
  filter: { actor?: string; action?: string; target?: string },
): Promise<AuditRow[]> {
  const params = new URLSearchParams()
  for (const [key, value] of Object.entries(filter)) {
    if (value !== undefined) {
      params.set(key, value)
    }
  }
  const answer = await pageApi(page, `/api/audit-log?${params.toString()}`)
  if (answer.status !== 200) {
    throw new Error(`audit search ${answer.status}: ${JSON.stringify(answer.body)}`)
  }
  if (answer.body === null || typeof answer.body !== 'object' || !('rows' in answer.body)) {
    throw new Error(`audit search served no rows: ${JSON.stringify(answer.body)}`)
  }
  const rows = answer.body.rows
  return Array.isArray(rows) ? (rows as AuditRow[]) : []
}

/** One recorded delivery of the host-side SMS sink: the generic HTTP
 * SMS endpoint the api's sms driver POSTs {"to","message"} to. */
export interface SmsDelivery {
  authorization: string
  to: string
  message: string
}

export interface SmsSink {
  port: number
  deliveries: SmsDelivery[]
  close: () => Promise<void>
}

/** Starts the SMS sink on the host: the api container reaches it at
 * http://host.docker.internal:<port>/sms (Docker Desktop routes the
 * name to the host loopback), and the channel credentials point the
 * sms driver at exactly that URL. */
export function startSmsSink(): Promise<SmsSink> {
  const deliveries: SmsDelivery[] = []
  const server: Server = createServer((req, res) => {
    const chunks: Buffer[] = []
    req.on('data', (chunk: Buffer) => chunks.push(chunk))
    req.on('end', () => {
      if (req.url === '/sms' && req.method === 'POST') {
        try {
          const raw = JSON.parse(Buffer.concat(chunks).toString('utf8')) as {
            to?: unknown
            message?: unknown
          }
          deliveries.push({
            authorization: req.headers.authorization ?? '',
            to: typeof raw.to === 'string' ? raw.to : '',
            message: typeof raw.message === 'string' ? raw.message : '',
          })
        } catch {
          deliveries.push({ authorization: req.headers.authorization ?? '', to: '', message: '' })
        }
      }
      res.writeHead(204)
      res.end()
    })
  })
  const { promise, resolve } = Promise.withResolvers<void>()
  server.listen(0, '127.0.0.1', () => resolve())
  return promise.then(() => {
    const address = server.address()
    if (address === null || typeof address === 'string') {
      throw new Error('sms sink did not bind')
    }
    const { promise: closed, resolve: done } = Promise.withResolvers<void>()
    return {
      port: address.port,
      deliveries,
      close: () => {
        server.close(() => done())
        return closed
      },
    }
  })
}
