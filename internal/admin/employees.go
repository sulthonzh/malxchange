// Employee storage (GLOSSARY: Employee — never "admin user"). All rows
// live in the admin schema; identities are emails stored lowercased,
// each Employee holds exactly one Role (FK), and the TOTP binding is
// two columns: totp_secret (confirmed — authenticates logins) and
// totp_staged (enrollment begin, awaiting its first valid code).
package admin

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/syaikhipin/malxchange/internal/auth"
)

// ErrAlreadyBootstrapped refuses the first-Employee RPC once any
// Employee row exists (ADR-0007/ADR-0012: one shot, ever).
var ErrAlreadyBootstrapped = errors.New("admin: an Employee already exists — bootstrap refused")

// TOTP enrollment refusals (handled per-endpoint, not leaked raw).
var (
	ErrTOTPAlreadyBound = errors.New("admin: totp already bound")
	ErrTOTPNotStaged    = errors.New("admin: totp enrollment not begun")
	ErrTOTPCodeRejected = errors.New("admin: totp code rejected")
)

// errEmployeeNotFound marks an id/email with no Employee row.
var errEmployeeNotFound = errors.New("admin: employee not found")

// Employee is one staff account row (redacted copies for responses and
// the audit envelope come from auditView).
type Employee struct {
	ID           string
	Email        string
	PasswordHash string
	RoleID       string
	RoleName     string
	Enabled      bool
	TOTPSecret   string // "" = unbound
	TOTPStaged   string // "" = nothing staged
	MustRotate   bool
}

// provisioned reports whether the Employee finished forced first-login
// setup: password rotated past the one-time credential and TOTP bound.
// A session of an unprovisioned Employee reaches only the setup
// endpoints — nothing else (ADR-0012: rotation + enrollment before
// anything else).
func (e Employee) provisioned() bool {
	return !e.MustRotate && e.TOTPSecret != ""
}

const employeeColumns = `id, email, password_hash, role_id, enabled,
	COALESCE(totp_secret, ''), COALESCE(totp_staged, ''), must_rotate`

func scanEmployee(row pgx.Row) (Employee, error) {
	var e Employee
	err := row.Scan(&e.ID, &e.Email, &e.PasswordHash, &e.RoleID, &e.Enabled,
		&e.TOTPSecret, &e.TOTPStaged, &e.MustRotate)
	return e, err
}

// employeeByEmail loads one Employee; the not-found sentinel passes
// through so callers answer unknown-email like the member surface does.
func (s *Server) employeeByEmail(ctx context.Context, email string) (Employee, error) {
	emp, err := scanEmployee(s.pool.QueryRow(ctx,
		`SELECT `+employeeColumns+` FROM employees WHERE email = $1`, email))
	if errors.Is(err, pgx.ErrNoRows) {
		return Employee{}, errEmployeeNotFound
	}
	return emp, err
}

