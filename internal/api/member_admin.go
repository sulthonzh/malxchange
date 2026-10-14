// Admin member-search and KYC-queue internal RPCs (ticket #58) — the
// read seams admin-api's Member and KYC-review surfaces proxy. Same
// conventions as sensitive.go's admin RPCs: prefix-free patterns on
// the internal mux, the {"error": …} envelope, no in-mux auth (the
// compose network boundary is the auth), X-Mal-Actor attribution on
// mutations by the caller. The pending queue deliberately carries
// metadata only — document images ride the per-member evidence RPC,
// so a page of the queue never becomes a multi-megabyte answer.
package api

import (
	"encoding/base64"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// adminPage clamps the pageNo/pageSize paging pair: pageNo < 1 → 1,
// pageSize into [1, 100] with a default of 20. Garbage answers the
// defaults — the admin UI is the only caller.
func adminPage(r *http.Request) (limit, offset int) {
	const (
		defaultSize = 20
		maxSize     = 100
	)
	pageNo, size := 1, defaultSize
	if v, err := strconv.Atoi(r.URL.Query().Get("pageNo")); err == nil {
		pageNo = v
	}
	if v, err := strconv.Atoi(r.URL.Query().Get("pageSize")); err == nil {
		size = v
	}
	if pageNo < 1 {
		pageNo = 1
	}
	if size < 1 {
		size = defaultSize
	}
	if size > maxSize {
		size = maxSize
	}
	return size, (pageNo - 1) * size
}

// adminMemberRow is one row of the search list — the same shape as the
// single-member view RPC (sensitive.go) plus the joined KYC state.
type adminMemberRow struct {
	ID                 string `json:"id"`
	Email              string `json:"email"`
	Status             string `json:"status"`
	Phone              string `json:"phone"`
	KYCStatus          string `json:"kycStatus"`
	AuthenticatorBound bool   `json:"authenticatorBound"`
	TelegramBound      bool   `json:"telegramBound"`
	CreatedAt          string `json:"createdAt"`
}

// handleAdminMemberSearch answers GET /members: q matches an email
// substring (case-insensitive) or an exact member id; status filters
// active/locked. Newest members first, stable on id ties.
func (a *memberAPI) handleAdminMemberSearch(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	status := r.URL.Query().Get("status")
	switch status {
	case "", "active", "locked":
	default:
		writeInternalErr(w, http.StatusBadRequest, "status must be active or locked")
		return
	}
	limit, offset := adminPage(r)
	ctx := r.Context()

	where := ` WHERE ($1 = '' OR strpos(lower(m.email), lower($1)) > 0 OR m.id = $1)
	            AND ($2 = '' OR m.status = $2)`
	var total int
	if err := a.pool.QueryRow(ctx,
		`SELECT count(*) FROM members m`+where, q, status).Scan(&total); err != nil {
		a.log.Error("admin member search count", "err", err)
		writeInternalErr(w, http.StatusInternalServerError, "member search failed")
		return
	}
	rows, err := a.pool.Query(ctx,
		`SELECT m.id, m.email, m.status, COALESCE(m.phone, ''),
		        COALESCE(k.status, 'unverified'),
		        EXISTS(SELECT 1 FROM authenticator_bindings ab WHERE ab.member_id = m.id AND ab.enabled),
		        EXISTS(SELECT 1 FROM member_telegram_chats tc WHERE tc.member_id = m.id),
		        m.created_at
		   FROM members m
		   LEFT JOIN kyc_applications k ON k.member_id = m.id`+
			where+`
		   ORDER BY m.created_at DESC, m.id DESC
		   LIMIT $3 OFFSET $4`, q, status, limit, offset)
	if err != nil {
		a.log.Error("admin member search", "err", err)
		writeInternalErr(w, http.StatusInternalServerError, "member search failed")
		return
	}
	defer rows.Close()
	members := []adminMemberRow{}
	for rows.Next() {
		var m adminMemberRow
		var createdAt time.Time
		if err := rows.Scan(&m.ID, &m.Email, &m.Status, &m.Phone, &m.KYCStatus,
			&m.AuthenticatorBound, &m.TelegramBound, &createdAt); err != nil {
			a.log.Error("admin member search scan", "err", err)
			writeInternalErr(w, http.StatusInternalServerError, "member search failed")
			return
		}
		m.CreatedAt = formatTime(createdAt)
		members = append(members, m)
	}
	if err := rows.Err(); err != nil {
		a.log.Error("admin member search rows", "err", err)
		writeInternalErr(w, http.StatusInternalServerError, "member search failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"total": total, "members": members})
}

// handleAdminKYCPending answers GET /members/kyc/pending: the pending
// KYC applications, oldest submission first, with the applicant's
// email joined for display. No document images here — clients fetch
// the evidence RPC per application when an Employee opens the review.
func (a *memberAPI) handleAdminKYCPending(w http.ResponseWriter, r *http.Request) {
	limit, offset := adminPage(r)
	ctx := r.Context()

	const where = ` WHERE k.status = 'pending'`
	var total int
	if err := a.pool.QueryRow(ctx,
		`SELECT count(*) FROM kyc_applications k`+where).Scan(&total); err != nil {
		a.log.Error("admin kyc queue count", "err", err)
		writeInternalErr(w, http.StatusInternalServerError, "kyc queue failed")
		return
	}
	rows, err := a.pool.Query(ctx,
		`SELECT k.member_id, m.email, k.full_name, k.document_number, k.submitted_at
		   FROM kyc_applications k
		   JOIN members m ON m.id = k.member_id`+where+`
		   ORDER BY k.submitted_at ASC, k.member_id ASC
		   LIMIT $1 OFFSET $2`, limit, offset)
	if err != nil {
		a.log.Error("admin kyc queue", "err", err)
		writeInternalErr(w, http.StatusInternalServerError, "kyc queue failed")
		return
	}
	defer rows.Close()
	applications := []map[string]any{}
	for rows.Next() {
		var memberID, email, fullName, documentNumber string
		var submittedAt time.Time
		if err := rows.Scan(&memberID, &email, &fullName, &documentNumber, &submittedAt); err != nil {
			a.log.Error("admin kyc queue scan", "err", err)
			writeInternalErr(w, http.StatusInternalServerError, "kyc queue failed")
			return
		}
		applications = append(applications, map[string]any{
			"memberId":       memberID,
			"email":          email,
			"fullName":       fullName,
			"documentNumber": documentNumber,
			"submittedAt":    formatTime(submittedAt),
		})
	}
	if err := rows.Err(); err != nil {
		a.log.Error("admin kyc queue rows", "err", err)
		writeInternalErr(w, http.StatusInternalServerError, "kyc queue failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"total": total, "applications": applications})
}

// handleAdminKYCEvidence answers GET /members/{id}/kyc/evidence: the
// application's identity data and both document images, base64 the way
// the member submitted them (0006_security stores raw bytes in-row).
// 404 when the member has no application at all.
func (a *memberAPI) handleAdminKYCEvidence(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var (
		fullName, documentNumber string
		front, back              []byte
	)
	err := a.pool.QueryRow(r.Context(),
		`SELECT full_name, document_number, front_image, back_image
		   FROM kyc_applications WHERE member_id = $1`, id).
		Scan(&fullName, &documentNumber, &front, &back)
	if errors.Is(err, pgx.ErrNoRows) {
		writeInternalErr(w, http.StatusNotFound, "no kyc application for this member")
		return
	}
	if err != nil {
		a.log.Error("admin kyc evidence", "err", err)
		writeInternalErr(w, http.StatusInternalServerError, "kyc evidence failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"memberId":       id,
		"fullName":       fullName,
		"documentNumber": documentNumber,
		"frontImage":     base64.StdEncoding.EncodeToString(front),
		"backImage":      base64.StdEncoding.EncodeToString(back),
	})
}
