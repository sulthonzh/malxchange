// Forced first-login setup (ADR-0012): password rotation and TOTP
// enrollment. These are the only mutations an unprovisioned Employee's
// restricted session can reach (see setupAllow); provisioning is
// derived state — the Employee is must_rotate-free AND TOTP-bound, so
// finishing setup unlocks the same session with no second login.
package admin

import (
	"errors"
	"net/http"
	"time"

	"github.com/syaikhipin/malxchange/internal/auth"
)

type passwordRequest struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
}

// handleRotatePassword changes the caller's own password. It is the
// forced first-login rotation for a one-time credential and stays as
// the voluntary self-service change afterwards; both revoke every live
// session (a credential change is a global sign-out).
func (s *Server) handleRotatePassword(w http.ResponseWriter, r *http.Request) {
	emp, _ := employeeFrom(r.Context())
	entry := auditEntry(r)
	var req passwordRequest
	if !decodeBody(w, r, &req) {
		return
	}
	if !auth.VerifyPassword(emp.PasswordHash, req.CurrentPassword) {
		WriteError(w, http.StatusForbidden, "Your current password is incorrect.")
		return
	}
	if err := auth.CheckPasswordPolicy(req.NewPassword); err != nil {
		WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	hash, err := auth.HashPassword(req.NewPassword)
	if err != nil {
		writeErr(w, s.log, err)
		return
	}
	before, after, err := s.RotatePassword(r.Context(), emp.ID, hash)
	if err != nil {
		writeErr(w, s.log, err)
		return
	}
	if entry != nil {
		entry.Before = mustJSON(auditView(before))
		entry.After = mustJSON(auditView(after))
		entry.Target = emp.ID
	}
	// Every session died with the rotation — including this one.
	clearSessionCookie(w)
	writeJSON(w, http.StatusOK, map[string]string{
		"message": "Password changed. Sign in with your new password.",
	})
}

type enrollResponse struct {
	Secret     string `json:"secret"`
	OtpauthURI string `json:"otpauth_uri"`
}

// handleEnrollTOTP begins enrollment: the generated secret is staged
// (authenticates nothing until confirmed) and shown exactly once.
func (s *Server) handleEnrollTOTP(w http.ResponseWriter, r *http.Request) {
	emp, _ := employeeFrom(r.Context())
	entry := auditEntry(r)
	if emp.TOTPSecret != "" {
		WriteError(w, http.StatusConflict, "Two-factor authentication is already enrolled.")
		return
	}
	secret, err := auth.GenerateTOTPSecret()
	if err != nil {
		writeErr(w, s.log, err)
		return
	}
	if err := s.StageTOTP(r.Context(), emp.ID, secret); err != nil {
		writeErr(w, s.log, err)
		return
	}
	if entry != nil {
		entry.After = mustJSON(map[string]any{"totp_staged": true})
	}
	writeJSON(w, http.StatusOK, enrollResponse{
		Secret:     secret,
		OtpauthURI: auth.OTPAuthURI(emp.Email, secret),
	})
}

type confirmRequest struct {
	Code string `json:"code"`
}

// handleConfirmTOTP verifies the first code against the staged secret
// and activates the binding.
func (s *Server) handleConfirmTOTP(w http.ResponseWriter, r *http.Request) {
	emp, _ := employeeFrom(r.Context())
	entry := auditEntry(r)
	var req confirmRequest
	if !decodeBody(w, r, &req) {
		return
	}
	before, after, err := s.ConfirmTOTP(r.Context(), emp.ID, req.Code, time.Now())
	switch {
	case errors.Is(err, ErrTOTPAlreadyBound):
		WriteError(w, http.StatusConflict, "Two-factor authentication is already enrolled.")
		return
	case errors.Is(err, ErrTOTPNotStaged):
		WriteError(w, http.StatusBadRequest, "Begin two-factor enrollment first.")
		return
	case errors.Is(err, ErrTOTPCodeRejected):
		WriteError(w, http.StatusBadRequest, "That code is not valid. Check your authenticator app and try again.")
		return
	case err != nil:
		writeErr(w, s.log, err)
		return
	}
	if entry != nil {
		entry.Before = mustJSON(auditView(before))
		entry.After = mustJSON(auditView(after))
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"totp_bound":  true,
		"must_rotate": after.MustRotate,
	})
}
