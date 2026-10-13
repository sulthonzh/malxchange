// Code-channel drivers (ticket #38, ADR-0006): the transports that
// deliver Verification codes once an admin activates them with
// credentials. Drivers are dormant by construction — the registry only
// invokes a driver for an active channel row, and every seeded row is
// inactive, so an unactivated driver is a dead path at runtime. Email
// is always on and lives in mailer.go, never here.
package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// The admin-activatable Code channels (GLOSSARY: a Code channel is the
// transport; email is the always-on one and has no registry row).
const (
	ChannelSMS      = "sms"
	ChannelWhatsApp = "whatsapp"
	ChannelTelegram = "telegram"
)

const (
	defaultTelegramAPIBase = "https://api.telegram.org"
	defaultWhatsAppGraph   = "https://graph.facebook.com"
	whatsappGraphVersion   = "v21.0"
)

var errUnknownChannel = errors.New("unknown code channel")

// ChannelCredentials is the admin-supplied credential set of one Code
// channel, stored as JSON in the registry and parsed per channel: only
// the named channel's keys are required, and driver defaults fill the
// rest. Secrets — never on any response, member-facing or internal.
type ChannelCredentials struct {
	Endpoint      string `json:"endpoint"`      // sms: the HTTP endpoint; whatsapp: Graph API base URL
	APIKey        string `json:"apiKey"`        // sms
	PhoneNumberID string `json:"phoneNumberId"` // whatsapp
	AccessToken   string `json:"accessToken"`   // whatsapp
	BotToken      string `json:"botToken"`      // telegram
	WebhookSecret string `json:"webhookSecret"` // telegram: echoed in X-Telegram-Bot-Api-Secret-Token
	APIBase       string `json:"apiBase"`       // telegram: Bot API base; the default is api.telegram.org (self-hosted servers and tests override)
}

// parseChannelCredentials validates one channel's credential JSON,
// filling driver defaults. The returned error names the missing keys so
// the admin RPC can refuse activation loudly.
func parseChannelCredentials(name string, raw []byte) (ChannelCredentials, error) {
	if len(raw) == 0 {
		raw = []byte("{}")
	}
	var c ChannelCredentials
	if err := json.Unmarshal(raw, &c); err != nil {
		return c, fmt.Errorf("channel %s credentials are not a JSON object: %w", name, err)
	}
	switch name {
	case ChannelSMS:
		if c.Endpoint == "" || c.APIKey == "" {
			return c, fmt.Errorf("channel sms needs credentials endpoint and apiKey")
		}
		if err := checkHTTPURL("sms endpoint", c.Endpoint); err != nil {
			return c, err
		}
	case ChannelWhatsApp:
		if c.PhoneNumberID == "" || c.AccessToken == "" {
			return c, fmt.Errorf("channel whatsapp needs credentials phoneNumberId and accessToken")
		}
		if c.Endpoint == "" {
			c.Endpoint = defaultWhatsAppGraph
		}
		if err := checkHTTPURL("whatsapp endpoint", c.Endpoint); err != nil {
			return c, err
		}
	case ChannelTelegram:
		if c.BotToken == "" || c.WebhookSecret == "" {
			return c, fmt.Errorf("channel telegram needs credentials botToken and webhookSecret")
		}
		if c.APIBase == "" {
			c.APIBase = defaultTelegramAPIBase
		}
		if err := checkHTTPURL("telegram apiBase", c.APIBase); err != nil {
			return c, err
		}
	default:
		return c, fmt.Errorf("%w: %q", errUnknownChannel, name)
	}
	return c, nil
}

func checkHTTPURL(what, raw string) error {
	u, err := url.Parse(raw)
	if err != nil || !u.IsAbs() || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("api: %s must be an absolute http(s) URL: %q", what, raw)
	}
	return nil
}

// Sender delivers one message over an activated Code channel; to is the
// member's contact on that channel (phone number or Telegram chat id).
// The Mailer's channel-scoped counterpart — email never goes through
// here.
type Sender interface {
	Send(ctx context.Context, c ChannelCredentials, to, message string) error
}

// channelSenders maps each admin-activatable channel to its driver.
var channelSenders = map[string]Sender{
	ChannelSMS:      smsSender{},
	ChannelWhatsApp: whatsappSender{},
	ChannelTelegram: telegramSender{},
}

// postJSON is the drivers' shared HTTP leg: one POST with a bearer
// token and a JSON body; any 2xx answer means delivered.
func postJSON(ctx context.Context, target, bearer string, body []byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("api: channel request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if bearer != "" {
		req.Header.Set("Authorization", bearer)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("api: channel request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("api: channel delivery: %s: %s", resp.Status, strings.TrimSpace(string(snippet)))
	}
	return nil
}

// smsSender talks to a generic HTTP SMS endpoint: POST {endpoint} with
// the API key as a bearer token and {"to", "message"}. The wire shape
// is api's own, so a single-host install can front any SMSC with a
// small adapter.
type smsSender struct{}

func (smsSender) Send(ctx context.Context, c ChannelCredentials, to, message string) error {
	body, err := json.Marshal(map[string]string{"to": to, "message": message})
	if err != nil {
		return err
	}
	return postJSON(ctx, c.Endpoint, "Bearer "+c.APIKey, body)
}

// whatsappSender talks to the WhatsApp Cloud API: POST
// {endpoint}/v21.0/{phoneNumberId}/messages with the access token.
type whatsappSender struct{}

func (whatsappSender) Send(ctx context.Context, c ChannelCredentials, to, message string) error {
	body, err := json.Marshal(map[string]any{
		"messaging_product": "whatsapp",
		"to":                to,
		"type":              "text",
		"text":              map[string]string{"body": message},
	})
	if err != nil {
		return err
	}
	target := strings.TrimSuffix(c.Endpoint, "/") + "/" + whatsappGraphVersion + "/" + c.PhoneNumberID + "/messages"
	return postJSON(ctx, target, "Bearer "+c.AccessToken, body)
}

// telegramSender talks to the Telegram Bot API.
type telegramSender struct{}

// telegramURL builds a Bot API method URL under the configured base.
func telegramURL(c ChannelCredentials, method string) string {
	return strings.TrimSuffix(c.APIBase, "/") + "/bot" + c.BotToken + "/" + method
}

func (telegramSender) Send(ctx context.Context, c ChannelCredentials, to, message string) error {
	chat, err := strconv.ParseInt(to, 10, 64)
	if err != nil {
		return fmt.Errorf("api: telegram chat id %q: %w", to, err)
	}
	body, err := json.Marshal(map[string]any{"chat_id": chat, "text": message})
	if err != nil {
		return err
	}
	return postJSON(ctx, telegramURL(c, "sendMessage"), "Bearer "+c.BotToken, body)
}

// BotUsername resolves the bot's @username (getMe) for the start-the-bot
// deep link.
func (telegramSender) BotUsername(ctx context.Context, c ChannelCredentials) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, telegramURL(c, "getMe"), nil)
	if err != nil {
		return "", fmt.Errorf("api: telegram getMe: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.BotToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("api: telegram getMe: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("api: telegram getMe: %s", resp.Status)
	}
	var out struct {
		OK     bool `json:"ok"`
		Result struct {
			Username string `json:"username"`
		} `json:"result"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<16)).Decode(&out); err != nil {
		return "", fmt.Errorf("api: telegram getMe decode: %w", err)
	}
	if !out.OK || out.Result.Username == "" {
		return "", errors.New("api: telegram getMe rejected the bot token")
	}
	return out.Result.Username, nil
}
