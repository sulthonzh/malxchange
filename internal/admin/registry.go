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

	// Config surfaces (ticket #61): the Coin editor fronts the wallet's
	// Coin config RPC, the Market editor the exchange's Market-config
	// RPC; engine status is the all-Roles read view. ADR-0012 matrix:
	// config edits are super-admin's; the engine status page is the
	// one surface every Role sees.
	PermViewCoin   = "view:coin"   // GET /api/coins
	PermEditCoin   = "edit:coin"   // PUT /api/coins/{name}
	PermViewMarket = "view:market" // GET /api/markets
	PermEditMarket = "edit:market" // PUT /api/markets/{symbol}
	PermViewEngine = "view:engine" // GET /api/engine

	// Member + KYC surfaces (issue #58): support owns member
	// view/lock, compliance owns KYC review + member TOTP reset
	// (Step-up), super-admin the read side (ADR-0012 matrix).
	PermSearchMember    = "search:member"     // GET    /api/members
	PermViewMember      = "view:member"       // GET    /api/members/{id}
	PermLockMember      = "lock:member"       // POST   /api/members/{id}/lock
	PermUnlockMember    = "unlock:member"     // POST   /api/members/{id}/unlock
	PermViewKYC         = "view:kyc"          // GET    /api/kyc/applications
	PermViewKYCEvidence = "view:kyc-evidence" // GET    /api/kyc/applications/{memberId}/evidence
	PermReviewKYC       = "review:kyc"        // POST   /api/kyc/applications/{memberId}/review
	PermResetMemberTOTP = "reset:member-totp" // POST   /api/members/{id}/totp/reset (Step-up)

	// Code channels (issue #62, ADR-0006): super-admin's alone.
	PermViewChannel       = "view:channel"       // GET    /api/channels
	PermActivateChannel   = "activate:channel"   // POST   /api/channels/{name}/activate (Step-up)
	PermDeactivateChannel = "deactivate:channel" // POST   /api/channels/{name}/deactivate
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
	// Config editors (ticket #61): super-admin's alone per the
	// ADR-0012 matrix; both edits are audited by construction, with
	// the upstream's before/after row JSON in the envelope.
	{Method: http.MethodGet, Path: "/api/coins", Perm: PermViewCoin, Matrix: []string{RoleSuperAdmin}, Handler: (*Server).handleListCoins},
	{Method: http.MethodPut, Path: "/api/coins/{name}", Perm: PermEditCoin, Matrix: []string{RoleSuperAdmin}, Handler: (*Server).handleUpdateCoin},
	{Method: http.MethodGet, Path: "/api/markets", Perm: PermViewMarket, Matrix: []string{RoleSuperAdmin}, Handler: (*Server).handleListMarkets},
	// Symbols carry a slash ("BTC/USDT"), so the edit route is
	// multi-segment; PathValue joins it back to the symbol.
	{Method: http.MethodPut, Path: "/api/markets/{symbol...}", Perm: PermEditMarket, Matrix: []string{RoleSuperAdmin}, Handler: (*Server).handleUpdateMarket},
	// The engine status page is the one surface every Role sees
	// (ADR-0012 matrix: read-side incl. engine status for all four).
	{Method: http.MethodGet, Path: "/api/engine", Perm: PermViewEngine, Matrix: []string{RoleSuperAdmin, RoleFinance, RoleCompliance, RoleSupport}, Handler: (*Server).handleEngineStatus},

	{Method: http.MethodGet, Path: "/api/members", Perm: PermSearchMember, Matrix: []string{RoleSupport, RoleSuperAdmin}, Handler: (*Server).handleSearchMembers},
	{Method: http.MethodGet, Path: "/api/members/{id}", Perm: PermViewMember, Matrix: []string{RoleSupport, RoleSuperAdmin}, Handler: (*Server).handleMemberView},
	{Method: http.MethodPost, Path: "/api/members/{id}/lock", Perm: PermLockMember, Matrix: []string{RoleSupport}, Handler: (*Server).handleMemberLock},
	{Method: http.MethodPost, Path: "/api/members/{id}/unlock", Perm: PermUnlockMember, Matrix: []string{RoleSupport}, Handler: (*Server).handleMemberUnlock},
	{Method: http.MethodGet, Path: "/api/kyc/applications", Perm: PermViewKYC, Matrix: []string{RoleCompliance, RoleSuperAdmin}, Handler: (*Server).handleKYCQueue},
	{Method: http.MethodGet, Path: "/api/kyc/applications/{memberId}/evidence", Perm: PermViewKYCEvidence, Matrix: []string{RoleCompliance, RoleSuperAdmin}, Handler: (*Server).handleKYCEvidence},
	{Method: http.MethodPost, Path: "/api/kyc/applications/{memberId}/review", Perm: PermReviewKYC, Matrix: []string{RoleCompliance}, Handler: (*Server).handleKYCReview},
	{Method: http.MethodPost, Path: "/api/members/{id}/totp/reset", Perm: PermResetMemberTOTP, Matrix: []string{RoleCompliance}, Handler: (*Server).handleMemberTOTPReset},

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
