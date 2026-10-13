package api

import (
	"bytes"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// stubAdminAPI serves the admin-api content contract exactly as
// internal/api/content_CONTRACT.md documents it, so the proxy's client
// side is exercised against the wire shape spec #25 will implement
// (never against the proxy's own structs — that would be tautological).
// It records every request it served for passthrough assertions.
type stubAdminAPI struct {
	srv *httptest.Server

	mu     sync.Mutex
	served []string
}

func newStubAdminAPI(t *testing.T) *stubAdminAPI {
	t.Helper()
	stub := &stubAdminAPI{}
	mux := http.NewServeMux()

	record := func(req *http.Request) {
		stub.mu.Lock()
		stub.served = append(stub.served, req.Method+" "+req.URL.RequestURI())
		stub.mu.Unlock()
	}

	mux.HandleFunc("GET /internal/content/announcements", func(w http.ResponseWriter, req *http.Request) {
		record(req)
		stubWrite(w, `{"content":[{"id":"a-2","title":"Second maintenance window","createTime":"2026-10-12T08:00:00Z"},`+
			`{"id":"a-1","title":"Listing announced","createTime":"2026-10-10T12:30:00Z"}],"totalElements":2}`)
	})
	mux.HandleFunc("GET /internal/content/announcements/{id}", func(w http.ResponseWriter, req *http.Request) {
		record(req)
		switch req.PathValue("id") {
		case "a-1":
			stubWrite(w, `{"info":{"id":"a-1","title":"Listing announced","createTime":"2026-10-10T12:30:00Z",`+
				`"content":"<p>BTC/USDT is live.</p>"},"back":null,`+
				`"next":{"id":"a-2","title":"Second maintenance window","createTime":"2026-10-12T08:00:00Z"}}`)
		case "a-2":
			stubWrite(w, `{"info":{"id":"a-2","title":"Second maintenance window","createTime":"2026-10-12T08:00:00Z",`+
				`"content":"<p>Read-only mode on Sunday.</p>"},"back":{"id":"a-1","title":"Listing announced",`+
				`"createTime":"2026-10-10T12:30:00Z"},"next":null}`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})
	mux.HandleFunc("GET /internal/content/help", func(w http.ResponseWriter, req *http.Request) {
		record(req)
		stubWrite(w, `[{"cate":"0","title":"Getting started","items":[{"id":"h-1","title":"How to deposit","createTime":"2026-10-01T09:00:00Z"}]},`+
			`{"cate":"1","title":"Fees","items":[]}]`)
	})
	mux.HandleFunc("GET /internal/content/help/categories/{cate}", func(w http.ResponseWriter, req *http.Request) {
		record(req)
		if req.PathValue("cate") == "0" {
			stubWrite(w, `{"content":[{"id":"h-1","title":"How to deposit","createTime":"2026-10-01T09:00:00Z"}],"totalElements":1}`)
			return
		}
		stubWrite(w, `{"content":[],"totalElements":0}`)
	})
	mux.HandleFunc("GET /internal/content/help/articles/{id}", func(w http.ResponseWriter, req *http.Request) {
		record(req)
		if req.PathValue("id") == "h-1" {
			stubWrite(w, `{"id":"h-1","title":"How to deposit","createTime":"2026-10-01T09:00:00Z",`+
				`"content":"<p>Deposits credit after the Coin's confirmation count.</p>","cate":"0"}`)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	})

	stub.srv = httptest.NewServer(mux)
	t.Cleanup(stub.srv.Close)
	return stub
}

// URL is the stub's origin, standing in for the admin-api compose
// service the proxy's MAL_API_ADMIN_BASE_URL points at.
func (s *stubAdminAPI) URL() string { return s.srv.URL }

// lastServed returns the most recent "METHOD path?query" the stub
// answered, for passthrough assertions.
func (s *stubAdminAPI) lastServed() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.served) == 0 {
		return ""
	}
	return s.served[len(s.served)-1]
}

// countServed returns how many requests the stub answered in total.
func (s *stubAdminAPI) countServed() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.served)
}

func stubWrite(w http.ResponseWriter, body string) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = io.WriteString(w, body)
}

// newContentMemberAPI mounts RegisterContent on a bare mux — the same
// registrar cmd/api's server wiring (ticket #37) hands the member
// patterns to. The request path carries the /api prefix because Caddy's
// `handle /api/*` reverse proxy does not strip it.
func newContentMemberAPI(t *testing.T, cfg ContentConfig) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	RegisterContent(mux, cfg)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func readBody(t *testing.T, resp *http.Response) string {
	t.Helper()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	// The JSON encoder terminates with a newline; that framing byte is
	// not part of the contract being compared.
	return strings.TrimSpace(string(b))
}

