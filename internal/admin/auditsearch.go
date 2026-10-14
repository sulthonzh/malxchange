// Audit log search and the permission catalog read (ticket #57). The
// search is the read side of the middleware-written audit_log (ADR-0012
// readable by super-admin/finance/compliance): four filters — actor,
// action, target, time range — over exact matches, newest first, paged.
// Rows render the redacted before/after JSON the envelope stored; the
// search never mutates, so the audit middleware does not apply. The
// catalog read answers exactly the permissions table — the grant
// editor's display-only row set (the UI may never invent a string).
package admin

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"
)

// Paging bounds: a default page of 50 rows keeps the rendered
// before/after JSON sane; the cap keeps one wild request from pulling
// the whole log.
const (
	auditSearchDefaultPageSize = 50
	auditSearchMaxPageSize     = 200
)

// auditSearchQuery is the parsed GET /api/audit-log request. Zero From
// and To mean an unbounded side of the range.
type auditSearchQuery struct {
	Actor    string
	Action   string
	Target   string
	From     time.Time
	To       time.Time
	Page     int
	PageSize int
}

func (q auditSearchQuery) offset() int {
	return (q.Page - 1) * q.PageSize
}

// parseAuditSearch reads and validates the query string, answering the
// 400 itself when a bound is not RFC 3339, the range is inverted, or
// the paging numbers are not numbers. Absent filters select everything.
func parseAuditSearch(w http.ResponseWriter, r *http.Request) (auditSearchQuery, bool) {
	params := r.URL.Query()
	q := auditSearchQuery{
		Actor:  params.Get("actor"),
		Action: params.Get("action"),
		Target: params.Get("target"),
		Page:   1,
	}
	if raw := params.Get("from"); raw != "" {
		at, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			WriteError(w, http.StatusBadRequest, "The from filter must be an RFC 3339 timestamp.")
			return q, false
		}
		q.From = at
	}
	if raw := params.Get("to"); raw != "" {
		at, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			WriteError(w, http.StatusBadRequest, "The to filter must be an RFC 3339 timestamp.")
			return q, false
		}
		q.To = at
	}
	if !q.From.IsZero() && !q.To.IsZero() && q.From.After(q.To) {
		WriteError(w, http.StatusBadRequest, "The start of the time range must not be after its end.")
		return q, false
	}
	if raw := params.Get("page"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil {
			WriteError(w, http.StatusBadRequest, "The page must be a positive number.")
			return q, false
		}
		if n < 1 {
			n = 1
		}
		q.Page = n
	}
	q.PageSize = auditSearchDefaultPageSize
	if raw := params.Get("page_size"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil {
			WriteError(w, http.StatusBadRequest, "The page size must be a positive number.")
			return q, false
		}
		if n < 1 {
			n = auditSearchDefaultPageSize
		}
		q.PageSize = n
	}
	if q.PageSize > auditSearchMaxPageSize {
		q.PageSize = auditSearchMaxPageSize
	}
	return q, true
}

// auditLogRow is one searched audit_log row on the wire. Before/After
// stay raw JSON (or null) exactly as stored — the UI renders them, it
// never reassembles them.
type auditLogRow struct {
	ID        int64           `json:"id"`
	Actor     string          `json:"actor"`
	Action    string          `json:"action"`
	Target    string          `json:"target"`
	Before    json.RawMessage `json:"before"`
	After     json.RawMessage `json:"after"`
	OriginIP  string          `json:"origin_ip"`
	CreatedAt time.Time       `json:"created_at"`
}

type auditSearchResponse struct {
	Rows     []auditLogRow `json:"rows"`
	Total    int           `json:"total"`
	Page     int           `json:"page"`
	PageSize int           `json:"page_size"`
}

// handleSearchAuditLog answers the filtered, paged audit trail. The
// WHERE clause composes only fixed condition literals — every value
// rides a bind parameter, so a filter value can never become SQL.
func (s *Server) handleSearchAuditLog(w http.ResponseWriter, r *http.Request) {
	q, ok := parseAuditSearch(w, r)
	if !ok {
		return
	}
	where, args := auditSearchWhere(q)
	ctx := r.Context()

	var total int
	if err := s.pool.QueryRow(ctx,
		`SELECT count(*) FROM audit_log`+where, args...).Scan(&total); err != nil {
		writeErr(w, s.log, err)
		return
	}

	args = append(args, q.PageSize, q.offset())
	rows, err := s.pool.Query(ctx,
		`SELECT id, actor, action, target, before, after, origin_ip, created_at
		   FROM audit_log`+where+`
		  ORDER BY created_at DESC, id DESC
		  LIMIT $`+strconv.Itoa(len(args)-1)+` OFFSET $`+strconv.Itoa(len(args)), args...)
	if err != nil {
		writeErr(w, s.log, err)
		return
	}
	defer rows.Close()
	out := auditSearchResponse{Rows: []auditLogRow{}, Total: total, Page: q.Page, PageSize: q.PageSize}
	for rows.Next() {
		var row auditLogRow
		var before, after []byte
		if err := rows.Scan(&row.ID, &row.Actor, &row.Action, &row.Target,
			&before, &after, &row.OriginIP, &row.CreatedAt); err != nil {
			writeErr(w, s.log, err)
			return
		}
		row.Before, row.After = json.RawMessage(before), json.RawMessage(after)
		out.Rows = append(out.Rows, row)
	}
	if err := rows.Err(); err != nil {
		writeErr(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// auditSearchWhere builds the filtered WHERE clause (leading " WHERE"
// or empty) and its bind arguments in a stable order.
func auditSearchWhere(q auditSearchQuery) (string, []any) {
	where := ""
	args := []any{}
	if q.Actor != "" {
		args = append(args, q.Actor)
		where += fmt.Sprintf(" AND actor = $%d", len(args))
	}
	if q.Action != "" {
		args = append(args, q.Action)
		where += fmt.Sprintf(" AND action = $%d", len(args))
	}
	if q.Target != "" {
		args = append(args, q.Target)
		where += fmt.Sprintf(" AND target = $%d", len(args))
	}
	if !q.From.IsZero() {
		args = append(args, q.From)
		where += fmt.Sprintf(" AND created_at >= $%d", len(args))
	}
	if !q.To.IsZero() {
		args = append(args, q.To)
		where += fmt.Sprintf(" AND created_at <= $%d", len(args))
	}
	if where == "" {
		return "", args
	}
	return " WHERE" + where[4:], args
}

// handleListPermissions answers the read-only catalog: the sorted
// permission strings exactly as seeded from the registry. The grant
// editor renders this as its rows; there is no write path and the UI
// never invents one.
func (s *Server) handleListPermissions(w http.ResponseWriter, r *http.Request) {
	rows, err := s.pool.Query(r.Context(), `SELECT name FROM permissions ORDER BY name`)
	if err != nil {
		writeErr(w, s.log, err)
		return
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			writeErr(w, s.log, err)
			return
		}
		out = append(out, name)
	}
	if err := rows.Err(); err != nil {
		writeErr(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
