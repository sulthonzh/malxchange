//go:build integration

// RBAC and the Employee/Role management surface over the served HTTP
// seam (ticket #54, ADR-0012): the seeded catalog equals the registry's
// checked strings; the role matrix refuses every non-matrix Role on
// every registry endpoint; a grant revoked from a Role bites on the
// next request of a live session; the Employee-management guards
// (self-disable, self-role-removal, last-super-admin) refuse; Step-up
// demands a fresh TOTP and never accepts password re-auth; Role
// deletion refuses seeded and in-use Roles while grant edits succeed;
// and login/me carry the caller's live permission set. Table reads
// below are the black-box read of what boot seeding must have written.
package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
)

// freshCookie signs p in with a freshly computed code and returns the
// live session cookie.
func (p provisioned) freshCookie(t *testing.T, s *itStack) *http.Cookie {
	t.Helper()
	code, err := totpNow(p.TOTPSecr)
	if err != nil {
		t.Fatal(err)
	}
	status, resp, raw := p.login(t, s, code)
	if status != http.StatusOK {
		t.Fatalf("login %s: %d %s", p.Email, status, raw)
	}
	return sessionCookieOf(t, resp)
}

// wrongCodeOf mangles one digit of the current code: well-formed, but
// verifiably invalid.
func wrongCodeOf(t *testing.T, secret string) string {
	t.Helper()
	code, err := totpNow(secret)
	if err != nil {
		t.Fatal(err)
	}
	if code[0] == '0' {
		return "1" + code[1:]
	}
	return "0" + code[1:]
}

// roleIDOf resolves a Role name through GET /api/roles.
func (s *itStack) roleIDOf(t *testing.T, cookie *http.Cookie, name string) string {
	t.Helper()
	status, _, raw := s.do(t, "GET", "/api/roles", "", cookie)
	if status != http.StatusOK {
		t.Fatalf("list roles: %d %s", status, raw)
	}
	var roles []roleView
	if err := json.Unmarshal([]byte(raw), &roles); err != nil {
		t.Fatal(err)
	}
	for _, r := range roles {
		if r.Name == name {
			return r.ID
		}
	}
	t.Fatalf("role %q not found among %s", name, raw)
	return ""
}

// setGrant toggles one grant through the editor endpoint.
func (s *itStack) setGrant(t *testing.T, cookie *http.Cookie, roleID, perm string, granted bool) {
	t.Helper()
	body := fmt.Sprintf(`{"permission":%q,"granted":%t}`, perm, granted)
	status, _, raw := s.post(t, "/api/roles/"+roleID+"/grants", body, cookie)
	if status != http.StatusOK {
		t.Fatalf("set grant %s=%t on %s: %d %s", perm, granted, roleID, status, raw)
	}
}

// employee creates a fully provisioned Employee in a seeded Role through
// the management surface (Step-up create + forced setup) — the exact
// journey the admin app drives.
func (s *itStack) employee(t *testing.T, root provisioned, rootCookie *http.Cookie, email, roleName string) provisioned {
	t.Helper()
	roleID := s.roleIDOf(t, rootCookie, roleName)
	code, err := totpNow(root.TOTPSecr)
	if err != nil {
		t.Fatal(err)
	}
	body := fmt.Sprintf(`{"email":%q,"password":"employee-one-time-1","role_id":%q,"code":%q}`, email, roleID, code)
	status, _, raw := s.post(t, "/api/employees", body, rootCookie)
	if status != http.StatusOK {
		t.Fatalf("create employee %s: %d %s", email, status, raw)
	}
	return s.finishSetup(t, email, "employee-one-time-1")
}

