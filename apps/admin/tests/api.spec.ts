import { afterEach, describe, expect, it, vi } from 'vitest'

import { ApiError, resolveAdminBase, useApi } from '../src/composables/useApi'

// The api client seam (issue #56): runtime-config origins (nothing
// baked in), the {"message"} error envelope the admin-api answers with,
// and the httpOnly-cookie session — the client sends no auth header at
// all; the mal_admin_session cookie rides the same-origin request.

const config = {
  adminApiBaseUrl: 'http://admin-api:8080',
  public: { adminApiBaseUrl: 'https://admin.exchange.example.com' },
}

describe('resolveAdminBase', () => {
  it('prefers the SSR origin server-side and falls back to the public one', () => {
    expect(resolveAdminBase(config, true)).toBe('http://admin-api:8080')
    expect(resolveAdminBase({ adminApiBaseUrl: '', public: config.public }, true)).toBe(
      'https://admin.exchange.example.com',
    )
  })

  it('uses the public origin client-side', () => {
    expect(resolveAdminBase(config, false)).toBe('https://admin.exchange.example.com')
  })

  it('stays empty when unconfigured, so requests fail loudly', () => {
    expect(resolveAdminBase({ adminApiBaseUrl: '', public: { adminApiBaseUrl: '' } }, false)).toBe('')
  })
})

describe('useApi apiFetch', () => {
  afterEach(() => {
    vi.unstubAllGlobals()
    vi.restoreAllMocks()
  })

  function stubClient(publicBase = 'https://edge.test') {
    vi.stubGlobal('useRuntimeConfig', () => ({ adminApiBaseUrl: '', public: { adminApiBaseUrl: publicBase } }))
  }

  it('throws the configuration sentence instead of calling a baked-in host', async () => {
    stubClient('')
    const fetchMock = vi.fn()
    vi.stubGlobal('fetch', fetchMock)
    const { apiFetch } = useApi()
    await expect(apiFetch('/api/me')).rejects.toThrow(
      'The admin api host is not configured. Set NUXT_PUBLIC_ADMIN_API_BASE_URL and restart the app.',
    )
    expect(fetchMock).not.toHaveBeenCalled()
  })

  it('posts JSON and returns the parsed body', async () => {
    stubClient()
    const fetchMock = vi.fn().mockResolvedValue(
      new Response(JSON.stringify({ status: 'signed out' }), { status: 200 }),
    )
    vi.stubGlobal('fetch', fetchMock)
    const { apiFetch } = useApi()
    await expect(apiFetch('/api/logout', { method: 'POST', body: { a: 1 } })).resolves.toEqual({
      status: 'signed out',
    })
    const [url, init] = fetchMock.mock.calls[0]!
    expect(url).toBe('https://edge.test/api/logout')
    expect(init.method).toBe('POST')
    expect(init.headers['Content-Type']).toBe('application/json')
    expect(init.body).toBe(JSON.stringify({ a: 1 }))
  })

  it('surfaces the {"message"} envelope as an ApiError with the status', async () => {
    stubClient()
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(
        new Response(JSON.stringify({ message: 'Email, password, or authenticator code is incorrect.' }), {
          status: 401,
        }),
      ),
    )
    const { apiFetch } = useApi()
    const error = await apiFetch('/api/login', { method: 'POST' }).catch((e: unknown) => e)
    expect(error).toBeInstanceOf(ApiError)
    expect((error as ApiError).status).toBe(401)
    expect((error as ApiError).message).toBe('Email, password, or authenticator code is incorrect.')
  })

  it('answers undefined for 204 and a sentence when the api is unreachable', async () => {
    stubClient()
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(null, { status: 204 })))
    const ok = useApi()
    await expect(ok.apiFetch('/api/me')).resolves.toBeUndefined()

    vi.stubGlobal('fetch', vi.fn().mockRejectedValue(new Error('network down')))
    const down = useApi()
    const error = await down.apiFetch('/api/me').catch((e: unknown) => e)
    expect(error).toBeInstanceOf(ApiError)
    expect((error as ApiError).status).toBe(0)
    expect((error as ApiError).message).toContain('could not be reached')
  })
})
