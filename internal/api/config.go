// Package api is the member surface of the exchange: the public REST
// under /api/* Caddy proxies to, and later the member WebSocket and the
// market-data proxy (issue #24). This file is the operator-tunable
// environment, following the wallet WalletEnv pattern.
package api

import (
	"sync"
	"time"

	"github.com/syaikhipin/malxchange/internal/config"
)

// APIEnv carries every operator-tunable knob of the api service. Defaults
// are single-host-install friendly; everything reaches the operator
// through deploy/env.example (make lint fails on drift).
type APIEnv struct {
	SMTPHost          string // empty = no outbound mail (codes are only observable through the capture mailer in tests)
	SMTPPort          string
	SMTPUser          string // optional; enables SMTP AUTH when set with SMTPPass
	SMTPPass          string
	SMTPFrom          string
	CodeTTL           time.Duration // Verification code lifetime (ADR-0006: single-use, purpose-scoped, TTL'd)
	CodeCooldown      time.Duration // minimum spacing between two codes for the same purpose+target
	MaxCodeAttempts   int           // wrong entries tolerated before the code row is dead
	RegisterTicketTTL time.Duration // registration handoff ticket lifetime after a verified code
	LockoutThreshold  int           // failed logins inside LockoutWindow before the email is locked
	LockoutWindow     time.Duration // failures older than this stop counting; the lock lifts with them
	SessionTTL        time.Duration // sliding expiry of a Session (ADR-0006: 7 days)
}

var (
	envOnce sync.Once
	envVal  *APIEnv
)

// LoadAPIEnv reads the MAL_API_* environment once per process.
func LoadAPIEnv() *APIEnv {
	envOnce.Do(func() {
		envVal = &APIEnv{
			SMTPHost:          config.EnvDefault("MAL_API_SMTP_HOST", ""),
			SMTPPort:          config.EnvDefault("MAL_API_SMTP_PORT", "25"),
			SMTPUser:          config.EnvDefault("MAL_API_SMTP_USER", ""),
			SMTPPass:          config.EnvDefault("MAL_API_SMTP_PASS", ""),
			SMTPFrom:          config.EnvDefault("MAL_API_SMTP_FROM", "malxchange@localhost"),
			CodeTTL:           envDuration("MAL_API_CODE_TTL", 5*time.Minute),
			CodeCooldown:      envDuration("MAL_API_CODE_COOLDOWN", 30*time.Second),
			MaxCodeAttempts:   5,
			RegisterTicketTTL: envDuration("MAL_API_REGISTER_TICKET_TTL", 15*time.Minute),
			LockoutThreshold:  5,
			LockoutWindow:     envDuration("MAL_API_LOCKOUT_WINDOW", 15*time.Minute),
			SessionTTL:        envDuration("MAL_API_SESSION_TTL", 7*24*time.Hour),
		}
	})
	return envVal
}

// envDuration parses an env var as a duration, falling back to def on any
// miss or parse error (misconfiguration must not brick the boot; the
// default is the documented value).
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
