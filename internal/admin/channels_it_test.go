//go:build integration

// The Code channel surface over the served seam (ticket #62, ADR-0006):
// HTTP against the production admin-api mux with the api service's
// channel RPC stubbed behind it. What is under test is admin-api's
// half of the compose boundary — the permission gate (the matrix loop
// in rbac_it_test.go already refuses every non-matrix Role for every
// registry endpoint), the Step-up on the credential save, the actor
// attribution on the forwarded call, api's refusal surfacing verbatim,
// the audited activate/deactivate rows, and the write-only rule:
// credentials never render back on any response. Channel state and
// per-driver credential validation live in api; its own tests pin
// those.
package admin

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// stubChannelAPI is the api service's internal channel RPC as
// admin-api sees it: the three routes of RegisterChannelAdmin,
// scripted statuses, and a recording of every forwarded call.
type stubChannelAPI struct {
	mu sync.Mutex

	listStatus int
	listBody   string

	activateStatus   map[string]int
	activateBody     map[string]string
	deactivateStatus map[string]int

	srv *httptest.Server

	requests []string
	actors   []string
	bodies   []string
}

func newStubChannelAPI(t *testing.T) *stubChannelAPI {
	t.Helper()
	stub := &stubChannelAPI{
		activateStatus:   map[string]int{},
		activateBody:     map[string]string{},
		deactivateStatus: map[string]int{},
	}
	stub.srv = httptest.NewServer(stub)
	t.Cleanup(stub.srv.Close)
	return stub
}

func (s *stubChannelAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	s.requests = append(s.requests, r.Method+" "+r.URL.Path)
	s.actors = append(s.actors, r.Header.Get("X-Mal-Actor"))
	s.bodies = append(s.bodies, string(body))

	w.Header().Set("Content-Type", "application/json")
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/channels":
		status := s.listStatus
		if status == 0 {
			status = http.StatusOK
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(s.listBody))
	case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/activate"):
		name := channelNameOf(r.URL.Path, "/activate")
		status := s.activateStatus[name]
		if status == 0 {
			status = http.StatusNoContent
		}
		w.WriteHeader(status)
		if msg := s.activateBody[name]; status != http.StatusNoContent && msg != "" {
			_, _ = w.Write([]byte(msg))
		}
	case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/deactivate"):
		name := channelNameOf(r.URL.Path, "/deactivate")
		status := s.deactivateStatus[name]
		if status == 0 {
			status = http.StatusNoContent
		}
		w.WriteHeader(status)
	default:
		http.NotFound(w, r)
	}
}

func channelNameOf(path, suffix string) string {
	return strings.TrimSuffix(strings.TrimPrefix(path, "/channels/"), suffix)
}

// forwarded snapshots the recorded calls.
func (s *stubChannelAPI) forwarded() (requests, actors, bodies []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string{}, s.requests...), append([]string{}, s.actors...), append([]string{}, s.bodies...)
}

// down kills the stub so the next call is a transport failure.
func (s *stubChannelAPI) down() { s.srv.Close() }

// newChannelsStack is newStack with the api channel RPC stubbed in and
// the server's client pointed at it.
func newChannelsStack(t *testing.T) (*itStack, *stubChannelAPI) {
	t.Helper()
	stub := newStubChannelAPI(t)
	s := newStack(t, func(env *AdminEnv) {
		env.APIBaseURL = stub.srv.URL
	})
	return s, stub
}

// channelListJSON is what api's list serves: status only, never
// credentials.
const channelListJSON = `[
  {"channel":"sms","active":false,"activatedAt":null,"updatedAt":"2026-01-02T03:04:05Z"},
  {"channel":"telegram","active":true,"activatedAt":"2026-01-02T03:04:05Z","updatedAt":"2026-01-02T03:04:05Z"},
  {"channel":"whatsapp","active":false,"activatedAt":null,"updatedAt":"2026-01-02T03:04:05Z"}
]`

