// E2E helpers (issue #56): the admin-api client through the Caddy edge
// (same-origin with the browser), RFC 6238 TOTP for the mandatory code,
// and the super-admin credential store that makes the journey
// repeatable — the first Employee can only ever be bootstrapped once
// (the internal RPC refuses forever after), so the first run rotates
// the one-time credential in the browser and persists the rotated
// credentials here (gitignored); later runs sign in with them.
import type { Page } from '@playwright/test'
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
