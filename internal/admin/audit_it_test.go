//go:build integration

// Audit log search and the permission catalog read over the served
// HTTP seam (ticket #57): the search endpoint answers the four filters
// (actor, action, target, time range) with rows carrying actor, action
// (verb:resource), target, origin IP, and the redacted before/after
// JSON; the catalog endpoint answers exactly the registry's checked
// strings — the grant editor's read-only row set. Non-matrix Roles are
// refused by the shared matrix loop in rbac_it_test.go; here the
// positive half proves a matrix grant lands through the editor and
// bites immediately.
package admin

import (
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"
)

func searchAudit(t *testing.T, s *itStack, cookie *http.Cookie, query string) (int, auditSearchResponse, string) {
	t.Helper()
	status, _, raw := s.do(t, "GET", "/api/audit-log"+query, "", cookie)
	var out auditSearchResponse
	if status == http.StatusOK {
		if err := json.Unmarshal([]byte(raw), &out); err != nil {
			t.Fatalf("search body %s: %v", raw, err)
		}
	}
	return status, out, raw
}

func TestAuditLogSearchFilters(t *testing.T) {
	s := newStack(t)
	root := s.fullyProvisioned(t, "audit-root@it.test")
	cookie := root.freshCookie(t, s)

	// Rows come from the real surface: two staff creates (each with the
	// journeys' logins around them).
	fin := s.staff(t, root, cookie, "audit-fin@it.test", RoleFinance)
	sup := s.staff(t, root, cookie, "audit-sup@it.test", RoleSupport)

	// Unfiltered: the log carries the creates plus every journey login,
	// newest first, each row fully rendered (actor, action, target,
	// origin IP, timestamps).
	status, page, raw := searchAudit(t, s, cookie, "")
	if status != http.StatusOK {
		t.Fatalf("unfiltered search: %d %s", status, raw)
	}
	if page.Total < 3 || len(page.Rows) == 0 {
		t.Fatalf("want the seeded journeys' rows, got total %d rows %d", page.Total, len(page.Rows))
	}
	if page.Page != 1 || page.PageSize != auditSearchDefaultPageSize {
		t.Fatalf("paging = %d/%d", page.Page, page.PageSize)
	}
	actions := map[string]bool{}
	for i, row := range page.Rows {
		if row.Actor == "" || row.Action == "" || row.OriginIP == "" || row.CreatedAt.IsZero() {
			t.Fatalf("row %d incompletely rendered: %+v", i, row)
		}
		actions[row.Action] = true
	}
	for _, want := range []string{PermCreateEmployee, ActionLogin} {
		if !actions[want] {
			t.Fatalf("no %q row in %v", want, actions)
		}
	}
	for i := 1; i < len(page.Rows); i++ {
		if page.Rows[i-1].CreatedAt.Before(page.Rows[i].CreatedAt) {
			t.Fatalf("rows not newest-first: %v before %v", page.Rows[i-1].CreatedAt, page.Rows[i].CreatedAt)
		}
	}

	// Action filter: the two staff creates plus the installer's
	// bootstrap (the same action string, system actor).
	status, page, raw = searchAudit(t, s, cookie, "?action="+PermCreateEmployee)
	if status != http.StatusOK || page.Total != 3 {
		t.Fatalf("action search: %d total %d body %s", status, page.Total, raw)
	}
	targets := map[string]bool{}
	for _, row := range page.Rows {
		if row.Action != PermCreateEmployee {
			t.Fatalf("filter leaked: %q row", row.Action)
		}
		targets[row.Target] = true
	}
	if !targets[fin.EmpID] || !targets[sup.EmpID] {
		t.Fatalf("create rows target the staff ids, got %v", targets)
	}

	// Actor filter: the root performed both creates; the finance staff
	// performed none.
	status, page, raw = searchAudit(t, s, cookie, "?action="+PermCreateEmployee+"&actor=audit-root@it.test")
	if status != http.StatusOK || page.Total != 2 {
		t.Fatalf("actor search: %d total %d body %s", status, page.Total, raw)
	}
	status, page, _ = searchAudit(t, s, cookie, "?action="+PermCreateEmployee+"&actor=audit-fin@it.test")
	if status != http.StatusOK || page.Total != 0 {
		t.Fatalf("staff actor search: %d total %d", status, page.Total)
	}

	// Target filter: one create row, the finance staff's.
	status, page, raw = searchAudit(t, s, cookie, "?action="+PermCreateEmployee+"&target="+fin.EmpID)
	if status != http.StatusOK || page.Total != 1 || page.Rows[0].Target != fin.EmpID {
		t.Fatalf("target search: %d total %d body %s", status, page.Total, raw)
	}

	// The create row's after view is the redacted Employee JSON —
	// exactly what the UI renders as before/after.
	var after map[string]any
	if err := json.Unmarshal(page.Rows[0].After, &after); err != nil {
		t.Fatalf("after view %s: %v", page.Rows[0].After, err)
	}
	if after["email"] != "audit-fin@it.test" {
		t.Fatalf("after view email = %v", after["email"])
	}
	for _, banned := range []string{"password", "password_hash", "totp_secret"} {
		if _, ok := after[banned]; ok {
			t.Fatalf("after view carries %q: %v", banned, after)
		}
	}

	// Time range: everything is in the past hour; a future window is
	// empty; a garbage bound is a 400, not a silent full scan.
	now := time.Now().UTC()
	status, page, raw = searchAudit(t, s, cookie,
		"?from="+now.Add(-time.Hour).Format(time.RFC3339)+"&to="+now.Add(time.Hour).Format(time.RFC3339))
	if status != http.StatusOK || page.Total < 3 {
		t.Fatalf("hour window: %d total %d body %s", status, page.Total, raw)
	}
	status, page, _ = searchAudit(t, s, cookie,
		"?from="+now.Add(time.Hour).Format(time.RFC3339))
	if status != http.StatusOK || page.Total != 0 {
		t.Fatalf("future window: %d total %d", status, page.Total)
	}
	if status, _, raw := s.do(t, "GET", "/api/audit-log?from=not-a-time", "", cookie); status != http.StatusBadRequest {
		t.Fatalf("garbage from: %d %s", status, raw)
	}

	// Paging: page_size bounds the page and total stays whole-log.
	status, page, raw = searchAudit(t, s, cookie, "?page_size=2&page=1")
	if status != http.StatusOK || len(page.Rows) != 2 || page.Total < 3 {
		t.Fatalf("paged search: %d rows %d total %d body %s", status, len(page.Rows), page.Total, raw)
	}
	seen := map[int64]bool{page.Rows[0].ID: true, page.Rows[1].ID: true}
	status, page2, _ := searchAudit(t, s, cookie, "?page_size=2&page=2")
	if status != http.StatusOK || len(page2.Rows) == 0 {
		t.Fatalf("second page: %d %s", status, raw)
	}
	for _, row := range page2.Rows {
		if seen[row.ID] {
			t.Fatal("page 2 repeats a page 1 row")
		}
	}

	// The matrix lands through the editor: grant view:audit-log to
	// finance, and the finance Employee reads the log on their LIVE
	// session — no re-login (the same immediacy the grant editor sells).
	financeID := s.roleIDOf(t, cookie, RoleFinance)
	s.setGrant(t, cookie, financeID, PermViewAuditLog, true)
	finCookie := fin.freshCookie(t, s)
	status, page, raw = searchAudit(t, s, finCookie, "?target="+sup.EmpID)
	if status != http.StatusOK || page.Total < 1 {
		t.Fatalf("finance read after grant: %d total %d body %s", status, page.Total, raw)
	}

	// Empty catalog matches nothing: an unknown action filter is simply
	// zero rows (filters select, they never invent).
	status, page, _ = searchAudit(t, s, cookie, "?action=invent:nothing")
	if status != http.StatusOK || page.Total != 0 {
		t.Fatalf("unknown action filter: %d total %d", status, page.Total)
	}
}

