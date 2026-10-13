package api

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
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
