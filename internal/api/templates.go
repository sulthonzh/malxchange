package api

import (
	"fmt"
	"strings"
	"text/template"
	"time"
)

// The one centralized set of member-facing English message templates
// (ADR-0013 rule 4: backend member-facing text lives here, complete
// sentences at single interpolation points with named parameters — no
// copy assembled from fragments elsewhere). Email templates render
// subject+body; the admin-activated Code channels (ticket #38) render
// one plain-text message.

var registerCodeTmpl = template.Must(template.New("register-code").Parse(
	`Enter this verification code to finish creating your account: {{.Code}}.

The code expires in {{.ExpiresIn}} and can be used only once.

If you did not try to create an account, you can ignore this email.`))

type registerCodeData struct {
	Code      string
	ExpiresIn string
}

// RegisterCodeEmail renders the registration Verification code email.
func RegisterCodeEmail(code string, ttl time.Duration) (subject, body string, err error) {
	var b strings.Builder
	if err := registerCodeTmpl.Execute(&b, registerCodeData{Code: code, ExpiresIn: ttlWords(ttl)}); err != nil {
		return "", "", fmt.Errorf("api: register code template: %w", err)
	}
	return "Your verification code", b.String(), nil
}

// codeTmplData interpolates every plain code message (phone and
// Telegram channels).
type codeTmplData struct {
	Code      string
	ExpiresIn string
}

var phoneCodeTmpl = template.Must(template.New("phone-code").Parse(
	`Enter this verification code to bind your phone: {{.Code}}.

The code expires in {{.ExpiresIn}} and can be used only once.

If you did not try to bind a phone, you can ignore this message.`))

// PhoneCodeMessage renders the phone-channel Verification code message
// (sms and whatsapp drivers).
func PhoneCodeMessage(code string, ttl time.Duration) (string, error) {
	var b strings.Builder
	if err := phoneCodeTmpl.Execute(&b, codeTmplData{Code: code, ExpiresIn: ttlWords(ttl)}); err != nil {
		return "", fmt.Errorf("api: phone code template: %w", err)
	}
	return b.String(), nil
}

var telegramCodeTmpl = template.Must(template.New("telegram-code").Parse(
	`Enter this verification code to confirm your Telegram: {{.Code}}.

The code expires in {{.ExpiresIn}} and can be used only once.

If you did not request it, you can ignore this message.`))

// TelegramCodeMessage renders the Telegram Verification code message.
func TelegramCodeMessage(code string, ttl time.Duration) (string, error) {
	var b strings.Builder
	if err := telegramCodeTmpl.Execute(&b, codeTmplData{Code: code, ExpiresIn: ttlWords(ttl)}); err != nil {
		return "", fmt.Errorf("api: telegram code template: %w", err)
	}
	return b.String(), nil
}

var securityCodeTmpl = template.Must(template.New("security-code").Parse(
	`Enter this verification code to {{.Action}}: {{.Code}}.

The code expires in {{.ExpiresIn}} and can be used only once.

If you did not request this, you can ignore this email.`))

type securityCodeData struct {
	Code      string
	Action    string
	ExpiresIn string
}

// SecurityCodeEmail renders a sensitive-flow Verification code email;
// action is the member-facing purpose ("change your password",
// "confirm your new email address", …) interpolated at one point.
func SecurityCodeEmail(code string, ttl time.Duration, action string) (subject, body string, err error) {
	var b strings.Builder
	if err := securityCodeTmpl.Execute(&b, securityCodeData{Code: code, Action: action, ExpiresIn: ttlWords(ttl)}); err != nil {
		return "", "", fmt.Errorf("api: security code template: %w", err)
	}
	return "Your verification code", b.String(), nil
}

var authenticatorResetTmpl = template.Must(template.New("authenticator-reset").Parse(
	`Your authenticator was reset by our support team after an identity check.

Withdrawals stay blocked until you bind a new authenticator in your member center security settings.

If you did not ask for this reset, contact support immediately.`))

// AuthenticatorResetEmail is the notification every reset sends the
// member (bound phone or Telegram channels are notified too — see the
// admin reset flow).
func AuthenticatorResetEmail() (subject, body string, err error) {
	var b strings.Builder
	if err := authenticatorResetTmpl.Execute(&b, nil); err != nil {
		return "", "", fmt.Errorf("api: authenticator reset template: %w", err)
	}
	return "Your authenticator was reset", b.String(), nil
}

// ttlWords renders a TTL as plain English words for template
// interpolation.
func ttlWords(d time.Duration) string {
	mins := int(d.Minutes())
	switch {
	case mins >= 60 && mins%60 == 0:
		h := mins / 60
		if h == 1 {
			return "1 hour"
		}
		return fmt.Sprintf("%d hours", h)
	case mins == 1:
		return "1 minute"
	case mins < 1:
		secs := int(d.Seconds())
		if secs == 1 {
			return "1 second"
		}
		return fmt.Sprintf("%d seconds", secs)
	default:
		return fmt.Sprintf("%d minutes", mins)
	}
}
