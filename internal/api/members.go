// Member identity HTTP surface: the email-code registration flow, login
// with failed-login lockout (ADR-0006 — lockout replaces third-party
// captcha), and the per-device session management the web app drives
// through the access-auth-token header.
package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/mail"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/syaikhipin/malxchange/internal/events"
	"github.com/syaikhipin/malxchange/internal/outbox"
)

// memberAPI carries the collaborators of the member identity routes.
type memberAPI struct {
	pool   *pgxpool.Pool
	env    *APIEnv
	mailer Mailer
	ob     *outbox.Outbox
	log    *slog.Logger
}

// RegisterMember mounts the member identity routes on the PUBLIC mux
// (Caddy proxies /api/* to this listener without stripping).
func (a *memberAPI) RegisterMember(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/uc/register/code", a.handleRegisterCode)
	mux.HandleFunc("POST /api/uc/register", a.handleRegister)
	mux.HandleFunc("POST /api/uc/login", a.handleLogin)
	mux.HandleFunc("POST /api/uc/logout", a.withSession(a.handleLogout))
	mux.HandleFunc("GET /api/uc/me", a.withSession(a.handleMe))
	mux.HandleFunc("GET /api/uc/sessions", a.withSession(a.handleSessions))
	mux.HandleFunc("POST /api/uc/sessions/revoke-all", a.withSession(a.handleRevokeAll))
}

// withMemberSession wraps a member-only handler: it resolves the
// access-auth-token session and rejects anonymous, expired, and revoked
// requests with 401. The one implementation behind every surface's
// gate (members, channels, orders, funds, favorites).
func withMemberSession(pool *pgxpool.Pool, env *APIEnv, next func(w http.ResponseWriter, r *http.Request, s Session)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := r.Header.Get("access-auth-token")
		if token == "" {
			WriteError(w, http.StatusUnauthorized, "Sign in to continue.")
			return
		}
		s, err := resolveSession(r.Context(), pool, token, env.SessionTTL)
		if err != nil {
			WriteError(w, http.StatusUnauthorized, "Your session has expired. Sign in again.")
			return
		}
		next(w, r, s)
	}
}

// withSession is memberAPI's binding of the shared member gate.
func (a *memberAPI) withSession(next func(w http.ResponseWriter, r *http.Request, s Session)) http.HandlerFunc {
	return withMemberSession(a.pool, a.env, next)
}

// ---- request/response payloads ----

type emailRequest struct {
	Email string `json:"email"`
}

type registerRequest struct {
	Email    string `json:"email"`
	Code     string `json:"code"`
	Password string `json:"password"`
}

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
	Device   string `json:"device"`
}

type memberSummary struct {
	ID        string `json:"id"`
	Email     string `json:"email"`
	Status    string `json:"status"`
	CreatedAt string `json:"createdAt"`
}

type tokenResponse struct {
	Token  string        `json:"token"`
	Member memberSummary `json:"member"`
}

type sessionView struct {
	ID        string `json:"id"`
	Device    string `json:"device"`
	CreatedAt string `json:"createdAt"`
	LastUsed  string `json:"lastUsed"`
	Current   bool   `json:"current"` // the session making the request
}

// ---- handlers ----