func TestChannelListServesStatusWithoutCredentials(t *testing.T) {
	s, stub := newChannelsStack(t)
	stub.listBody = channelListJSON
	root := s.fullyProvisioned(t, "channels-root@it.test")
	cookie := root.freshCookie(t, s)

	status, _, raw := s.do(t, "GET", "/api/channels", "", cookie)
	if status != http.StatusOK {
		t.Fatalf("list: status %d body %s", status, raw)
	}
	var channels []map[string]any
	if err := json.Unmarshal([]byte(raw), &channels); err != nil {
		t.Fatalf("list body %s: %v", raw, err)
	}
	if len(channels) != 3 {
		t.Fatalf("want 3 channels, got %d: %s", len(channels), raw)
	}
	// The write-only rule: no credential material renders back, ever.
	for _, banned := range []string{"credentials", "endpoint", "apiKey", "phoneNumberId", "accessToken", "botToken", "webhookSecret"} {
		if strings.Contains(raw, banned) {
			t.Fatalf("list response carries credential field %q: %s", banned, raw)
		}
	}

	// Anonymous: the session gate refuses before anything else.
	status, _, _ = s.do(t, "GET", "/api/channels", "", nil)
	if status != http.StatusUnauthorized {
		t.Fatalf("anonymous list: %d", status)
	}
}

func TestChannelActivateDemandsStepUp(t *testing.T) {
	s, stub := newChannelsStack(t)
	root := s.fullyProvisioned(t, "channels-stepup@it.test")
	cookie := root.freshCookie(t, s)

	// No code: step-up required, and api is never called.
	body := `{"credentials":{"endpoint":"http://sms.local/send","apiKey":"k1"}}`
	status, _, raw := s.post(t, "/api/channels/sms/activate", body, cookie)
	if status != http.StatusUnauthorized || !strings.Contains(raw, errStepUpRequired) {
		t.Fatalf("missing code: %d %s", status, raw)
	}
	// A wrong code: refused the same way.
	wrong := strings.Replace(body, "}}", `},"code":"000000"}`, 1)
	status, _, raw = s.post(t, "/api/channels/sms/activate", wrong, cookie)
	if status != http.StatusUnauthorized || !strings.Contains(raw, errStepUpInvalid) {
		t.Fatalf("wrong code: %d %s", status, raw)
	}
	if requests, _, _ := stub.forwarded(); len(requests) != 0 {
		t.Fatalf("refused activations reached api: %v", requests)
	}
}

func TestChannelActivateForwardsCredentialsAndAudits(t *testing.T) {
	s, stub := newChannelsStack(t)
	root := s.fullyProvisioned(t, "channels-activate@it.test")
	cookie := root.freshCookie(t, s)

	code, err := totpNow(root.TOTPSecr)
	if err != nil {
		t.Fatal(err)
	}
	body := fmt.Sprintf(`{"credentials":{"endpoint":"http://sms.local/send","apiKey":"k1"},"code":%q}`, code)
	status, _, raw := s.post(t, "/api/channels/sms/activate", body, cookie)
	if status != http.StatusNoContent {
		t.Fatalf("activate: %d %s", status, raw)
	}

	// api got exactly one call: the channel's credentials verbatim,
	// the acting Employee attributed, and NO step-up code downstream.
	requests, actors, bodies := stub.forwarded()
	if len(requests) != 1 || requests[0] != "POST /channels/sms/activate" {
		t.Fatalf("forwarded requests = %v", requests)
	}
	if actors[0] != root.Email {
		t.Fatalf("X-Mal-Actor = %q, want %q", actors[0], root.Email)
	}
	if !strings.Contains(bodies[0], `"apiKey":"k1"`) {
		t.Fatalf("credentials did not pass through: %s", bodies[0])
	}
	if strings.Contains(bodies[0], "code") {
		t.Fatalf("step-up code leaked downstream: %s", bodies[0])
	}

	// The audit row: action = the permission string, target = the
	// channel, and no credential material in the envelope.
	rows := s.auditRows(t, PermActivateChannel)
	if len(rows) != 1 {
		t.Fatalf("want 1 %s audit row, got %d", PermActivateChannel, len(rows))
	}
	if rows[0].Actor != root.Email || rows[0].Target != "sms" {
		t.Fatalf("audit row actor=%q target=%q", rows[0].Actor, rows[0].Target)
	}
	if strings.Contains(string(rows[0].Before), "k1") || strings.Contains(string(rows[0].After), "k1") {
		t.Fatalf("audit row carries credential material: %+v", rows[0])
	}
}

