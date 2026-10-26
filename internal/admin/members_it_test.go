//go:build integration

// Member + KYC admin surfaces (ticket #58) over the real seam: the
// registry gates (session → permission → audit middleware) in front of
// a stub standing in for api's internal member RPC. Asserts the
// journeys through HTTP only — search/detail/lock/unlock, the KYC
// queue with evidence and approve/reject, and the Step-up-gated
// Authenticator reset — plus the audit rows the middleware owes and
// the X-Mal-Actor attribution the api side records.
package admin

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
	"time"
)

// ---- the api upstream stub ----

type stubCall struct {
	Method string
	Path   string
	Actor  string // X-Mal-Actor header
	Body   string
	Query  string
}

type stubMemberAPI struct {
	mu    sync.Mutex
	t     *testing.T
	srv   *httptest.Server
	calls []stubCall

	search   map[string]any // body for GET /members
	members  map[string]map[string]any
	queue    map[string]any
	evidence map[string]map[string]any
	// refusals: member id → the {"error"} message api answers 409 with
	noPending      map[string]string // kyc/review
	notKYCVerified map[string]string // authenticator/reset
}

func newStubMemberAPI(t *testing.T) *stubMemberAPI {
	st := &stubMemberAPI{
		t:              t,
		search:         map[string]any{"total": 0, "members": []any{}},
		members:        map[string]map[string]any{},
		queue:          map[string]any{"total": 0, "applications": []any{}},
		evidence:       map[string]map[string]any{},
		noPending:      map[string]string{},
		notKYCVerified: map[string]string{},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /internal/members/kyc/pending", st.serve(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, st.queue)
	}))
	mux.HandleFunc("GET /internal/members/{id}/kyc/evidence", st.serve(func(w http.ResponseWriter, r *http.Request) {
		ev, ok := st.evidence[r.PathValue("id")]
		if !ok {
			writeInternalErr(w, http.StatusNotFound, "no kyc application for this member")
			return
		}
		writeJSON(w, http.StatusOK, ev)
	}))
	mux.HandleFunc("GET /internal/members", st.serve(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, st.search)
	}))
	mux.HandleFunc("GET /internal/members/{id}", st.serve(func(w http.ResponseWriter, r *http.Request) {
		m, ok := st.members[r.PathValue("id")]
		if !ok {
			writeInternalErr(w, http.StatusNotFound, "member not found")
			return
		}
		writeJSON(w, http.StatusOK, m)
	}))
	mux.HandleFunc("POST /internal/members/{id}/lock", st.serve(func(w http.ResponseWriter, r *http.Request) {
		st.statusChange(w, r, "locked")
	}))
	mux.HandleFunc("POST /internal/members/{id}/unlock", st.serve(func(w http.ResponseWriter, r *http.Request) {
		st.statusChange(w, r, "active")
	}))
	mux.HandleFunc("POST /internal/members/{id}/kyc/review", st.serve(func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if msg, refused := st.noPending[id]; refused {
			writeInternalErr(w, http.StatusConflict, msg)
			return
		}
		var req struct {
			Approved bool   `json:"approved"`
			Reason   string `json:"reason"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		status := "rejected"
		if req.Approved {
			status = "verified"
		}
		writeJSON(w, http.StatusOK, map[string]string{"memberId": id, "kycStatus": status})
	}))
	mux.HandleFunc("POST /internal/members/{id}/authenticator/reset", st.serve(func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if msg, refused := st.notKYCVerified[id]; refused {
			writeInternalErr(w, http.StatusConflict, msg)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"memberId": id, "status": "authenticator reset"})
	}))
	st.srv = httptest.NewServer(mux)
	t.Cleanup(st.srv.Close)
	return st
}

func (st *stubMemberAPI) serve(next func(http.ResponseWriter, *http.Request)) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		var raw []byte
		if r.Body != nil {
			raw, _ = io.ReadAll(r.Body)
			r.Body.Close()
		}
		st.mu.Lock()
		st.calls = append(st.calls, stubCall{
			Method: r.Method, Path: r.URL.Path, Query: r.URL.RawQuery,
			Actor: r.Header.Get("X-Mal-Actor"), Body: string(raw),
		})
		st.mu.Unlock()
		// The handlers downstream decode the body too — hand them a
		// fresh reader over what was recorded.
		r.Body = io.NopCloser(bytes.NewReader(raw))
		next(w, r)
	}
}

func (st *stubMemberAPI) statusChange(w http.ResponseWriter, r *http.Request, status string) {
	id := r.PathValue("id")
	if _, ok := st.members[id]; !ok {
		writeInternalErr(w, http.StatusNotFound, "member not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"id": id, "status": status})
}

func (st *stubMemberAPI) recorded() []stubCall {
	st.mu.Lock()
	defer st.mu.Unlock()
	return append([]stubCall(nil), st.calls...)
}

// newMemberStack wires a second Server on the shared throwaway admin
// database — this one with its api client pointed at the stub — and
// re-points the itStack helpers at it.
func newMemberStack(t *testing.T) (*itStack, *stubMemberAPI) {
	s := newStack(t)
	stub := newStubMemberAPI(t)
	srv := NewServer(s.pool, s.env, nil)
	srv.api = &apiClient{base: stub.srv.URL + "/internal", http: &http.Client{Timeout: 5 * time.Second}}
	root := http.NewServeMux()
	internal := http.NewServeMux()
	srv.RegisterPublic(root)
	srv.RegisterInternal(internal)
	root.Handle("/internal/", http.StripPrefix("/internal", internal))
	s.rest = httptest.NewServer(root)
	t.Cleanup(s.rest.Close)
	return s, stub
}

// ---- journeys ----

func TestMemberSearchDetailLockUnlockJourney(t *testing.T) {
	s, stub := newMemberStack(t)
	root := s.fullyProvisioned(t, "member-root@it.test")
	rootCookie := root.freshCookie(t, s)
	support := s.employee(t, root, rootCookie, "member-support@it.test", RoleSupport)
	supportCookie := support.freshCookie(t, s)
	compliance := s.employee(t, root, rootCookie, "member-compliance@it.test", RoleCompliance)
	complianceCookie := compliance.freshCookie(t, s)

	stub.members["m-1"] = map[string]any{
		"id": "m-1", "email": "alice@example.com", "status": "active",
		"phone": "", "authenticatorBound": true, "kycStatus": "pending",
		"telegramBound": false, "createdAt": "2026-01-01T00:00:00Z",
	}
	stub.search = map[string]any{"total": 1, "members": []any{stub.members["m-1"]}}

	// Search forwards the admin's filters and answers the list.
	status, _, raw := s.do(t, "GET", "/api/members?q=ali&pageNo=2&pageSize=50", "", supportCookie)
	if status != http.StatusOK {
		t.Fatalf("support search: %d %s", status, raw)
	}
	var list struct {
		Total   int `json:"total"`
		Members []struct {
			ID    string `json:"id"`
			Email string `json:"email"`
		} `json:"members"`
	}
	if err := json.Unmarshal([]byte(raw), &list); err != nil || list.Total != 1 || list.Members[0].ID != "m-1" {
		t.Fatalf("search body %s err %v", raw, err)
	}
	calls := stub.recorded()
	if len(calls) == 0 || calls[len(calls)-1].Path != "/internal/members" ||
		calls[len(calls)-1].Query != "pageNo=2&pageSize=50&q=ali" {
		t.Fatalf("upstream never saw the filters: %+v", calls)
	}

	// Detail view.
	status, _, raw = s.do(t, "GET", "/api/members/m-1", "", supportCookie)
	if status != http.StatusOK || !strings.Contains(raw, `"authenticatorBound":true`) {
		t.Fatalf("detail: %d %s", status, raw)
	}

	// Lock: attribution reaches api, audit row carries target + actor.
	status, _, raw = s.post(t, "/api/members/m-1/lock", `{}`, supportCookie)
	if status != http.StatusOK || !strings.Contains(raw, `"status":"locked"`) {
		t.Fatalf("lock: %d %s", status, raw)
	}
	calls = stub.recorded()
	if len(calls) == 0 || calls[len(calls)-1].Path != "/internal/members/m-1/lock" ||
		calls[len(calls)-1].Actor != support.Email {
		t.Fatalf("lock upstream call wrong: %+v", calls[len(calls)-1:])
	}
	lockRows := s.auditRows(t, "lock:member")
	if len(lockRows) != 1 || lockRows[0].Target != "m-1" || lockRows[0].Actor != support.Email {
		t.Fatalf("lock audit rows = %+v", lockRows)
	}

	// Unlock mirrors it.
	if status, _, raw = s.post(t, "/api/members/m-1/unlock", `{}`, supportCookie); status != http.StatusOK {
		t.Fatalf("unlock: %d %s", status, raw)
	}
	unlockRows := s.auditRows(t, "unlock:member")
	if len(unlockRows) != 1 || unlockRows[0].Target != "m-1" {
		t.Fatalf("unlock audit rows = %+v", unlockRows)
	}

	// Unknown member: api's 404 sentence passes through.
	status, _, raw = s.do(t, "GET", "/api/members/ghost", "", supportCookie)
	if status != http.StatusNotFound || !strings.Contains(raw, "member not found") {
		t.Fatalf("ghost detail: %d %s", status, raw)
	}

	// Compliance holds no member-view/lock grants: 403 before any
	// upstream call (the registry matrix, asserted on the live path).
	if status, _, _ := s.do(t, "GET", "/api/members", "", complianceCookie); status != http.StatusForbidden {
		t.Fatalf("compliance search: %d", status)
	}
	if status, _, _ := s.post(t, "/api/members/m-1/lock", `{}`, complianceCookie); status != http.StatusForbidden {
		t.Fatalf("compliance lock: %d", status)
	}
	// Unauthenticated: refused by the session gate.
	if status, _, _ := s.do(t, "GET", "/api/members", "", nil); status != http.StatusUnauthorized {
		t.Fatalf("anonymous search: %d", status)
	}
}

func TestKYCQueueEvidenceReviewJourney(t *testing.T) {
	s, stub := newMemberStack(t)
	root := s.fullyProvisioned(t, "kyc-root@it.test")
	rootCookie := root.freshCookie(t, s)
	compliance := s.employee(t, root, rootCookie, "kyc-compliance@it.test", RoleCompliance)
	complianceCookie := compliance.freshCookie(t, s)
	support := s.employee(t, root, rootCookie, "kyc-support@it.test", RoleSupport)
	supportCookie := support.freshCookie(t, s)

	stub.queue = map[string]any{"total": 1, "applications": []any{map[string]any{
		"memberId": "m-7", "email": "applicant@example.com",
		"fullName": "App Llicant", "documentNumber": "X-9", "submittedAt": "2026-01-02T03:04:05Z",
	}}}
	stub.evidence["m-7"] = map[string]any{
		"memberId": "m-7", "fullName": "App Llicant", "documentNumber": "X-9",
		"frontImage": "ZnJvbnQ=", "backImage": "YmFjaw==",
	}

	// The queue forwards paging only.
	status, _, raw := s.do(t, "GET", "/api/kyc/applications?pageNo=1&pageSize=20", "", complianceCookie)
	if status != http.StatusOK || !strings.Contains(raw, "App Llicant") {
		t.Fatalf("queue: %d %s", status, raw)
	}
	calls := stub.recorded()
	if calls[len(calls)-1].Query != "pageNo=1&pageSize=20" {
		t.Fatalf("queue upstream query: %+v", calls[len(calls)-1])
	}

	// Evidence round-trips to the review dialog.
	status, _, raw = s.do(t, "GET", "/api/kyc/applications/m-7/evidence", "", complianceCookie)
	if status != http.StatusOK || !strings.Contains(raw, "ZnJvbnQ=") {
		t.Fatalf("evidence: %d %s", status, raw)
	}

	// Approve with a reason: audit row with target + after state.
	status, _, raw = s.post(t, "/api/kyc/applications/m-7/review",
		fmt.Sprintf(`{"approved":true,"reason":"documents match"}`), complianceCookie)
	if status != http.StatusOK || !strings.Contains(raw, `"kycStatus":"verified"`) {
		t.Fatalf("approve: %d %s", status, raw)
	}
	calls = stub.recorded()
	if calls[len(calls)-1].Actor != compliance.Email || !strings.Contains(calls[len(calls)-1].Body, `"approved":true`) {
		t.Fatalf("review upstream call: %+v", calls[len(calls)-1])
	}
	rows := s.auditRows(t, "review:kyc")
	if len(rows) != 1 || rows[0].Target != "m-7" || rows[0].Actor != compliance.Email {
		t.Fatalf("review audit rows = %+v", rows)
	}

	// A rejection without a reason is refused before api is called.
	before := len(stub.recorded())
	status, _, raw = s.post(t, "/api/kyc/applications/m-7/review", `{"approved":false}`, complianceCookie)
	if status != http.StatusBadRequest {
		t.Fatalf("reasonless reject: %d %s", status, raw)
	}
	if len(stub.recorded()) != before {
		t.Fatal("reasonless reject reached api")
	}

	// api's no-pending 409 passes through and is not audited.
	stub.noPending["m-8"] = "no pending kyc application for this member"
	status, _, raw = s.post(t, "/api/kyc/applications/m-8/review", `{"approved":true}`, complianceCookie)
	if status != http.StatusConflict || !strings.Contains(raw, "no pending kyc application") {
		t.Fatalf("no-pending review: %d %s", status, raw)
	}
	if rows := s.auditRows(t, "review:kyc"); len(rows) != 1 {
		t.Fatalf("refused review was audited: %+v", rows)
	}

	// Support cannot even see the queue (the matrix: KYC is compliance's).
	if status, _, _ := s.do(t, "GET", "/api/kyc/applications", "", supportCookie); status != http.StatusForbidden {
		t.Fatalf("support queue: %d", status)
	}
	if status, _, _ := s.post(t, "/api/kyc/applications/m-7/review", `{"approved":true}`, supportCookie); status != http.StatusForbidden {
		t.Fatalf("support review: %d", status)
	}
}

func TestMemberTOTPResetStepUpAuditPassthrough(t *testing.T) {
	s, stub := newMemberStack(t)
	root := s.fullyProvisioned(t, "reset-root@it.test")
	rootCookie := root.freshCookie(t, s)
	compliance := s.employee(t, root, rootCookie, "reset-compliance@it.test", RoleCompliance)
	complianceCookie := compliance.freshCookie(t, s)
	support := s.employee(t, root, rootCookie, "reset-support@it.test", RoleSupport)
	supportCookie := support.freshCookie(t, s)

	stub.members["m-9"] = map[string]any{"id": "m-9", "email": "totpless@example.com", "status": "active"}

	// No code → step-up required; wrong code → invalid.
	status, _, raw := s.post(t, "/api/members/m-9/totp/reset", `{}`, complianceCookie)
	if status != http.StatusUnauthorized || !strings.Contains(raw, "fresh authenticator code is required") {
		t.Fatalf("missing code: %d %s", status, raw)
	}
	status, _, raw = s.post(t, "/api/members/m-9/totp/reset",
		fmt.Sprintf(`{"code":%q}`, wrongCodeOf(t, compliance.TOTPSecr)), complianceCookie)
	if status != http.StatusUnauthorized || !strings.Contains(raw, "not valid") {
		t.Fatalf("wrong code: %d %s", status, raw)
	}

	// The good code: upstream call with attribution + audit row.
	code, err := totpNow(compliance.TOTPSecr)
	if err != nil {
		t.Fatal(err)
	}
	status, _, raw = s.post(t, "/api/members/m-9/totp/reset", fmt.Sprintf(`{"code":%q}`, code), complianceCookie)
	if status != http.StatusOK || !strings.Contains(raw, `"totpBound":false`) {
		t.Fatalf("reset: %d %s", status, raw)
	}
	calls := stub.recorded()
	if calls[len(calls)-1].Path != "/internal/members/m-9/authenticator/reset" || calls[len(calls)-1].Actor != compliance.Email {
		t.Fatalf("reset upstream call: %+v", calls[len(calls)-1])
	}
	rows := s.auditRows(t, "reset:member-totp")
	if len(rows) != 1 || rows[0].Target != "m-9" || rows[0].Actor != compliance.Email {
		t.Fatalf("reset audit rows = %+v", rows)
	}

	// api's KYC gate passes through and is not audited (the reset
	// never happened).
	stub.notKYCVerified["m-10"] = "authenticator reset requires a KYC-verified member"
	s.clearConsumedStep(t, compliance.Email) // the good reset above consumed the step (#103)
	code, err = totpNow(compliance.TOTPSecr)
	if err != nil {
		t.Fatal(err)
	}
	status, _, raw = s.post(t, "/api/members/m-10/totp/reset", fmt.Sprintf(`{"code":%q}`, code), complianceCookie)
	if status != http.StatusConflict || !strings.Contains(raw, "KYC-verified member") {
		t.Fatalf("unverified reset: %d %s", status, raw)
	}
	if rows := s.auditRows(t, "reset:member-totp"); len(rows) != 1 {
		t.Fatalf("refused reset was audited: %+v", rows)
	}

	// Support holds no reset grant.
	code, err = totpNow(support.TOTPSecr)
	if err != nil {
		t.Fatal(err)
	}
	if status, _, _ := s.post(t, "/api/members/m-9/totp/reset", fmt.Sprintf(`{"code":%q}`, code), supportCookie); status != http.StatusForbidden {
		t.Fatalf("support reset: %d", status)
	}
}
