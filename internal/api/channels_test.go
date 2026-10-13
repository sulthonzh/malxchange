// Unit coverage for the Code-channel machinery that needs no Postgres
// (the registry's HTTP/state legs are the integration seam in
// channels_it_test.go): credential parsing per driver, each driver's
// wire contract against a stub HTTP server, and the status-only shape
// of the registry list.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestParseChannelCredentials(t *testing.T) {
	for name, raw := range map[string]string{
		ChannelSMS:      `{"endpoint":"http://sms.local/send","apiKey":"k"}`,
		ChannelWhatsApp: `{"phoneNumberId":"1","accessToken":"t"}`,
		ChannelTelegram: `{"botToken":"b","webhookSecret":"s"}`,
	} {
		c, err := parseChannelCredentials(name, []byte(raw))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		switch name {
		case ChannelWhatsApp:
			if c.Endpoint != defaultWhatsAppGraph {
				t.Fatalf("whatsapp endpoint default = %q", c.Endpoint)
			}
		case ChannelTelegram:
			if c.APIBase != defaultTelegramAPIBase {
				t.Fatalf("telegram apiBase default = %q", c.APIBase)
			}
		}
	}
	// Per-channel missing keys are named, not silently accepted.
	for name, raw := range map[string]string{
		ChannelSMS:      `{"endpoint":"http://x"}`,
		ChannelWhatsApp: `{"endpoint":"http://x","apiKey":"k"}`,
		ChannelTelegram: `{"botToken":"b"}`,
	} {
		if _, err := parseChannelCredentials(name, []byte(raw)); err == nil {
			t.Fatalf("%s: expected missing-credential error for %s", name, raw)
		}
	}
	if _, err := parseChannelCredentials(ChannelSMS, nil); err == nil {
		t.Fatal("empty credentials accepted for sms")
	}
	if _, err := parseChannelCredentials("pigeon", []byte(`{}`)); !errors.Is(err, errUnknownChannel) {
		t.Fatalf("unknown channel error = %v", err)
	}
	if _, err := parseChannelCredentials(ChannelSMS, []byte(`{"endpoint":"ftp://x","apiKey":"k"}`)); err == nil {
		t.Fatal("non-http endpoint accepted")
	}
	if _, err := parseChannelCredentials(ChannelTelegram, []byte(`not json`)); err == nil {
		t.Fatal("garbage credentials accepted")
	}
}

// stubServer records one request and answers with the check's response;
// the driver contract tests assert on exactly what the driver put on
// the wire.
func stubServer(t *testing.T, check func(w http.ResponseWriter, r *http.Request, body []byte)) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<16))
		check(w, r, body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func decode(t *testing.T, body []byte) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("driver body not JSON: %v (%s)", err, body)
	}
	return out
}

func TestSMSSenderContract(t *testing.T) {
	srv := stubServer(t, func(w http.ResponseWriter, r *http.Request, body []byte) {
		if r.Method != http.MethodPost || r.URL.Path != "/send" {
			t.Errorf("sms request %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer k1" {
			t.Errorf("sms auth = %q", got)
		}
		b := decode(t, body)
		if b["to"] != "+15551234567" || b["message"] != "hello" {
			t.Errorf("sms body = %v", b)
		}
		w.WriteHeader(http.StatusOK)
	})
	c, err := parseChannelCredentials(ChannelSMS,
		[]byte(fmt.Sprintf(`{"endpoint":%q,"apiKey":"k1"}`, srv.URL+"/send")))
	if err != nil {
		t.Fatal(err)
	}
	if err := channelSenders[ChannelSMS].Send(context.Background(), c, "+15551234567", "hello"); err != nil {
		t.Fatal(err)
	}

	fail := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "nope", http.StatusBadGateway)
	}))
	defer fail.Close()
	c2, _ := parseChannelCredentials(ChannelSMS, []byte(fmt.Sprintf(`{"endpoint":%q,"apiKey":"k1"}`, fail.URL)))
	if err := channelSenders[ChannelSMS].Send(context.Background(), c2, "+15551234567", "hello"); err == nil {
		t.Fatal("non-2xx sms answer counted as delivered")
	}
}

