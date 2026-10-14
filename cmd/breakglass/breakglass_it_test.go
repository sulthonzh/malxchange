//go:build integration

// Per-command coverage of the break-glass CLI (ticket #55), the
// walletctl test pattern against real Postgres: run() is driven
// in-process — no subprocess, no admin-api HTTP surface exists in this
// stack at all, which is the acceptance point itself. The database is
// a per-run throwaway; assertions read rows back as tripwires on what
// the command must have written (employee state, sessions, audit_log).
package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/syaikhipin/malxchange/internal/auth"
	"github.com/syaikhipin/malxchange/internal/migrations"
	"github.com/syaikhipin/malxchange/internal/pgmig"
)

var (
	itPGHost = os.Getenv("MAL_TEST_PG_HOST")
	itPGPW   = os.Getenv("MAL_TEST_PG_SUPERUSER_PASSWORD")
)

func TestMain(m *testing.M) {
	if itPGHost == "" || itPGPW == "" {
		fmt.Println("skipping breakglass integration tests: set MAL_TEST_PG_* (make test-integration)")
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// bgStack is a throwaway database with the admin migrations applied,
// plus the environment the CLI's connection flags fall back to.
type bgStack struct {
	pool *pgxpool.Pool // search_path=admin, for seeds and tripwires
}

func newStack(t *testing.T) *bgStack {
	t.Helper()
	ctx := context.Background()

	suffix := make([]byte, 6)
	if _, err := rand.Read(suffix); err != nil {
		t.Fatal(err)
	}
	dbName := "malxchange_itbg_" + hex.EncodeToString(suffix)

	admin, err := pgxpool.New(ctx, fmt.Sprintf("postgres://%s:%s@%s:5432/postgres",
		os.Getenv("MAL_TEST_PG_SUPERUSER"), itPGPW, itPGHost))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(ctx, fmt.Sprintf(`DROP DATABASE IF EXISTS %s WITH (FORCE)`, dbName)); err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(ctx, fmt.Sprintf(`CREATE DATABASE %s`, dbName)); err != nil {
		t.Fatal(err)
	}
	admin.Close()
	t.Cleanup(func() {
		admin, err := pgxpool.New(ctx, fmt.Sprintf("postgres://%s:%s@%s:5432/postgres",
			os.Getenv("MAL_TEST_PG_SUPERUSER"), itPGPW, itPGHost))
		if err != nil {
			return
		}
		defer admin.Close()
		_, _ = admin.Exec(ctx, fmt.Sprintf(`DROP DATABASE IF EXISTS %s WITH (FORCE)`, dbName))
	})

	pool, err := pgxpool.New(ctx, fmt.Sprintf("postgres://%s:%s@%s:5432/%s?sslmode=disable&search_path=admin",
		os.Getenv("MAL_TEST_PG_SUPERUSER"), itPGPW, itPGHost, dbName))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if _, err := pool.Exec(ctx, `CREATE SCHEMA IF NOT EXISTS admin`); err != nil {
		t.Fatal(err)
	}
	u := url.URL{
		Scheme:   "postgres",
		User:     url.UserPassword(os.Getenv("MAL_TEST_PG_SUPERUSER"), itPGPW),
		Host:     itPGHost + ":5432",
		Path:     dbName,
		RawQuery: "sslmode=disable&search_path=admin",
	}
	if err := pgmig.Run(u.String(), migrations.FS, "admin"); err != nil {
		t.Fatalf("admin migrations: %v", err)
	}

	// The CLI's connection flags fall back to these; point them at the
	// throwaway database so every command run() lands here.
	t.Setenv("MAL_PG_HOST", itPGHost)
	t.Setenv("MAL_PG_PORT", "5432")
	t.Setenv("MAL_PG_DATABASE", dbName)
	t.Setenv("MAL_PG_USER", os.Getenv("MAL_TEST_PG_SUPERUSER"))
	t.Setenv("MAL_PG_PASSWORD", itPGPW)
	t.Setenv("MAL_PG_SCHEMA", "admin")

	return &bgStack{pool: pool}
}

// seedEmployee inserts a role (idempotent) and an Employee row with a
// known password; totpBound seeds a confirmed binding carrying a
// recognizable sentinel secret.
func (s *bgStack) seedEmployee(t *testing.T, email, role string, totpBound bool) string {
	t.Helper()
	ctx := context.Background()
	hash, err := auth.HashPassword("known-password-1")
	if err != nil {
		t.Fatal(err)
	}
	roleID := uuid.NewString()
	if _, err := s.pool.Exec(ctx,
		`INSERT INTO roles (id, name, system) VALUES ($1, $2, true) ON CONFLICT (name) DO NOTHING`,
		roleID, role); err != nil {
		t.Fatal(err)
	}
	if err := s.pool.QueryRow(ctx, `SELECT id FROM roles WHERE name = $1`, role).Scan(&roleID); err != nil {
		t.Fatal(err)
	}
	id := uuid.NewString()
	if _, err := s.pool.Exec(ctx,
		`INSERT INTO employees (id, email, password_hash, role_id, enabled, must_rotate, totp_secret)
		 VALUES ($1, $2, $3, $4, true, false, NULLIF($5, ''))`,
		id, email, hash, roleID, boundSecret(totpBound)); err != nil {
		t.Fatal(err)
	}
	return id
}

func boundSecret(bound bool) string {
	if bound {
		return "SECRETSARENOTFORLOGS"
	}
	return ""
}

// seedSession inserts one live session row for the Employee.
func (s *bgStack) seedSession(t *testing.T, employeeID, tokenHash string, revoked bool) {
	t.Helper()
	revokedAt := any(nil)
	if revoked {
		revokedAt = time.Now()
	}
	_, err := s.pool.Exec(context.Background(),
		`INSERT INTO sessions (id, member_id, token_hash, device, expires_at, revoked_at)
		 VALUES ($1, $2, $3, 'it', now() + interval '1 hour', $4)`,
		uuid.NewString(), employeeID, tokenHash, revokedAt)
	if err != nil {
		t.Fatal(err)
	}
}

// seedFailures records wrong-password strikes against the email.
func (s *bgStack) seedFailures(t *testing.T, email string, n int) {
	t.Helper()
	for range n {
		if _, err := s.pool.Exec(context.Background(),
			`INSERT INTO login_failures (email) VALUES ($1)`, email); err != nil {
			t.Fatal(err)
		}
	}
}

// runCLI runs the CLI in-process and fails the test on error.
func runCLI(t *testing.T, args ...string) string {
	t.Helper()
	var buf bytes.Buffer
	if err := run(args, &buf); err != nil {
		t.Fatalf("run(%v): %v", args, err)
	}
	return buf.String()
}

// runCLIErr runs the CLI expecting failure and returns the error.
func runCLIErr(t *testing.T, args ...string) error {
	t.Helper()
	var buf bytes.Buffer
	err := run(args, &buf)
	if err == nil {
		t.Fatalf("run(%v): expected error, got success (out: %q)", args, buf.String())
	}
	return err
}

// employee row tripwire.
type empRow struct {
	id, hash, totpSecret, totpStaged string
	enabled, mustRotate              bool
}

func (s *bgStack) employee(t *testing.T, email string) empRow {
	t.Helper()
	var e empRow
	var secret, staged *string
	err := s.pool.QueryRow(context.Background(),
		`SELECT id, password_hash, enabled, must_rotate, totp_secret, totp_staged
		   FROM employees WHERE email = $1`, email).
		Scan(&e.id, &e.hash, &e.enabled, &e.mustRotate, &secret, &staged)
	if err != nil {
		t.Fatalf("employee %s: %v", email, err)
	}
	if secret != nil {
		e.totpSecret = *secret
	}
	if staged != nil {
		e.totpStaged = *staged
	}
	return e
}

// auditRow is the audit_log tripwire shape.
type auditRow struct {
	actor, action, target, originIP string
	beforeNull, afterNull           bool
}

func (s *bgStack) audit(t *testing.T, action string) []auditRow {
	t.Helper()
	rows, err := s.pool.Query(context.Background(),
		`SELECT actor, action, target, origin_ip, before IS NULL, after IS NULL
		   FROM audit_log WHERE action = $1 ORDER BY id`, action)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []auditRow
	for rows.Next() {
		var r auditRow
		if err := rows.Scan(&r.actor, &r.action, &r.target, &r.originIP, &r.beforeNull, &r.afterNull); err != nil {
			t.Fatal(err)
		}
		out = append(out, r)
	}
	return out
}

func (s *bgStack) mustAuditOne(t *testing.T, action string) auditRow {
	t.Helper()
	rows := s.audit(t, action)
	if len(rows) != 1 {
		t.Fatalf("action %s: %d audit rows, want 1", action, len(rows))
	}
	return rows[0]
}

func (s *bgStack) liveSessionCount(t *testing.T, employeeID string) int {
	t.Helper()
	var n int
	if err := s.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM sessions
		  WHERE member_id = $1 AND revoked_at IS NULL AND expires_at > now()`, employeeID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func (s *bgStack) failureCount(t *testing.T, email string) int {
	t.Helper()
	var n int
	if err := s.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM login_failures WHERE email = $1`, email).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// ---- the commands ----

func TestListCommand(t *testing.T) {
	s := newStack(t)
	s.seedEmployee(t, "root@example.com", "super-admin", true)
	s.seedEmployee(t, "money@example.com", "finance", false)

	out := runCLI(t, "list")
	for _, want := range []string{
		"root@example.com", "super-admin", "yes", // enabled + bound
		"money@example.com", "finance",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("list output missing %q:\n%s", want, out)
		}
	}
	if n := strings.Count(out, "\n"); n != 3 { // header + two rows
		t.Fatalf("list printed %d lines, want 3:\n%s", n, out)
	}
}

func TestResetPasswordCommandOperatorSupplied(t *testing.T) {
	s := newStack(t)
	id := s.seedEmployee(t, "root@example.com", "super-admin", true)
	s.seedSession(t, id, "live-token-hash", false)
	s.seedSession(t, id, "already-revoked-hash", true)
	s.seedFailures(t, "root@example.com", 5)

	t.Setenv("BREAKGLASS_NEW_PASS", "operator-chosen-pass-1")
	out := runCLI(t, "reset-password", "--email", "Root@Example.com", "--password-env", "BREAKGLASS_NEW_PASS")

	// The new password authenticates; it is never echoed.
	e := s.employee(t, "root@example.com")
	if !auth.VerifyPassword(e.hash, "operator-chosen-pass-1") {
		t.Fatal("stored hash does not verify against the operator-supplied password")
	}
	if strings.Contains(out, "operator-chosen-pass-1") {
		t.Fatalf("operator-supplied password echoed to stdout:\n%s", out)
	}
	// Forced rotation re-armed, sessions closed, counters cleared.
	if !e.mustRotate {
		t.Fatal("must_rotate not re-armed by the reset")
	}
	if e.totpSecret == "" {
		t.Fatal("reset-password must not touch the TOTP binding")
	}
	if n := s.liveSessionCount(t, id); n != 0 {
		t.Fatalf("%d live sessions survive the reset", n)
	}
	if n := s.failureCount(t, "root@example.com"); n != 0 {
		t.Fatalf("%d login failures survive the reset", n)
	}
	// The system-actor audit row, with before/after state.
	row := s.mustAuditOne(t, "reset:employee-password")
	if row.actor != "system:breakglass" {
		t.Fatalf("audit actor = %q, want system:breakglass", row.actor)
	}
	if row.target != id {
		t.Fatalf("audit target = %q, want employee id %q", row.target, id)
	}
	if row.originIP != "host" {
		t.Fatalf("audit origin_ip = %q, want host", row.originIP)
	}
	if row.beforeNull || row.afterNull {
		t.Fatal("reset audit row must carry before and after state")
	}
	if !strings.Contains(out, "1 live session(s) revoked") {
		t.Fatalf("output does not report the revoked session:\n%s", out)
	}
}

func TestResetPasswordCommandGenerated(t *testing.T) {
	s := newStack(t)
	s.seedEmployee(t, "root@example.com", "super-admin", false)

	out := runCLI(t, "reset-password", "--email", "root@example.com")

	re := regexp.MustCompile(`(?m)^one-time password: ([0-9a-f]{48})$`)
	m := re.FindStringSubmatch(out)
	if m == nil {
		t.Fatalf("no generated one-time password in output:\n%s", out)
	}
	e := s.employee(t, "root@example.com")
	if !auth.VerifyPassword(e.hash, m[1]) {
		t.Fatal("stored hash does not verify against the printed one-time password")
	}
	if !e.mustRotate {
		t.Fatal("generated credential must arm forced rotation")
	}
	s.mustAuditOne(t, "reset:employee-password")
}

func TestResetTOTPCommand(t *testing.T) {
	s := newStack(t)
	id := s.seedEmployee(t, "root@example.com", "super-admin", true)
	if _, err := s.pool.Exec(context.Background(),
		`UPDATE employees SET totp_staged = 'SECRETSARENOTFORLOGS' WHERE id = $1`, id); err != nil {
		t.Fatal(err)
	}

	runCLI(t, "reset-totp", "--email", "root@example.com")

	e := s.employee(t, "root@example.com")
	if e.totpSecret != "" || e.totpStaged != "" {
		t.Fatalf("totp columns survive the reset: secret=%q staged=%q", e.totpSecret, e.totpStaged)
	}
	if n := s.liveSessionCount(t, id); n != 0 {
		t.Fatalf("%d live sessions survive the TOTP reset", n)
	}
	if row := s.mustAuditOne(t, "reset:employee-totp"); row.actor != "system:breakglass" {
		t.Fatalf("audit actor = %q, want system:breakglass", row.actor)
	}
	// The audit views must reflect the binding flip, never the secret
	// (jsonb text rendering inserts spaces; compare space-free).
	raw := strings.ReplaceAll(s.auditJSON(t, "reset:employee-totp"), " ", "")
	if !strings.Contains(raw, `"totp_bound":true`) || !strings.Contains(raw, `"totp_bound":false`) {
		t.Fatalf("audit before/after does not show the binding flip:\n%s", raw)
	}
	if strings.Contains(raw, "SECRETSARENOTFORLOGS") {
		t.Fatalf("TOTP secret leaked into the audit row:\n%s", raw)
	}
}

func TestEnableCommand(t *testing.T) {
	s := newStack(t)
	id := s.seedEmployee(t, "root@example.com", "super-admin", false)
	if _, err := s.pool.Exec(context.Background(),
		`UPDATE employees SET enabled = false WHERE id = $1`, id); err != nil {
		t.Fatal(err)
	}

	runCLI(t, "enable", "--email", "root@example.com")

	if e := s.employee(t, "root@example.com"); !e.enabled {
		t.Fatal("employee still disabled after enable")
	}
	if row := s.mustAuditOne(t, "enable:employee"); row.actor != "system:breakglass" || row.target != id {
		t.Fatalf("unexpected audit row: %+v", row)
	}
}

func TestCreateCommand(t *testing.T) {
	s := newStack(t) // no roles, no employees: the guarantee is the CLI's

	out := runCLI(t, "create", "--email", "new-root@example.com")

	re := regexp.MustCompile(`(?m)^one-time password: ([0-9a-f]{48})$`)
	m := re.FindStringSubmatch(out)
	if m == nil {
		t.Fatalf("no generated one-time password in output:\n%s", out)
	}
	e := s.employee(t, "new-root@example.com")
	if !auth.VerifyPassword(e.hash, m[1]) {
		t.Fatal("stored hash does not verify against the printed one-time password")
	}
	if !e.mustRotate || !e.enabled {
		t.Fatal("created super-admin must be enabled with forced rotation armed")
	}
	var role string
	if err := s.pool.QueryRow(context.Background(),
		`SELECT r.name FROM employees e JOIN roles r ON r.id = e.role_id WHERE e.email = $1`,
		"new-root@example.com").Scan(&role); err != nil {
		t.Fatal(err)
	}
	if role != "super-admin" {
		t.Fatalf("created employee role = %q, want super-admin", role)
	}
	// Same audit shape the bootstrap RPC writes: created row, no before.
	row := s.mustAuditOne(t, "create:employee")
	if row.actor != "system:breakglass" || !row.beforeNull || row.afterNull {
		t.Fatalf("unexpected create audit row: %+v", row)
	}
}

func TestRefusesNonSuperAdminTarget(t *testing.T) {
	s := newStack(t)
	s.seedEmployee(t, "money@example.com", "finance", false)
	before := s.employee(t, "money@example.com")
	t.Setenv("BREAKGLASS_X", "any-password-123")

	err := runCLIErr(t, "reset-password", "--email", "money@example.com", "--password-env", "BREAKGLASS_X")

	if !strings.Contains(err.Error(), "not a super-admin") {
		t.Fatalf("error %q does not refuse the non-super-admin target", err)
	}
	after := s.employee(t, "money@example.com")
	if after.hash != before.hash || after.mustRotate != before.mustRotate {
		t.Fatal("refused reset still mutated the employee row")
	}
	if rows := s.audit(t, "reset:employee-password"); len(rows) != 0 {
		t.Fatalf("refused reset wrote %d audit rows", len(rows))
	}
}

func TestUnknownEmailFails(t *testing.T) {
	newStack(t)
	err := runCLIErr(t, "reset-totp", "--email", "ghost@example.com")
	if !strings.Contains(err.Error(), "employee not found") {
		t.Fatalf("error %q does not report the unknown employee", err)
	}
}

func TestCreateRefusesDuplicateEmail(t *testing.T) {
	s := newStack(t)
	s.seedEmployee(t, "root@example.com", "super-admin", false)
	err := runCLIErr(t, "create", "--email", "root@example.com")
	if !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("error %q does not refuse the duplicate email", err)
	}
}

// auditJSON returns the raw before+after JSON of the action's rows, for
// redaction and flip assertions.
func (s *bgStack) auditJSON(t *testing.T, action string) string {
	t.Helper()
	var raw string
	err := s.pool.QueryRow(context.Background(),
		`SELECT string_agg(before::text || '|' || after::text, E'\n') FROM audit_log WHERE action = $1`,
		action).Scan(&raw)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