// The announcements listing round-trips the documented contract through
// the served member surface: identical JSON shape, with the pagination
// defaults forwarded upstream. The request carries no session header —
// content routes are public for anonymous visitors (spec #24 story 26).
func TestAnnouncementsListingRoundTripsContract(t *testing.T) {
	up := newStubAdminAPI(t)
	srv := newContentMemberAPI(t, ContentConfig{AdminBaseURL: up.URL()})

	resp, err := srv.Client().Get(srv.URL + "/api/announcements")
	if err != nil {
		t.Fatalf("get announcements: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	got := readBody(t, resp)
	want := `{"content":[{"id":"a-2","title":"Second maintenance window","createTime":"2026-10-12T08:00:00Z"},` +
		`{"id":"a-1","title":"Listing announced","createTime":"2026-10-10T12:30:00Z"}],"totalElements":2}`
	if got != want {
		t.Errorf("body =\n%s\nwant\n%s", got, want)
	}

	if up.lastServed() != "GET /internal/content/announcements?pageNo=1&pageSize=10" {
		t.Errorf("upstream served %q, want the defaulted pagination forwarded", up.lastServed())
	}
}

// Explicit pagination is forwarded verbatim, not re-defaulted.
func TestAnnouncementsListingForwardsExplicitPagination(t *testing.T) {
	up := newStubAdminAPI(t)
	srv := newContentMemberAPI(t, ContentConfig{AdminBaseURL: up.URL()})

	resp, err := srv.Client().Get(srv.URL + "/api/announcements?pageNo=2&pageSize=5")
	if err != nil {
		t.Fatalf("get announcements: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if up.lastServed() != "GET /internal/content/announcements?pageNo=2&pageSize=5" {
		t.Errorf("upstream served %q, want the caller's pagination forwarded", up.lastServed())
	}
}

// The announcement detail round-trips the documented contract: the
// body under info plus the adjacent summaries for prev/next
// navigation. An unknown id answers 404 with the public error envelope
// ({"message":...}, the shape apps/web's useApi reads).
func TestAnnouncementDetailRoundTripsContract(t *testing.T) {
	up := newStubAdminAPI(t)
	srv := newContentMemberAPI(t, ContentConfig{AdminBaseURL: up.URL()})

	resp, err := srv.Client().Get(srv.URL + "/api/announcements/a-2")
	if err != nil {
		t.Fatalf("get announcement: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	got := readBody(t, resp)
	want := `{"info":{"id":"a-2","title":"Second maintenance window","createTime":"2026-10-12T08:00:00Z",` +
		`"content":"<p>Read-only mode on Sunday.</p>"},"back":{"id":"a-1","title":"Listing announced",` +
		`"createTime":"2026-10-10T12:30:00Z"},"next":null}`
	if got != want {
		t.Errorf("body =\n%s\nwant\n%s", got, want)
	}
	if up.lastServed() != "GET /internal/content/announcements/a-2" {
		t.Errorf("upstream served %q, want the detail document", up.lastServed())
	}

	resp, err = srv.Client().Get(srv.URL + "/api/announcements/nope")
	if err != nil {
		t.Fatalf("get unknown announcement: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
	if got, want := readBody(t, resp), `{"message":"announcement not found"}`; got != want {
		t.Errorf("error body = %s, want %s", got, want)
	}
}

// The help surfaces round-trip the documented contract: the category
// overview, one category's paginated listing, and an article body.
func TestHelpSurfacesRoundTripContract(t *testing.T) {
	up := newStubAdminAPI(t)
	srv := newContentMemberAPI(t, ContentConfig{AdminBaseURL: up.URL()})
	client := srv.Client()

	resp, err := client.Get(srv.URL + "/api/help")
	if err != nil {
		t.Fatalf("get help center: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("help center status = %d, want 200", resp.StatusCode)
	}
	got := readBody(t, resp)
	want := `[{"cate":"0","title":"Getting started","items":[{"id":"h-1","title":"How to deposit","createTime":"2026-10-01T09:00:00Z"}]},` +
		`{"cate":"1","title":"Fees","items":[]}]`
	if got != want {
		t.Errorf("help center body =\n%s\nwant\n%s", got, want)
	}
	if up.lastServed() != "GET /internal/content/help" {
		t.Errorf("upstream served %q, want the help overview", up.lastServed())
	}

	resp, err = client.Get(srv.URL + "/api/help/categories/0?pageNo=1&pageSize=10")
	if err != nil {
		t.Fatalf("get help list: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("help list status = %d, want 200", resp.StatusCode)
	}
	got = readBody(t, resp)
	want = `{"content":[{"id":"h-1","title":"How to deposit","createTime":"2026-10-01T09:00:00Z"}],"totalElements":1}`
	if got != want {
		t.Errorf("help list body =\n%s\nwant\n%s", got, want)
	}
	if up.lastServed() != "GET /internal/content/help/categories/0?pageNo=1&pageSize=10" {
		t.Errorf("upstream served %q, want the category listing", up.lastServed())
	}

	resp, err = client.Get(srv.URL + "/api/help/articles/h-1")
	if err != nil {
		t.Fatalf("get help article: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("help article status = %d, want 200", resp.StatusCode)
	}
	got = readBody(t, resp)
	want = `{"id":"h-1","title":"How to deposit","createTime":"2026-10-01T09:00:00Z",` +
		`"content":"<p>Deposits credit after the Coin's confirmation count.</p>","cate":"0"}`
	if got != want {
		t.Errorf("help article body =\n%s\nwant\n%s", got, want)
	}
	if up.lastServed() != "GET /internal/content/help/articles/h-1" {
		t.Errorf("upstream served %q, want the article document", up.lastServed())
	}
}

// An unknown help article answers 404 with the public error envelope,
// and an unknown category is an empty listing, not an error (the stub's
// contract: unknown categories hold no articles).
func TestHelpUnknownArticleAndCategory(t *testing.T) {
	up := newStubAdminAPI(t)
	srv := newContentMemberAPI(t, ContentConfig{AdminBaseURL: up.URL()})
	client := srv.Client()

	resp, err := client.Get(srv.URL + "/api/help/articles/nope")
	if err != nil {
		t.Fatalf("get unknown article: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
	if got, want := readBody(t, resp), `{"message":"help article not found"}`; got != want {
		t.Errorf("error body = %s, want %s", got, want)
	}

	resp, err = client.Get(srv.URL + "/api/help/categories/9")
	if err != nil {
		t.Fatalf("get unknown category: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if got, want := readBody(t, resp), `{"content":[],"totalElements":0}`; got != want {
		t.Errorf("body = %s, want %s", got, want)
	}
}

// A configured-but-unreachable admin-api degrades to the empty content
// set (ticket #45): listings answer 200-empty, details 404, and the
// degradation warns exactly ONCE no matter how many requests hit the
// surface — the front renders empty states, the log never spams.
func TestUnreachableUpstreamDegradesToEmptyWithOneWarn(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelInfo}))
	// Port 1 on loopback has no listener: every dial fails fast.
	srv := newContentMemberAPI(t, ContentConfig{
		AdminBaseURL: "http://127.0.0.1:1",
		Log:          logger,
		Client:       &http.Client{Timeout: time.Second},
	})
	client := srv.Client()

	for range 5 {
		resp, err := client.Get(srv.URL + "/api/announcements")
		if err != nil {
			t.Fatalf("get announcements: %v", err)
		}
		if got := readBody(t, resp); got != `{"content":[],"totalElements":0}` {
			t.Fatalf("announcements body = %s, want the empty page", got)
		}
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want 200", resp.StatusCode)
		}
	}
	resp, err := client.Get(srv.URL + "/api/help")
	if err != nil {
		t.Fatalf("get help center: %v", err)
	}
	if got := readBody(t, resp); got != `[]` {
		t.Errorf("help center body = %s, want the empty overview", got)
	}
	for _, path := range []string{"/api/announcements/any", "/api/help/articles/any"} {
		resp, err := client.Get(srv.URL + path)
		if err != nil {
			t.Fatalf("get %s: %v", path, err)
		}
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("%s status = %d, want 404 (degraded = empty content set)", path, resp.StatusCode)
		}
	}

	if warns := strings.Count(logs.String(), "level=WARN"); warns != 1 {
		t.Errorf("logged %d warnings, want exactly 1 across every request", warns)
	}
}

// An unconfigured proxy (MAL_API_ADMIN_BASE_URL unset) behaves like an
// unreachable one — empty content set, one warning naming the variable.
func TestUnconfiguredUpstreamDegradesToEmptyWithOneWarn(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	srv := newContentMemberAPI(t, ContentConfig{Log: logger})
	client := srv.Client()

	resp, err := client.Get(srv.URL + "/api/announcements")
	if err != nil {
		t.Fatalf("get announcements: %v", err)
	}
	if got := readBody(t, resp); got != `{"content":[],"totalElements":0}` {
		t.Errorf("announcements body = %s, want the empty page", got)
	}
	if !strings.Contains(logs.String(), "MAL_API_ADMIN_BASE_URL") {
		t.Errorf("the one warning should name the unset variable, got: %s", logs.String())
	}
	if warns := strings.Count(logs.String(), "level=WARN"); warns != 1 {
		t.Errorf("logged %d warnings, want exactly 1", warns)
	}
}

// Warnings ride state changes, not requests: a flapping upstream logs
// one warn per fall and one info per recovery, and every fall still
// serves the degraded answer.
func TestUpstreamFlapLogsPerStateChange(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelInfo}))

	var down atomic.Bool
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if down.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		stubWrite(w, `{"content":[{"id":"a-1","title":"Back again","createTime":"2026-10-12T08:00:00Z"}],"totalElements":1}`)
	}))
	t.Cleanup(up.Close)

	srv := newContentMemberAPI(t, ContentConfig{AdminBaseURL: up.URL, Log: logger})
	client := srv.Client()
	listing := func(want string) {
		t.Helper()
		resp, err := client.Get(srv.URL + "/api/announcements")
		if err != nil {
			t.Fatalf("get announcements: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want 200", resp.StatusCode)
		}
		if got := readBody(t, resp); got != want {
			t.Errorf("body = %s, want %s", got, want)
		}
	}

	down.Store(true)
	listing(`{"content":[],"totalElements":0}`)
	down.Store(false)
	listing(`{"content":[{"id":"a-1","title":"Back again","createTime":"2026-10-12T08:00:00Z"}],"totalElements":1}`)
	down.Store(true)
	listing(`{"content":[],"totalElements":0}`)

	out := logs.String()
	if warns := strings.Count(out, "level=WARN"); warns != 2 {
		t.Errorf("logged %d warnings, want 2 (one per fall)", warns)
	}
	if infos := strings.Count(out, "level=INFO"); infos != 1 {
		t.Errorf("logged %d infos, want 1 (one recovery)", infos)
	}
}

// Pagination outside the documented bounds is a named bad request with
// the public error envelope — never a silent clamp (the market
// service's limit discipline).
func TestPaginationOutOfRangeAnswersBadRequest(t *testing.T) {
	up := newStubAdminAPI(t)
	srv := newContentMemberAPI(t, ContentConfig{AdminBaseURL: up.URL()})
	client := srv.Client()
	wantMsg := `{"message":"pageNo must be an integer of at least 1 and pageSize an integer between 1 and 50"}`

	for _, q := range []string{"pageNo=0", "pageNo=-1", "pageNo=abc", "pageSize=0", "pageSize=51", "pageSize=abc"} {
		resp, err := client.Get(srv.URL + "/api/announcements?" + q)
		if err != nil {
			t.Fatalf("get announcements?%s: %v", q, err)
		}
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("?%s status = %d, want 400", q, resp.StatusCode)
		}
		if got := readBody(t, resp); got != wantMsg {
			t.Errorf("?%s body = %s, want %s", q, got, wantMsg)
		}
	}

	// The bounds themselves pass through to the upstream.
	resp, err := client.Get(srv.URL + "/api/announcements?pageNo=1&pageSize=50")
	if err != nil {
		t.Fatalf("get announcements at the bounds: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("bounds status = %d, want 200", resp.StatusCode)
	}
	if up.lastServed() != "GET /internal/content/announcements?pageNo=1&pageSize=50" {
		t.Errorf("upstream served %q, want the bounded pagination forwarded", up.lastServed())
	}
}

// The pagination parser's table: absent = defaults, valid bounds
// accepted, everything else rejected (supporting unit — the seam tests
// above cover the served behavior).
func TestContentPageParams(t *testing.T) {
	tests := []struct {
		query    string
		pageNo   int
		pageSize int
		wantErr  bool
	}{
		{"", 1, 10, false},
		{"pageNo=3&pageSize=7", 3, 7, false},
		{"pageNo=1&pageSize=50", 1, 50, false},
		{"pageNo=0", 0, 0, true},
		{"pageSize=51", 0, 0, true},
		{"pageSize=-2", 0, 0, true},
		{"pageNo=x&pageSize=y", 0, 0, true},
	}
	for _, tt := range tests {
		req, err := http.NewRequest(http.MethodGet, "/api/announcements?"+tt.query, nil)
		if err != nil {
			t.Fatal(err)
		}
		pageNo, pageSize, err := contentPageParams(req)
		if tt.wantErr {
			if err == nil {
				t.Errorf("?%s parsed to %d/%d, want an error", tt.query, pageNo, pageSize)
			}
			continue
		}
		if err != nil {
			t.Errorf("?%s errored: %v", tt.query, err)
			continue
		}
		if pageNo != tt.pageNo || pageSize != tt.pageSize {
			t.Errorf("?%s parsed to %d/%d, want %d/%d", tt.query, pageNo, pageSize, tt.pageNo, tt.pageSize)
		}
	}
}