func TestWhatsAppSenderContract(t *testing.T) {
	srv := stubServer(t, func(w http.ResponseWriter, r *http.Request, body []byte) {
		if r.URL.Path != "/v21.0/ph1/messages" {
			t.Errorf("whatsapp path = %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer wa-tok" {
			t.Errorf("whatsapp auth = %q", got)
		}
		b := decode(t, body)
		if b["messaging_product"] != "whatsapp" || b["type"] != "text" || b["to"] != "+15551234567" {
			t.Errorf("whatsapp body = %v", b)
		}
		if text, _ := b["text"].(map[string]any); text["body"] != "hello" {
			t.Errorf("whatsapp text = %v", b["text"])
		}
		w.WriteHeader(http.StatusOK)
	})
	c, err := parseChannelCredentials(ChannelWhatsApp,
		[]byte(fmt.Sprintf(`{"endpoint":%q,"phoneNumberId":"ph1","accessToken":"wa-tok"}`, srv.URL)))
	if err != nil {
		t.Fatal(err)
	}
	if err := channelSenders[ChannelWhatsApp].Send(context.Background(), c, "+15551234567", "hello"); err != nil {
		t.Fatal(err)
	}
}

func TestTelegramSenderContract(t *testing.T) {
	const token = "7:bottok"
	srv := stubServer(t, func(w http.ResponseWriter, r *http.Request, body []byte) {
		if r.URL.Path != "/bot"+token+"/sendMessage" {
			t.Errorf("telegram path = %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer "+token {
			t.Errorf("telegram auth = %q", got)
		}
		b := decode(t, body)
		if b["chat_id"] != float64(4242) || b["text"] != "hello" {
			t.Errorf("telegram body = %v", b)
		}
		w.WriteHeader(http.StatusOK)
	})
	c, err := parseChannelCredentials(ChannelTelegram,
		[]byte(fmt.Sprintf(`{"botToken":%q,"webhookSecret":"s","apiBase":%q}`, token, srv.URL)))
	if err != nil {
		t.Fatal(err)
	}
	if err := channelSenders[ChannelTelegram].Send(context.Background(), c, "4242", "hello"); err != nil {
		t.Fatal(err)
	}
	if err := channelSenders[ChannelTelegram].Send(context.Background(), c, "not-a-chat", "hello"); err == nil {
		t.Fatal("non-numeric chat id accepted")
	}
}

func TestTelegramBotUsername(t *testing.T) {
	srv := stubServer(t, func(w http.ResponseWriter, r *http.Request, _ []byte) {
		if r.Method != http.MethodGet || r.URL.Path != "/bot7:bottok/getMe" {
			t.Errorf("getMe request %s %s", r.Method, r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"ok":true,"result":{"id":7,"username":"my_bot"}}`))
	})
	c, _ := parseChannelCredentials(ChannelTelegram,
		[]byte(fmt.Sprintf(`{"botToken":"7:bottok","webhookSecret":"s","apiBase":%q}`, srv.URL)))
	bot, err := (telegramSender{}).BotUsername(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	if bot != "my_bot" {
		t.Fatalf("bot username = %q", bot)
	}

	rejected := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"ok":false,"error_code":401}`))
	}))
	defer rejected.Close()
	c2, _ := parseChannelCredentials(ChannelTelegram,
		[]byte(fmt.Sprintf(`{"botToken":"7:bottok","webhookSecret":"s","apiBase":%q}`, rejected.URL)))
	if _, err := (telegramSender{}).BotUsername(context.Background(), c2); err == nil {
		t.Fatal("rejected bot token produced a username")
	}
}

func TestChannelStateCarriesNoCredentials(t *testing.T) {
	raw, err := json.Marshal(channelState{Channel: ChannelSMS, Active: true, UpdatedAt: "2026-10-13T00:00:00Z"})
	if err != nil {
		t.Fatal(err)
	}
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(raw, &probe); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"channel", "active", "activatedAt", "updatedAt"} {
		if _, ok := probe[k]; !ok {
			t.Fatalf("channelState missing key %q: %s", k, raw)
		}
	}
	for _, k := range []string{"credentials", "apiKey", "accessToken", "botToken", "webhookSecret"} {
		if _, ok := probe[k]; ok {
			t.Fatalf("channelState leaked credential field %q: %s", k, raw)
		}
	}
}

func TestValidPhone(t *testing.T) {
	for _, p := range []string{"+15551234567", "+441632960961", "+8613800138000"} {
		if !validPhone(p) {
			t.Errorf("valid phone rejected: %s", p)
		}
	}
	for _, p := range []string{"15551234567", "+123456", "+15551234567890123", "+1x551234567", " +15551234567", ""} {
		if validPhone(p) {
			t.Errorf("invalid phone accepted: %q", p)
		}
	}
}

func TestChannelMessageTemplates(t *testing.T) {
	msg, err := PhoneCodeMessage("123456", 3000*1e9) // 50 minutes
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg, "123456") || !strings.Contains(msg, "50 minutes") {
		t.Fatalf("phone message = %q", msg)
	}
	msg, err = TelegramCodeMessage("654321", 3000*1e9)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg, "654321") {
		t.Fatalf("telegram message = %q", msg)
	}
}
