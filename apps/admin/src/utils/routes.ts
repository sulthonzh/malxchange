// The static route table (spec #25, issue #56) — the port of the legacy
// admin's findSlideArr (04_Web_Admin/src/router/router.js): navigation
// is data, never route-permission wiring in pages. Each entry is keyed
// by the one permission string the admin-api endpoint registry
// (internal/admin/registry.go) gates its surface on; the sidebar is
// this table intersected with the login response's live permission set
// (see visibleRoutes). The sidebar is presentation only — the server's
// permission middleware is the gate, so an unauthorized URL is refused
// server-side regardless of what the UI shows.
//
// Entries appear here as their slices land; permission strings come
// only from the registry (the catalog is read-only — no UI-invented
// strings). The Employee/Role management pages are slice #57's.

export interface AdminRoute {
  /** History-mode app route the entry navigates to. */
  path: string
  /** English-only sidebar label (ADR-0013). */
  title: string
  /** The registry permission string that gates the surface. */
  permission: string
}

export interface AdminSection {
  title: string
  routes: AdminRoute[]
}

// The System section exists today because its endpoints do (#54):
// Employee and Role management, super-admin's alone in the ADR-0012
// matrix. Members (#58), the Compliance KYC queue, the Code channels
// (#62), and the Config editors + engine status (#61) landed with
// their registry entries; Finance, Content, and Audit grow with
// #59-#63.
export const adminSections: AdminSection[] = [
  {
    title: 'Members',
    routes: [{ path: '/members', title: 'Members', permission: 'search:member' }],
  },
  {
    title: 'Compliance',
    routes: [{ path: '/kyc', title: 'KYC review', permission: 'view:kyc' }],
  },
  {
    title: 'System',
    routes: [
      { path: '/system/employees', title: 'Employees', permission: 'view:employee' },
      { path: '/system/roles', title: 'Roles', permission: 'view:role' },
    ],
  },
  {
    // Wallet Ops (issue #60): custody + operations over wallet's
    // internal RPCs — finance + super-admin surfaces.
    title: 'Operations',
    routes: [
      { path: '/operations', title: 'Wallet Ops', permission: 'view:ops' },
      { path: '/operations/transfers', title: 'Transfers', permission: 'view:hot-transfer' },
      { path: '/operations/incidents', title: 'Incidents', permission: 'view:incident' },
      { path: '/operations/reconcile', title: 'Reconcile', permission: 'view:reconcile' },
    ],
  },
  {
    // Config (#62): the Code channel registry — status list, activation
    // with per-driver credentials (Step-up on the save), deactivation.
    // Super-admin's alone in the matrix; email is always on and has no
    // entry here.
    title: 'Config',
    routes: [
      { path: '/config/coins', title: 'Coins', permission: 'view:coin' },
      { path: '/config/markets', title: 'Markets', permission: 'view:market' },
      // Code channels (#62): status list, activation with per-driver
      // credentials (Step-up on the save), deactivation. Super-admin's
      // alone; email is always on and has no entry here.
      { path: '/config/channels', title: 'Code channels', permission: 'view:channel' },
    ],
  },
  {
    title: 'Ops',
    routes: [
      { path: '/ops/engine', title: 'Engine status', permission: 'view:engine' },
    ],
  },
]

/** Sections whose entries the permission set admits; emptied sections drop. */
export function visibleRoutes(sections: AdminSection[], permissions: string[]): AdminSection[] {
  const granted = new Set(permissions)
  return sections
    .map((section) => ({
      ...section,
      routes: section.routes.filter((route) => granted.has(route.permission)),
    }))
    .filter((section) => section.routes.length > 0)
}