func TestChannelActivateSurfacesAPIRefusal(t *testing.T) {
	s, stub := newChannelsStack(t)
	root := s.fullyProvisioned(t, "channels-refusal@it.test")
	cookie := root.freshCookie(t, s)
	code, err := totpNow(root.TOTPSecr)
	if err != nil {
		t.Fatal(err)
	}

	// api's per-driver refusal passes through: status and sentence.
	stub.activateStatus["sms"] = http.StatusBadRequest
	stub.activateBody["sms"] = `{"error":"channel sms needs credentials endpoint and apiKey"}`
	status, _, raw := s.post(t, "/api/channels/sms/activate",
		fmt.Sprintf(`{"credentials":{"apiKey":"k1"},"code":%q}`, code), cookie)
	if status != http.StatusBadRequest || !strings.Contains(raw, "channel sms needs credentials endpoint and apiKey") {
		t.Fatalf("driver refusal: %d %s", status, raw)
	}

	// An unknown channel keeps api's 404.
	stub.activateStatus["pigeon"] = http.StatusNotFound
	stub.activateBody["pigeon"] = `{"error":"unknown code channel: \"pigeon\""}`
	status, _, raw = s.post(t, "/api/channels/pigeon/activate",
		fmt.Sprintf(`{"credentials":{},"code":%q}`, code), cookie)
	if status != http.StatusNotFound || !strings.Contains(raw, "pigeon") {
		t.Fatalf("unknown channel: %d %s", status, raw)
	}

	// Refusals are not success mutations: nothing landed in the log.
	if rows := s.auditRows(t, PermActivateChannel); len(rows) != 0 {
		t.Fatalf("refused activations wrote %d audit rows", len(rows))
	}
}

func TestChannelDeactivateForwardsAndAudits(t *testing.T) {
	s, stub := newChannelsStack(t)
	root := s.fullyProvisioned(t, "channels-deactivate@it.test")
	cookie := root.freshCookie(t, s)

	status, _, raw := s.post(t, "/api/channels/telegram/deactivate", `{}`, cookie)
	if status != http.StatusNoContent {
		t.Fatalf("deactivate: %d %s", status, raw)
	}
	requests, actors, _ := stub.forwarded()
	if len(requests) != 1 || requests[0] != "POST /channels/telegram/deactivate" {
		t.Fatalf("forwarded requests = %v", requests)
	}
	if actors[0] != root.Email {
		t.Fatalf("X-Mal-Actor = %q, want %q", actors[0], root.Email)
	}
	rows := s.auditRows(t, PermDeactivateChannel)
	if len(rows) != 1 {
		t.Fatalf("want 1 %s audit row, got %d", PermDeactivateChannel, len(rows))
	}
	if rows[0].Actor != root.Email || rows[0].Target != "telegram" {
		t.Fatalf("audit row actor=%q target=%q", rows[0].Actor, rows[0].Target)
	}
}

func TestChannelSurfaceAnswers503WhenAPIDown(t *testing.T) {
	s, stub := newChannelsStack(t)
	root := s.fullyProvisioned(t, "channels-down@it.test")
	cookie := root.freshCookie(t, s)

	stub.down()
	status, _, raw := s.do(t, "GET", "/api/channels", "", cookie)
	if status != http.StatusServiceUnavailable {
		t.Fatalf("list with api down: %d %s", status, raw)
	}
	code, err := totpNow(root.TOTPSecr)
	if err != nil {
		t.Fatal(err)
	}
	status, _, raw = s.post(t, "/api/channels/sms/activate",
		fmt.Sprintf(`{"credentials":{"endpoint":"http://sms.local/send","apiKey":"k1"},"code":%q}`, code), cookie)
	if status != http.StatusServiceUnavailable {
		t.Fatalf("activate with api down: %d %s", status, raw)
	}
}
