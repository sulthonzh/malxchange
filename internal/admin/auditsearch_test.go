package admin

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// The search parameters are the whole contract the audit page programs
// against: the four filters pass through untouched, the time range is
// RFC 3339 (a garbage timestamp is a client bug — 400, not a silent
// full scan), and the paging window is clamped so a wild client cannot
// demand the whole log in one response.
func TestParseAuditSearchAcceptsFiltersAndClampsPaging(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet,
		"/api/audit-log?actor=root@it.test&action=create:employee"+
			"&target=emp-1&from=2026-01-02T03:04:05Z&to=2026-01-03T03:04:05Z&page=2&page_size=10",
		nil)
	w := httptest.NewRecorder()
	q, ok := parseAuditSearch(w, r)
	if !ok {
		t.Fatal("valid filters refused")
	}
	if q.Actor != "root@it.test" || q.Action != "create:employee" || q.Target != "emp-1" {
		t.Fatalf("filters lost: %+v", q)
	}
	wantFrom, _ := time.Parse(time.RFC3339, "2026-01-02T03:04:05Z")
	wantTo, _ := time.Parse(time.RFC3339, "2026-01-03T03:04:05Z")
	if !q.From.Equal(wantFrom) || !q.To.Equal(wantTo) {
		t.Fatalf("time range = %v..%v, want %v..%v", q.From, q.To, wantFrom, wantTo)
	}
	if q.Page != 2 || q.PageSize != 10 {
		t.Fatalf("paging = %d/%d, want 2/10", q.Page, q.PageSize)
	}
}

func TestParseAuditSearchDefaultsWhenAbsent(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/api/audit-log", nil)
	w := httptest.NewRecorder()
	q, ok := parseAuditSearch(w, r)
	if !ok {
		t.Fatal("empty query refused")
	}
	if q.Actor != "" || q.Action != "" || q.Target != "" || !q.From.IsZero() || !q.To.IsZero() {
		t.Fatalf("absent filters must be zero values: %+v", q)
	}
	if q.Page != 1 || q.PageSize != auditSearchDefaultPageSize {
		t.Fatalf("paging defaults = %d/%d, want 1/%d", q.Page, q.PageSize, auditSearchDefaultPageSize)
	}
}

func TestParseAuditSearchRefusesGarbageRange(t *testing.T) {
	for name, query := range map[string]string{
		"bad from":       "from=yesterday",
		"bad to":         "to=soon",
		"from after to":  "from=2026-02-02T00:00:00Z&to=2026-01-01T00:00:00Z",
		"page is a word": "page=first",
		"size is a word": "page_size=lots",
	} {
		r := httptest.NewRequest(http.MethodGet, "/api/audit-log?"+query, nil)
		w := httptest.NewRecorder()
		if _, ok := parseAuditSearch(w, r); ok {
			t.Fatalf("%s: accepted %q", name, query)
		}
		if w.Code != http.StatusBadRequest {
			t.Fatalf("%s: status %d, want 400", name, w.Code)
		}
	}
}

func TestParseAuditSearchClampsOversizePage(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/api/audit-log?page=-3&page_size=100000", nil)
	w := httptest.NewRecorder()
	q, ok := parseAuditSearch(w, r)
	if !ok {
		t.Fatal("clamping must not refuse")
	}
	if q.Page != 1 || q.PageSize != auditSearchMaxPageSize {
		t.Fatalf("clamped paging = %d/%d, want 1/%d", q.Page, q.PageSize, auditSearchMaxPageSize)
	}
}
