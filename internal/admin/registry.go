// The endpoint registry is the single seam the admin surface grows
// through: every permissioned admin endpoint appears here exactly once
// with the one permission string its handler is gated on and the
// seeded Roles the ADR-0012 matrix grants it to. Boot seeds the
// permission catalog idempotently from this list — a string exists
// because a handler checks it; there is no other source, and the
// catalog is read-only everywhere else. Later tickets (#57-#63) add
// surfaces by appending entries; nothing else moves.
package admin

import "net/http"

// Permission strings are 'verb:resource' (ADR-0012) — verb first,
// resource singular, the same vocabulary the audit actions use (see
// audit.go's const block). Exactly one endpoint checks each string.
const (
	PermViewEmployee          = "view:employee"           // GET    /api/employees
	PermCreateEmployee        = "create:employee"         // POST   /api/employees (Step-up)
	PermDisableEmployee       = "disable:employee"        // POST   /api/employees/{id}/disable
	PermEnableEmployee        = "enable:employee"         // POST   /api/employees/{id}/enable
	PermResetEmployeePassword = "reset:employee-password" // POST   /api/employees/{id}/reset-password (Step-up)
	PermResetEmployeeTOTP     = "reset:employee-totp"     // POST   /api/employees/{id}/reset-totp (Step-up)
	PermAssignEmployeeRole    = "assign:employee-role"    // POST   /api/employees/{id}/role
	PermViewRole              = "view:role"               // GET    /api/roles
	PermEditRole              = "edit:role"               // POST   /api/roles/{id}/grants
	PermDeleteRole            = "delete:role"             // DELETE /api/roles/{id}

	// Wallet Ops (ticket #60): the custody + operations surface over
	// wallet's internal RPCs (ADR-0008). Finance + super-admin per the
	// ADR-0012 matrix; Step-up where the matrix demands it (hot→cold
	// creation, every retry).
	PermViewOps            = "view:ops"             // GET    /api/ops (the dashboard summary)
	PermViewReconcile      = "view:reconcile"       // GET    /api/ops/reconcile
	PermViewIncident       = "view:incident"        // GET    /api/incidents
	PermResolveIncident    = "resolve:incident"     // POST   /api/incidents/{id}/resolve
	PermPauseSweep         = "pause:sweep"          // POST   /api/coins/{name}/sweep-pause
	PermViewSweep          = "view:sweep"           // GET    /api/sweeps
	PermRetrySweep         = "retry:sweep"          // POST   /api/sweeps/{id}/retry (Step-up)
	PermViewHotTransfer    = "view:hot-transfer"    // GET    /api/hot-transfers
	PermCreateHotTransfer  = "create:hot-transfer"  // POST   /api/hot-transfers (Step-up)
	PermRetryHotTransfer   = "retry:hot-transfer"   // POST   /api/hot-transfers/{id}/retry (Step-up)
	PermViewColdTransfer   = "view:cold-transfer"   // GET    /api/cold-transfers
	PermRecordColdTransfer = "record:cold-transfer" // POST   /api/cold-transfers
	PermViewChannel        = "view:channel"         // GET    /api/channels
	PermActivateChannel    = "activate:channel"     // POST   /api/channels/{name}/activate (Step-up)
	PermDeactivateChannel  = "deactivate:channel"   // POST   /api/channels/{name}/deactivate
)

// Endpoint is one registered admin endpoint. Handler is a method
// expression, so the registry stays plain data describing the surface.
type Endpoint struct {
	Method  string
	Path    string
	Perm    string   // the single permission string this endpoint checks
	Matrix  []string // seeded Role names the ADR-0012 matrix grants this permission to
	Handler func(*Server, http.ResponseWriter, *http.Request)
}

