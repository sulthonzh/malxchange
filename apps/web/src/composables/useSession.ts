export interface MemberSummary {
  email: string
}

// The Member session (ADR-0006): an opaque per-device session token
// carried by the access-auth-token header. The token lives in a cookie so
// the server render and the client agree on the session state, replacing
// the legacy localStorage TOKEN/MEMBER pair. The legacy interceptor's
// implicit redirect on auth failure is dead — session-gated navigation
// happens only through the explicit `member` route middleware.
const TOKEN_COOKIE = 'access-auth-token'

export function useSession() {
  const token = useCookie(TOKEN_COOKIE, {
    // The api slides the 7-day window on use (ADR-0006); the cookie's
    // maxAge only stops the browser from outliving the session.
    maxAge: 60 * 60 * 24 * 7,
    sameSite: 'lax',
    path: '/',
  })
  const member = useState<MemberSummary | null>('session:member', () => null)

  const loggedIn = computed(() => typeof token.value === 'string' && token.value !== '')

  // The login ticket calls this with the session token and the member
  // summary the api returns.
  function setSession(value: string, summary: MemberSummary): void {
    token.value = value
    member.value = summary
  }

  // Seam: once the api's logout endpoint exists (login ticket), call it
  // here before clearing the client state; clearing first keeps the
  // browser signed out even if that call fails.
  function clearSession(): void {
    token.value = null
    member.value = null
  }

  return { token, member, loggedIn, setSession, clearSession }
}
