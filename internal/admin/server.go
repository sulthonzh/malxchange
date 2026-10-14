// Server carries the admin-api surface wiring: the Employee session
// gate over the opaque httpOnly cookie, the shared HTTP helpers, and
// the restricted-session rule (an Employee who has not finished forced
// first-login setup reaches only the setup endpoints).
package admin

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/mail"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/syaikhipin/malxchange/internal/auth"
)

// Server is the admin-api Employee-auth surface.
type Server struct {
	pool   *pgxpool.Pool
	env    *AdminEnv
	log    *slog.Logger
	grants grantCache // roleID → granted permission strings, invalidated on grant edits
	apis   *apiClient // api's internal member RPC (issue #58); nil → those surfaces answer 503
}

// NewServer wires the surface onto a live pool.
func NewServer(pool *pgxpool.Pool, env *AdminEnv, log *slog.Logger) *Server {
	if log == nil {
		log = slog.Default()
	}
	return &Server{pool: pool, env: env, log: log, grants: grantCache{sets: map[string]grantSet{}}, apis: newAPIClient(env)}
}

// The public error envelope, same shape as the member surface's
// ({"message": ...}, English-only per ADR-0013); internal-RPC errors
// keep the service convention's {"error": ...} (writeInternalErr).
func WriteError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"message": message})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, log *slog.Logger, err error) {
	if log != nil {
		log.Error("admin internal error", "err", err)
	}
	WriteError(w, http.StatusInternalServerError, "Something went wrong. Try again.")
}

// writeInternalErr is the internal-RPC error envelope ({"error": ...}).
func writeInternalErr(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": message})
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

// ---- session context ----

type (
	ctxEmployee struct{}
	ctxSession  struct{}
)

// employeeFrom returns the authenticated Employee the session gate put
// on the request context.
func employeeFrom(ctx context.Context) (Employee, bool) {
	emp, ok := ctx.Value(ctxEmployee{}).(Employee)
	return emp, ok
}

func sessionFrom(ctx context.Context) (auth.Session, bool) {
	sess, ok := ctx.Value(ctxSession{}).(auth.Session)
	return sess, ok
}

// setSessionCookie issues the opaque token: httpOnly, Secure, SameSite
// lax, and deliberately NO Max-Age/Expires — the browser-session cookie
// dies with the browser while the server-side row obeys the 12 h
// absolute + 30 min idle policy. No remember-me path exists
// (ADR-0012).
func setSessionCookie(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	})
}

func clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
}

// setupAllow is what a restricted (unprovisioned) Employee session may
// reach: the forced-setup endpoints plus me/logout. Everything else is
// refused until the password is rotated and TOTP is bound
// (ADR-0012: rotation + enrollment before anything else).
var setupAllow = map[string]bool{
	"/api/me":           true,
	"/api/password":     true,
	"/api/totp/enroll":  true,
	"/api/totp/confirm": true,
	"/api/logout":       true,
}

// withSession resolves the cookie to a live Employee session and gates
// restricted ones. Expiry arithmetic (12 h absolute, 30 min idle) is
// the shared auth package's, shaped by the admin SessionPolicy.
func (s *Server) withSession(next func(http.ResponseWriter, *http.Request)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(sessionCookie)
		if err != nil || cookie.Value == "" {
			WriteError(w, http.StatusUnauthorized, "Sign in to continue.")
			return
		}
		sess, err := auth.ResolveSession(r.Context(), s.pool, cookie.Value, s.env.SessionPolicy())
		if errors.Is(err, auth.ErrSessionRejected) {
			WriteError(w, http.StatusUnauthorized, "Your session has expired. Sign in again.")
			return
		}
		if err != nil {
			writeErr(w, s.log, err)
			return
		}
		emp, err := s.employeeByID(r.Context(), sess.MemberID)
		if errors.Is(err, errEmployeeNotFound) {
			WriteError(w, http.StatusUnauthorized, "Your session has expired. Sign in again.")
			return
		}
		if err != nil {
			writeErr(w, s.log, err)
			return
		}
		if !emp.Enabled {
			WriteError(w, http.StatusForbidden, "This account is disabled.")
			return
		}
		if !emp.provisioned() && !setupAllow[r.URL.Path] {
			WriteError(w, http.StatusForbidden,
				"Finish setting up your account: change your password and enroll two-factor authentication.")
			return
		}
		ctx := context.WithValue(r.Context(), ctxEmployee{}, emp)
		ctx = context.WithValue(ctx, ctxSession{}, sess)
		next(w, r.WithContext(ctx))
	}
}
