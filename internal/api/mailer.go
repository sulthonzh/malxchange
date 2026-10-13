// Mail delivery behind one interface so tests capture codes and operators
// point the SMTP driver at any relay (or the compose test-profile mail
// sink). Backend member-facing text lives here as the centralized English
// template set (ADR-0013 rule 4: one template per message).
package api

import (
	"context"
	"log/slog"
	"net/smtp"
	"strings"
	"sync"
)

// Mailer delivers a plain-text email. Errors surface to the caller, which
// turns them into an English member-facing message.
type Mailer interface {
	Send(ctx context.Context, to, subject, body string) error
}

// NewMailer picks the driver from the environment: SMTP when a host is
// configured, otherwise a discard driver that logs (single-host installs
// without a relay still boot; registration codes are then undeliverable
// and that is visible in the log).
func NewMailer(env *APIEnv, log *slog.Logger) Mailer {
	if env.SMTPHost == "" {
		log.Warn("api mail delivery disabled: MAL_API_SMTP_HOST is empty")
		return discardMailer{log: log}
	}
	return &smtpMailer{env: env}
}

type smtpMailer struct{ env *APIEnv }

func (m *smtpMailer) Send(_ context.Context, to, subject, body string) error {
	addr := m.env.SMTPHost + ":" + m.env.SMTPPort
	msg := strings.Join([]string{
		"From: " + m.env.SMTPFrom,
		"To: " + to,
		"Subject: " + subject,
		"MIME-Version: 1.0",
		"Content-Type: text/plain; charset=utf-8",
		"",
		body,
	}, "\r\n")
	// net/smtp authenticates whenever the server advertises AUTH, and
	// smtp.PlainAuth refuses to send credentials over a cleartext
	// connection — so a relay that advertises AUTH but not STARTTLS
	// (the test mail sink) fails the authenticated attempt. Fall back
	// to an unauthenticated delivery; a relay that truly requires auth
	// rejects that too, and SendMail reports it.
	if m.env.SMTPUser == "" {
		return smtp.SendMail(addr, nil, m.env.SMTPFrom, []string{to}, []byte(msg))
	}
	auth := smtp.PlainAuth("", m.env.SMTPUser, m.env.SMTPPass, m.env.SMTPHost)
	if err := smtp.SendMail(addr, auth, m.env.SMTPFrom, []string{to}, []byte(msg)); err == nil {
		return nil
	}
	return smtp.SendMail(addr, nil, m.env.SMTPFrom, []string{to}, []byte(msg))
}

type discardMailer struct{ log *slog.Logger }

func (d discardMailer) Send(_ context.Context, to, subject, _ string) error {
	d.log.Warn("discarding email (no SMTP configured)", "to", to, "subject", subject)
	return nil
}

// CapturedMail is one delivered message as the test saw it.
type CapturedMail struct{ To, Subject, Body string }

// CaptureMailer records every Send in memory; integration tests read the
// Verification codes out of the bodies. Safe for concurrent use.
type CaptureMailer struct {
	mu   sync.Mutex
	mail []CapturedMail
}

func (c *CaptureMailer) Send(_ context.Context, to, subject, body string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.mail = append(c.mail, CapturedMail{To: to, Subject: subject, Body: body})
	return nil
}

// Mail returns every captured message in delivery order.
func (c *CaptureMailer) Mail() []CapturedMail {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]CapturedMail(nil), c.mail...)
}
