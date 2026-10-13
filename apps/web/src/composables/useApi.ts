// Shared api client: every request goes through here. The host comes
// from runtime env config (no hardcoded hosts — spec #24 story 33), and
// the access-auth-token header rides along whenever a session exists
// (ADR-0006). There is no interceptor and no implicit redirect: a 401 is
// an ordinary error the caller renders, and session-gated navigation is
// the `member` route middleware's job.
export class ApiError extends Error {
  constructor(
    message: string,
    readonly status: number,
  ) {
    super(message)
    this.name = 'ApiError'
  }
}

interface FetchOptions {
  method?: 'GET' | 'POST' | 'PUT' | 'DELETE'
  body?: unknown
}

export function useApi() {
  const config = useRuntimeConfig()
  const { token } = useSession()
  const apiBaseUrl = config.public.apiBaseUrl

  async function apiFetch<T>(path: string, options: FetchOptions = {}): Promise<T> {
    if (typeof apiBaseUrl !== 'string' || apiBaseUrl === '') {
      throw new ApiError(
        'The api host is not configured. Set NUXT_PUBLIC_API_BASE_URL and restart the app.',
        0,
      )
    }
    const headers: Record<string, string> = { 'Content-Type': 'application/json' }
    if (token.value) {
      headers['access-auth-token'] = token.value
    }
    let response: Response
    try {
      response = await fetch(`${apiBaseUrl}${path}`, {
        method: options.method ?? 'GET',
        headers,
        body: options.body === undefined ? undefined : JSON.stringify(options.body),
      })
    } catch (cause) {
      throw new ApiError(
        `The api at ${apiBaseUrl} could not be reached.`,
        0,
        { cause },
      )
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
  return `The api answered with status ${response.status}.`
}
