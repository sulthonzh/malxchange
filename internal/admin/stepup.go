// Step-up (ADR-0012): the fresh-TOTP-at-action-time challenge for
// fund- and credential-moving actions. Password re-auth is NEVER
// Step-up — a session thief already has the password — so this helper
// reads only the request's code field and never any password field.
// It is the seam every later surface calls: manual balance adjust,
// member TOTP reset, withdrawal approval, hot→cold transfer,
// broadcast/sweep retry, and Code channel credential saves (#58-#63);
// employee create and employee password/TOTP resets (#54) are the
// first callers.
package admin

import (
	"net/http"
	"time"

	"github.com/syaikhipin/malxchange/internal/auth"
)

const (
	errStepUpRequired = "A fresh authenticator code is required for this action."
	errStepUpInvalid  = "That code is not valid. Check your authenticator app and try again."
)

// requireStepUp verifies a fresh authenticator code against the
// CALLER's bound TOTP secret at action time and answers the refusal
// itself. Callers hand it the code field of their own request body.
func (s *Server) requireStepUp(w http.ResponseWriter, r *http.Request, code string) bool {
	emp, _ := employeeFrom(r.Context())
	if code == "" {
		WriteError(w, http.StatusUnauthorized, errStepUpRequired)
		return false
	}
	if emp.TOTPSecret == "" || !auth.VerifyTOTP(emp.TOTPSecret, code, time.Now()) {
		WriteError(w, http.StatusUnauthorized, errStepUpInvalid)
		return false
	}
	return true
}
