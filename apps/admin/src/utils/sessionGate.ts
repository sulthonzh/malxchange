import type { AdminSession } from '../composables/useAdminSession'

// The routing half of the Employee session gate (issue #56), applied by
// the `employee` route middleware: null = the path is allowed, a string
// = the redirect target. Mirrors the server's restricted-session rule
// (internal/admin/server.go withSession): until the one-time credential
// is rotated past AND two-factor is bound, an Employee reaches nothing
// but the forced-setup screens — password first, then TOTP enrollment
// (ADR-0012). The server gate stays the authority; this only keeps the
// UI out of states it cannot use.
export function sessionGate(session: AdminSession | null, path: string): string | null {
  if (session === null) {
    return path === '/login' ? null : '/login'
  }
  if (session.mustRotate || !session.totpBound) {
    // Unprovisioned: everything funnels into /setup, in order.
    if (session.mustRotate) {
      return path === '/setup/password' ? null : '/setup/password'
    }
    return path === '/setup/totp' ? null : '/setup/totp'
  }
  // Provisioned: the auth screens are behind them.
  if (path === '/login' || path.startsWith('/setup')) {
    return '/'
  }
  return null
}

/** Where the login page lands a freshly authenticated Employee. */
export function loginTarget(session: AdminSession): string {
  if (session.mustRotate) {
    return '/setup/password'
  }
  return session.totpBound ? '/' : '/setup/totp'
}
