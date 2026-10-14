//go:build integration

// Admin member-search + KYC-queue internal RPCs (ticket #58): the read
// seams admin-api's Member/KYC surfaces proxy. HTTP against the served
// internal mux (member_it_test.go's stack), assertions through the
// surface only.
package api

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"testing"
)

func TestAdminMemberSearchRPC(t *testing.T) {
	s := newStack(t)

	aliceToken, aliceID := s.register2("search-alice@example.com", "the-password")
	_, bobID := s.register2("search-bob@example.com", "the-password")
	_, _ = s.register2("search-carol@example.com", "the-password")

	// Alice binds her Authenticator so the flag shows up in the list.
	s.bindAuthenticator(aliceToken, "search-alice@example.com")

	// Full list: newest first, one row per member with the joined
	// identity/KYC/Authenticator state.
	status, raw := s.adminDo(http.MethodGet, "/internal/members?pageNo=1&pageSize=10", nil)
	if status != http.StatusOK {
		t.Fatalf("member list: status %d body %s", status, raw)
	}
	var list struct {
		Total   int `json:"total"`
		Members []struct {
			ID                 string `json:"id"`
			Email              string `json:"email"`
			Status             string `json:"status"`
			KYCStatus          string `json:"kycStatus"`
			AuthenticatorBound bool   `json:"authenticatorBound"`
			CreatedAt          string `json:"createdAt"`
		} `json:"members"`
	}
	if err := json.Unmarshal([]byte(raw), &list); err != nil {
		t.Fatalf("member list body: %v", err)
	}
	if list.Total != 3 || len(list.Members) != 3 {
		t.Fatalf("member list total=%d rows=%d, want 3/3: %s", list.Total, len(list.Members), raw)
	}
	if list.Members[0].Email != "search-carol@example.com" {
		t.Fatalf("newest member first, got %s", list.Members[0].Email)
	}
	if list.Members[0].KYCStatus != "unverified" || list.Members[0].AuthenticatorBound {
		t.Fatalf("carol defaults wrong: %s", raw)
	}
	for _, m := range list.Members {
		if m.Email == "search-alice@example.com" && (!m.AuthenticatorBound || m.ID != aliceID) {
			t.Fatalf("alice row wrong: %s", raw)
		}
	}

	// Substring search on email.
	status, raw = s.adminDo(http.MethodGet, "/internal/members?q=alice", nil)
	if status != http.StatusOK {
		t.Fatalf("search q=alice: status %d body %s", status, raw)
	}
	if err := json.Unmarshal([]byte(raw), &list); err != nil {
		t.Fatal(err)
	}
	if list.Total != 1 || len(list.Members) != 1 || list.Members[0].ID != aliceID {
		t.Fatalf("q=alice → %s", raw)
	}

	// Exact-id search.
	status, raw = s.adminDo(http.MethodGet, "/internal/members?q="+bobID, nil)
	if status != http.StatusOK {
		t.Fatalf("search by id: status %d body %s", status, raw)
	}
	if err := json.Unmarshal([]byte(raw), &list); err != nil {
		t.Fatal(err)
	}
	if list.Total != 1 || list.Members[0].ID != bobID {
		t.Fatalf("q=<bob id> → %s", raw)
	}

	// Status filter: lock bob through the admin RPC, then filter.
	if status, raw := s.adminDo(http.MethodPost, "/internal/members/"+bobID+"/lock", nil); status != http.StatusOK {
		t.Fatalf("lock bob: status %d body %s", status, raw)
	}
	status, raw = s.adminDo(http.MethodGet, "/internal/members?status=locked", nil)
	if status != http.StatusOK {
		t.Fatalf("status filter: status %d body %s", status, raw)
	}
	if err := json.Unmarshal([]byte(raw), &list); err != nil {
		t.Fatal(err)
	}
	if list.Total != 1 || list.Members[0].ID != bobID || list.Members[0].Status != "locked" {
		t.Fatalf("status=locked → %s", raw)
	}

	// Pagination: two pages over three members.
	status, raw = s.adminDo(http.MethodGet, "/internal/members?pageNo=2&pageSize=2", nil)
	if status != http.StatusOK {
		t.Fatalf("page 2: status %d body %s", status, raw)
	}
	if err := json.Unmarshal([]byte(raw), &list); err != nil {
		t.Fatal(err)
	}
	if list.Total != 3 || len(list.Members) != 1 || list.Members[0].ID != aliceID {
		t.Fatalf("page 2 → %s", raw)
	}

	// Unknown member detail stays a 404.
	if status, raw := s.adminDo(http.MethodGet, "/internal/members/no-such-id", nil); status != http.StatusNotFound {
		t.Fatalf("unknown member view: status %d body %s", status, raw)
	}
}

