// Role storage and the Role management surface (ticket #54): the Role
// list with its grants, the grant editor (the only write path to
// role_permissions — the permission catalog itself is read-only), and
// Role deletion refusing seeded and in-use Roles. Grant edits
// invalidate the grant cache, so a revocation bites on the next
// request of every Employee holding the Role.
package admin

import (
	"context"
	"errors"
	"net/http"

	"github.com/jackc/pgx/v5"
)

var errRoleNotFound = errors.New("admin: role not found")

// Role is one roles row (GLOSSARY: Role).
type Role struct {
	ID     string
	Name   string
	System bool
}

// roleByID loads one Role; the not-found sentinel passes through.
func (s *Server) roleByID(ctx context.Context, id string) (Role, error) {
	var r Role
	err := s.pool.QueryRow(ctx,
		`SELECT id, name, system FROM roles WHERE id = $1`, id).
		Scan(&r.ID, &r.Name, &r.System)
	if errors.Is(err, pgx.ErrNoRows) {
		return Role{}, errRoleNotFound
	}
	return r, err
}

// roleView is the wire shape of GET /api/roles: the Role plus its
// granted permission strings — the editor's matrix column.
type roleView struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	System      bool     `json:"system"`
	Permissions []string `json:"permissions"`
}

// handleListRoles answers the seeded Roles with their live grants.
func (s *Server) handleListRoles(w http.ResponseWriter, r *http.Request) {
	rows, err := s.pool.Query(r.Context(),
		`SELECT ro.id, ro.name, ro.system,
		        COALESCE(array_agg(p.name ORDER BY p.name) FILTER (WHERE p.name IS NOT NULL), '{}')
		   FROM roles ro
		   LEFT JOIN role_permissions rp ON rp.role_id = ro.id
		   LEFT JOIN permissions p ON p.id = rp.permission_id
		  GROUP BY ro.id, ro.name, ro.system
		  ORDER BY ro.name`)
	if err != nil {
		writeErr(w, s.log, err)
		return
	}
	defer rows.Close()
	out := []roleView{}
	for rows.Next() {
		var v roleView
		if err := rows.Scan(&v.ID, &v.Name, &v.System, &v.Permissions); err != nil {
			writeErr(w, s.log, err)
			return
		}
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		writeErr(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

type grantRequest struct {
	Permission string `json:"permission"`
	Granted    bool   `json:"granted"`
}

// handleEditRoleGrants toggles one grant. The permission string must
// exist in the catalog — a UI-invented or renamed string would
// silently gate nothing, so it is refused (ADR-0012). The audit row's
// action names the direction (grant:role / revoke:role) with the
// before/after grant state.
func (s *Server) handleEditRoleGrants(w http.ResponseWriter, r *http.Request) {
	entry := auditEntry(r)
	var req grantRequest
	if !decodeBody(w, r, &req) {
		return
	}
	role, err := s.roleByID(r.Context(), r.PathValue("id"))
	if errors.Is(err, errRoleNotFound) {
		WriteError(w, http.StatusNotFound, "Role not found.")
		return
	}
	if err != nil {
		writeErr(w, s.log, err)
		return
	}
	had, err := s.setGrant(r.Context(), role.ID, req.Permission, req.Granted)
	if errors.Is(err, errUnknownPermission) {
		WriteError(w, http.StatusBadRequest, "That permission does not exist.")
		return
	}
	if err != nil {
		writeErr(w, s.log, err)
		return
	}
	s.invalidateGrants(role.ID)
	if entry != nil {
		entry.Target = role.ID
		if req.Granted {
			entry.Action = ActionGrantRole
		} else {
			entry.Action = ActionRevokeRole
		}
		entry.Before = mustJSON(grantState{Permission: req.Permission, Granted: had})
		entry.After = mustJSON(grantState{Permission: req.Permission, Granted: req.Granted})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"role":       role.Name,
		"permission": req.Permission,
		"granted":    req.Granted,
	})
}

// grantState is the redacted audit shape of one toggle.
type grantState struct {
	Permission string `json:"permission"`
	Granted    bool   `json:"granted"`
}

// errUnknownPermission marks a grant edit naming a string outside the
// catalog.
var errUnknownPermission = errors.New("admin: unknown permission")

// setGrant adds or removes one role_permissions row and reports the
// previous state for the audit envelope. Granting what is already
// granted (or revoking what is not) is an idempotent no-op.
func (s *Server) setGrant(ctx context.Context, roleID, permName string, granted bool) (had bool, err error) {
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var permID string
		err := tx.QueryRow(ctx,
			`SELECT id FROM permissions WHERE name = $1`, permName).Scan(&permID)
		if errors.Is(err, pgx.ErrNoRows) {
			return errUnknownPermission
		}
		if err != nil {
			return err
		}
		var one int
		err = tx.QueryRow(ctx,
			`SELECT 1 FROM role_permissions WHERE role_id = $1 AND permission_id = $2`,
			roleID, permID).Scan(&one)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			had = false
		case err != nil:
			return err
		default:
			had = true
		}
		if granted == had {
			return nil
		}
		if granted {
			_, err = tx.Exec(ctx,
				`INSERT INTO role_permissions (role_id, permission_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`,
				roleID, permID)
			return err
		}
		_, err = tx.Exec(ctx,
			`DELETE FROM role_permissions WHERE role_id = $1 AND permission_id = $2`,
			roleID, permID)
		return err
	})
	return had, err
}

// handleDeleteRole refuses the two undeletable shapes (ADR-0012): a
// seeded Role, and a Role held by any Employee. Seeded Roles keep
// editable grants; deletion is the only refused operation.
func (s *Server) handleDeleteRole(w http.ResponseWriter, r *http.Request) {
	entry := auditEntry(r)
	role, err := s.roleByID(r.Context(), r.PathValue("id"))
	if errors.Is(err, errRoleNotFound) {
		WriteError(w, http.StatusNotFound, "Role not found.")
		return
	}
	if err != nil {
		writeErr(w, s.log, err)
		return
	}
	if role.System {
		WriteError(w, http.StatusConflict, "Seeded roles cannot be deleted.")
		return
	}
	n, err := s.countEmployeesWithRole(r.Context(), role.ID)
	if err != nil {
		writeErr(w, s.log, err)
		return
	}
	if n > 0 {
		WriteError(w, http.StatusConflict, "This Role is held by an Employee and cannot be deleted.")
		return
	}
	if err := s.deleteRole(r.Context(), role.ID); err != nil {
		writeErr(w, s.log, err)
		return
	}
	s.invalidateGrants(role.ID)
	if entry != nil {
		entry.Target = role.ID
		entry.After = mustJSON(map[string]any{"id": role.ID, "name": role.Name, "system": role.System})
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// countEmployeesWithRole answers how many Employees hold the Role.
func (s *Server) countEmployeesWithRole(ctx context.Context, roleID string) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx,
		`SELECT count(*) FROM employees WHERE role_id = $1`, roleID).Scan(&n)
	return n, err
}

// deleteRole removes the Role and its grant rows (no FK cascade in the
// schema — the grants go first in the same transaction).
func (s *Server) deleteRole(ctx context.Context, roleID string) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx,
			`DELETE FROM role_permissions WHERE role_id = $1`, roleID); err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, `DELETE FROM roles WHERE id = $1`, roleID)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return errRoleNotFound
		}
		return nil
	})
}
