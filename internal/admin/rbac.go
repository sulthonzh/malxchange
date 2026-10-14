// RBAC core (ADR-0012): the four seeded Roles, the idempotent boot
// seeding of the permission catalog and the matrix grants, per-request
// grant resolution from the Employee's single Role behind a
// short-lived per-process cache invalidated on every grant edit — a
// revoked grant bites on the next request, mid-session — and the
// requirePerm middleware.
package admin

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"net/http"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// The seeded Role names — ADR-0012's matrix. super-admin adds Coin and
// Market configuration, Code channels, Employee management, and the
// read side; the fund, identity, and member/content verbs land with
// their surfaces (#58-#63) and their registry entries carry the
// matrix rows.
const (
	RoleSuperAdmin = "super-admin"
	RoleFinance    = "finance"
	RoleCompliance = "compliance"
	RoleSupport    = "support"
)

// seededRoles is the boot-seeded Role list, in matrix order.
var seededRoles = []string{RoleSuperAdmin, RoleFinance, RoleCompliance, RoleSupport}

// grantCacheTTL bounds how long a resolved grant set may serve. Edits
// invalidate exactly and immediately (see invalidateGrants); the TTL
// only self-heels drift from out-of-band writes (a future break-glass
// path, direct SQL) and keeps lookups off the hot path otherwise.
const grantCacheTTL = 30 * time.Second

// errNoPermission is the authorization refusal. It is distinct from
// the restricted-session refusal so tests and the UI can tell an
// unprovisioned Employee from an unauthorized one apart.
const errNoPermission = "You do not have permission to do that."

type (
	// grantCache memoizes roleID → granted permission strings per
	// process. The dev/prod topology runs one admin-api instance, so
	// in-process invalidation is exact.
	grantCache struct {
		mu   sync.RWMutex
		sets map[string]grantSet
	}
	grantSet struct {
		perms map[string]struct{}
		at    time.Time
	}
)

// grantsFor resolves the Employee's live grants from their single
// Role: the cache while fresh, else the database.
func (s *Server) grantsFor(ctx context.Context, roleID string) (map[string]struct{}, error) {
	s.grants.mu.RLock()
	set, ok := s.grants.sets[roleID]
	s.grants.mu.RUnlock()
	if ok && time.Since(set.at) < grantCacheTTL {
		return set.perms, nil
	}
	perms, err := s.loadGrants(ctx, roleID)
	if err != nil {
		return nil, err
	}
	s.grants.mu.Lock()
	s.grants.sets[roleID] = grantSet{perms: perms, at: time.Now()}
	s.grants.mu.Unlock()
	return perms, nil
}

// loadGrants is the uncached resolution: one indexed join.
func (s *Server) loadGrants(ctx context.Context, roleID string) (map[string]struct{}, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT p.name
		   FROM role_permissions rp
		   JOIN permissions p ON p.id = rp.permission_id
		  WHERE rp.role_id = $1`, roleID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	perms := map[string]struct{}{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		perms[name] = struct{}{}
	}
	return perms, rows.Err()
}

// invalidateGrants drops a Role's cached grant set; the next request
// re-resolves from the database — revocation bites immediately.
func (s *Server) invalidateGrants(roleID string) {
	s.grants.mu.Lock()
	delete(s.grants.sets, roleID)
	s.grants.mu.Unlock()
}

// permissionList is the Employee's live permission set for the login
// and me views: sorted, never nil (the sidebar derives from it).
func (s *Server) permissionList(ctx context.Context, roleID string) []string {
	perms, err := s.grantsFor(ctx, roleID)
	if err != nil {
		s.log.Error("admin: resolve permissions", "err", err)
		return []string{}
	}
	out := make([]string, 0, len(perms))
	for name := range perms {
		out = append(out, name)
	}
	slices.Sort(out)
	return out
}

// requirePerm is the server-side gate every permissioned endpoint
// runs behind: it resolves the caller's grants from their Role for
// THIS request and refuses without them. The UI never gates anything.
func (s *Server) requirePerm(perm string, next func(http.ResponseWriter, *http.Request)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		emp, ok := employeeFrom(r.Context())
		if !ok { // unreachable behind withSession; refuse closed anyway
			WriteError(w, http.StatusUnauthorized, "Sign in to continue.")
			return
		}
		perms, err := s.grantsFor(r.Context(), emp.RoleID)
		if err != nil {
			writeErr(w, s.log, err)
			return
		}
		if _, has := perms[perm]; !has {
			WriteError(w, http.StatusForbidden, errNoPermission)
			return
		}
		next(w, r)
	}
}

// SeedRBAC is the idempotent boot seeding: the four Roles, then — for
// every registry entry — the permission string and its matrix grants.
// A permission row is only ever INSERTed when new (ON CONFLICT DO
// NOTHING RETURNING): a string that already exists never re-grants,
// so operator edits made through the grant editor survive reboots.
// The whole pass shares one transaction behind an advisory lock, so
// concurrent boots cannot interleave.
func (s *Server) SeedRBAC(ctx context.Context) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('admin:seed:rbac'))`); err != nil {
			return err
		}
		for _, name := range seededRoles {
			if _, err := tx.Exec(ctx,
				`INSERT INTO roles (id, name, system) VALUES ($1, $2, true)
				 ON CONFLICT (name) DO NOTHING`,
				uuid.NewString(), name); err != nil {
				return fmt.Errorf("admin: seed role %s: %w", name, err)
			}
		}
		for _, e := range Endpoints {
			var permID string
			err := tx.QueryRow(ctx,
				`INSERT INTO permissions (id, name) VALUES ($1, $2)
				 ON CONFLICT (name) DO NOTHING RETURNING id`,
				uuid.NewString(), e.Perm).Scan(&permID)
			if errors.Is(err, pgx.ErrNoRows) {
				continue // already cataloged; grant edits are never reverted
			}
			if err != nil {
				return fmt.Errorf("admin: seed permission %s: %w", e.Perm, err)
			}
			for _, role := range e.Matrix {
				if _, err := tx.Exec(ctx,
					`INSERT INTO role_permissions (role_id, permission_id)
					 SELECT id, $2 FROM roles WHERE name = $1
					 ON CONFLICT DO NOTHING`,
					role, permID); err != nil {
					return fmt.Errorf("admin: seed grant %s → %s: %w", role, e.Perm, err)
				}
			}
		}
		return nil
	})
}

// registeredPermissions is the registry's checked strings — the exact
// set the catalog must equal. System tests assert the seeded catalog
// against it.
func registeredPermissions() []string {
	out := make([]string, 0, len(Endpoints))
	for _, e := range Endpoints {
		out = append(out, e.Perm)
	}
	slices.Sort(out)
	return out
}
