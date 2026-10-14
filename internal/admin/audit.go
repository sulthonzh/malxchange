// The Audit envelope (ADR-0012): middleware — never individual
// handlers — writes one audit_log row per audited request, so every
// later admin surface inherits gapless auditing by construction (the
// annotation-opt-in log is exactly how the legacy reset-password hole
// went unlogged). Handlers only fill in the envelope's metadata; the
// wrapping audited() middleware persists it.
package admin

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"strings"
)

// Audit action strings are 'verb:resource' (ADR-0012): verb-first and
// resource-singular, the same vocabulary the permission catalog
// (registry.go) seeds from — a mutating endpoint's audited action IS
// its permission string. Keep new ones in that shape.
const (
	ActionLogin          = "login:employee"  // successful sign-in
	ActionLoginFailed    = "login:failed"    // refused: wrong credentials, unknown email, disabled
	ActionLockout        = "login:lockout"   // refused: the email is locked (5 strikes / 15 min)
	ActionLogout         = "logout:employee" // sign-out revoking the session
	ActionRotatePassword = "rotate:password" // self password change (forced first-login or voluntary)
	ActionEnrollTOTP     = "enroll:totp"     // TOTP enrollment begin (secret staged)
	ActionBindTOTP       = "bind:totp"       // TOTP enrollment confirm (binding activated)
	// The first super-admin from the installer (system actor); the
	// management endpoint's rows share the string with a human actor.
	ActionCreateEmployee = PermCreateEmployee
	ActionGrantRole      = "grant:role"  // grant editor: one permission granted to a Role
	ActionRevokeRole     = "revoke:role" // grant editor: one permission revoked from a Role
)

// systemActor marks rows the installer bootstrap writes (ADR-0012's
// system actor; the break-glass CLI reuses the convention later).
const systemActor = "system:bootstrap"

// AuditEntry is the envelope one audited request fills. The middleware
// resolves the actor from the session when there is one; login-like
// endpoints (no session yet) set Actor explicitly. Force records the
// row even though the response is an error — authentication refusals
// are exactly the events the log must not drop.
type AuditEntry struct {
	Actor  string          `json:"actor"`
	Action string          `json:"action"`
	Target string          `json:"target"`
	Before json.RawMessage `json:"before,omitempty"`
	After  json.RawMessage `json:"after,omitempty"`
	Force  bool            `json:"-"`
}

type auditKeyType struct{}

// auditEntry returns the envelope the audited middleware planted for
// this request, or nil when the handler runs unaudited.
func auditEntry(r *http.Request) *AuditEntry {
	entry, _ := r.Context().Value(auditKeyType{}).(*AuditEntry)
	return entry
}

// statusWriter captures the response status so the middleware can
// decide whether the row is a success-mutation or a forced event.
type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

// audited wraps a handler: after the handler returns, the middleware —
// not the handler — writes the row. Handlers reach the envelope via
// auditEntry(r) and may override any field (login picks
// login:employee / login:failed / login:lockout per outcome). A 4xx/5xx
// response drops the row unless the handler set Force.
func (s *Server) audited(action string, next func(http.ResponseWriter, *http.Request)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		entry := &AuditEntry{Action: action}
		if emp, ok := employeeFrom(r.Context()); ok {
			entry.Actor = emp.Email
			entry.Target = emp.ID
		}
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next(sw, r.WithContext(context.WithValue(r.Context(), auditKeyType{}, entry)))
		if sw.status >= http.StatusBadRequest && !entry.Force {
			return
		}
		if entry.Actor == "" {
			return
		}
		s.writeAudit(r.Context(), entry, originIP(r))
	}
}

// writeAudit persists one row. A failed write never fails the served
// request (the mutation already committed), but it is logged loudly —
// a silent gap is the legacy failure mode ADR-0012 exists to prevent.
func (s *Server) writeAudit(ctx context.Context, e *AuditEntry, ip string) {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO audit_log (actor, action, target, before, after, origin_ip)
		 VALUES ($1, $2, $3, $4, $5, $6)`,
		e.Actor, e.Action, e.Target, nullableJSON(e.Before), nullableJSON(e.After), ip)
	if err != nil {
		s.log.Error("admin: audit write failed", "action", e.Action, "actor", e.Actor, "err", err)
	}
}

// originIP is the auditing origin: the first X-Forwarded-For hop when
// the edge (Caddy) supplied one, else the socket peer. Everything past
// the edge is compose-internal, so XFF is operator-topology truth.
func originIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if hop, _, err := net.SplitHostPort(strings.TrimSpace(strings.Split(xff, ",")[0]) + ":0"); err == nil {
			return hop
		}
		return strings.TrimSpace(strings.Split(xff, ",")[0])
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// nullableJSON maps an unset half of the envelope to SQL NULL (a
// login row carries no before/after row state).
func nullableJSON(raw json.RawMessage) any {
	if len(raw) == 0 {
		return nil
	}
	return []byte(raw)
}

// employeeAuditView is the redacted row shape audited before/after JSON
// carries. Password hashes and TOTP secrets NEVER enter the log — it is
// readable by super-admin/finance/compliance (ADR-0012) and must not
// become a secret-exfiltration surface.
type employeeAuditView struct {
	ID         string `json:"id"`
	Email      string `json:"email"`
	RoleID     string `json:"role_id"`
	Enabled    bool   `json:"enabled"`
	TOTPBound  bool   `json:"totp_bound"`
	MustRotate bool   `json:"must_rotate"`
}

// auditView snapshots the redacted state for the envelope. A staged
// (unconfirmed) enrollment does not count as bound — only a confirmed
// totp_secret does.
func auditView(e Employee) employeeAuditView {
	return employeeAuditView{
		ID:         e.ID,
		Email:      e.Email,
		RoleID:     e.RoleID,
		Enabled:    e.Enabled,
		TOTPBound:  e.TOTPSecret != "",
		MustRotate: e.MustRotate,
	}
}

// mustJSON marshals an audit view for the envelope. The shapes in this
// package are plain data and cannot fail to marshal; a panic would be
// an internal bug, not an input-handling path.
func mustJSON(v any) json.RawMessage {
	raw, err := json.Marshal(v)
	if err != nil {
		panic("admin: audit view marshal: " + err.Error())
	}
	return raw
}