func (a *memberAPI) handleRegisterCode(w http.ResponseWriter, r *http.Request) {
	var req emailRequest
	if !decodeBody(w, r, &req) {
		return
	}
	email, ok := normalizeEmail(w, req.Email)
	if !ok {
		return
	}
	var code string
	var tooSoon bool
	err := pgx.BeginFunc(r.Context(), a.pool, func(tx pgx.Tx) error {
		recent, err := recentCode(r.Context(), tx, purposeRegister, email, a.env.CodeCooldown)
		if err != nil {
			return err
		}
		tooSoon = recent
		if recent {
			return nil
		}
		// The plaintext exists only in this closure; delivery happens
		// after commit so a failed send never strands an unusable row.
		code, err = issueCode(r.Context(), tx, purposeRegister, email, a.env.CodeTTL)
		return err
	})
	if err != nil {
		writeErr(w, a.log, err)
		return
	}
	if tooSoon {
		WriteError(w, http.StatusTooManyRequests,
			"A verification code was just sent. Check your inbox before requesting another.")
		return
	}
	subject, body, err := RegisterCodeEmail(code, a.env.CodeTTL)
	if err != nil {
		writeErr(w, a.log, err)
		return
	}
	if err := a.mailer.Send(r.Context(), email, subject, body); err != nil {
		a.log.Error("registration code email failed", "err", err)
		WriteError(w, http.StatusServiceUnavailable,
			"We could not send the verification email right now. Try again shortly.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "code sent"})
}

func (a *memberAPI) handleRegister(w http.ResponseWriter, r *http.Request) {
	var req registerRequest
	if !decodeBody(w, r, &req) {
		return
	}
	email, ok := normalizeEmail(w, req.Email)
	if !ok {
		return
	}
	if len(req.Code) != 6 {
		WriteError(w, http.StatusBadRequest, "Enter the 6-digit code from your email.")
		return
	}
	if err := checkPasswordPolicy(req.Password); err != nil {
		WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	// The code consumes in its own transaction: a burned attempt or the
	// used mark must survive even when this request later fails.
	if err := verifyCode(r.Context(), a.pool, purposeRegister, email, req.Code, a.env.MaxCodeAttempts); err != nil {
		WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	var member memberSummary
	err := pgx.BeginFunc(r.Context(), a.pool, func(tx pgx.Tx) error {
		id := uuid.NewString()
		hash, err := hashPassword(req.Password)
		if err != nil {
			return err
		}
		if err := tx.QueryRow(r.Context(),
			`INSERT INTO members (id, email, password_hash) VALUES ($1, $2, $3)
			 RETURNING id, email, status, to_char(created_at, 'YYYY-MM-DD"T"HH24:MI:SSOF')`,
			id, email, hash).
			Scan(&member.ID, &member.Email, &member.Status, &member.CreatedAt); err != nil {
			return fmt.Errorf("api: insert member: %w", err)
		}
		// Wallet opens the member's Ledger accounts from this event
		// (spec #22 contract) — same transaction as the member row.
		_, body, err := events.New(uuid.NewString(), id, events.TopicMemberRegister,
			time.Now().UTC(), events.MemberRegistered{MemberID: id, Email: email})
		if err != nil {
			return err
		}
		return a.ob.Enqueue(r.Context(), tx, events.TopicMemberRegister, id, body)
	})
	if err != nil {
		switch {
		case isUniqueViolation(err):
			WriteError(w, http.StatusConflict, "An account with this email already exists. Sign in instead.")
		default:
			writeErr(w, a.log, err)
		}
		return
	}
	// The member row + event committed; signing in the fresh member is a
	// separate step (a failed mint means "register then log in", never a
	// half-registered member).
	token, _, err := mintSession(r.Context(), a.pool, member.ID, "registration", a.env.SessionTTL)
	if err != nil {
		writeErr(w, a.log, err)
		return
	}
	writeJSON(w, http.StatusOK, tokenResponse{Token: token, Member: member})
}

func (a *memberAPI) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if !decodeBody(w, r, &req) {
		return
	}
	email, ok := normalizeEmail(w, req.Email)
	if !ok {
		return
	}
	locked, err := loginLockedOut(r.Context(), a.pool, email, a.env.LockoutThreshold, a.env.LockoutWindow)
	if err != nil {
		writeErr(w, a.log, err)
		return
	}
	if locked {
		WriteError(w, http.StatusTooManyRequests,
			"Too many failed sign-in attempts. Try again in a few minutes.")
		return
	}
	var member memberSummary
	var hash string
	err = a.pool.QueryRow(r.Context(),
		`SELECT id, email, status, password_hash, to_char(created_at, 'YYYY-MM-DD"T"HH24:MI:SSOF')
		   FROM members WHERE email = $1`, email).
		Scan(&member.ID, &member.Email, &member.Status, &hash, &member.CreatedAt)
	// Unknown email and wrong password answer identically (no account oracle).
	if errors.Is(err, pgx.ErrNoRows) {
		WriteError(w, http.StatusUnauthorized, "Email or password is incorrect.")
		return
	}
	if err != nil {
		writeErr(w, a.log, err)
		return
	}
	if member.Status == "locked" {
		WriteError(w, http.StatusForbidden, "This account is locked. Contact support.")
		return
	}
	if !verifyPassword(hash, req.Password) {
		// Only real member emails are counted: a stranger must not be
		// able to lock an address they guessed.
		if err := recordLoginFailure(r.Context(), a.pool, email); err != nil {
			writeErr(w, a.log, err)
			return
		}
		WriteError(w, http.StatusUnauthorized, "Email or password is incorrect.")
		return
	}
	token, _, err := mintSession(r.Context(), a.pool, member.ID, req.Device, a.env.SessionTTL)
	if err != nil {
		writeErr(w, a.log, err)
		return
	}
	if err := clearLoginFailures(r.Context(), a.pool, email); err != nil {
		a.log.Error("clearing login failures", "err", err) // non-fatal
	}
	writeJSON(w, http.StatusOK, tokenResponse{Token: token, Member: member})
}

func (a *memberAPI) handleLogout(w http.ResponseWriter, r *http.Request, s Session) {
	if err := revokeSession(r.Context(), a.pool, s.ID); err != nil {
		writeErr(w, a.log, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "signed out"})
}

func (a *memberAPI) handleMe(w http.ResponseWriter, r *http.Request, s Session) {
	var member memberSummary
	err := a.pool.QueryRow(r.Context(),
		`SELECT id, email, status, to_char(created_at, 'YYYY-MM-DD"T"HH24:MI:SSOF')
		   FROM members WHERE id = $1`, s.MemberID).
		Scan(&member.ID, &member.Email, &member.Status, &member.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		WriteError(w, http.StatusUnauthorized, "Your session has expired. Sign in again.")
		return
	}
	if err != nil {
		writeErr(w, a.log, err)
		return
	}
	writeJSON(w, http.StatusOK, member)
}

func (a *memberAPI) handleSessions(w http.ResponseWriter, r *http.Request, s Session) {
	live, err := listSessions(r.Context(), a.pool, s.MemberID)
	if err != nil {
		writeErr(w, a.log, err)
		return
	}
	views := make([]sessionView, 0, len(live))
	for _, sess := range live {
		views = append(views, sessionView{
			ID:        sess.ID,
			Device:    sess.Device,
			CreatedAt: formatTime(sess.CreatedAt),
			LastUsed:  formatTime(sess.LastUsedAt),
			Current:   sess.ID == s.ID,
		})
	}
	writeJSON(w, http.StatusOK, views)
}

func (a *memberAPI) handleRevokeAll(w http.ResponseWriter, r *http.Request, s Session) {
	n, err := revokeAllSessions(r.Context(), a.pool, s.MemberID)
	if err != nil {
		writeErr(w, a.log, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "all sessions revoked", "revoked": n})
}

// ---- shared helpers ----

// writeErr is the internal 500 path: operators get the error in the log,
// members get one honest English sentence (ADR-0013 — zero locale state).
func writeErr(w http.ResponseWriter, log *slog.Logger, err error) {
	if log != nil {
		log.Error("api internal error", "err", err)
	}
	WriteError(w, http.StatusInternalServerError, "Something went wrong. Try again.")
}

func decodeBody(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<16)
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		WriteError(w, http.StatusBadRequest, "The request body is not valid JSON.")
		return false
	}
	return true
}

func normalizeEmail(w http.ResponseWriter, raw string) (string, bool) {
	email := strings.ToLower(strings.TrimSpace(raw))
	addr, err := mail.ParseAddress(email)
	if err != nil || addr.Address != email || len(email) > 254 {
		WriteError(w, http.StatusBadRequest, "Enter a valid email address.")
		return "", false
	}
	return email, true
}

func checkPasswordPolicy(pw string) error {
	if len(pw) < 8 {
		return errors.New("Choose a password of at least 8 characters.")
	}
	if len(pw) > 512 {
		return errors.New("Choose a password of at most 512 characters.")
	}
	return nil
}

// formatTime renders a timestamp for member JSON in one place.
func formatTime(t time.Time) string { return t.UTC().Format(time.RFC3339) }

// isUniqueViolation reports a Postgres unique-constraint failure.
func isUniqueViolation(err error) bool {
	var pgErr interface{ SQLState() string }
	return errors.As(err, &pgErr) && pgErr.SQLState() == "23505"
}
