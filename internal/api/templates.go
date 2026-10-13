package api

import (
	"fmt"
	"strings"
	"text/template"
	"time"
)

// The one centralized set of member-facing English email templates
// (ADR-0013 rule 4: backend member-facing text lives here, complete
// sentences at single interpolation points with named parameters — no
// copy assembled from fragments elsewhere).

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
