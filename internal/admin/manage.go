// Employee management endpoints (ticket #54): list, create (Step-up),
// enable/disable, password/TOTP reset (Step-up), and Role assignment
// — with the ADR-0012 guards: no self-disable, no self-role-removal,
// and the last enabled super-admin can neither be disabled nor lose
// the Role. The middleware writes every mutation's audit row; these
// handlers only fill the envelope (target id + redacted before/after).
package admin

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/syaikhipin/malxchange/internal/auth"
)

var errEmailTaken = errors.New("admin: email already taken")

// employeeListEntry is the redacted wire shape of the management list.
type employeeListEntry struct {
	ID         string    `json:"id"`
	Email      string    `json:"email"`
	RoleID     string    `json:"role_id"`
	Role       string    `json:"role"`
	Enabled    bool      `json:"enabled"`
	TOTPBound  bool      `json:"totp_bound"`
	MustRotate bool      `json:"must_rotate"`
	CreatedAt  time.Time `json:"created_at"`
}

// handleListEmployees answers the staff list, redacted — no hashes,
// no secrets, ever.
func (s *Server) handleListEmployees(w http.ResponseWriter, r *http.Request) {
	rows, err := s.pool.Query(r.Context(),
		`SELECT e.id, e.email, e.role_id, ro.name, e.enabled,
		        COALESCE(e.totp_secret, '') <> '', e.must_rotate, e.created_at
		   FROM employees e
		   JOIN roles ro ON ro.id = e.role_id
		  ORDER BY e.email`)
	if err != nil {
		writeErr(w, s.log, err)
		return
	}
	defer rows.Close()
	out := []employeeListEntry{}
	for rows.Next() {
		var e employeeListEntry
		if err := rows.Scan(&e.ID, &e.Email, &e.RoleID, &e.Role, &e.Enabled,
			&e.TOTPBound, &e.MustRotate, &e.CreatedAt); err != nil {
			writeErr(w, s.log, err)
			return
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		writeErr(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

type createEmployeeRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
	RoleID   string `json:"role_id"`
	Code     string `json:"code"`
}

// handleCreateEmployee provisions a new Employee (internal
// provisioning — no email-verification flow, ADR-0012) behind a fresh
// Step-up code. The password lands as a one-time credential
// (must_rotate): the Employee's first login forces rotation + TOTP
// enrollment before anything else.
func (s *Server) handleCreateEmployee(w http.ResponseWriter, r *http.Request) {
	entry := auditEntry(r)
	var req createEmployeeRequest
	if !decodeBody(w, r, &req) {
		return
	}
	if !s.requireStepUp(w, r, req.Code) {
		return
	}
	email, ok := normalizeEmail(w, req.Email)
	if !ok {
		return
	}
	if err := auth.CheckPasswordPolicy(req.Password); err != nil {
		WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	if _, err := s.roleByID(r.Context(), req.RoleID); errors.Is(err, errRoleNotFound) {
		WriteError(w, http.StatusBadRequest, "Choose a Role from the list.")
		return
	} else if err != nil {
		writeErr(w, s.log, err)
		return
	}
	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		writeErr(w, s.log, err)
		return
	}
	created, err := s.createEmployee(r.Context(), email, hash, req.RoleID)
	if errors.Is(err, errEmailTaken) {
		WriteError(w, http.StatusConflict, "An Employee with this email already exists.")
		return
	}
	if err != nil {
		writeErr(w, s.log, err)
		return
	}
	if entry != nil {
		entry.Target = created.ID
		entry.After = mustJSON(auditView(created))
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id":          created.ID,
		"email":       created.Email,
		"role":        s.roleName(r.Context(), created),
		"must_rotate": true,
	})
}

// createEmployee inserts the row; the unique email constraint maps to
// the sentinel.
func (s *Server) createEmployee(ctx context.Context, email, passwordHash, roleID string) (Employee, error) {
	emp := Employee{ID: uuid.NewString(), Email: email, RoleID: roleID, Enabled: true, MustRotate: true}
	_, err := s.pool.Exec(ctx,
		`INSERT INTO employees (id, email, password_hash, role_id, enabled, must_rotate)
		 VALUES ($1, $2, $3, $4, true, true)`,
		emp.ID, emp.Email, passwordHash, emp.RoleID)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return Employee{}, errEmailTaken
	}
	if err != nil {
		return Employee{}, err
	}
	return emp, nil
}

// targetEmployee loads the {id} path Employee and answers 404 itself.
func (s *Server) targetEmployee(w http.ResponseWriter, r *http.Request) (Employee, bool) {
	emp, err := s.employeeByID(r.Context(), r.PathValue("id"))
	if errors.Is(err, errEmployeeNotFound) {
		WriteError(w, http.StatusNotFound, "Employee not found.")
		return Employee{}, false
	}
	if err != nil {
		writeErr(w, s.log, err)
		return Employee{}, false
	}
	return emp, true
}

// soleEnabledSuperAdmin reports whether the target is the last enabled
// super-admin — the state the guards refuse to end (disabling or
// de-Roling it would lock the back office out, ADR-0012).
func (s *Server) soleEnabledSuperAdmin(ctx context.Context, target Employee) (bool, error) {
	if !target.Enabled {
		return false, nil
	}
	role, err := s.roleByID(ctx, target.RoleID)
	if errors.Is(err, errRoleNotFound) {
		return false, nil // unreachable behind the FK; refuse nothing
	}
	if err != nil {
		return false, err
	}
	if role.Name != RoleSuperAdmin {
		return false, nil
	}
	var n int
	if err := s.pool.QueryRow(ctx,
		`SELECT count(*)
		   FROM employees e JOIN roles ro ON ro.id = e.role_id
		  WHERE ro.name = $1 AND e.enabled`, RoleSuperAdmin).Scan(&n); err != nil {
		return false, err
	}
	return n <= 1, nil
}

// handleDisableEmployee refuses self-disable and the last enabled
// super-admin, then flips the flag and revokes the target's sessions
// in one transaction.
func (s *Server) handleDisableEmployee(w http.ResponseWriter, r *http.Request) {
	caller, _ := employeeFrom(r.Context())
	entry := auditEntry(r)
	target, ok := s.targetEmployee(w, r)
	if !ok {
		return
	}
	if target.ID == caller.ID {
		WriteError(w, http.StatusConflict, "You cannot disable your own account.")
		return
	}
	sole, err := s.soleEnabledSuperAdmin(r.Context(), target)
	if err != nil {
		writeErr(w, s.log, err)
		return
	}
	if sole {
		WriteError(w, http.StatusConflict, "The last enabled super-admin cannot be disabled.")
		return
	}
	before, after, err := s.setEmployeeEnabled(r.Context(), target.ID, false)
	if err != nil {
		writeErr(w, s.log, err)
		return
	}
	s.auditEmployeeChange(entry, before, after)
	writeJSON(w, http.StatusOK, map[string]any{"id": after.ID, "enabled": after.Enabled})
}

// handleEnableEmployee re-enables a disabled Employee; there is no
// guard — enabling is always safe.
func (s *Server) handleEnableEmployee(w http.ResponseWriter, r *http.Request) {
	entry := auditEntry(r)
	target, ok := s.targetEmployee(w, r)
	if !ok {
		return
	}
	before, after, err := s.setEmployeeEnabled(r.Context(), target.ID, true)
	if err != nil {
		writeErr(w, s.log, err)
		return
	}
	s.auditEmployeeChange(entry, before, after)
	writeJSON(w, http.StatusOK, map[string]any{"id": after.ID, "enabled": after.Enabled})
}

// auditEmployeeChange fills the envelope with the affected Employee
// and its redacted before/after views.
func (s *Server) auditEmployeeChange(entry *AuditEntry, before, after Employee) {
	if entry == nil {
		return
	}
	entry.Target = after.ID
	entry.Before = mustJSON(auditView(before))
	entry.After = mustJSON(auditView(after))
}

// setEmployeeEnabled flips enabled; a disable revokes every live
// session of the target in the same transaction.
func (s *Server) setEmployeeEnabled(ctx context.Context, id string, enabled bool) (before, after Employee, err error) {
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		row := tx.QueryRow(ctx,
			`SELECT `+employeeColumns+` FROM employees WHERE id = $1 FOR UPDATE`, id)
		before, err = scanEmployee(row)
		if err != nil {
			return err
		}
		after = before
		after.Enabled = enabled
		tag, err := tx.Exec(ctx,
			`UPDATE employees SET enabled = $2, updated_at = now() WHERE id = $1`, id, enabled)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return fmt.Errorf("admin: set enabled: employee %s vanished", id)
		}
		if !enabled {
			return revokeSessionsTx(ctx, tx, id)
		}
		return nil
	})
	return before, after, err
}

