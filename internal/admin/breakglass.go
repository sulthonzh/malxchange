// The break-glass storage seam (ticket #55, ADR-0012): the host-side
// recovery path for a locked-out sole super-admin. No admin-api HTTP
// surface is involved — these functions write the admin schema
// directly, which is exactly why each mutation records its audit row in
// the SAME transaction: a break-glass action without its system-actor
// record must never be able to commit. The audit rows reuse the #53
// envelope shape (verb:resource action, redacted employeeAuditView
// before/after); the CLI in cmd/breakglass is the only intended caller.
package admin

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/syaikhipin/malxchange/internal/auth"
)

// breakGlassActor is the system actor of ADR-0012's recovery path,
// mirroring system:bootstrap: the row says the host (via the runbook)
// acted because no Employee could.
const breakGlassActor = "system:breakglass"

// breakGlassOrigin is the origin recorded on break-glass audit rows:
// the host console, not the edge — there is no request, no IP.
const breakGlassOrigin = "host"

// Break-glass audit rows reuse the exact action strings of the
// employee-management endpoints (the #54 catalog): one string per
// semantic action, with the actor column carrying who did it — a human
// Employee through the Step-up-gated UI, or the system break-glass
// actor here.
const (
	actionBreakGlassResetPassword = PermResetEmployeePassword
	actionBreakGlassResetTOTP     = PermResetEmployeeTOTP
	actionBreakGlassEnable        = PermEnableEmployee
)

var (
	// errBreakGlassTarget refuses anything that is not a super-admin:
	// break-glass exists to recover the sole super-admin, not to route
	// around the (step-up-gated) employee-management surface.
	errBreakGlassTarget = errors.New("admin: break-glass: Employee is not a super-admin")

	// errBreakGlassDuplicate refuses create when the email is taken.
	errBreakGlassDuplicate = errors.New("admin: break-glass: an Employee with this email already exists")
)

// BreakglassRow is one read-only list row: exactly the state the
// runbook's step 1 needs, and never a secret (no hashes, no TOTP
// secrets — same redaction rule as the audit views).
type BreakglassRow struct {
	Email      string
	Role       string
	Enabled    bool
	TOTPBound  bool
	MustRotate bool
}

// BreakglassChange reports what one break-glass mutation did: the
// redacted after-state plus the bookkeeping the runbook prints. It
// carries no hash or secret on purpose.
type BreakglassChange struct {
	Email           string
	Role            string
	Actor           string // the system actor the audit row is attributed to
	Action          string // the audit_log action recorded with the mutation
	SessionsRevoked int64  // live sessions closed by a credential reset
	MustRotate      bool   // after-state: forced first-login rotation armed
	TOTPBound       bool   // after-state: a confirmed binding exists
}

