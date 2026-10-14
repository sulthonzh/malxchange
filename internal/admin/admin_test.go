package admin

import (
	"encoding/json"
	"testing"
	"time"
)

func TestLoadAdminEnvDefaultsMatchADR0012(t *testing.T) {
	env := loadAdminEnv() // pure reader: no env vars set in the test process
	if env.SessionAbsolute != 12*time.Hour {
		t.Fatalf("SessionAbsolute default = %v, want 12h", env.SessionAbsolute)
	}
	if env.SessionIdle != 30*time.Minute {
		t.Fatalf("SessionIdle default = %v, want 30m", env.SessionIdle)
	}
	if env.LockoutWindow != 15*time.Minute {
		t.Fatalf("LockoutWindow default = %v, want 15m", env.LockoutWindow)
	}
	if env.LockoutThreshold != 5 {
		t.Fatalf("LockoutThreshold = %d, want 5", env.LockoutThreshold)
	}
}

func TestSessionPolicyIsAbsolutePlusIdle(t *testing.T) {
	env := loadAdminEnv()
	p := env.SessionPolicy()
	if p.Absolute != 12*time.Hour || p.Idle != 30*time.Minute {
		t.Fatalf("SessionPolicy = %+v, want {12h, 30m}", p)
	}
}

func TestEnvDurationFallsBackOnGarbage(t *testing.T) {
	t.Setenv("MAL_ADMIN_TEST_GARBAGE", "not-a-duration")
	if got := envDuration("MAL_ADMIN_TEST_GARBAGE", 15*time.Minute); got != 15*time.Minute {
		t.Fatalf("garbage duration = %v, want the default", got)
	}
	t.Setenv("MAL_ADMIN_TEST_NEGATIVE", "-5m")
	if got := envDuration("MAL_ADMIN_TEST_NEGATIVE", 15*time.Minute); got != 15*time.Minute {
		t.Fatalf("negative duration = %v, want the default", got)
	}
}

// The audit envelope's row view is the redaction contract: secret
// material never enters audit_log (it is readable by three roles,
// ADR-0012), the staging state is a flag, and the JSON shape is what
// later surfaces render.
func TestAuditViewRedactsSecrets(t *testing.T) {
	e := Employee{
		ID:           "emp-1",
		Email:        "root@example.com",
		PasswordHash: "$argon2id$v=19$m=65536,t=1,p=4$c2FsdA$aGFzaA",
		RoleID:       "role-1",
		Enabled:      true,
		TOTPSecret:   "JBSWY3DPEHPK3PXP",
		TOTPStaged:   "",
		MustRotate:   false,
	}
	raw := mustJSON(auditView(e))
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"id":          "emp-1",
		"email":       "root@example.com",
		"role_id":     "role-1",
		"enabled":     true,
		"totp_bound":  true,
		"must_rotate": false,
	}
	for k, v := range want {
		got, ok := m[k]
		if !ok {
			t.Fatalf("audit view lost %q: %s", k, raw)
		}
		if got != v {
			t.Fatalf("audit view %q = %v, want %v", k, got, v)
		}
	}
	for _, banned := range []string{"password_hash", "totp_secret", "totp_staged", "password"} {
		if _, ok := m[banned]; ok {
			t.Fatalf("audit view carries secret field %q: %s", banned, raw)
		}
	}
}

// An unprovisioned Employee reads as not-yet-bound and must-rotate —
// the before/after pair that proves rotation flipped the flag.
func TestAuditViewUnprovisionedShape(t *testing.T) {
	e := Employee{ID: "emp-2", Email: "x@example.com", RoleID: "role-1", Enabled: true, MustRotate: true}
	view := auditView(e)
	if view.TOTPBound || !view.MustRotate {
		t.Fatalf("unprovisioned view wrong: %+v", view)
	}
}

func TestNullableJSONNilForEmpty(t *testing.T) {
	if got := nullableJSON(nil); got != nil {
		t.Fatalf("nullableJSON(nil) = %v, want nil", got)
	}
	if got := nullableJSON(json.RawMessage(`{}`)); got == nil {
		t.Fatalf("nullableJSON of content became nil")
	}
}