// revokeSessionsTx closes every live session of the Employee inside
// the caller's transaction (credential/flag changes are global
// sign-outs).
func revokeSessionsTx(ctx context.Context, tx pgx.Tx, employeeID string) error {
	_, err := tx.Exec(ctx,
		`UPDATE sessions SET revoked_at = now()
		  WHERE member_id = $1 AND revoked_at IS NULL AND expires_at > now()`, employeeID)
	return err
}

type resetPasswordRequest struct {
	Password string `json:"password"`
	Code     string `json:"code"`
}

// handleResetEmployeePassword resets another Employee's password
// behind a fresh Step-up code (password re-auth is never accepted).
// The replacement is a one-time credential: chosen in the body or
// generated server-side and shown exactly once; either way
// must_rotate forces the change at next login and every live session
// of the target dies.
func (s *Server) handleResetEmployeePassword(w http.ResponseWriter, r *http.Request) {
	entry := auditEntry(r)
	var req resetPasswordRequest
	if !decodeBody(w, r, &req) {
		return
	}
	if !s.requireStepUp(w, r, req.Code) {
		return
	}
	target, ok := s.targetEmployee(w, r)
	if !ok {
		return
	}
	password := req.Password
	generated := false
	if password == "" {
		pw, err := oneTimePassword()
		if err != nil {
			writeErr(w, s.log, err)
			return
		}
		password, generated = pw, true
	} else if err := auth.CheckPasswordPolicy(password); err != nil {
		WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	hash, err := auth.HashPassword(password)
	if err != nil {
		writeErr(w, s.log, err)
		return
	}
	before, after, err := s.resetEmployeePassword(r.Context(), target.ID, hash)
	if err != nil {
		writeErr(w, s.log, err)
		return
	}
	s.auditEmployeeChange(entry, before, after)
	resp := map[string]any{"id": after.ID, "must_rotate": true}
	if generated {
		resp["one_time_password"] = password
	}
	writeJSON(w, http.StatusOK, resp)
}

// oneTimePassword mints a random replacement credential (18 hex
// chars, ~72 bits) for server-generated resets.
func oneTimePassword() (string, error) {
	raw := make([]byte, 9)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("admin: one-time password: %w", err)
	}
	return hex.EncodeToString(raw), nil
}

