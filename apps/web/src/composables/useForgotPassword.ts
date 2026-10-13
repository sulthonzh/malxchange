// Forgot-password seam (ticket #47 page ↔ ticket #39 api).
//
// The page never talks to useApi directly for this flow: every request
// goes through this composable so the reset purpose the api adds in #39
// has exactly one place to land. Paths are relative to the api base,
// which already carries the /api prefix (see .env.example). The routes
// the web app expects are:
//
//   POST /uc/password/forgot/code   {email}
//   POST /uc/password/forgot/reset  {email, code, newPassword}
export function useForgotPassword() {
  const { apiFetch } = useApi()

  async function requestCode(email: string): Promise<void> {
    await apiFetch('/uc/password/forgot/code', { method: 'POST', body: { email } })
  }

  // A successful reset revokes every active session server-side
  // (ADR-0006 sensitive-flow matrix); the page shows that notice.
  async function resetPassword(email: string, code: string, password: string): Promise<void> {
    await apiFetch('/uc/password/forgot/reset', {
      method: 'POST',
      body: { email, code, newPassword: password },
    })
  }

  return { requestCode, resetPassword }
}
