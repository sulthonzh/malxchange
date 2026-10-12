package market

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The internal rebuild endpoint's contract (ticket #34): POST /internal/
// rebuild wipes the derived state and rewinds the consumer group through
// the ordinary projection loop. These hermetic tests pin the HTTP shape
// only — the wipe-and-replay behavior is the drill IT's job (the drill IS
// the test, spec testing decision).

// stubRebuilder records calls; Rebuild itself is exercised against real
// Postgres + Kafka in the integration drill, never faked here.
type stubRebuilder struct {
	err   error
	calls int
}

func (r *stubRebuilder) Rebuild(context.Context) error {
	r.calls++
	return r.err
}

func newRebuildServer(t *testing.T, r *RPC) *httptest.Server {
	t.Helper()
	root := http.NewServeMux()
	internal := http.NewServeMux()
	root.Handle("/internal/", http.StripPrefix("/internal", internal))
	RegisterRPC(internal, r)
	srv := httptest.NewServer(root)
	t.Cleanup(srv.Close)
	return srv
}

func postRebuild(t *testing.T, srv *httptest.Server) (int, map[string]string) {
	t.Helper()
	resp, err := http.Post(srv.URL+"/internal/rebuild", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]string
	if len(body) > 0 {
		if err := json.Unmarshal(body, &decoded); err != nil {
			t.Fatalf("response body %q: %v", body, err)
		}
	}
	return resp.StatusCode, decoded
}

// TestRebuildWithoutProjectorAnswersUnavailable: a market process that
// never wired the projector (no consumer to rewind) must refuse the drill
// with 503, never silently pretend a rebuild happened.
func TestRebuildWithoutProjectorAnswersUnavailable(t *testing.T) {
	srv := newRebuildServer(t, &RPC{})

	status, body := postRebuild(t, srv)
	if status != http.StatusServiceUnavailable {
		t.Fatalf("POST /internal/rebuild without projector: status %d, want 503", status)
	}
	if !strings.Contains(body["error"], "rebuild unavailable") {
		t.Errorf("error %q, want it to name the rebuild", body["error"])
	}
}

// TestRebuildTriggersRebuilder: the route answers 204 (no content to
// return — convergence is observable on the market-data endpoints) and
// triggers exactly one rebuild.
func TestRebuildTriggersRebuilder(t *testing.T) {
	rb := &stubRebuilder{}
	srv := newRebuildServer(t, &RPC{Rebuild: rb})

	status, body := postRebuild(t, srv)
	if status != http.StatusNoContent {
		t.Fatalf("POST /internal/rebuild: status %d, want 204", status)
	}
	if len(body) != 0 {
		t.Errorf("204 body %v, want empty", body)
	}
	if rb.calls != 1 {
		t.Errorf("rebuild triggered %d times, want 1", rb.calls)
	}
}

// TestRebuildPropagatesFailure: a failed wipe surfaces as 500 so the
// operator retries — the rebuild is idempotent, never half-reported.
func TestRebuildPropagatesFailure(t *testing.T) {
	rb := &stubRebuilder{err: errors.New("wipe: connection refused")}
	srv := newRebuildServer(t, &RPC{Rebuild: rb})

	status, body := postRebuild(t, srv)
	if status != http.StatusInternalServerError {
		t.Fatalf("POST /internal/rebuild on failure: status %d, want 500", status)
	}
	if !strings.Contains(body["error"], "wipe") {
		t.Errorf("error %q, want it to carry the cause", body["error"])
	}
}