// resetEmployeePassword stores the new hash, re-arms must_rotate, and
// revokes every live session of the Employee in one transaction.
func (s *Server) resetEmployeePassword(ctx context.Context, id, passwordHash string) (before, after Employee, err error) {
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		row := tx.QueryRow(ctx,
			`SELECT `+employeeColumns+` FROM employees WHERE id = $1 FOR UPDATE`, id)
		before, err = scanEmployee(row)
		if err != nil {
			return err
		}
		after = before
		after.PasswordHash = passwordHash
		after.MustRotate = true
		tag, err := tx.Exec(ctx,
			`UPDATE employees SET password_hash = $2, must_rotate = true, updated_at = now()
			  WHERE id = $1`, id, passwordHash)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return fmt.Errorf("admin: reset password: employee %s vanished", id)
		}
		return revokeSessionsTx(ctx, tx, id)
	})
	return before, after, err
}

type codeOnlyRequest struct {
	Code string `json:"code"`
}

// handleResetEmployeeTOTP clears another Employee's TOTP binding
// behind a fresh Step-up code. The target lands unprovisioned
// (rotation already done, binding gone), so the session rule forces
// re-enrollment before anything else — and their sessions die with
// the reset.
func (s *Server) handleResetEmployeeTOTP(w http.ResponseWriter, r *http.Request) {
	entry := auditEntry(r)
	var req codeOnlyRequest
	if !decodeBody(w, r, &req) {
		return
	}
	if !s.requireStepUp(w, r, req.Code) {
		return
	}
	target, ok := s.targetEmployee(w, r)
	if !ok {
		return
	}
	before, after, err := s.resetEmployeeTOTP(r.Context(), target.ID)
	if err != nil {
		writeErr(w, s.log, err)
		return
	}
	s.auditEmployeeChange(entry, before, after)
	writeJSON(w, http.StatusOK, map[string]any{"id": after.ID, "totp_bound": false})
}

