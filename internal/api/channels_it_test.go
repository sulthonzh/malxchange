//go:build integration

// Code channels over the real seam (ticket #38, ADR-0006): the admin
// internal RPC against the registry, delivery through stub HTTP
// providers (tronstub style — fake servers that record what delivery
// sent), the Telegram start-the-bot binding gate, and credential
// secrecy. Assertions go through the served surface only: activation
// flips delivery, dormant drivers are dead paths, and channel
// credentials never appear on any member-facing response.
package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// ---- stub providers (test infrastructure, not providers) ----

// stubHit is one request a stub provider served.
type stubHit struct {
	Method string
	Path   string
	Auth   string
	Body   map[string]any
}

// channelStub is a fake HTTP Code-channel provider: every request is
// recorded, then a channel-specific handler answers the driver's wire
// contract.
type channelStub struct {
	*httptest.Server
	mu   sync.Mutex
	hits []stubHit
}

func newChannelStub(t *testing.T, handle http.HandlerFunc) *channelStub {
	t.Helper()
	s := &channelStub{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hit := stubHit{Method: r.Method, Path: r.URL.Path, Auth: r.Header.Get("Authorization")}
		if r.Body != nil {
			raw, _ := io.ReadAll(io.LimitReader(r.Body, 1<<16))
			_ = json.Unmarshal(raw, &hit.Body)
		}
		s.mu.Lock()
		s.hits = append(s.hits, hit)
		s.mu.Unlock()
		handle(w, r)
	}))
	t.Cleanup(s.Close)
	return s
}

func (s *channelStub) recorded() []stubHit {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]stubHit(nil), s.hits...)
}

// newSMSStub answers the generic HTTP SMS contract (any 2xx).
func newSMSStub(t *testing.T) *channelStub {
	return newChannelStub(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"accepted":true}`))
	})
}

// newTelegramStub answers the Bot API slice the drivers use: getMe and
// sendMessage under /bot<token>/.
func newTelegramStub(t *testing.T, botToken, username string) *channelStub {
	return newChannelStub(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/bot" + botToken + "/getMe":
			_, _ = w.Write([]byte(fmt.Sprintf(`{"ok":true,"result":{"id":7,"username":%q}}`, username)))
		case "/bot" + botToken + "/sendMessage":
			_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":1}}`))
		default:
			http.NotFound(w, r)
		}
	})
}

// ---- admin-RPC helpers (raw bodies: the list endpoint answers an array) ----

func (s *itStack) adminDo(method, path string, body any) (int, string) {
	s.t.Helper()
	var rdr io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			s.t.Fatal(err)
		}
		rdr = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, s.rest.URL+path, rdr)
	if err != nil {
		s.t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		s.t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(raw)
}

