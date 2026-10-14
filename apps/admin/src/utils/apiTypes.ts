// Wire shapes of the admin-api surfaces the management pages consume
// (internal/admin's Go structs are the source of truth — snake_case on
// the wire). Employee/Role rows are the redacted list views; audit rows
// carry the stored before/after JSON verbatim.

/** GET /api/employees entry (internal/admin/manage.go employeeListEntry). */
export interface EmployeeRow {
  id: string
  email: string
  role_id: string
  role: string
  enabled: boolean
  totp_bound: boolean
  must_rotate: boolean
  created_at: string
}

/** GET /api/roles entry (internal/admin/roles.go roleView). */
export interface RoleRow {
  id: string
  name: string
  /** Seeded Role: grants editable, never deletable. */
  system: boolean
  /** Held by at least one Employee — never deletable either. */
  in_use: boolean
  permissions: string[]
}

/** GET /api/audit-log row (internal/admin/auditsearch.go auditLogRow). */
export interface AuditLogRow {
  id: number
  actor: string
  action: string
  target: string
  before: unknown
  after: unknown
  origin_ip: string
  created_at: string
}

/** GET /api/audit-log envelope. */
export interface AuditSearchResponse {
  rows: AuditLogRow[]
  total: number
  page: number
  page_size: number
}

/** The one Role the ADR-0012 guards single out (internal/admin/rbac.go). */
export const SUPER_ADMIN_ROLE = 'super-admin'