// resetEmployeeTOTP clears confirmed and staged secrets and revokes
// every live session of the Employee in one transaction.
func (s *Server) resetEmployeeTOTP(ctx context.Context, id string) (before, after Employee, err error) {
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		row := tx.QueryRow(ctx,
			`SELECT `+employeeColumns+` FROM employees WHERE id = $1 FOR UPDATE`, id)
		before, err = scanEmployee(row)
		if err != nil {
			return err
		}
		after = before
		after.TOTPSecret = ""
		after.TOTPStaged = ""
		tag, err := tx.Exec(ctx,
			`UPDATE employees SET totp_secret = NULL, totp_staged = NULL, totp_bound_at = NULL,
			        updated_at = now()
			  WHERE id = $1`, id)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return fmt.Errorf("admin: reset totp: employee %s vanished", id)
		}
		return revokeSessionsTx(ctx, tx, id)
	})
	return before, after, err
}

type assignRoleRequest struct {
	RoleID string `json:"role_id"`
}

// handleAssignEmployeeRole moves an Employee to another Role —
// exactly one hat per person (ADR-0012). Guards: no self-role-removal
// (you cannot shed your own Role), and the last enabled super-admin
// cannot lose the super-admin Role. The target's next request already
// resolves the new grants — no re-login needed.
func (s *Server) handleAssignEmployeeRole(w http.ResponseWriter, r *http.Request) {
	caller, _ := employeeFrom(r.Context())
	entry := auditEntry(r)
	var req assignRoleRequest
	if !decodeBody(w, r, &req) {
		return
	}
	target, ok := s.targetEmployee(w, r)
	if !ok {
		return
	}
	newRole, err := s.roleByID(r.Context(), req.RoleID)
	if errors.Is(err, errRoleNotFound) {
		WriteError(w, http.StatusBadRequest, "Choose a Role from the list.")
		return
	}
	if err != nil {
		writeErr(w, s.log, err)
		return
	}
	if target.ID == caller.ID {
		WriteError(w, http.StatusConflict, "You cannot change your own Role.")
		return
	}
	if newRole.Name != RoleSuperAdmin {
		sole, err := s.soleEnabledSuperAdmin(r.Context(), target)
		if err != nil {
			writeErr(w, s.log, err)
			return
		}
		if sole {
			WriteError(w, http.StatusConflict, "The last enabled super-admin cannot lose the Role.")
			return
		}
	}
	before, after, err := s.assignEmployeeRole(r.Context(), target.ID, newRole.ID)
	if err != nil {
		writeErr(w, s.log, err)
		return
	}
	s.auditEmployeeChange(entry, before, after)
	writeJSON(w, http.StatusOK, map[string]any{"id": after.ID, "role": newRole.Name})
}

// assignEmployeeRole updates the single role_id FK.
func (s *Server) assignEmployeeRole(ctx context.Context, id, roleID string) (before, after Employee, err error) {
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		row := tx.QueryRow(ctx,
			`SELECT `+employeeColumns+` FROM employees WHERE id = $1 FOR UPDATE`, id)
		before, err = scanEmployee(row)
		if err != nil {
			return err
		}
		after = before
		after.RoleID = roleID
		tag, err := tx.Exec(ctx,
			`UPDATE employees SET role_id = $2, updated_at = now() WHERE id = $1`, id, roleID)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return fmt.Errorf("admin: assign role: employee %s vanished", id)
		}
		return nil
	})
	return before, after, err
}
