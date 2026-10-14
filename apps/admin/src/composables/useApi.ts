// Shared admin-api client (issue #56), apps/web's useApi pattern with
// one deliberate difference: the Employee session is the admin-api's
// httpOnly mal_admin_session cookie (ADR-0012), so this client carries
// no auth header and never sees the token. Browser requests are
// same-origin through the Caddy edge (the cookie rides along); on SSR
// the incoming request's cookie header is forwarded server-to-server —
// SSR runs inside the compose network where the browser's origin is not
// reachable. There is no interceptor and no implicit redirect: a 401 is
// an ordinary error the caller renders, and session-gated navigation is
// the `employee` route middleware's job.
export class ApiError extends Error {
  constructor(
    message: string,
    readonly status: number,
    options?: { cause?: unknown },
  ) {
    super(message, options)
    this.name = 'ApiError'
  }
}

interface FetchOptions {
  method?: 'GET' | 'POST' | 'PUT' | 'DELETE'
  body?: unknown
}

interface RuntimeConfigShape {
  adminApiBaseUrl?: string
  public: { adminApiBaseUrl?: string }
}

/** Origins are runtime config; the /api/* prefix lives in the paths. */
export function resolveAdminBase(config: RuntimeConfigShape, serverSide: boolean): string {
  if (serverSide) {
    return config.adminApiBaseUrl || config.public.adminApiBaseUrl || ''
  }
  return config.public.adminApiBaseUrl || ''
}

export function useApi() {
  const config = useRuntimeConfig()
  const serverSide = import.meta.server
  const apiBaseUrl = resolveAdminBase(config, serverSide)
  // Forward the browser's cookies on SSR — useRequestHeaders is a no-op
  // outside a request scope, so the client side simply omits it (the
  // browser attaches cookies itself).
  const forwarded = serverSide ? useRequestHeaders(['cookie']) : {}

  async function apiFetch<T>(path: string, options: FetchOptions = {}): Promise<T> {
    if (apiBaseUrl === '') {
      throw new ApiError(
        serverSide
          ? 'The admin api host is not configured. Set NUXT_ADMIN_API_BASE_URL and restart the app.'
          : 'The admin api host is not configured. Set NUXT_PUBLIC_ADMIN_API_BASE_URL and restart the app.',
        0,
      )
    }
    let response: Response
    try {
      response = await fetch(`${apiBaseUrl}${path}`, {
        method: options.method ?? 'GET',
        headers: { ...forwarded, 'Content-Type': 'application/json' },
        body: options.body === undefined ? undefined : JSON.stringify(options.body),
      })
    } catch (cause) {
      throw new ApiError(`The admin api at ${apiBaseUrl} could not be reached.`, 0, { cause })
    }
    if (!response.ok) {
      throw new ApiError(await errorMessage(response), response.status)
    }
    if (response.status === 204) {
      return undefined as T
    }
    return (await response.json()) as T
  }

  return { apiFetch, apiBaseUrl }
}

async function errorMessage(response: Response): Promise<string> {
  try {
    const body: unknown = await response.json()
    if (typeof body === 'object' && body !== null && 'message' in body) {
      const message = body.message
      if (typeof message === 'string' && message !== '') {
        return message
      }
    }
  } catch {
    // Not a JSON body — fall through to the status line.
  }
  return `The admin api answered with status ${response.status}.`
}
