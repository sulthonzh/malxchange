// The Member + KYC support/compliance surfaces (ticket #58): member
// search and detail, lock/unlock, the pending-KYC queue with document
// evidence and approve/reject, and the Step-up-gated Authenticator
// reset. admin-api owns the permission gate and the audit envelope;
// the member state itself is api's — every handler proxies one
// internal RPC (memberapi.go) and passes api's error sentences
// through untouched. The ADR-0012 matrix: support owns member
// view/lock, compliance owns KYC review + member TOTP reset,
// super-admin the read side.
package admin

import (
	"errors"
	"net/http"
	"net/url"
	"strings"
)

// memberSearchView is the wire shape of GET /api/members (api's
// /internal/members answer passed through).
type memberSearchView struct {
	Total   int               `json:"total"`
	Members []adminMemberView `json:"members"`
}

// adminMemberView is one member row — identity, status, and the
// joined security state (api's member view RPC shape).
type adminMemberView struct {
	ID                 string `json:"id"`
	Email              string `json:"email"`
	Status             string `json:"status"`
	Phone              string `json:"phone"`
	KYCStatus          string `json:"kycStatus"`
	AuthenticatorBound bool   `json:"authenticatorBound"`
	TelegramBound      bool   `json:"telegramBound"`
	CreatedAt          string `json:"createdAt"`
}

// kycQueueView is the wire shape of GET /api/kyc/applications.
type kycQueueView struct {
	Total        int            `json:"total"`
	Applications []kycQueueItem `json:"applications"`
}

type kycQueueItem struct {
	MemberID       string `json:"memberId"`
	Email          string `json:"email"`
	FullName       string `json:"fullName"`
	DocumentNumber string `json:"documentNumber"`
	SubmittedAt    string `json:"submittedAt"`
}

// kycEvidenceView is the review-dialog payload: the application's
// identity data plus both document images, base64 (api's evidence RPC
// shape). It rides callLimit with evidenceCap — submitted images are
// up to 5 MiB each and base64 inflates 4/3.
type kycEvidenceView struct {
	MemberID       string `json:"memberId"`
	FullName       string `json:"fullName"`
	DocumentNumber string `json:"documentNumber"`
	FrontImage     string `json:"frontImage"`
	BackImage      string `json:"backImage"`
}

// answerUpstream maps one api client error onto the public envelope:
// upstream statuses pass their English sentence through (404 unknown
// member, 409 refused review/reset), a dead api is one honest 503.
// It reports whether the call succeeded.
func (s *Server) answerUpstream(w http.ResponseWriter, err error) bool {
	if err == nil {
		return true
	}
	var ue *apiUpstreamError
	if errors.As(err, &ue) {
		WriteError(w, ue.Status, ue.Message)
		return false
	}
	if errors.Is(err, errAPIDown) {
		s.log.Error("admin member rpc unreachable", "err", err)
		WriteError(w, http.StatusServiceUnavailable, "The member service is unreachable. Try again.")
		return false
	}
	writeErr(w, s.log, err)
	return false
}

// pagingForwards copies the pagination params the admin UI may send;
// everything else stays out of the upstream query.
func pagingForwards(r *http.Request, extra ...string) string {
	v := url.Values{}
	for _, key := range append([]string{"pageNo", "pageSize"}, extra...) {
		if val := r.URL.Query().Get(key); val != "" {
			v.Set(key, val)
		}
	}
	return v.Encode()
}

// evidenceCap is the answer cap for one KYC evidence read (5 MiB per
// submitted image, base64-inflated 4/3, plus identity fields).
const evidenceCap = 16 << 20