// Endpoints is the whole permissioned admin surface. The self-service
// auth endpoints (login/logout/me/password/totp — no permission can
// gate them: they run before or entirely outside any Role) and the
// internal RPCs (the compose network boundary is their auth) stay
// outside this list by design. Employee and Role management is
// super-admin's alone in the matrix (ADR-0012).
var Endpoints = []Endpoint{
	{Method: http.MethodGet, Path: "/api/employees", Perm: PermViewEmployee, Matrix: []string{RoleSuperAdmin}, Handler: (*Server).handleListEmployees},
	{Method: http.MethodPost, Path: "/api/employees", Perm: PermCreateEmployee, Matrix: []string{RoleSuperAdmin}, Handler: (*Server).handleCreateEmployee},
	{Method: http.MethodPost, Path: "/api/employees/{id}/disable", Perm: PermDisableEmployee, Matrix: []string{RoleSuperAdmin}, Handler: (*Server).handleDisableEmployee},
	{Method: http.MethodPost, Path: "/api/employees/{id}/enable", Perm: PermEnableEmployee, Matrix: []string{RoleSuperAdmin}, Handler: (*Server).handleEnableEmployee},
	{Method: http.MethodPost, Path: "/api/employees/{id}/reset-password", Perm: PermResetEmployeePassword, Matrix: []string{RoleSuperAdmin}, Handler: (*Server).handleResetEmployeePassword},
	{Method: http.MethodPost, Path: "/api/employees/{id}/reset-totp", Perm: PermResetEmployeeTOTP, Matrix: []string{RoleSuperAdmin}, Handler: (*Server).handleResetEmployeeTOTP},
	{Method: http.MethodPost, Path: "/api/employees/{id}/role", Perm: PermAssignEmployeeRole, Matrix: []string{RoleSuperAdmin}, Handler: (*Server).handleAssignEmployeeRole},
	{Method: http.MethodGet, Path: "/api/roles", Perm: PermViewRole, Matrix: []string{RoleSuperAdmin}, Handler: (*Server).handleListRoles},
	{Method: http.MethodPost, Path: "/api/roles/{id}/grants", Perm: PermEditRole, Matrix: []string{RoleSuperAdmin}, Handler: (*Server).handleEditRoleGrants},
	{Method: http.MethodDelete, Path: "/api/roles/{id}", Perm: PermDeleteRole, Matrix: []string{RoleSuperAdmin}, Handler: (*Server).handleDeleteRole},

	// Wallet Ops (ticket #60): finance + super-admin. Mutating entries
	// are audit-logged by construction with action == permission; the
	// Step-up entries are hot→cold creation and the three retries.
	{Method: http.MethodGet, Path: "/api/ops", Perm: PermViewOps, Matrix: []string{RoleFinance, RoleSuperAdmin}, Handler: (*Server).handleOpsSummary},
	{Method: http.MethodGet, Path: "/api/ops/reconcile", Perm: PermViewReconcile, Matrix: []string{RoleFinance, RoleSuperAdmin}, Handler: (*Server).handleOpsReconcile},
	{Method: http.MethodGet, Path: "/api/incidents", Perm: PermViewIncident, Matrix: []string{RoleFinance, RoleSuperAdmin}, Handler: (*Server).handleListIncidents},
	{Method: http.MethodPost, Path: "/api/incidents/{id}/resolve", Perm: PermResolveIncident, Matrix: []string{RoleFinance, RoleSuperAdmin}, Handler: (*Server).handleResolveIncident},
	{Method: http.MethodPost, Path: "/api/coins/{name}/sweep-pause", Perm: PermPauseSweep, Matrix: []string{RoleFinance, RoleSuperAdmin}, Handler: (*Server).handleSweepPause},
	{Method: http.MethodGet, Path: "/api/sweeps", Perm: PermViewSweep, Matrix: []string{RoleFinance, RoleSuperAdmin}, Handler: (*Server).handleListSweeps},
	{Method: http.MethodPost, Path: "/api/sweeps/{id}/retry", Perm: PermRetrySweep, Matrix: []string{RoleFinance, RoleSuperAdmin}, Handler: (*Server).handleSweepRetry},
	{Method: http.MethodGet, Path: "/api/hot-transfers", Perm: PermViewHotTransfer, Matrix: []string{RoleFinance, RoleSuperAdmin}, Handler: (*Server).handleListHotTransfers},
	{Method: http.MethodPost, Path: "/api/hot-transfers", Perm: PermCreateHotTransfer, Matrix: []string{RoleFinance, RoleSuperAdmin}, Handler: (*Server).handleCreateHotTransfer},
	{Method: http.MethodPost, Path: "/api/hot-transfers/{id}/retry", Perm: PermRetryHotTransfer, Matrix: []string{RoleFinance, RoleSuperAdmin}, Handler: (*Server).handleHotTransferRetry},
	{Method: http.MethodGet, Path: "/api/cold-transfers", Perm: PermViewColdTransfer, Matrix: []string{RoleFinance, RoleSuperAdmin}, Handler: (*Server).handleListColdTransfers},
	{Method: http.MethodPost, Path: "/api/cold-transfers", Perm: PermRecordColdTransfer, Matrix: []string{RoleFinance, RoleSuperAdmin}, Handler: (*Server).handleRecordColdTransfer},

	// Code channels (#62, ADR-0006): state and credential validation
	// live in api; this surface is the gated front. Super-admin's alone
	// in the ADR-0012 matrix. Activation carries the credential save,
	// so it demands Step-up inside the handler.
	{Method: http.MethodGet, Path: "/api/channels", Perm: PermViewChannel, Matrix: []string{RoleSuperAdmin}, Handler: (*Server).handleListChannels},
	{Method: http.MethodPost, Path: "/api/channels/{name}/activate", Perm: PermActivateChannel, Matrix: []string{RoleSuperAdmin}, Handler: (*Server).handleActivateChannel},
	{Method: http.MethodPost, Path: "/api/channels/{name}/deactivate", Perm: PermDeactivateChannel, Matrix: []string{RoleSuperAdmin}, Handler: (*Server).handleDeactivateChannel},
}

// registerAdminEndpoint mounts one registry entry behind the full
// gate chain, outermost first: the session gate (authentication +
// the restricted-session rule), the permission middleware (the only
// real gate — the UI never is), and — for mutating methods — the
// middleware-written Audit log whose action is the endpoint's
// permission string.
func (s *Server) registerAdminEndpoint(mux *http.ServeMux, e Endpoint) {
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { e.Handler(s, w, r) })
	if e.Method != http.MethodGet && e.Method != http.MethodHead {
		h = s.audited(e.Perm, h)
	}
	mux.HandleFunc(e.Method+" "+e.Path, s.withSession(s.requirePerm(e.Perm, h)))
}

// RegisterEndpoints mounts every registry entry (used by RegisterPublic
// and by tests that mirror the production wiring).
func (s *Server) RegisterEndpoints(mux *http.ServeMux) {
	for _, e := range Endpoints {
		s.registerAdminEndpoint(mux, e)
	}
}