// webhook simulates Telegram calling the binding endpoint with the
// secret-token header.
func (s *itStack) webhook(secret string, upd any) (int, map[string]any) {
	s.t.Helper()
	raw, err := json.Marshal(upd)
	if err != nil {
		s.t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPost, s.rest.URL+"/api/uc/telegram/webhook", bytes.NewReader(raw))
	if err != nil {
		s.t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(telegramWebhookHeader, secret)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		s.t.Fatal(err)
	}
	defer resp.Body.Close()
	out := map[string]any{}
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

// ---- the seam ----

// Dormant drivers are dead paths: with no channel activated only email
// codes work, phone/Telegram requests are refused, and the stubs prove
// nothing was ever sent.
func TestChannelsDormantUntilActivated(t *testing.T) {
	s := newStack(t)
	sms := newSMSStub(t)
	tg := newTelegramStub(t, "dormant-token", "dormant_bot")

	status, raw := s.adminDo(http.MethodGet, "/internal/channels", nil)
	if status != http.StatusOK {
		t.Fatalf("list channels: status %d body %s", status, raw)
	}
	var states []map[string]any
	if err := json.Unmarshal([]byte(raw), &states); err != nil {
		t.Fatalf("list channels decode: %v (%s)", err, raw)
	}
	if len(states) != 3 {
		t.Fatalf("want 3 dormant channels, got %d (%s)", len(states), raw)
	}
	for _, st := range states {
		if st["active"] != false {
			t.Fatalf("channel %v ships active: %s", st["channel"], raw)
		}
	}

	token := s.register("dormant@example.com", "a-fine-password")

	if status, body := s.post("/api/uc/phone/code", map[string]string{"phone": "+15551234567"}, token); status != http.StatusConflict {
		t.Fatalf("phone code while dormant: status %d body %v", status, body)
	}
	if status, body := s.post("/api/uc/channels/telegram/link", nil, token); status != http.StatusConflict {
		t.Fatalf("telegram link while dormant: status %d body %v", status, body)
	}
	if status, body := s.post("/api/uc/telegram/webhook", map[string]any{
		"message": map[string]any{"chat": map[string]any{"id": 1}, "text": "/start x"},
	}, ""); status != http.StatusNotFound {
		t.Fatalf("webhook while dormant: status %d body %v", status, body)
	}
	if n := len(sms.recorded()) + len(tg.recorded()); n != 0 {
		t.Fatalf("dormant drivers talked to providers: %d requests", n)
	}

	// Email stays always on: the register above already captured its code.
	if mail := s.mailer.Mail(); len(mail) == 0 {
		t.Fatal("email delivery did not run while channels were dormant")
	}
}

// Activation flips delivery: the internal RPC turns the sms channel on
// with the stub's endpoint, phone codes deliver through it, deactivation
// kills the path again, and whatsapp picks the phone up when it is the
// active one.
func TestActivationFlipsPhoneDelivery(t *testing.T) {
	s := newStack(t)
	sms := newSMSStub(t)
	wa := newChannelStub(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"messages":[{"id":"wamid.1"}]}`))
	})

	status, raw := s.adminDo(http.MethodPost, "/internal/channels/sms/activate", map[string]any{
		"credentials": map[string]string{"endpoint": sms.URL, "apiKey": "sekrit-sms-key"},
	})
	if status != http.StatusNoContent {
		t.Fatalf("activate sms: status %d body %s", status, raw)
	}
	status, raw = s.adminDo(http.MethodGet, "/internal/channels", nil)
	if !strings.Contains(raw, `"channel":"sms","active":true`) {
		t.Fatalf("sms not listed active: %s", raw)
	}

	token := s.register("phones@example.com", "a-fine-password")
	phone := "+15551234567"
	if status, body := s.post("/api/uc/phone/code", map[string]string{"phone": phone}, token); status != http.StatusOK {
		t.Fatalf("phone code: status %d body %v", status, body)
	}
	hits := sms.recorded()
	if len(hits) != 1 {
		t.Fatalf("sms provider: want 1 delivery, got %d", len(hits))
	}
	if hits[0].Method != http.MethodPost || hits[0].Auth != "Bearer sekrit-sms-key" {
		t.Fatalf("sms delivery request wrong: %+v", hits[0])
	}
	if hits[0].Body["to"] != phone {
		t.Fatalf("sms delivered to %v, want %s", hits[0].Body["to"], phone)
	}
	msg, _ := hits[0].Body["message"].(string)
	if match := codeRe.FindStringSubmatch(msg); match == nil {
		t.Fatalf("sms message carries no code: %q", msg)
	}

	// Deactivate: the driver is a dead path again.
	if status, raw := s.adminDo(http.MethodPost, "/internal/channels/sms/deactivate", nil); status != http.StatusNoContent {
		t.Fatalf("deactivate sms: status %d body %s", status, raw)
	}
	if status, body := s.post("/api/uc/phone/code", map[string]string{"phone": phone}, token); status != http.StatusConflict {
		t.Fatalf("phone code after deactivate: status %d body %v", status, body)
	}

	// WhatsApp as the only active phone channel: codes ride it instead.
	if status, raw := s.adminDo(http.MethodPost, "/internal/channels/whatsapp/activate", map[string]any{
		"credentials": map[string]string{
			"endpoint": wa.URL, "phoneNumberId": "102030405", "accessToken": "sekrit-wa-token"},
	}); status != http.StatusNoContent {
		t.Fatalf("activate whatsapp: status %d body %s", status, raw)
	}
	// A different phone: the first one is still inside its cooldown.
	waPhone := "+447700900123"
	if status, body := s.post("/api/uc/phone/code", map[string]string{"phone": waPhone}, token); status != http.StatusOK {
		t.Fatalf("phone code over whatsapp: status %d body %v", status, body)
	}
	waHits := wa.recorded()
	if len(waHits) != 1 || waHits[0].Path != "/v21.0/102030405/messages" || waHits[0].Auth != "Bearer sekrit-wa-token" {
		t.Fatalf("whatsapp delivery request wrong: %+v", waHits)
	}
	if waHits[0].Body["to"] != waPhone {
		t.Fatalf("whatsapp delivered to %v, want %s", waHits[0].Body["to"], waPhone)
	}
	if n := len(sms.recorded()); n != 1 {
		t.Fatalf("sms provider got extra traffic after deactivation: %d", n)
	}
}

// The Telegram binding flow: deep-link token → webhook binds the chat →
// codes deliver to that chat; codes to unbound members are rejected.
func TestTelegramBindingGate(t *testing.T) {
	s := newStack(t)
	const botToken, webhookSecret, username = "tg-bot-token", "whsekrit", "malx_test_bot"
	tg := newTelegramStub(t, botToken, username)

	status, raw := s.adminDo(http.MethodPost, "/internal/channels/telegram/activate", map[string]any{
		"credentials": map[string]string{
			"botToken": botToken, "webhookSecret": webhookSecret, "apiBase": tg.URL},
	})
	if status != http.StatusNoContent {
		t.Fatalf("activate telegram: status %d body %s", status, raw)
	}

	token := s.register("tg@example.com", "a-fine-password")

	// Codes to an unbound member are rejected before anything is sent.
	if status, body := s.post("/api/uc/channels/telegram/code", nil, token); status != http.StatusBadRequest {
		t.Fatalf("telegram code while unbound: status %d body %v", status, body)
	}

	// The deep link: bot username resolved live from getMe plus the
	// per-member binding token.
	status, body := s.post("/api/uc/channels/telegram/link", nil, token)
	if status != http.StatusOK {
		t.Fatalf("telegram link: status %d body %v", status, body)
	}
	link, _ := body["link"].(string)
	prefix := "https://t.me/" + username + "?start="
	if !strings.HasPrefix(link, prefix) {
		t.Fatalf("deep link %q lacks %q", link, prefix)
	}
	binding := strings.TrimPrefix(link, prefix)

	// The webhook rejects a wrong secret outright.
	if status, body := s.webhook("wrong-secret", map[string]any{
		"message": map[string]any{"chat": map[string]any{"id": 424242}, "text": "/start " + binding},
	}); status != http.StatusForbidden {
		t.Fatalf("webhook wrong secret: status %d body %v", status, body)
	}

	// Telegram's update carrying /start <token> binds chat ↔ member.
	if status, body := s.webhook(webhookSecret, map[string]any{
		"update_id": 1,
		"message":   map[string]any{"chat": map[string]any{"id": 424242}, "text": "/start " + binding},
	}); status != http.StatusOK {
		t.Fatalf("webhook bind: status %d body %v", status, body)
	}

	// Codes now flow to the bound chat.
	if status, body := s.post("/api/uc/channels/telegram/code", nil, token); status != http.StatusOK {
		t.Fatalf("telegram code: status %d body %v", status, body)
	}
	var sent *stubHit
	for i, h := range tg.recorded() {
		if h.Path == "/bot"+botToken+"/sendMessage" {
			sent = &tg.recorded()[i]
			break
		}
	}
	if sent == nil {
		t.Fatal("no sendMessage hit on the telegram stub")
	}
	if sent.Body["chat_id"] != float64(424242) {
		t.Fatalf("telegram code sent to chat %v, want 424242", sent.Body["chat_id"])
	}
	text, _ := sent.Body["text"].(string)
	match := codeRe.FindStringSubmatch(text)
	if match == nil {
		t.Fatalf("telegram message carries no code: %q", text)
	}

	// The delivered code verifies over the member seam.
	if status, body := s.post("/api/uc/channels/telegram/verify", map[string]string{"code": match[1]}, token); status != http.StatusOK {
		t.Fatalf("telegram verify: status %d body %v", status, body)
	}

	// A second member stays gated: the binding is per member.
	token2 := s.register("tg2@example.com", "a-fine-password")
	if status, body := s.post("/api/uc/channels/telegram/code", nil, token2); status != http.StatusBadRequest {
		t.Fatalf("unbound member got telegram codes: status %d body %v", status, body)
	}
}

// Channel credentials never appear on any member-facing response (nor
// on the internal list — the registry answers status only).
func TestChannelCredentialSecrecy(t *testing.T) {
	s := newStack(t)
	sms := newSMSStub(t)
	tg := newTelegramStub(t, "sec-tg-token", "secrecy_bot")
	secrets := []string{
		"sekrit-sms-key", "sekrit-wa-token", "sekrit-tg-bot", "sekrit-tg-wh", "102030405",
	}
	for path, creds := range map[string]map[string]string{
		"/internal/channels/sms/activate": {"endpoint": sms.URL, "apiKey": secrets[0]},
		"/internal/channels/whatsapp/activate": {
			"phoneNumberId": secrets[4], "accessToken": secrets[1]},
		"/internal/channels/telegram/activate": {
			"botToken": secrets[2], "webhookSecret": secrets[3], "apiBase": tg.URL},
	} {
		if status, raw := s.adminDo(http.MethodPost, path, map[string]any{"credentials": creds}); status != http.StatusNoContent {
			t.Fatalf("activate %s: status %d body %s", path, status, raw)
		}
	}

	email := "secrecy@example.com"
	s.requestCode(email) // exercises register/code
	code, err := s.requestCode(email)
	if err != nil {
		t.Fatal(err)
	}
	bodies := []string{}
	status, raw := s.adminDo(http.MethodGet, "/internal/channels", nil)
	if status != http.StatusOK {
		t.Fatalf("list channels: %d %s", status, raw)
	}
	bodies = append(bodies, raw)

	var token string
	status, body := s.post("/api/uc/register", map[string]string{
		"email": email, "code": code, "password": "a-fine-password"}, "")
	if status != http.StatusOK {
		t.Fatalf("register: %d %v", status, body)
	}
	token = body["token"].(string)
	bodies = append(bodies, marshalBody(t, body))

	// Every member-facing endpoint answers; each body joins the scan.
	// (The telegram/code leg lands 400 — this member never bound a chat
	// — and verify lands 400 on the dummy code; both bodies count.)
	st, b := s.post("/api/uc/login", map[string]string{
		"email": email, "password": "a-fine-password", "device": "it"}, "")
	expect(t, "/api/uc/login", st, b, http.StatusOK, &bodies)
	for path, call := range map[string]func() (int, map[string]any){
		"/api/uc/me":       func() (int, map[string]any) { return s.get("/api/uc/me", token) },
		"/api/uc/sessions": func() (int, map[string]any) { return s.get("/api/uc/sessions", token) },
		"/api/uc/phone/code": func() (int, map[string]any) {
			return s.post("/api/uc/phone/code", map[string]string{"phone": "+15551234567"}, token)
		},
		"/api/uc/telegram/link": func() (int, map[string]any) {
			return s.post("/api/uc/channels/telegram/link", nil, token)
		},
		"/api/uc/telegram/code": func() (int, map[string]any) {
			return s.post("/api/uc/channels/telegram/code", nil, token)
		},
		"/api/uc/telegram/verify": func() (int, map[string]any) {
			return s.post("/api/uc/channels/telegram/verify", map[string]string{"code": "000000"}, token)
		},
		"/api/uc/telegram/webhook": func() (int, map[string]any) {
			return s.post("/api/uc/telegram/webhook", map[string]any{
				"message": map[string]any{"chat": map[string]any{"id": 9}, "text": "hello"}}, "")
		},
	} {
		st, b := call()
		expect(t, path, st, b, 0, &bodies) // 0 = any status: the point is the body scan
	}

	for _, b := range bodies {
		for _, secret := range secrets {
			if strings.Contains(b, secret) {
				t.Fatalf("credential %q leaked on a response: %s", secret, b)
			}
		}
	}
}

func marshalBody(t *testing.T, body map[string]any) string {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// expect records one response body for the secrecy scan; want 0 means
// any status is acceptable (the body is what matters).
func expect(t *testing.T, path string, status int, body map[string]any, want int, bodies *[]string) {
	t.Helper()
	if want != 0 && status != want {
		t.Fatalf("%s: status %d body %v", path, status, body)
	}
	*bodies = append(*bodies, marshalBody(t, body))
}