func TestCatalogSeededFromRegistry(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()

	// The catalog IS the endpoint list's checked strings — nothing
	// more (no UI-invented strings), nothing less (no dropped seed).
	got := []string{}
	rows, err := s.pool.Query(ctx, `SELECT name FROM permissions ORDER BY name`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		got = append(got, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if want := registeredPermissions(); !slices.Equal(got, want) {
		t.Fatalf("seeded catalog %v != registry strings %v", got, want)
	}

	// The four seeded Roles, all flagged system (undeletable).
	type roleRow struct {
		Name   string
		System bool
	}
	roleRows := []roleRow{}
	rows, err = s.pool.Query(ctx, `SELECT name, system FROM roles ORDER BY name`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var r roleRow
		if err := rows.Scan(&r.Name, &r.System); err != nil {
			t.Fatal(err)
		}
		roleRows = append(roleRows, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(roleRows) != len(seededRoles) {
		t.Fatalf("want %d seeded roles, got %v", len(seededRoles), roleRows)
	}
	for i, r := range roleRows {
		want := append([]string(nil), seededRoles...) // set compare: SQL orders by name
		slices.Sort(want)
		if r.Name != want[i] || !r.System {
			t.Fatalf("seeded role %d = %+v, want %q system=true", i, r, want[i])
		}
	}

	// Every matrix grant landed exactly once.
	for _, e := range Endpoints {
		for _, role := range e.Matrix {
			var n int
			if err := s.pool.QueryRow(ctx,
				`SELECT count(*)
				   FROM role_permissions rp
				   JOIN roles r ON r.id = rp.role_id
				   JOIN permissions p ON p.id = rp.permission_id
				  WHERE r.name = $1 AND p.name = $2`, role, e.Perm).Scan(&n); err != nil {
				t.Fatal(err)
			}
			if n != 1 {
				t.Fatalf("matrix grant %s → %s seeded %d times", role, e.Perm, n)
			}
		}
	}

	// Re-seeding (the next boot) is idempotent and never reverts
	// operator grant edits: a wiped grant table stays wiped.
	if _, err := s.pool.Exec(ctx, `DELETE FROM role_permissions`); err != nil {
		t.Fatal(err)
	}
	if err := NewServer(s.pool, s.env, nil).SeedRBAC(ctx); err != nil {
		t.Fatalf("re-seed: %v", err)
	}
	var n int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM role_permissions`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("re-seed re-granted %d removed grants", n)
	}
}

// TestRoleMatrixRefusesNonMatrixRoles walks every registry endpoint
// with a live session of every Role NOT in its matrix and demands the
// permission refusal. The loop is over the registry, so endpoints
// added by later tickets join the matrix test automatically; their
// positive halves live in each surface's own journeys.
func TestRoleMatrixRefusesNonMatrixRoles(t *testing.T) {
	s := newStack(t)
	root := s.fullyProvisioned(t, "matrix-root@it.test")
	rootCookie := root.freshCookie(t, s)

	actors := map[string]*http.Cookie{RoleSuperAdmin: rootCookie}
	for _, name := range []string{RoleFinance, RoleCompliance, RoleSupport} {
		p := s.employee(t, root, rootCookie, "matrix-"+name+"@it.test", name)
		actors[name] = p.freshCookie(t, s)
	}

	for _, e := range Endpoints {
		for role, cookie := range actors {
			if slices.Contains(e.Matrix, role) {
				continue
			}
			path := strings.ReplaceAll(e.Path, "{id}", "00000000-0000-0000-0000-000000000000")
			body := ""
			if e.Method != http.MethodGet {
				body = "{}"
			}
			status, _, raw := s.do(t, e.Method, path, body, cookie)
			if status != http.StatusForbidden || !strings.Contains(raw, "You do not have permission") {
				t.Fatalf("%s %s as %s: %d %s", e.Method, e.Path, role, status, raw)
			}
		}
	}

	// No session at all is 401, not the permission 403.
	if status, _, _ := s.do(t, "GET", "/api/employees", "", nil); status != http.StatusUnauthorized {
		t.Fatalf("unauthenticated list employees: %d", status)
	}
}

func TestGrantRevocationBitesNextRequest(t *testing.T) {
	s := newStack(t)
	root := s.fullyProvisioned(t, "revoke-root@it.test")
	cookie := root.freshCookie(t, s)

	if status, _, raw := s.do(t, "GET", "/api/employees", "", cookie); status != http.StatusOK {
		t.Fatalf("list before revoke: %d %s", status, raw)
	}

	// Revoke the caller's own Role's grant — the editor works on
	// seeded Roles (grants editable, deletion not).
	superID := s.roleIDOf(t, cookie, RoleSuperAdmin)
	s.setGrant(t, cookie, superID, PermViewEmployee, false)

	// The SAME live session: the next request is refused mid-session.
	status, _, raw := s.do(t, "GET", "/api/employees", "", cookie)
	if status != http.StatusForbidden || !strings.Contains(raw, "You do not have permission") {
		t.Fatalf("list after revoke: %d %s", status, raw)
	}

	// /api/me reflects the live set: the revoked string is gone.
	status, _, raw = s.do(t, "GET", "/api/me", "", cookie)
	if status != http.StatusOK {
		t.Fatalf("me after revoke: %d %s", status, raw)
	}
	var me struct {
		Permissions []string `json:"permissions"`
	}
	if err := json.Unmarshal([]byte(raw), &me); err != nil {
		t.Fatal(err)
	}
	if slices.Contains(me.Permissions, PermViewEmployee) {
		t.Fatalf("me still carries revoked %s: %v", PermViewEmployee, me.Permissions)
	}

	// Granting back bites just as fast.
	s.setGrant(t, cookie, superID, PermViewEmployee, true)
	if status, _, raw := s.do(t, "GET", "/api/employees", "", cookie); status != http.StatusOK {
		t.Fatalf("list after re-grant: %d %s", status, raw)
	}

	// Both toggles are audited with the direction as the action.
	for _, action := range []string{ActionRevokeRole, ActionGrantRole} {
		rows := s.auditRows(t, action)
		if len(rows) != 1 {
			t.Fatalf("want 1 %s row, got %d", action, len(rows))
		}
		var state grantState
		if err := json.Unmarshal(rows[0].After, &state); err != nil {
			t.Fatal(err)
		}
		if state.Permission != PermViewEmployee || rows[0].Target != superID {
			t.Fatalf("%s row = %+v target %q", action, state, rows[0].Target)
		}
		if (action == ActionRevokeRole) == state.Granted {
			t.Fatalf("%s row after.granted = %v", action, state.Granted)
		}
	}
}

func TestEmployeeGuardsRefused(t *testing.T) {
	s := newStack(t)
	root := s.fullyProvisioned(t, "guard-root@it.test")
	cookie := root.freshCookie(t, s)

	// Self-disable.
	status, _, raw := s.post(t, "/api/employees/"+root.EmpID+"/disable", "{}", cookie)
	if status != http.StatusConflict || !strings.Contains(raw, "own account") {
		t.Fatalf("self-disable: %d %s", status, raw)
	}

	// Self-role-removal.
	financeID := s.roleIDOf(t, cookie, RoleFinance)
	status, _, raw = s.post(t, "/api/employees/"+root.EmpID+"/role",
		fmt.Sprintf(`{"role_id":%q}`, financeID), cookie)
	if status != http.StatusConflict || !strings.Contains(raw, "own Role") {
		t.Fatalf("self-role-removal: %d %s", status, raw)
	}

	// The last-super-admin guards need an actor who is NOT the sole
	// enabled super-admin but holds the permission — reachable only by
	// granting the matrix strings to another Role, which is exactly
	// what the editor is for.
	s.setGrant(t, cookie, financeID, PermDisableEmployee, true)
	s.setGrant(t, cookie, financeID, PermAssignEmployeeRole, true)
	fin := s.employee(t, root, cookie, "guard-finance@it.test", RoleFinance)
	finCookie := fin.freshCookie(t, s)

	status, _, raw = s.post(t, "/api/employees/"+root.EmpID+"/disable", "{}", finCookie)
	if status != http.StatusConflict || !strings.Contains(raw, "last enabled super-admin") {
		t.Fatalf("last-super-admin disable: %d %s", status, raw)
	}
	status, _, raw = s.post(t, "/api/employees/"+root.EmpID+"/role",
		fmt.Sprintf(`{"role_id":%q}`, financeID), finCookie)
	if status != http.StatusConflict || !strings.Contains(raw, "last enabled super-admin") {
		t.Fatalf("last-super-admin de-role: %d %s", status, raw)
	}

	// Refused guards write no audit rows (they changed nothing).
	for _, action := range []string{PermDisableEmployee, PermAssignEmployeeRole} {
		if rows := s.auditRows(t, action); len(rows) != 0 {
			t.Fatalf("refused %s wrote %d audit rows", action, len(rows))
		}
	}
}

func TestEmployeeLifecycleWithStepUp(t *testing.T) {
	s := newStack(t)
	root := s.fullyProvisioned(t, "life-root@it.test")
	cookie := root.freshCookie(t, s)
	financeID := s.roleIDOf(t, cookie, RoleFinance)
	supportID := s.roleIDOf(t, cookie, RoleSupport)
	const email = "life-employee@it.test"
	createBody := func(code string) string {
		return fmt.Sprintf(`{"email":%q,"password":"employee-one-time-1","role_id":%q,"code":%q}`,
			email, financeID, code)
	}

	// Step-up comes before anything: no code, the caller's PASSWORD in
	// the code field, and a well-formed wrong code are all refused.
	// Password re-auth is never Step-up (ADR-0012).
	if status, _, raw := s.post(t, "/api/employees", createBody(""), cookie); status != http.StatusUnauthorized {
		t.Fatalf("create without code: %d %s", status, raw)
	}
	if status, _, raw := s.post(t, "/api/employees", createBody(root.Password), cookie); status != http.StatusUnauthorized {
		t.Fatalf("create with password as code: %d %s", status, raw)
	}
	if status, _, raw := s.post(t, "/api/employees", createBody(wrongCodeOf(t, root.TOTPSecr)), cookie); status != http.StatusUnauthorized {
		t.Fatalf("create with wrong code: %d %s", status, raw)
	}

	// Fresh code: created as an unprovisioned Employee.
	code, err := totpNow(root.TOTPSecr)
	if err != nil {
		t.Fatal(err)
	}
	status, _, raw := s.post(t, "/api/employees", createBody(code), cookie)
	if status != http.StatusOK {
		t.Fatalf("create with fresh code: %d %s", status, raw)
	}
	var created struct {
		ID         string `json:"id"`
		MustRotate bool   `json:"must_rotate"`
		Role       string `json:"role"`
	}
	if err := json.Unmarshal([]byte(raw), &created); err != nil || !created.MustRotate || created.Role != RoleFinance {
		t.Fatalf("create body %s err %v", raw, err)
	}

	// Duplicate email is a conflict.
	s.clearConsumedStep(t, root.Email) // the create above consumed the step (#103)
	code, err = totpNow(root.TOTPSecr)
	if err != nil {
		t.Fatal(err)
	}
	if status, _, raw := s.post(t, "/api/employees", createBody(code), cookie); status != http.StatusConflict {
		t.Fatalf("duplicate email: %d %s", status, raw)
	}

	employee := s.finishSetup(t, email, "employee-one-time-1")

	// Disable: the target's sessions are revoked with the flag (one
	// transaction), login is refused as disabled.
	employeeCookie := employee.freshCookie(t, s)
	if status, _, raw := s.post(t, "/api/employees/"+employee.EmpID+"/disable", "{}", cookie); status != http.StatusOK {
		t.Fatalf("disable: %d %s", status, raw)
	}
	if status, _, _ := s.do(t, "GET", "/api/me", "", employeeCookie); status != http.StatusUnauthorized {
		t.Fatalf("session survived disable: %d", status)
	}
	if status, _, _ := employee.login(t, s, ""); status != http.StatusForbidden {
		t.Fatalf("disabled login: %d", status)
	}
	if status, _, raw := s.post(t, "/api/employees/"+employee.EmpID+"/enable", "{}", cookie); status != http.StatusOK {
		t.Fatalf("enable: %d %s", status, raw)
	}
	if status, _, raw := s.post(t, "/api/employees/"+employee.EmpID+"/reset-password", "{}", cookie); status != http.StatusUnauthorized {
		t.Fatalf("reset-password without code: %d %s", status, raw)
	}
	if status, _, raw := s.post(t, "/api/employees/"+employee.EmpID+"/reset-password",
		fmt.Sprintf(`{"code":%q}`, wrongCodeOf(t, root.TOTPSecr)), cookie); status != http.StatusUnauthorized {
		t.Fatalf("reset-password with wrong code: %d %s", status, raw)
	}
	s.clearConsumedStep(t, root.Email) // earlier step-ups consumed their steps (#103)
	code, err = totpNow(root.TOTPSecr)
	if err != nil {
		t.Fatal(err)
	}
	status, _, raw = s.post(t, "/api/employees/"+employee.EmpID+"/reset-password",
		fmt.Sprintf(`{"code":%q}`, code), cookie)
	if status != http.StatusOK {
		t.Fatalf("reset-password with fresh code: %d %s", status, raw)
	}
	var reset struct {
		OneTimePassword string `json:"one_time_password"`
	}
	if err := json.Unmarshal([]byte(raw), &reset); err != nil || reset.OneTimePassword == "" {
		t.Fatalf("reset body %s err %v", raw, err)
	}

	// The old password is dead (valid code, wrong password → 401); the
	// one-time credential signs in (restricted: rotation forced again).
	if status, _, _ := employee.login(t, s, mustTOTP(t, employee.TOTPSecr)); status != http.StatusUnauthorized {
		t.Fatalf("old password after reset: %d", status)
	}
	employee.Password = reset.OneTimePassword
	if status, _, _ := employee.login(t, s, mustTOTP(t, employee.TOTPSecr)); status != http.StatusOK {
		t.Fatalf("one-time password after reset: %d", status)
	}

	// TOTP reset (Step-up): binding gone, list shows it.
	s.clearConsumedStep(t, root.Email) // earlier step-ups consumed their steps (#103)
	code, err = totpNow(root.TOTPSecr)
	if err != nil {
		t.Fatal(err)
	}
	if status, _, raw := s.post(t, "/api/employees/"+employee.EmpID+"/reset-totp",
		fmt.Sprintf(`{"code":%q}`, code), cookie); status != http.StatusOK {
		t.Fatalf("reset-totp: %d %s", status, raw)
	}

	// Role assignment lands and is visible.
	if status, _, raw := s.post(t, "/api/employees/"+employee.EmpID+"/role",
		fmt.Sprintf(`{"role_id":%q}`, supportID), cookie); status != http.StatusOK {
		t.Fatalf("assign role: %d %s", status, raw)
	}
	status, _, raw = s.do(t, "GET", "/api/employees", "", cookie)
	if status != http.StatusOK {
		t.Fatalf("list employees: %d %s", status, raw)
	}
	var list []employeeListEntry
	if err := json.Unmarshal([]byte(raw), &list); err != nil {
		t.Fatal(err)
	}
	var entry *employeeListEntry
	for i := range list {
		if list[i].Email == email {
			entry = &list[i]
		}
	}
	if entry == nil || entry.Role != RoleSupport || entry.TOTPBound || !entry.MustRotate {
		t.Fatalf("employee list entry = %+v", entry)
	}

	// Every lifecycle mutation produced its middleware-written audit
	// row, named for the affected Employee.
	for _, action := range []string{
		PermCreateEmployee, PermDisableEmployee, PermEnableEmployee,
		PermResetEmployeePassword, PermResetEmployeeTOTP, PermAssignEmployeeRole,
	} {
		found := false
		for _, row := range s.auditRows(t, action) {
			if row.Target == employee.EmpID {
				found = true
			}
		}
		if !found {
			t.Fatalf("no %s audit row for %s", action, employee.EmpID)
		}
	}
}

func TestRoleEndpointsGrantEditAndDeleteRefusals(t *testing.T) {
	s := newStack(t)
	root := s.fullyProvisioned(t, "roles-root@it.test")
	cookie := root.freshCookie(t, s)
	ctx := context.Background()

	// The list: four seeded Roles, all system, each carrying at least
	// its matrix grants (the editor's matrix column).
	status, _, raw := s.do(t, "GET", "/api/roles", "", cookie)
	if status != http.StatusOK {
		t.Fatalf("list roles: %d %s", status, raw)
	}
	var roles []roleView
	if err := json.Unmarshal([]byte(raw), &roles); err != nil {
		t.Fatal(err)
	}
	if len(roles) != len(seededRoles) {
		t.Fatalf("want %d roles, got %s", len(seededRoles), raw)
	}
	for _, e := range Endpoints {
		for _, r := range roles {
			if slices.Contains(e.Matrix, r.Name) && !slices.Contains(r.Permissions, e.Perm) {
				t.Fatalf("role %q misses matrix grant %s", r.Name, e.Perm)
			}
		}
	}

	// No UI-invented strings: a grant edit naming an unknown string is
	// refused, not silently stored.
	superID := s.roleIDOf(t, cookie, RoleSuperAdmin)
	if status, _, raw := s.post(t, "/api/roles/"+superID+"/grants",
		`{"permission":"invent:nothing","granted":true}`, cookie); status != http.StatusBadRequest {
		t.Fatalf("invented permission: %d %s", status, raw)
	}

	// Seeded Roles refuse deletion everywhere.
	for _, name := range seededRoles {
		id := s.roleIDOf(t, cookie, name)
		status, _, raw := s.do(t, "DELETE", "/api/roles/"+id, "", cookie)
		if status != http.StatusConflict || !strings.Contains(raw, "Seeded roles") {
			t.Fatalf("delete seeded %s: %d %s", name, status, raw)
		}
	}

	// Out-of-band fixtures (the break-glass CLI's register, #55): an
	// in-use custom Role refuses deletion; an unused one deletes.
	if _, err := s.pool.Exec(ctx,
		`INSERT INTO roles (id, name, system) VALUES
		 ('it-role-auditor', 'it-auditor', false),
		 ('it-role-unused', 'it-unused', false)`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx,
		`INSERT INTO employees (id, email, password_hash, role_id, enabled, must_rotate)
		 VALUES ('it-emp-holder', 'holder@it.test', 'x', 'it-role-auditor', true, false)`); err != nil {
		t.Fatal(err)
	}
	// The list marks held Roles in use — the UI's undeletable signal
	// alongside `system` for the seeded ones.
	status, _, raw = s.do(t, "GET", "/api/roles", "", cookie)
	if status != http.StatusOK {
		t.Fatalf("re-list roles: %d %s", status, raw)
	}
	if err := json.Unmarshal([]byte(raw), &roles); err != nil {
		t.Fatal(err)
	}
	inUse := map[string]bool{}
	for _, r := range roles {
		inUse[r.Name] = r.InUse
	}
	if !inUse[RoleSuperAdmin] || !inUse["it-auditor"] {
		t.Fatalf("held Roles must read in_use: %v", inUse)
	}
	if inUse["it-unused"] || inUse[RoleFinance] || inUse[RoleCompliance] || inUse[RoleSupport] {
		t.Fatalf("unheld Roles must not read in_use: %v", inUse)
	}

	if status, _, raw := s.do(t, "DELETE", "/api/roles/it-role-auditor", "", cookie); status != http.StatusConflict || !strings.Contains(raw, "held by an Employee") {
		t.Fatalf("delete in-use role: %d %s", status, raw)
	}
	if status, _, raw := s.do(t, "DELETE", "/api/roles/it-role-unused", "", cookie); status != http.StatusOK {
		t.Fatalf("delete unused role: %d %s", status, raw)
	}
	var n int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM roles WHERE id = 'it-role-unused'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatal("unused role survived deletion")
	}
}

func TestLoginAndMeCarryLivePermissionSet(t *testing.T) {
	s := newStack(t)
	p := s.fullyProvisioned(t, "perms@it.test")

	want := []string{}
	for _, e := range Endpoints {
		if slices.Contains(e.Matrix, RoleSuperAdmin) {
			want = append(want, e.Perm)
		}
	}
	slices.Sort(want)

	status, resp, raw := p.login(t, s, mustTOTP(t, p.TOTPSecr))
	if status != http.StatusOK {
		t.Fatalf("login: %d %s", status, raw)
	}
	var body struct {
		Permissions []string `json:"permissions"`
	}
	if err := json.Unmarshal([]byte(raw), &body); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(body.Permissions, want) {
		t.Fatalf("login permissions %v != live set %v", body.Permissions, want)
	}

	cookie := sessionCookieOf(t, resp)
	status, _, raw = s.do(t, "GET", "/api/me", "", cookie)
	if status != http.StatusOK {
		t.Fatalf("me: %d %s", status, raw)
	}
	if err := json.Unmarshal([]byte(raw), &body); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(body.Permissions, want) {
		t.Fatalf("me permissions %v != live set %v", body.Permissions, want)
	}
}

// mustTOTP computes the current code or fails the test.
func mustTOTP(t *testing.T, secret string) string {
	t.Helper()
	code, err := totpNow(secret)
	if err != nil {
		t.Fatal(err)
	}
	return code
}