// BreakglassList shows every Employee's recovery-relevant state.
func BreakglassList(ctx context.Context, db *pgxpool.Pool) ([]BreakglassRow, error) {
	rows, err := db.Query(ctx, `
		SELECT e.email, r.name, e.enabled, COALESCE(e.totp_secret, '') <> '', e.must_rotate
		  FROM employees e JOIN roles r ON r.id = e.role_id
		 ORDER BY e.email`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []BreakglassRow
	for rows.Next() {
		var r BreakglassRow
		if err := rows.Scan(&r.Email, &r.Role, &r.Enabled, &r.TOTPBound, &r.MustRotate); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// BreakglassResetPassword sets a fresh one-time password for a
// super-admin. It re-arms must_rotate (the #53 forced-rotation
// semantics: an operator-handed credential is single-use — the next
// sign-in lands in the restricted state and forces a new password plus
// TOTP enrollment), revokes every live session (a credential change is
// a global sign-out), and clears the email's login-failure counters so
// strikes against the lost credential cannot lock the new one.
func BreakglassResetPassword(ctx context.Context, db *pgxpool.Pool, email, newPassword string) (BreakglassChange, error) {
	hash, err := auth.HashPassword(newPassword)
	if err != nil {
		return BreakglassChange{}, fmt.Errorf("admin: hash one-time password: %w", err)
	}
	var ch BreakglassChange
	err = pgx.BeginFunc(ctx, db, func(tx pgx.Tx) error {
		before, role, err := lockSuperAdmin(ctx, tx, email)
		if err != nil {
			return err
		}
		after := before
		after.PasswordHash = hash
		after.MustRotate = true
		if _, err := tx.Exec(ctx,
			`UPDATE employees SET password_hash = $2, must_rotate = true, updated_at = now()
			  WHERE id = $1`, before.ID, hash); err != nil {
			return err
		}
		revoked, err := revokeLiveSessions(ctx, tx, before.ID)
		if err != nil {
			return err
		}
		// Fresh credential, fresh counter: failures recorded against the
		// lost password must not lock the recovered Employee out of the
		// runbook's very next step (signing in).
		if _, err := tx.Exec(ctx, `DELETE FROM login_failures WHERE email = $1`, email); err != nil {
			return err
		}
		if err := breakGlassAudit(ctx, tx, actionBreakGlassResetPassword, before, after); err != nil {
			return err
		}
		ch = changeOf(role, after, actionBreakGlassResetPassword, revoked)
		return nil
	})
	if err != nil {
		return BreakglassChange{}, err
	}
	return ch, nil
}

// BreakglassResetTOTP clears a super-admin's TOTP binding (confirmed
// and staged halves both die). The Employee becomes unprovisioned, so
// the next sign-in — password only — lands in the restricted state and
// forces fresh enrollment before anything else (#53's forced-setup
// path). Every live session is revoked: the second factor changed, so
// existing sessions must re-authenticate under the new state.
func BreakglassResetTOTP(ctx context.Context, db *pgxpool.Pool, email string) (BreakglassChange, error) {
	var ch BreakglassChange
	err := pgx.BeginFunc(ctx, db, func(tx pgx.Tx) error {
		before, role, err := lockSuperAdmin(ctx, tx, email)
		if err != nil {
			return err
		}
		after := before
		after.TOTPSecret = ""
		after.TOTPStaged = ""
		if _, err := tx.Exec(ctx,
			`UPDATE employees SET totp_secret = NULL, totp_staged = NULL, totp_bound_at = NULL,
			        updated_at = now()
			  WHERE id = $1`, before.ID); err != nil {
			return err
		}
		revoked, err := revokeLiveSessions(ctx, tx, before.ID)
		if err != nil {
			return err
		}
		// A TOTP reset usually follows repeated wrong-code lockouts; the
		// counters belong to the binding that no longer exists.
		if _, err := tx.Exec(ctx, `DELETE FROM login_failures WHERE email = $1`, email); err != nil {
			return err
		}
		if err := breakGlassAudit(ctx, tx, actionBreakGlassResetTOTP, before, after); err != nil {
			return err
		}
		ch = changeOf(role, after, actionBreakGlassResetTOTP, revoked)
		return nil
	})
	if err != nil {
		return BreakglassChange{}, err
	}
	return ch, nil
}

// BreakglassEnable re-enables a disabled super-admin. A disabled
// Employee holds no usable sessions (the session gate refuses them), so
// there is nothing to revoke and nothing to re-arm — this is the
// minimal un-disable.
func BreakglassEnable(ctx context.Context, db *pgxpool.Pool, email string) (BreakglassChange, error) {
	var ch BreakglassChange
	err := pgx.BeginFunc(ctx, db, func(tx pgx.Tx) error {
		before, role, err := lockSuperAdmin(ctx, tx, email)
		if err != nil {
			return err
		}
		after := before
		after.Enabled = true
		if _, err := tx.Exec(ctx,
			`UPDATE employees SET enabled = true, updated_at = now() WHERE id = $1`, before.ID); err != nil {
			return err
		}
		if err := breakGlassAudit(ctx, tx, actionBreakGlassEnable, before, after); err != nil {
			return err
		}
		ch = changeOf(role, after, actionBreakGlassEnable, 0)
		return nil
	})
	if err != nil {
		return BreakglassChange{}, err
	}
	return ch, nil
}

// BreakglassCreateSuperAdmin creates a new super-admin with a one-time
// password (must_rotate armed — the same first-login contract as the
// installer's bootstrap Employee). Unlike the installer's one-shot RPC
// this is allowed when Employees already exist: that is the recovery
// point when the existing super-admin row is unusable and reset is not
// the answer (e.g. the person is gone for good). It guarantees the
// seeded super-admin Role exactly like CreateFirstEmployee.
func BreakglassCreateSuperAdmin(ctx context.Context, db *pgxpool.Pool, email, password string) (BreakglassChange, error) {
	hash, err := auth.HashPassword(password)
	if err != nil {
		return BreakglassChange{}, fmt.Errorf("admin: hash one-time password: %w", err)
	}
	var ch BreakglassChange
	err = pgx.BeginFunc(ctx, db, func(tx pgx.Tx) error {
		var exists bool
		if err := tx.QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM employees WHERE email = $1)`, email).Scan(&exists); err != nil {
			return err
		}
		if exists {
			return errBreakGlassDuplicate
		}
		roleID := uuid.NewString()
		if err := tx.QueryRow(ctx,
			`INSERT INTO roles (id, name, system) VALUES ($1, 'super-admin', true)
			 ON CONFLICT (name) DO UPDATE SET system = true
			 RETURNING id`, roleID).Scan(&roleID); err != nil {
			return fmt.Errorf("admin: guarantee super-admin role: %w", err)
		}
		emp := Employee{ID: uuid.NewString(), Email: email, RoleID: roleID, Enabled: true, MustRotate: true}
		if _, err := tx.Exec(ctx,
			`INSERT INTO employees (id, email, password_hash, role_id, enabled, must_rotate)
			 VALUES ($1, $2, $3, $4, true, true)`,
			emp.ID, emp.Email, hash, emp.RoleID); err != nil {
			return err
		}
		// No before-state: the row did not exist. Same shape the
		// bootstrap RPC audits for its created Employee.
		if err := breakGlassAudit(ctx, tx, ActionCreateEmployee, Employee{}, emp); err != nil {
			return err
		}
		ch = changeOf(RoleSuperAdmin, emp, ActionCreateEmployee, 0)
		return nil
	})
	if err != nil {
		return BreakglassChange{}, err
	}
	return ch, nil
}

// lockSuperAdmin loads the target Employee row FOR UPDATE (the mutation
// below runs against a locked row) and refuses any Employee whose Role
// is not the seeded super-admin one.
func lockSuperAdmin(ctx context.Context, tx pgx.Tx, email string) (Employee, string, error) {
	row := tx.QueryRow(ctx, `
		SELECT e.id, e.email, e.password_hash, e.role_id, e.enabled,
		       COALESCE(e.totp_secret, ''), COALESCE(e.totp_staged, ''), e.must_rotate, r.name
		  FROM employees e JOIN roles r ON r.id = e.role_id
		 WHERE e.email = $1
		 FOR UPDATE OF e`, email)
	var e Employee
	var role string
	err := row.Scan(&e.ID, &e.Email, &e.PasswordHash, &e.RoleID, &e.Enabled,
		&e.TOTPSecret, &e.TOTPStaged, &e.MustRotate, &role)
	if errors.Is(err, pgx.ErrNoRows) {
		return Employee{}, "", errEmployeeNotFound
	}
	if err != nil {
		return Employee{}, "", err
	}
	if role != RoleSuperAdmin {
		return Employee{}, "", errBreakGlassTarget
	}
	return e, role, nil
}

// revokeLiveSessions performs the same revoke RotatePassword does —
// inside the caller's transaction — and reports how many live sessions
// closed.
func revokeLiveSessions(ctx context.Context, tx pgx.Tx, employeeID string) (int64, error) {
	tag, err := tx.Exec(ctx,
		`UPDATE sessions SET revoked_at = now()
		  WHERE member_id = $1 AND revoked_at IS NULL AND expires_at > now()`, employeeID)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// breakGlassAudit records the system-actor row inside the caller's
// transaction, reusing the #53 envelope: verb:resource action, the
// redacted employeeAuditView before/after (never a hash or secret). An
// empty before-ID (a created row) audits no before-state, exactly like
// the bootstrap RPC's envelope.
func breakGlassAudit(ctx context.Context, tx pgx.Tx, action string, before, after Employee) error {
	var prior any
	if before.ID != "" {
		prior = []byte(mustJSON(auditView(before)))
	}
	_, err := tx.Exec(ctx,
		`INSERT INTO audit_log (actor, action, target, before, after, origin_ip)
		 VALUES ($1, $2, $3, $4, $5, $6)`,
		breakGlassActor, action, after.ID, prior,
		[]byte(mustJSON(auditView(after))), breakGlassOrigin)
	return err
}

// changeOf snapshots the redacted outcome for the CLI.
func changeOf(role string, after Employee, action string, revoked int64) BreakglassChange {
	return BreakglassChange{
		Email:           after.Email,
		Role:            role,
		Actor:           breakGlassActor,
		Action:          action,
		SessionsRevoked: revoked,
		MustRotate:      after.MustRotate,
		TOTPBound:       after.TOTPSecret != "",
	}
}