func TestAdminKYCPendingQueueAndEvidence(t *testing.T) {
	s := newStack(t)

	_, aliceID := s.register2("kycq-alice@example.com", "the-password")
	bobToken, bobID := s.register2("kycq-bob@example.com", "the-password")
	carolToken, carolID := s.register2("kycq-carol@example.com", "the-password")

	frontBob := base64.StdEncoding.EncodeToString([]byte("front-of-bob"))
	backBob := base64.StdEncoding.EncodeToString([]byte("back-of-bob"))
	if status, body := s.post("/api/uc/kyc", map[string]string{
		"fullName": "Bob Applicant", "documentNumber": "B-222",
		"frontImage": frontBob, "backImage": backBob}, bobToken); status != http.StatusOK {
		t.Fatalf("bob kyc submit: status %d body %v", status, body)
	}
	frontCarol := base64.StdEncoding.EncodeToString([]byte("front-of-carol"))
	backCarol := base64.StdEncoding.EncodeToString([]byte("back-of-carol"))
	if status, body := s.post("/api/uc/kyc", map[string]string{
		"fullName": "Carol Applicant", "documentNumber": "C-333",
		"frontImage": frontCarol, "backImage": backCarol}, carolToken); status != http.StatusOK {
		t.Fatalf("carol kyc submit: status %d body %v", status, body)
	}

	// The pending queue: metadata only — document images ride the
	// evidence RPC, never the list.
	status, raw := s.adminDo(http.MethodGet, "/internal/members/kyc/pending", nil)
	if status != http.StatusOK {
		t.Fatalf("kyc queue: status %d body %s", status, raw)
	}
	var queue struct {
		Total        int `json:"total"`
		Applications []struct {
			MemberID       string `json:"memberId"`
			Email          string `json:"email"`
			FullName       string `json:"fullName"`
			DocumentNumber string `json:"documentNumber"`
			SubmittedAt    string `json:"submittedAt"`
		} `json:"applications"`
	}
	if err := json.Unmarshal([]byte(raw), &queue); err != nil {
		t.Fatalf("queue body: %v", err)
	}
	if queue.Total != 2 || len(queue.Applications) != 2 {
		t.Fatalf("queue total=%d rows=%d: %s", queue.Total, len(queue.Applications), raw)
	}
	// Oldest submission first.
	if queue.Applications[0].MemberID != bobID || queue.Applications[0].FullName != "Bob Applicant" ||
		queue.Applications[0].DocumentNumber != "B-222" || queue.Applications[0].Email != "kycq-bob@example.com" {
		t.Fatalf("queue head wrong: %s", raw)
	}
	if queue.Applications[1].MemberID != carolID {
		t.Fatalf("queue second wrong: %s", raw)
	}
	if contains := len(raw) > 0 && func() bool {
		return jsonUncontainsImage(raw)
	}(); contains {
		t.Fatal("queue response carries document images")
	}

	// Evidence: the base64 images round-trip exactly as submitted.
	status, raw = s.adminDo(http.MethodGet, "/internal/members/"+carolID+"/kyc/evidence", nil)
	if status != http.StatusOK {
		t.Fatalf("carol evidence: status %d body %s", status, raw)
	}
	var ev struct {
		MemberID       string `json:"memberId"`
		FullName       string `json:"fullName"`
		DocumentNumber string `json:"documentNumber"`
		FrontImage     string `json:"frontImage"`
		BackImage      string `json:"backImage"`
	}
	if err := json.Unmarshal([]byte(raw), &ev); err != nil {
		t.Fatalf("evidence body: %v", err)
	}
	if ev.MemberID != carolID || ev.FullName != "Carol Applicant" || ev.DocumentNumber != "C-333" {
		t.Fatalf("evidence identity wrong: %s", raw)
	}
	if ev.FrontImage != frontCarol || ev.BackImage != backCarol {
		t.Fatalf("evidence images wrong: %s", raw)
	}

	// No application → 404.
	if status, raw := s.adminDo(http.MethodGet, "/internal/members/"+aliceID+"/kyc/evidence", nil); status != http.StatusNotFound {
		t.Fatalf("alice evidence: status %d body %s", status, raw)
	}

	// Approving bob drops him from the pending queue.
	if status, raw := s.adminDo(http.MethodPost, "/internal/members/"+bobID+"/kyc/review",
		map[string]any{"approved": true, "actor": "compliance-1"}); status != http.StatusOK {
		t.Fatalf("bob review: status %d body %s", status, raw)
	}
	status, raw = s.adminDo(http.MethodGet, "/internal/members/kyc/pending", nil)
	if status != http.StatusOK {
		t.Fatalf("queue after review: status %d body %s", status, raw)
	}
	if err := json.Unmarshal([]byte(raw), &queue); err != nil {
		t.Fatal(err)
	}
	if queue.Total != 1 || len(queue.Applications) != 1 || queue.Applications[0].MemberID != carolID {
		t.Fatalf("queue after review → %s", raw)
	}

	// Pagination over the queue: pending is now [carol, dave] (oldest
	// first), so page two holds dave.
	daveToken, daveID := s.register2("kycq-dave@example.com", "the-password")
	if status, body := s.post("/api/uc/kyc", map[string]string{
		"fullName": "Dave Applicant", "documentNumber": "D-444",
		"frontImage": frontCarol, "backImage": backCarol}, daveToken); status != http.StatusOK {
		t.Fatalf("dave kyc submit: status %d body %v", status, body)
	}
	status, raw = s.adminDo(http.MethodGet, "/internal/members/kyc/pending?pageNo=2&pageSize=1", nil)
	if status != http.StatusOK {
		t.Fatalf("queue page 2: status %d body %s", status, raw)
	}
	if err := json.Unmarshal([]byte(raw), &queue); err != nil {
		t.Fatal(err)
	}
	if queue.Total != 2 || len(queue.Applications) != 1 || queue.Applications[0].MemberID != daveID {
		t.Fatalf("queue page 2 → %s", raw)
	}
}

// jsonUncontainsImage reports whether a raw queue response smuggles a
// base64 document image (the evidence RPC's job, not the list's).
func jsonUncontainsImage(raw string) bool {
	var body map[string]any
	if err := json.Unmarshal([]byte(raw), &body); err != nil {
		return false
	}
	apps, _ := body["applications"].([]any)
	for _, a := range apps {
		m, ok := a.(map[string]any)
		if !ok {
			continue
		}
		if _, has := m["frontImage"]; has {
			return true
		}
		if _, has := m["backImage"]; has {
			return true
		}
	}
	return false
}
