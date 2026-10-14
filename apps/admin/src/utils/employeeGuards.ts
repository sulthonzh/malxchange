// Client-side mirrors of the Employee-management guards (ADR-0012, the
// server's sentences in internal/admin/manage.go): self-disable,
// self-role-removal, and the last-enabled-super-admin lockout. Each
// helper answers the refusal sentence to show, or null when the action
// may proceed. The server stays the authority — these only keep the UI
// out of states it cannot reach, and a refusal that slips through a
// stale list renders the api's own message.
import { SUPER_ADMIN_ROLE, type EmployeeRow } from './apiTypes'

/** True when `target` is the only enabled Employee in the super-admin Role. */
export function isSoleEnabledSuperAdmin(target: EmployeeRow, employees: EmployeeRow[]): boolean {
  if (!target.enabled || target.role !== SUPER_ADMIN_ROLE) {
    return false
  }
  const enabledSuperAdmins = employees.filter(
    (row) => row.role === SUPER_ADMIN_ROLE && row.enabled,
  )
  return enabledSuperAdmins.length <= 1
}

/** The refusal against disabling `target`, or null when it may proceed. */
export function disableRefusal(
  target: EmployeeRow,
  selfId: string,
  employees: EmployeeRow[],
): string | null {
  if (target.id === selfId) {
    return 'You cannot disable your own account.'
  }
  if (isSoleEnabledSuperAdmin(target, employees)) {
    return 'The last enabled super-admin cannot be disabled.'
  }
  return null
}

/** The refusal against moving `target` to `nextRoleName`, or null when it may proceed. */
export function roleChangeRefusal(
  target: EmployeeRow,
  selfId: string,
  nextRoleName: string,
  employees: EmployeeRow[],
): string | null {
  if (target.id === selfId) {
    return 'You cannot change your own Role.'
  }
  if (nextRoleName !== SUPER_ADMIN_ROLE && isSoleEnabledSuperAdmin(target, employees)) {
    return 'The last enabled super-admin cannot lose the Role.'
  }
  return null
}