// handleSearchMembers serves GET /api/members — the support/super-admin
// search list (q, status filter, paging).
func (s *Server) handleSearchMembers(w http.ResponseWriter, r *http.Request) {
	if s.api == nil {
		WriteError(w, http.StatusServiceUnavailable, "The member service is unreachable. Try again.")
		return
	}
	var out memberSearchView
	err := s.api.call(r.Context(), http.MethodGet, "/members?"+pagingForwards(r, "q", "status"), "", nil, &out)
	if !s.answerUpstream(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// handleMemberView serves GET /api/members/{id} — the detail view
// (identity, status, KYC state, Authenticator/Code-channel bindings).
func (s *Server) handleMemberView(w http.ResponseWriter, r *http.Request) {
	if s.api == nil {
		WriteError(w, http.StatusServiceUnavailable, "The member service is unreachable. Try again.")
		return
	}
	var out adminMemberView
	err := s.api.call(r.Context(), http.MethodGet, "/members/"+r.PathValue("id"), "", nil, &out)
	if !s.answerUpstream(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// handleMemberLock / handleMemberUnlock serve the status flips. api's
// lock RPC revokes the member's live sessions in the same transaction;
// the audit row's target is the affected member, never the caller.
func (s *Server) handleMemberLock(w http.ResponseWriter, r *http.Request) {
	s.memberSetStatus(w, r, "locked")
}

func (s *Server) handleMemberUnlock(w http.ResponseWriter, r *http.Request) {
	s.memberSetStatus(w, r, "active")
}

func (s *Server) memberSetStatus(w http.ResponseWriter, r *http.Request, status string) {
	if s.api == nil {
		WriteError(w, http.StatusServiceUnavailable, "The member service is unreachable. Try again.")
		return
	}
	id := r.PathValue("id")
	actor, _ := employeeFrom(r.Context())
	var out struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}
	err := s.api.call(r.Context(), http.MethodPost, "/members/"+id+"/"+statusOf(status), actor.Email, map[string]string{}, &out)
	if !s.answerUpstream(w, err) {
		return
	}
	if entry := auditEntry(r); entry != nil {
		entry.Target = id
		entry.After = mustJSON(map[string]string{"status": status})
	}
	writeJSON(w, http.StatusOK, out)
}

// statusOf maps the member status to api's RPC action segment — the
// lock/unlock verbs, not the status vocabulary.
func statusOf(status string) string {
	if status == "locked" {
		return "lock"
	}
	return "unlock"
}

// handleKYCQueue serves GET /api/kyc/applications — compliance's
// pending queue (metadata; evidence loads per application).
func (s *Server) handleKYCQueue(w http.ResponseWriter, r *http.Request) {
	if s.api == nil {
		WriteError(w, http.StatusServiceUnavailable, "The member service is unreachable. Try again.")
		return
	}
	var out kycQueueView
	err := s.api.call(r.Context(), http.MethodGet, "/members/kyc/pending?"+pagingForwards(r), "", nil, &out)
	if !s.answerUpstream(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// handleKYCEvidence serves GET /api/kyc/applications/{memberId}/evidence —
// the review dialog's document images.
func (s *Server) handleKYCEvidence(w http.ResponseWriter, r *http.Request) {
	if s.api == nil {
		WriteError(w, http.StatusServiceUnavailable, "The member service is unreachable. Try again.")
		return
	}
	var out kycEvidenceView
	err := s.api.callLimit(r.Context(), http.MethodGet,
		"/members/"+r.PathValue("memberId")+"/kyc/evidence", "", nil, &out, evidenceCap)
	if !s.answerUpstream(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// handleKYCReview serves POST /api/kyc/applications/{memberId}/review —
// approve/reject with a reason. A rejection without a reason is
// refused before api is called; the audit row names the member and
// the resulting state.
func (s *Server) handleKYCReview(w http.ResponseWriter, r *http.Request) {
	if s.api == nil {
		WriteError(w, http.StatusServiceUnavailable, "The member service is unreachable. Try again.")
		return
	}
	var req struct {
		Approved bool   `json:"approved"`
		Reason   string `json:"reason"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	reason := strings.TrimSpace(req.Reason)
	if !req.Approved && reason == "" {
		WriteError(w, http.StatusBadRequest, "A reason is required to reject a KYC application.")
		return
	}
	id := r.PathValue("memberId")
	actor, _ := employeeFrom(r.Context())
	var out struct {
		MemberID  string `json:"memberId"`
		KYCStatus string `json:"kycStatus"`
	}
	err := s.api.call(r.Context(), http.MethodPost, "/members/"+id+"/kyc/review", actor.Email,
		struct {
			Approved bool   `json:"approved"`
			Reason   string `json:"reason"`
		}{Approved: req.Approved, Reason: reason}, &out)
	if !s.answerUpstream(w, err) {
		return
	}
	if entry := auditEntry(r); entry != nil {
		entry.Target = id
		entry.After = mustJSON(map[string]string{"kycStatus": out.KYCStatus, "reason": reason})
	}
	writeJSON(w, http.StatusOK, out)
}

// handleMemberTOTPReset serves POST /api/members/{id}/totp/reset — a
// fresh Step-up code from the CALLER, then api's reset RPC (which
// enforces the KYC-verified gate, revokes the binding, writes api's
// security_audit row, and notifies the member's active Code channels).
func (s *Server) handleMemberTOTPReset(w http.ResponseWriter, r *http.Request) {
	if s.api == nil {
		WriteError(w, http.StatusServiceUnavailable, "The member service is unreachable. Try again.")
		return
	}
	var req struct {
		Code string `json:"code"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	if !s.requireStepUp(w, r, req.Code) {
		return
	}
	id := r.PathValue("id")
	actor, _ := employeeFrom(r.Context())
	var out struct {
		MemberID string `json:"memberId"`
		Status   string `json:"status"`
	}
	err := s.api.call(r.Context(), http.MethodPost, "/members/"+id+"/authenticator/reset", actor.Email,
		map[string]string{"actor": actor.Email}, &out)
	if !s.answerUpstream(w, err) {
		return
	}
	if entry := auditEntry(r); entry != nil {
		entry.Target = id
		entry.After = mustJSON(map[string]any{"totpBound": false})
	}
	writeJSON(w, http.StatusOK, map[string]any{"memberId": id, "totpBound": false})
}