// employeeByID loads one Employee for the session gate.
func (s *Server) employeeByID(ctx context.Context, id string) (Employee, error) {
	emp, err := scanEmployee(s.pool.QueryRow(ctx,
		`SELECT `+employeeColumns+` FROM employees WHERE id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return Employee{}, errEmployeeNotFound
	}
	return emp, err
}

// CreateFirstEmployee backs the installer's bootstrap RPC (ADR-0007):
// it refuses once any Employee exists, guarantees the super-admin Role
// the first Employee must hold (full role seeding is #54 — this is the
// single row the FK demands), and stores the one-time password only as
// its argon2id hash. The exists-check and the insert share one
// transaction behind an advisory lock, so concurrent bootstrap calls
// cannot both pass the check.
func (s *Server) CreateFirstEmployee(ctx context.Context, email, password string) (Employee, error) {
	hash, err := auth.HashPassword(password)
	if err != nil {
		return Employee{}, fmt.Errorf("admin: hash one-time password: %w", err)
	}
	var emp Employee
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('admin:bootstrap:employee'))`); err != nil {
			return err
		}
		var n int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM employees`).Scan(&n); err != nil {
			return err
		}
		if n > 0 {
			return ErrAlreadyBootstrapped
		}
		roleID := uuid.NewString()
		if err := tx.QueryRow(ctx,
			`INSERT INTO roles (id, name, system) VALUES ($1, 'super-admin', true)
			 ON CONFLICT (name) DO UPDATE SET system = true
			 RETURNING id`, roleID).Scan(&roleID); err != nil {
			return fmt.Errorf("admin: guarantee super-admin role: %w", err)
		}
		emp = Employee{ID: uuid.NewString(), Email: email, RoleID: roleID, Enabled: true, MustRotate: true}
		_, err := tx.Exec(ctx,
			`INSERT INTO employees (id, email, password_hash, role_id, enabled, must_rotate)
			 VALUES ($1, $2, $3, $4, true, true)`,
			emp.ID, emp.Email, hash, emp.RoleID)
		return err
	})
	if err != nil {
		return Employee{}, err
	}
	return emp, nil
}

// RotatePassword changes the Employee's own password and closes every
// live session in the same transaction (member-surface parity: a
// credential change is a global sign-out). It clears must_rotate — the
// one-time credential's whole purpose — and returns the redacted
// before/after views for the audit envelope.
func (s *Server) RotatePassword(ctx context.Context, employeeID, newPasswordHash string) (before, after Employee, err error) {
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		row := tx.QueryRow(ctx,
			`SELECT `+employeeColumns+` FROM employees WHERE id = $1 FOR UPDATE`, employeeID)
		before, err = scanEmployee(row)
		if err != nil {
			return err
		}
		after = before
		after.PasswordHash = newPasswordHash
		after.MustRotate = false
		tag, err := tx.Exec(ctx,
			`UPDATE employees SET password_hash = $2, must_rotate = false, updated_at = now()
			  WHERE id = $1`, employeeID, newPasswordHash)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return fmt.Errorf("admin: rotate password: employee %s vanished", employeeID)
		}
		// The same revoke RevokeAllSessions does, inside the rotation
		// transaction — the caller's own session dies with the rest.
		_, err = tx.Exec(ctx,
			`UPDATE sessions SET revoked_at = now()
			  WHERE member_id = $1 AND revoked_at IS NULL AND expires_at > now()`, employeeID)
		return err
	})
	return before, after, err
}

// StageTOTP begins enrollment: the generated secret is stored as staged
// (it authenticates nothing until confirmed) and handed to the Employee
// exactly once via the otpauth URI.
func (s *Server) StageTOTP(ctx context.Context, employeeID, secret string) error {
	tag, err := s.pool.Exec(ctx,
		`UPDATE employees SET totp_staged = $2, updated_at = now() WHERE id = $1`,
		employeeID, secret)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return fmt.Errorf("admin: stage totp: employee %s vanished", employeeID)
	}
	return nil
}

// ConfirmTOTP activates the staged binding when code verifies. A stale
// confirmation is refused: confirming needs a staged secret, and a
// confirmed binding is replaced only through the admin reset (#54).
func (s *Server) ConfirmTOTP(ctx context.Context, employeeID, code string, now time.Time) (before, after Employee, err error) {
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		row := tx.QueryRow(ctx,
			`SELECT `+employeeColumns+` FROM employees WHERE id = $1 FOR UPDATE`, employeeID)
		before, err = scanEmployee(row)
		if err != nil {
			return err
		}
		if before.TOTPSecret != "" {
			return ErrTOTPAlreadyBound
		}
		if before.TOTPStaged == "" {
			return ErrTOTPNotStaged
		}
		if !auth.VerifyTOTP(before.TOTPStaged, code, now) {
			return ErrTOTPCodeRejected
		}
		after = before
		after.TOTPSecret = before.TOTPStaged
		after.TOTPStaged = ""
		tag, err := tx.Exec(ctx,
			`UPDATE employees SET totp_secret = totp_staged, totp_staged = NULL,
			        totp_bound_at = now(), updated_at = now()
			  WHERE id = $1`, employeeID)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return fmt.Errorf("admin: confirm totp: employee %s vanished", employeeID)
		}
		return nil
	})
	return before, after, err
}
