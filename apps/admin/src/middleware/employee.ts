// The Employee session gate (issue #56): every page except the login
// and forced-setup screens opts in via definePageMeta({ middleware:
// 'employee' }). It resolves the session once per navigation — /api/me
// over the httpOnly cookie (SSR forwards it server-to-server) — and
// applies utils/sessionGate's decision. There is no interceptor and no
// implicit redirect anywhere else: a 401 mid-session is an ordinary
// error the page renders.
export default defineNuxtRouteMiddleware(async (to) => {
  const { session } = useAdminSession()
  if (!session.value) {
    const { apiFetch } = useApi()
    try {
      session.value = toAdminSession(await apiFetch<LoginResponse>('/api/me'))
    } catch {
      // No cookie, an expired session, a disabled account: anonymous.
      session.value = null
    }
  }
  const target = sessionGate(session.value, to.path)
  if (target !== null) {
    return navigateTo(target)
  }
})
