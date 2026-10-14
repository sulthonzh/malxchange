package admin

import (
	"regexp"
	"slices"
	"testing"
)

// permissionStringRE pins the 'verb:resource' shape (ADR-0012): verb
// first, resource singular, both lower-case with hyphenated compounds.
var permissionStringRE = regexp.MustCompile(`^[a-z]+(-[a-z]+)*:[a-z]+(-[a-z]+)*$`)

// The registry is the single seam the admin surface grows through, so
// its invariants are load-bearing for every later ticket: exactly one
// endpoint per permission string (a string exists because a handler
// checks it — and only one), no duplicated routes, matrix Roles are
// the seeded ones, and every entry carries a handler.
func TestRegistryOneEndpointPerPermissionString(t *testing.T) {
	perms := map[string]bool{}
	routes := map[string]bool{}
	for _, e := range Endpoints {
		if e.Handler == nil {
			t.Fatalf("endpoint for %q has no handler", e.Perm)
		}
		if !permissionStringRE.MatchString(e.Perm) {
			t.Fatalf("permission %q is not verb:resource shaped", e.Perm)
		}
		if perms[e.Perm] {
			t.Fatalf("permission %q is checked by more than one endpoint", e.Perm)
		}
		perms[e.Perm] = true
		route := e.Method + " " + e.Path
		if routes[route] {
			t.Fatalf("route %q registered twice", route)
		}
		routes[route] = true
		if len(e.Matrix) == 0 {
			t.Fatalf("endpoint %q names no seeded Role in its matrix", e.Perm)
		}
		for _, role := range e.Matrix {
			if !slices.Contains(seededRoles, role) {
				t.Fatalf("endpoint %q matrix names unknown Role %q", e.Perm, role)
			}
		}
	}
	if len(Endpoints) == 0 {
		t.Fatal("the registry is empty")
	}
}

// The four seeded Roles are ADR-0012's matrix — the catalog and grant
// seeding key off these names.
func TestSeededRolesMatchADR0012Matrix(t *testing.T) {
	want := []string{RoleSuperAdmin, RoleFinance, RoleCompliance, RoleSupport}
	if !slices.Equal(seededRoles, want) {
		t.Fatalf("seededRoles = %v, want %v", seededRoles, want)
	}
	if len(want) != len(map[string]bool{want[0]: true, want[1]: true, want[2]: true, want[3]: true}) {
		t.Fatal("seeded role names are not unique")
	}
}
