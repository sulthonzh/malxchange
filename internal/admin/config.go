// Package admin is the admin-api service surface: Employee auth core
// (issue #53, ADR-0012) — login/logout over an opaque httpOnly cookie
// with the 12 h absolute + 30 min idle session policy, the 5-failures
// lockout, forced first-login rotation + TOTP enrollment, the
// middleware-written Audit log, and the installer's first-Employee
// bootstrap RPC. RBAC enforcement, the permission catalog, role
// seeding, and Step-up land with #54 on top of these seams.
package admin

import (
	"sync"
	"time"

	"github.com/syaikhipin/malxchange/internal/auth"
	"github.com/syaikhipin/malxchange/internal/config"
)

// LockoutThreshold is the failed-login count inside LockoutWindow that
// locks the account (ADR-0012: 5 strikes, no captcha). A constant like
// the member api's — the window is the operator knob.
const LockoutThreshold = 5

// sessionCookie is the opaque-token httpOnly cookie. No Max-Age/Expires
// is ever set on it: a browser-session cookie with the server-side
// 12 h absolute cap is the whole lifetime model — no remember-me path
// exists (ADR-0012).
const sessionCookie = "mal_admin_session"

// AdminEnv carries every operator-tunable knob of the admin-api service,
// following the api APIEnv / wallet WalletEnv pattern. Defaults are the
// ADR-0012 values; everything reaches the operator through
// deploy/env.example (make lint fails on drift).
type AdminEnv struct {
	SessionAbsolute  time.Duration // total session lifetime from creation (ADR-0012: 12 h)
	SessionIdle      time.Duration // inactivity window, slid on every authenticated use (ADR-0012: 30 min)
	LockoutThreshold int           // failed logins inside LockoutWindow before the email locks
	LockoutWindow    time.Duration // failures older than this stop counting; the lock lifts with them
}

// SessionPolicy shapes the shared auth package's expiry arithmetic:
// absolute + idle (member api slides purely; Employees do not).
func (e *AdminEnv) SessionPolicy() auth.SessionPolicy {
	return auth.SessionPolicy{Absolute: e.SessionAbsolute, Idle: e.SessionIdle}
}

var (
	envOnce sync.Once
	envVal  *AdminEnv
)

// LoadAdminEnv reads the MAL_ADMIN_* environment once per process.
func LoadAdminEnv() *AdminEnv {
	envOnce.Do(func() {
		envVal = loadAdminEnv()
	})
	return envVal
}

// loadAdminEnv is the pure reader (unit tests hit this, not the Once).
func loadAdminEnv() *AdminEnv {
	return &AdminEnv{
		SessionAbsolute:  envDuration("MAL_ADMIN_SESSION_ABSOLUTE", 12*time.Hour),
		SessionIdle:      envDuration("MAL_ADMIN_SESSION_IDLE", 30*time.Minute),
		LockoutThreshold: LockoutThreshold,
		LockoutWindow:    envDuration("MAL_ADMIN_LOCKOUT_WINDOW", 15*time.Minute),
	}
}

// envDuration parses an env var as a duration, falling back to def on
// any miss or parse error (misconfiguration must not brick the boot;
// the default is the documented ADR-0012 value).
func envDuration(key string, def time.Duration) time.Duration {
	v := config.EnvDefault(key, "")
	if v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		return def
	}
	return d
}
