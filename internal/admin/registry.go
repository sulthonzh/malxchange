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

	// Content (ticket #63): support's alone in the matrix — the
	// ADR-0012 resolution gives announcements/help to support and to
	// nobody else (finance/compliance are refused; super-admin has no
	// content verbs). Publishing is an edit of the published flag, not
	// a workflow of its own.
	PermViewAnnouncement   = "view:announcement"    // GET    /api/announcements
	PermCreateAnnouncement = "create:announcement"  // POST   /api/announcements
	PermEditAnnouncement   = "edit:announcement"    // PUT    /api/announcements/{id}
	PermDeleteAnnouncement = "delete:announcement"  // DELETE /api/announcements/{id}
	PermViewHelpCategory   = "view:help-category"   // GET    /api/help/categories
	PermCreateHelpCategory = "create:help-category" // POST  /api/help/categories
	PermEditHelpCategory   = "edit:help-category"   // PUT    /api/help/categories/{id}
	PermDeleteHelpCategory = "delete:help-category" // DELETE /api/help/categories/{id}
	PermViewHelpArticle    = "view:help-article"    // GET    /api/help/articles
	PermCreateHelpArticle  = "create:help-article"  // POST   /api/help/articles
	PermEditHelpArticle    = "edit:help-article"    // PUT    /api/help/articles/{id}
	PermDeleteHelpArticle  = "delete:help-article"  // DELETE /api/help/articles/{id}

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
	// Finance funds surface (#59): the withdrawal audit queue and the
	// manual balance adjustment. Read side is finance + super-admin
	// (read-side everything, ADR-0012); the money-moving decisions are
	// finance's alone — separation of duties is the point of the
	// matrix. Step-up: approve and retry (withdrawal approval /
	// failed-broadcast retry in ADR-0012's step-up list), and the
	// balance adjustment.
	PermListWithdrawal    = "list:withdrawal"    // GET  /api/withdrawals
	PermViewWithdrawal    = "view:withdrawal"    // GET  /api/withdrawals/{id}
	PermApproveWithdrawal = "approve:withdrawal" // POST /api/withdrawals/{id}/approve (Step-up)
	PermRejectWithdrawal  = "reject:withdrawal"  // POST /api/withdrawals/{id}/reject
	PermRetryWithdrawal   = "retry:withdrawal"   // POST /api/withdrawals/{id}/retry (Step-up)
	PermRefundWithdrawal  = "refund:withdrawal"  // POST /api/withdrawals/{id}/refund
	PermAdjustBalance     = "adjust:balance"     // POST /api/adjustments (Step-up)

	// Permission catalog + audit log (issue #57): the grant editor's
	// read-only catalog row set and the audit-log search.
	PermViewPermission = "view:permission" // GET /api/permissions (the read-only catalog)
	PermViewAuditLog   = "view:audit-log"  // GET /api/audit-log
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

	// Content (ticket #63): support's alone in the matrix (ADR-0012).
	// Mutating endpoints are audited by construction — the middleware's
	// action is the permission string.
	{Method: http.MethodGet, Path: "/api/announcements", Perm: PermViewAnnouncement, Matrix: []string{RoleSupport}, Handler: (*Server).handleListAnnouncements},
	{Method: http.MethodPost, Path: "/api/announcements", Perm: PermCreateAnnouncement, Matrix: []string{RoleSupport}, Handler: (*Server).handleCreateAnnouncement},
	{Method: http.MethodPut, Path: "/api/announcements/{id}", Perm: PermEditAnnouncement, Matrix: []string{RoleSupport}, Handler: (*Server).handleEditAnnouncement},
	{Method: http.MethodDelete, Path: "/api/announcements/{id}", Perm: PermDeleteAnnouncement, Matrix: []string{RoleSupport}, Handler: (*Server).handleDeleteAnnouncement},
	{Method: http.MethodGet, Path: "/api/help/categories", Perm: PermViewHelpCategory, Matrix: []string{RoleSupport}, Handler: (*Server).handleListHelpCategories},
	{Method: http.MethodPost, Path: "/api/help/categories", Perm: PermCreateHelpCategory, Matrix: []string{RoleSupport}, Handler: (*Server).handleCreateHelpCategory},
	{Method: http.MethodPut, Path: "/api/help/categories/{id}", Perm: PermEditHelpCategory, Matrix: []string{RoleSupport}, Handler: (*Server).handleEditHelpCategory},
	{Method: http.MethodDelete, Path: "/api/help/categories/{id}", Perm: PermDeleteHelpCategory, Matrix: []string{RoleSupport}, Handler: (*Server).handleDeleteHelpCategory},
	{Method: http.MethodGet, Path: "/api/help/articles", Perm: PermViewHelpArticle, Matrix: []string{RoleSupport}, Handler: (*Server).handleListHelpArticles},
	{Method: http.MethodPost, Path: "/api/help/articles", Perm: PermCreateHelpArticle, Matrix: []string{RoleSupport}, Handler: (*Server).handleCreateHelpArticle},
	{Method: http.MethodPut, Path: "/api/help/articles/{id}", Perm: PermEditHelpArticle, Matrix: []string{RoleSupport}, Handler: (*Server).handleEditHelpArticle},
	{Method: http.MethodDelete, Path: "/api/help/articles/{id}", Perm: PermDeleteHelpArticle, Matrix: []string{RoleSupport}, Handler: (*Server).handleDeleteHelpArticle},

	// Member + KYC surfaces (issue #58): support owns member view/lock,
	// compliance owns KYC review + member TOTP reset.
	{Method: http.MethodGet, Path: "/api/members", Perm: PermSearchMember, Matrix: []string{RoleSupport, RoleSuperAdmin}, Handler: (*Server).handleSearchMembers},
	{Method: http.MethodGet, Path: "/api/members/{id}", Perm: PermViewMember, Matrix: []string{RoleSupport, RoleSuperAdmin}, Handler: (*Server).handleMemberView},
	{Method: http.MethodPost, Path: "/api/members/{id}/lock", Perm: PermLockMember, Matrix: []string{RoleSupport}, Handler: (*Server).handleMemberLock},
	{Method: http.MethodPost, Path: "/api/members/{id}/unlock", Perm: PermUnlockMember, Matrix: []string{RoleSupport}, Handler: (*Server).handleMemberUnlock},
	{Method: http.MethodGet, Path: "/api/kyc/applications", Perm: PermViewKYC, Matrix: []string{RoleCompliance, RoleSuperAdmin}, Handler: (*Server).handleKYCQueue},
	{Method: http.MethodGet, Path: "/api/kyc/applications/{memberId}/evidence", Perm: PermViewKYCEvidence, Matrix: []string{RoleCompliance, RoleSuperAdmin}, Handler: (*Server).handleKYCEvidence},
	{Method: http.MethodPost, Path: "/api/kyc/applications/{memberId}/review", Perm: PermReviewKYC, Matrix: []string{RoleCompliance}, Handler: (*Server).handleKYCReview},
	{Method: http.MethodPost, Path: "/api/members/{id}/totp/reset", Perm: PermResetMemberTOTP, Matrix: []string{RoleCompliance}, Handler: (*Server).handleMemberTOTPReset},

	// The grant editor's catalog read (ticket #57): the row set the
	// editor renders is exactly this table — display-only everywhere.
	// Same audience as the Role surface it feeds.
	{Method: http.MethodGet, Path: "/api/permissions", Perm: PermViewPermission, Matrix: []string{RoleSuperAdmin}, Handler: (*Server).handleListPermissions},
	// Audit log search (ticket #57): ADR-0012 reads the log with
	// super-admin/finance/compliance. A GET — searching writes nothing,
	// so the audit middleware does not apply.
	{Method: http.MethodGet, Path: "/api/audit-log", Perm: PermViewAuditLog, Matrix: []string{RoleSuperAdmin, RoleFinance, RoleCompliance}, Handler: (*Server).handleSearchAuditLog},
	// Code channels (#62, ADR-0006): state and credential validation
	// live in api; this surface is the gated front. Super-admin's alone
	// in the ADR-0012 matrix. Activation carries the credential save,
	// so it demands Step-up inside the handler.
	{Method: http.MethodGet, Path: "/api/channels", Perm: PermViewChannel, Matrix: []string{RoleSuperAdmin}, Handler: (*Server).handleListChannels},
	{Method: http.MethodPost, Path: "/api/channels/{name}/activate", Perm: PermActivateChannel, Matrix: []string{RoleSuperAdmin}, Handler: (*Server).handleActivateChannel},
	{Method: http.MethodPost, Path: "/api/channels/{name}/deactivate", Perm: PermDeactivateChannel, Matrix: []string{RoleSuperAdmin}, Handler: (*Server).handleDeactivateChannel},
	// Finance funds surface (#59): withdrawal audit queue + manual
	// balance adjustment (comment on the permission strings).
	{Method: http.MethodGet, Path: "/api/withdrawals", Perm: PermListWithdrawal, Matrix: []string{RoleFinance, RoleSuperAdmin}, Handler: (*Server).handleListWithdrawals},
	{Method: http.MethodGet, Path: "/api/withdrawals/{id}", Perm: PermViewWithdrawal, Matrix: []string{RoleFinance, RoleSuperAdmin}, Handler: (*Server).handleWithdrawalDetail},
	{Method: http.MethodPost, Path: "/api/withdrawals/{id}/approve", Perm: PermApproveWithdrawal, Matrix: []string{RoleFinance}, Handler: (*Server).handleApproveWithdrawal},
	{Method: http.MethodPost, Path: "/api/withdrawals/{id}/reject", Perm: PermRejectWithdrawal, Matrix: []string{RoleFinance}, Handler: (*Server).handleRejectWithdrawal},
	{Method: http.MethodPost, Path: "/api/withdrawals/{id}/retry", Perm: PermRetryWithdrawal, Matrix: []string{RoleFinance}, Handler: (*Server).handleRetryWithdrawal},
	{Method: http.MethodPost, Path: "/api/withdrawals/{id}/refund", Perm: PermRefundWithdrawal, Matrix: []string{RoleFinance}, Handler: (*Server).handleRefundWithdrawal},
	{Method: http.MethodPost, Path: "/api/adjustments", Perm: PermAdjustBalance, Matrix: []string{RoleFinance}, Handler: (*Server).handleAdjustment},
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