func TestPermissionCatalogEndpoint(t *testing.T) {
	s := newStack(t)
	root := s.fullyProvisioned(t, "catalog-root@it.test")
	cookie := root.freshCookie(t, s)

	status, _, raw := s.do(t, "GET", "/api/permissions", "", cookie)
	if status != http.StatusOK {
		t.Fatalf("catalog: %d %s", status, raw)
	}
	var catalog []string
	if err := json.Unmarshal([]byte(raw), &catalog); err != nil {
		t.Fatalf("catalog body %s: %v", raw, err)
	}
	if !slices.Equal(catalog, registeredPermissions()) {
		t.Fatalf("catalog %v != registry strings %v", catalog, registeredPermissions())
	}

	// The catalog is read-only everywhere: the endpoint is a GET, and a
	// UI-invented string is refused by the grant editor's catalog check
	// (covered in rbac_it_test.go). Here: the matrix read — grant
	// view:permission to finance and the finance Employee sees it too.
	financeID := s.roleIDOf(t, cookie, RoleFinance)
	s.setGrant(t, cookie, financeID, PermViewPermission, true)
	fin := s.staff(t, root, cookie, "catalog-fin@it.test", RoleFinance)
	if status, _, raw := s.do(t, "GET", "/api/permissions", "", fin.freshCookie(t, s)); status != http.StatusOK || !strings.Contains(raw, PermViewEmployee) {
		t.Fatalf("finance catalog read after grant: %d %s", status, raw)
	}
}
