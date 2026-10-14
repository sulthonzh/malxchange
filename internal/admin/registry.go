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
