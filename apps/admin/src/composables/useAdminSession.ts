// The Employee session view the admin-api returns from POST /api/login
// and GET /api/me (internal/admin/login.go loginResponse): identity,
// forced-setup state, and the live permission set the sidebar derives
// from. The session token itself never appears here — it lives in the
// httpOnly mal_admin_session cookie the admin-api owns (ADR-0012); the
// app only mirrors what the api answers. Callers assign `session.value`
// directly (login fills it from the login body; sign-out nulls it after
// POST /api/logout) — there is no wrapper to keep in sync.
export interface AdminEmployee {
  id: string
  email: string
  role: string
}

export interface AdminSession {
  employee: AdminEmployee
  mustRotate: boolean
  totpBound: boolean
  /** The caller's live grant set — the sidebar's source of truth. */
  permissions: string[]
}

/** The wire shape of POST /api/login and GET /api/me (snake_case JSON). */
export interface LoginResponse {
  employee: AdminEmployee
  must_rotate: boolean
  totp_bound: boolean
  permissions: string[]
}

export function toAdminSession(response: LoginResponse): AdminSession {
  return {
    employee: response.employee,
    mustRotate: response.must_rotate,
    totpBound: response.totp_bound,
    permissions: response.permissions ?? [],
  }
}

export function useAdminSession() {
  const session = useState<AdminSession | null>('admin:session', () => null)
  const loggedIn = computed(() => session.value !== null)
  return { session, loggedIn }
}
