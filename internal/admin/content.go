// The CMS (ticket #63): announcements and the help center (categories +
// articles) — support's surface per the ADR-0012 matrix. Two sides live
// here:
//
//   - the admin CRUD surface: registry-gated, audited by construction,
//     sees drafts, paginated like every growing admin list;
//   - the internal read RPC api's member content proxy pins
//     (internal/api/content_CONTRACT.md): published rows only, served
//     prefix-free so the boot harness answers /internal/content/*.
//
// The internal wire shapes below are the contract's, field order
// included: api decodes into its own twins and re-encodes, so the byte
// shape a member sees is decided by the declarations here. createTime
// is RFC3339 UTC, arrays are never null, and an unknown or still-draft
// id answers 404 — a member cannot tell a draft from a deletion.
package admin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Pagination bounds are the content contract's (api's
// contentPageParams discipline): absent means the default, anything
// malformed or out of range is one named bad request — never a silent
// clamp. The same bounds serve the admin lists.
const (
	defaultContentPageNo   = 1
	defaultContentPageSize = 10
	maxContentPageSize     = 50
	maxContentTitleChars   = 200   // bytes — titles are English sentences (ADR-0013)
	maxContentBodyChars    = 50000 // bytes of stored HTML — a rich article, still inside decodeBody's 64 KiB body cap
)

var errBadContentPage = errors.New("pageNo must be an integer of at least 1 and pageSize an integer between 1 and 50")

// contentPageParams parses the shared pagination parameters: absent =
// defaults, anything malformed or out of range is the one named bad
// request. Mirrors api's parser exactly, since api forwards the
// member's parameters verbatim.
func contentPageParams(r *http.Request) (pageNo, pageSize int, err error) {
	pageNo = defaultContentPageNo
	if raw := r.URL.Query().Get("pageNo"); raw != "" {
		pageNo, err = strconv.Atoi(raw)
		if err != nil || pageNo < 1 {
			return 0, 0, errBadContentPage
		}
	}
	pageSize = defaultContentPageSize
	if raw := r.URL.Query().Get("pageSize"); raw != "" {
		pageSize, err = strconv.Atoi(raw)
		if err != nil || pageSize < 1 || pageSize > maxContentPageSize {
			return 0, 0, errBadContentPage
		}
	}
	return pageNo, pageSize, nil
}

// contentText validates one required editable text field (a title or a
// stored HTML body): trimmed non-empty and inside the byte budget.
func contentText(raw string, maxChars int) (string, bool) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || len(trimmed) > maxChars {
		return "", false
	}
	return trimmed, true
}

// ---- admin CRUD wire shapes ----

// announcementRow is one announcement as the management list and the
// write endpoints answer it — the body included, so the edit dialog
// prefills without a second fetch (bodies are bounded by
// maxContentBodyChars).
type announcementRow struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	Content   string    `json:"content"`
	Published bool      `json:"published"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// contentBodyRequest is the create/edit payload of an announcement or
// help article: the stored fields plus the publish switch (publishing
// is an edit of published state, not a separate workflow).
type contentBodyRequest struct {
	Title     string `json:"title"`
	Content   string `json:"content"`
	Published bool   `json:"published"`
}

type createCategoryRequest struct {
	Title string `json:"title"`
}

// helpCategoryRow is one help category as management sees it: its
// article count spans drafts and published alike.
type helpCategoryRow struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	Articles  int       `json:"articles"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// helpArticleRow is one help article as management sees it, with the
// owning category's id and title for the filter and table.
type helpArticleRow struct {
	ID         string    `json:"id"`
	CategoryID string    `json:"category_id"`
	Category   string    `json:"category"`
	Title      string    `json:"title"`
	Content    string    `json:"content"`
	Published  bool      `json:"published"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// contentAuditView is the redacted before/after shape content
// mutations carry into the Audit log: identifying fields and publish
// state only — the stored HTML is editable content, not log material,
// and full bodies would bloat every row.
type contentAuditView struct {
	ID         string `json:"id"`
	Title      string `json:"title"`
	Published  bool   `json:"published"`
	CategoryID string `json:"category_id,omitempty"` // help articles only
}

// ---- announcements: admin CRUD ----

// handleListAnnouncements answers the management list — drafts
// included — newest first, paginated.
func (s *Server) handleListAnnouncements(w http.ResponseWriter, r *http.Request) {
	pageNo, pageSize, err := contentPageParams(r)
	if err != nil {
		WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	rows, err := s.pool.Query(r.Context(),
		`SELECT id, title, content, published_at IS NOT NULL, created_at, updated_at, count(*) OVER()
		   FROM announcements
		  ORDER BY created_at DESC, id DESC
		  OFFSET $1 LIMIT $2`, (pageNo-1)*pageSize, pageSize)
	if err != nil {
		writeErr(w, s.log, err)
		return
	}
	defer rows.Close()
	out := []announcementRow{}
	total := 0
	for rows.Next() {
		var a announcementRow
		if err := rows.Scan(&a.ID, &a.Title, &a.Content, &a.Published, &a.CreatedAt, &a.UpdatedAt, &total); err != nil {
			writeErr(w, s.log, err)
			return
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		writeErr(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"content": out, "total": total})
}

// handleCreateAnnouncement stores a new announcement (draft or
// published straight away) and fills the audit envelope.
func (s *Server) handleCreateAnnouncement(w http.ResponseWriter, r *http.Request) {
	entry := auditEntry(r)
	title, body, published, ok := decodeContentBody(w, r)
	if !ok {
		return
	}
	id := uuid.NewString()
	after, err := s.insertAnnouncement(r.Context(), id, title, body, published)
	if err != nil {
		writeErr(w, s.log, err)
		return
	}
	if entry != nil {
		entry.Target = after.ID
		entry.After = mustJSON(contentAuditView{ID: after.ID, Title: after.Title, Published: after.Published})
	}
	writeJSON(w, http.StatusOK, after)
}

// handleEditAnnouncement updates title, body, and the publish switch
// of one announcement. The publish switch keeps a row's original
// publish time when already published and clears it back to draft on
// unpublish.
func (s *Server) handleEditAnnouncement(w http.ResponseWriter, r *http.Request) {
	entry := auditEntry(r)
	title, body, published, ok := decodeContentBody(w, r)
	if !ok {
		return
	}
	before, after, err := s.updateAnnouncement(r.Context(), r.PathValue("id"), title, body, published)
	if errors.Is(err, pgx.ErrNoRows) {
		WriteError(w, http.StatusNotFound, "Announcement not found.")
		return
	}
	if err != nil {
		writeErr(w, s.log, err)
		return
	}
	if entry != nil {
		entry.Target = after.ID
		entry.Before = mustJSON(contentAuditView{ID: before.ID, Title: before.Title, Published: before.Published})
		entry.After = mustJSON(contentAuditView{ID: after.ID, Title: after.Title, Published: after.Published})
	}
	writeJSON(w, http.StatusOK, after)
}

// handleDeleteAnnouncement removes one announcement for good and fills
// the envelope's Before with what was lost.
func (s *Server) handleDeleteAnnouncement(w http.ResponseWriter, r *http.Request) {
	entry := auditEntry(r)
	before, err := s.deleteAnnouncement(r.Context(), r.PathValue("id"))
	if errors.Is(err, pgx.ErrNoRows) {
		WriteError(w, http.StatusNotFound, "Announcement not found.")
		return
	}
	if err != nil {
		writeErr(w, s.log, err)
		return
	}
	if entry != nil {
		entry.Target = before.ID
		entry.Before = mustJSON(contentAuditView{ID: before.ID, Title: before.Title, Published: before.Published})
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": before.ID})
}

// decodeContentBody decodes and validates the shared
// title+body+published payload of announcements and help articles.
func decodeContentBody(w http.ResponseWriter, r *http.Request) (title, body string, published, ok bool) {
	var req contentBodyRequest
	if !decodeBody(w, r, &req) {
		return "", "", false, false
	}
	title, ok = contentText(req.Title, maxContentTitleChars)
	if !ok {
		WriteError(w, http.StatusBadRequest, "Enter a title of at most 200 characters.")
		return "", "", false, false
	}
	body, ok = contentText(req.Content, maxContentBodyChars)
	if !ok {
		WriteError(w, http.StatusBadRequest, "Enter a body of at most 50000 characters.")
		return "", "", false, false
	}
	return title, body, req.Published, true
}

func (s *Server) insertAnnouncement(ctx context.Context, id, title, body string, published bool) (announcementRow, error) {
	row := announcementRow{ID: id, Title: title, Content: body, Published: published}
	err := s.pool.QueryRow(ctx,
		`INSERT INTO announcements (id, title, content, published_at)
		 VALUES ($1, $2, $3, CASE WHEN $4 THEN now() ELSE NULL END)
		 RETURNING created_at, updated_at`,
		id, title, body, published).Scan(&row.CreatedAt, &row.UpdatedAt)
	return row, err
}

// updateAnnouncement rewrites one announcement inside a transaction —
// the row is locked and its prior state captured for the audit
// envelope in the same transaction that applies the edit.
func (s *Server) updateAnnouncement(ctx context.Context, id, title, body string, published bool) (before, after announcementRow, err error) {
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		before, err = scanAnnouncementRow(tx.QueryRow(ctx,
			`SELECT id, title, content, published_at IS NOT NULL, created_at, updated_at
			   FROM announcements WHERE id = $1 FOR UPDATE`, id))
		if err != nil {
			return err
		}
		after, err = scanAnnouncementRow(tx.QueryRow(ctx,
			`UPDATE announcements
			    SET title = $2, content = $3,
			        published_at = CASE WHEN $4 THEN COALESCE(published_at, now()) ELSE NULL END,
			        updated_at = now()
			  WHERE id = $1
			  RETURNING id, title, content, published_at IS NOT NULL, created_at, updated_at`,
			id, title, body, published))
		return err
	})
	return before, after, err
}

func (s *Server) deleteAnnouncement(ctx context.Context, id string) (before announcementRow, err error) {
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		before, err = scanAnnouncementRow(tx.QueryRow(ctx,
			`SELECT id, title, content, published_at IS NOT NULL, created_at, updated_at
			   FROM announcements WHERE id = $1 FOR UPDATE`, id))
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `DELETE FROM announcements WHERE id = $1`, id)
		return err
	})
	return before, err
}

func scanAnnouncementRow(row pgx.Row) (announcementRow, error) {
	var a announcementRow
	err := row.Scan(&a.ID, &a.Title, &a.Content, &a.Published, &a.CreatedAt, &a.UpdatedAt)
	return a, err
}

// ---- help categories: admin CRUD ----

// handleListHelpCategories answers every category with its total
// article count (drafts included). Categories are deliberately few —
// the member overview serves them whole — so this list is unpaged.
func (s *Server) handleListHelpCategories(w http.ResponseWriter, r *http.Request) {
	rows, err := s.pool.Query(r.Context(),
		`SELECT c.id, c.title, count(a.id), c.created_at, c.updated_at
		   FROM help_categories c
		   LEFT JOIN help_articles a ON a.category_id = c.id
		  GROUP BY c.id
		  ORDER BY c.created_at ASC, c.id ASC`)
	if err != nil {
		writeErr(w, s.log, err)
		return
	}
	defer rows.Close()
	out := []helpCategoryRow{}
	for rows.Next() {
		var c helpCategoryRow
		if err := rows.Scan(&c.ID, &c.Title, &c.Articles, &c.CreatedAt, &c.UpdatedAt); err != nil {
			writeErr(w, s.log, err)
			return
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		writeErr(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// handleCreateHelpCategory stores a new (always draft-empty) category.
func (s *Server) handleCreateHelpCategory(w http.ResponseWriter, r *http.Request) {
	entry := auditEntry(r)
	title, ok := decodeCategoryTitle(w, r)
	if !ok {
		return
	}
	row := helpCategoryRow{ID: uuid.NewString(), Title: title}
	if err := s.pool.QueryRow(r.Context(),
		`INSERT INTO help_categories (id, title) VALUES ($1, $2)
		 RETURNING created_at, updated_at`, row.ID, title).
		Scan(&row.CreatedAt, &row.UpdatedAt); err != nil {
		writeErr(w, s.log, err)
		return
	}
	if entry != nil {
		entry.Target = row.ID
		entry.After = mustJSON(contentAuditView{ID: row.ID, Title: row.Title})
	}
	writeJSON(w, http.StatusOK, row)
}

// handleEditHelpCategory renames one category; the member overview
// shows the new title on the next read.
func (s *Server) handleEditHelpCategory(w http.ResponseWriter, r *http.Request) {
	entry := auditEntry(r)
	title, ok := decodeCategoryTitle(w, r)
	if !ok {
		return
	}
	var before, after helpCategoryRow
	err := pgx.BeginFunc(r.Context(), s.pool, func(tx pgx.Tx) error {
		if err := tx.QueryRow(r.Context(),
			`SELECT id, title FROM help_categories WHERE id = $1 FOR UPDATE`,
			r.PathValue("id")).Scan(&before.ID, &before.Title); err != nil {
			return err
		}
		return tx.QueryRow(r.Context(),
			`UPDATE help_categories SET title = $2, updated_at = now()
			  WHERE id = $1
			  RETURNING id, title, created_at, updated_at`,
			before.ID, title).
			Scan(&after.ID, &after.Title, &after.CreatedAt, &after.UpdatedAt)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		WriteError(w, http.StatusNotFound, "Help category not found.")
		return
	}
	if err != nil {
		writeErr(w, s.log, err)
		return
	}
	after.Articles = s.categoryArticleCount(r.Context(), after.ID)
	if entry != nil {
		entry.Target = after.ID
		entry.Before = mustJSON(contentAuditView{ID: before.ID, Title: before.Title})
		entry.After = mustJSON(contentAuditView{ID: after.ID, Title: after.Title})
	}
	writeJSON(w, http.StatusOK, after)
}

// handleDeleteHelpCategory removes an empty category. A category that
// still holds articles (drafts included) is refused — deleting live
// structure under articles is a mistake, not a workflow.
func (s *Server) handleDeleteHelpCategory(w http.ResponseWriter, r *http.Request) {
	entry := auditEntry(r)
	var before helpCategoryRow
	var articles int
	err := pgx.BeginFunc(r.Context(), s.pool, func(tx pgx.Tx) error {
		if err := tx.QueryRow(r.Context(),
			`SELECT id, title FROM help_categories WHERE id = $1 FOR UPDATE`,
			r.PathValue("id")).Scan(&before.ID, &before.Title); err != nil {
			return err
		}
		return tx.QueryRow(r.Context(),
			`SELECT count(*) FROM help_articles WHERE category_id = $1`, before.ID).Scan(&articles)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		WriteError(w, http.StatusNotFound, "Help category not found.")
		return
	}
	if err != nil {
		writeErr(w, s.log, err)
		return
	}
	if articles > 0 {
		WriteError(w, http.StatusConflict, "The category still has articles. Delete or move them first.")
		return
	}
	if _, err := s.pool.Exec(r.Context(), `DELETE FROM help_categories WHERE id = $1`, before.ID); err != nil {
		writeErr(w, s.log, err)
		return
	}
	if entry != nil {
		entry.Target = before.ID
		entry.Before = mustJSON(contentAuditView{ID: before.ID, Title: before.Title})
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": before.ID})
}

// decodeCategoryTitle validates the category payload.
func decodeCategoryTitle(w http.ResponseWriter, r *http.Request) (string, bool) {
	var req createCategoryRequest
	if !decodeBody(w, r, &req) {
		return "", false
	}
	title, ok := contentText(req.Title, maxContentTitleChars)
	if !ok {
		WriteError(w, http.StatusBadRequest, "Enter a title of at most 200 characters.")
		return "", false
	}
	return title, true
}

func (s *Server) categoryArticleCount(ctx context.Context, categoryID string) int {
	n := 0
	_ = s.pool.QueryRow(ctx,
		`SELECT count(*) FROM help_articles WHERE category_id = $1`, categoryID).Scan(&n)
	return n
}

// ---- help articles: admin CRUD ----

// handleListHelpArticles answers the management list — drafts included
// — newest first, optionally one category, paginated.
func (s *Server) handleListHelpArticles(w http.ResponseWriter, r *http.Request) {
	pageNo, pageSize, err := contentPageParams(r)
	if err != nil {
		WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	cate := r.URL.Query().Get("cate")
	rows, err := s.pool.Query(r.Context(),
		`SELECT a.id, a.category_id, c.title, a.title, a.content, a.published_at IS NOT NULL,
		        a.created_at, a.updated_at, count(*) OVER()
		   FROM help_articles a
		   JOIN help_categories c ON c.id = a.category_id
		  WHERE ($1 = '' OR a.category_id = $1)
		  ORDER BY a.created_at DESC, a.id DESC
		  OFFSET $2 LIMIT $3`, cate, (pageNo-1)*pageSize, pageSize)
	if err != nil {
		writeErr(w, s.log, err)
		return
	}
	defer rows.Close()
	out := []helpArticleRow{}
	total := 0
	for rows.Next() {
		var a helpArticleRow
		if err := rows.Scan(&a.ID, &a.CategoryID, &a.Category, &a.Title, &a.Content, &a.Published,
			&a.CreatedAt, &a.UpdatedAt, &total); err != nil {
			writeErr(w, s.log, err)
			return
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		writeErr(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"content": out, "total": total})
}

// handleCreateHelpArticle stores a new article in an existing category.
func (s *Server) handleCreateHelpArticle(w http.ResponseWriter, r *http.Request) {
	entry := auditEntry(r)
	var req struct {
		contentBodyRequest
		CategoryID string `json:"category_id"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	title, body, ok := contentBodyFields(w, req.Title, req.Content)
	if !ok {
		return
	}
	if !s.categoryExists(r.Context(), req.CategoryID) {
		WriteError(w, http.StatusBadRequest, "Choose a category from the list.")
		return
	}
	id := uuid.NewString()
	row := helpArticleRow{ID: id, CategoryID: req.CategoryID, Title: title, Content: body, Published: req.Published}
	err := s.pool.QueryRow(r.Context(),
		`INSERT INTO help_articles (id, category_id, title, content, published_at)
		 VALUES ($1, $2, $3, $4, CASE WHEN $5 THEN now() ELSE NULL END)
		 RETURNING (SELECT title FROM help_categories WHERE id = $2), created_at, updated_at`,
		id, req.CategoryID, title, body, req.Published).
		Scan(&row.Category, &row.CreatedAt, &row.UpdatedAt)
	if err != nil {
		writeErr(w, s.log, err)
		return
	}
	if entry != nil {
		entry.Target = row.ID
		entry.After = mustJSON(contentAuditView{ID: row.ID, Title: row.Title, Published: row.Published, CategoryID: row.CategoryID})
	}
	writeJSON(w, http.StatusOK, row)
}

// handleEditHelpArticle updates title, body, category, and the publish
// switch of one article.
func (s *Server) handleEditHelpArticle(w http.ResponseWriter, r *http.Request) {
	entry := auditEntry(r)
	var req struct {
		contentBodyRequest
		CategoryID string `json:"category_id"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	title, body, ok := contentBodyFields(w, req.Title, req.Content)
	if !ok {
		return
	}
	if !s.categoryExists(r.Context(), req.CategoryID) {
		WriteError(w, http.StatusBadRequest, "Choose a category from the list.")
		return
	}
	var before, after helpArticleRow
	err := pgx.BeginFunc(r.Context(), s.pool, func(tx pgx.Tx) error {
		if err := tx.QueryRow(r.Context(),
			`SELECT id, category_id, title, content, published_at IS NOT NULL
			   FROM help_articles WHERE id = $1 FOR UPDATE`, r.PathValue("id")).
			Scan(&before.ID, &before.CategoryID, &before.Title, &before.Content, &before.Published); err != nil {
			return err
		}
		return tx.QueryRow(r.Context(),
			`UPDATE help_articles
			    SET category_id = $2, title = $3, content = $4,
			        published_at = CASE WHEN $5 THEN COALESCE(published_at, now()) ELSE NULL END,
			        updated_at = now()
			  WHERE id = $1
			  RETURNING (SELECT title FROM help_categories WHERE id = $2), created_at, updated_at`,
			before.ID, req.CategoryID, title, body, req.Published).
			Scan(&after.Category, &after.CreatedAt, &after.UpdatedAt)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		WriteError(w, http.StatusNotFound, "Help article not found.")
		return
	}
	if err != nil {
		writeErr(w, s.log, err)
		return
	}
	after.ID, after.CategoryID, after.Title, after.Content, after.Published =
		before.ID, req.CategoryID, title, body, req.Published
	if entry != nil {
		entry.Target = after.ID
		entry.Before = mustJSON(contentAuditView{ID: before.ID, Title: before.Title, Published: before.Published, CategoryID: before.CategoryID})
		entry.After = mustJSON(contentAuditView{ID: after.ID, Title: after.Title, Published: after.Published, CategoryID: after.CategoryID})
	}
	writeJSON(w, http.StatusOK, after)
}

// handleDeleteHelpArticle removes one article for good.
func (s *Server) handleDeleteHelpArticle(w http.ResponseWriter, r *http.Request) {
	entry := auditEntry(r)
	var before helpArticleRow
	err := pgx.BeginFunc(r.Context(), s.pool, func(tx pgx.Tx) error {
		if err := tx.QueryRow(r.Context(),
			`SELECT id, category_id, title, content, published_at IS NOT NULL
			   FROM help_articles WHERE id = $1 FOR UPDATE`, r.PathValue("id")).
			Scan(&before.ID, &before.CategoryID, &before.Title, &before.Content, &before.Published); err != nil {
			return err
		}
		_, err := tx.Exec(r.Context(), `DELETE FROM help_articles WHERE id = $1`, before.ID)
		return err
	})
	if errors.Is(err, pgx.ErrNoRows) {
		WriteError(w, http.StatusNotFound, "Help article not found.")
		return
	}
	if err != nil {
		writeErr(w, s.log, err)
		return
	}
	if entry != nil {
		entry.Target = before.ID
		entry.Before = mustJSON(contentAuditView{ID: before.ID, Title: before.Title, Published: before.Published, CategoryID: before.CategoryID})
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": before.ID})
}

// contentBodyFields validates the title+body half of an article
// payload (the embedded struct carries the category id).
func contentBodyFields(w http.ResponseWriter, rawTitle, rawBody string) (title, body string, ok bool) {
	title, ok = contentText(rawTitle, maxContentTitleChars)
	if !ok {
		WriteError(w, http.StatusBadRequest, "Enter a title of at most 200 characters.")
		return "", "", false
	}
	body, ok = contentText(rawBody, maxContentBodyChars)
	if !ok {
		WriteError(w, http.StatusBadRequest, "Enter a body of at most 50000 characters.")
		return "", "", false
	}
	return title, body, true
}

func (s *Server) categoryExists(ctx context.Context, id string) bool {
	var one int
	return s.pool.QueryRow(ctx,
		`SELECT 1 FROM help_categories WHERE id = $1`, id).Scan(&one) == nil
}

// ---- internal read RPC (the contract side) ----

// contentSummary is one row of a member content listing: identifying
// and browsing fields, no body (the contract's shape, field order
// included — api re-encodes these byte-for-byte through its own twins).
type contentSummary struct {
	ID         string `json:"id"`
	Title      string `json:"title"`
	CreateTime string `json:"createTime"`
}

// contentPage is the listings' wire shape (announcements and help
// alike). Content is never null — an empty page is [].
type contentPage struct {
	Content       []contentSummary `json:"content"`
	TotalElements int              `json:"totalElements"`
}

// announcementBody is an announcement's full document.
type announcementBody struct {
	ID         string `json:"id"`
	Title      string `json:"title"`
	CreateTime string `json:"createTime"`
	Content    string `json:"content"`
}

// announcementDetail is one announcement with the adjacent summaries
// for prev/next navigation; Back and Next are null at the ends of the
// published listing (back = older, next = newer).
type announcementDetail struct {
	Info announcementBody `json:"info"`
	Back *contentSummary  `json:"back"`
	Next *contentSummary  `json:"next"`
}

// helpCategoryDoc is one section of the help center: the category's id
// (the member surface's cate), its title, and its published articles
// in reading order. Items is never null.
type helpCategoryDoc struct {
	Cate  string           `json:"cate"`
	Title string           `json:"title"`
	Items []contentSummary `json:"items"`
}

// helpArticleDoc is one help document: the summary fields, the HTML
// body, and the category it belongs to.
type helpArticleDoc struct {
	ID         string `json:"id"`
	Title      string `json:"title"`
	CreateTime string `json:"createTime"`
	Content    string `json:"content"`
	Cate       string `json:"cate"`
}

// contentTime renders the contract's createTime: RFC3339 UTC.
func contentTime(t time.Time) string {
	return t.UTC().Format(time.RFC3339)
}

// contentWriteJSON answers the internal RPC with HTML left literal
// (no \u003c-escaping): the contract pins the upstream's bytes
// (content_CONTRACT.md: CMS bodies pass through literally), and api's
// proxy writer keeps the same setting on the way out.
func contentWriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
}

// RegisterContentRPC mounts the member read side (prefix-free
// patterns; the boot harness serves them at /internal/content/*).
// Transport auth is the compose network boundary — these are not
// registry endpoints and carry no session or permission gate.
func (s *Server) RegisterContentRPC(mux *http.ServeMux) {
	mux.HandleFunc("GET /content/announcements", s.handleContentAnnouncements)
	mux.HandleFunc("GET /content/announcements/{id}", s.handleContentAnnouncementDetail)
	mux.HandleFunc("GET /content/help", s.handleContentHelpCenter)
	mux.HandleFunc("GET /content/help/categories/{cate}", s.handleContentHelpCategory)
	mux.HandleFunc("GET /content/help/articles/{id}", s.handleContentHelpArticle)
}

// handleContentAnnouncements serves the published announcements page,
// newest first (the contract's news order).
func (s *Server) handleContentAnnouncements(w http.ResponseWriter, r *http.Request) {
	pageNo, pageSize, err := contentPageParams(r)
	if err != nil {
		writeInternalErr(w, http.StatusBadRequest, err.Error())
		return
	}
	page := contentPage{Content: []contentSummary{}}
	rows, err := s.pool.Query(r.Context(),
		`SELECT id, title, created_at, count(*) OVER()
		   FROM announcements
		  WHERE published_at IS NOT NULL
		  ORDER BY created_at DESC, id DESC
		  OFFSET $1 LIMIT $2`, (pageNo-1)*pageSize, pageSize)
	if err != nil {
		s.contentRPCFailed(w, err)
		return
	}
	defer rows.Close()
	for rows.Next() {
		var id, title string
		var at time.Time
		if err := rows.Scan(&id, &title, &at, &page.TotalElements); err != nil {
			s.contentRPCFailed(w, err)
			return
		}
		page.Content = append(page.Content, contentSummary{ID: id, Title: title, CreateTime: contentTime(at)})
	}
	if err := rows.Err(); err != nil {
		s.contentRPCFailed(w, err)
		return
	}
	contentWriteJSON(w, http.StatusOK, page)
}

// handleContentAnnouncementDetail serves one published announcement
// with its older/newer neighbors; unknown or draft ids answer 404.
func (s *Server) handleContentAnnouncementDetail(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var doc announcementBody
	var createdAt time.Time
	err := s.pool.QueryRow(r.Context(),
		`SELECT id, title, created_at, content
		   FROM announcements
		  WHERE id = $1 AND published_at IS NOT NULL`, id).
		Scan(&doc.ID, &doc.Title, &createdAt, &doc.Content)
	if errors.Is(err, pgx.ErrNoRows) {
		writeInternalErr(w, http.StatusNotFound, "announcement not found")
		return
	}
	if err != nil {
		s.contentRPCFailed(w, err)
		return
	}
	doc.CreateTime = contentTime(createdAt)
	detail := announcementDetail{Info: doc}
	// The neighbors are adjacent in the published listing's order
	// (newest first): back = the next-older row, next = the next-newer
	// row, and the id breaks created_at ties exactly as the listing's
	// ORDER BY does. Either end of the listing leaves its pointer nil —
	// the contract's null.
	var back, next contentSummary
	var at time.Time
	err = s.pool.QueryRow(r.Context(),
		`SELECT id, title, created_at
		   FROM announcements
		  WHERE published_at IS NOT NULL
		    AND (created_at < $2 OR (created_at = $2 AND id < $1))
		  ORDER BY created_at DESC, id DESC
		  LIMIT 1`, id, createdAt).
		Scan(&back.ID, &back.Title, &at)
	switch {
	case err == nil:
		back.CreateTime = contentTime(at)
		detail.Back = &back
	case errors.Is(err, pgx.ErrNoRows):
	default:
		s.contentRPCFailed(w, err)
		return
	}
	err = s.pool.QueryRow(r.Context(),
		`SELECT id, title, created_at
		   FROM announcements
		  WHERE published_at IS NOT NULL
		    AND (created_at > $2 OR (created_at = $2 AND id > $1))
		  ORDER BY created_at ASC, id ASC
		  LIMIT 1`, id, createdAt).
		Scan(&next.ID, &next.Title, &at)
	switch {
	case err == nil:
		next.CreateTime = contentTime(at)
		detail.Next = &next
	case errors.Is(err, pgx.ErrNoRows):
	default:
		s.contentRPCFailed(w, err)
		return
	}
	contentWriteJSON(w, http.StatusOK, detail)
}

// handleContentHelpCenter serves the help-center overview: every
// category in creation order, each with its published articles in
// reading order (oldest first). Categories with no published articles
// still appear — items is [].
func (s *Server) handleContentHelpCenter(w http.ResponseWriter, r *http.Request) {
	rows, err := s.pool.Query(r.Context(),
		`SELECT c.id, c.title, a.id, a.title, a.created_at
		   FROM help_categories c
		   LEFT JOIN help_articles a ON a.category_id = c.id AND a.published_at IS NOT NULL
		  ORDER BY c.created_at ASC, c.id ASC, a.created_at ASC, a.id ASC`)
	if err != nil {
		s.contentRPCFailed(w, err)
		return
	}
	defer rows.Close()
	out := []helpCategoryDoc{}
	indexOf := map[string]int{}
	for rows.Next() {
		var cateID, cateTitle string
		var articleID, articleTitle *string
		var at *time.Time
		if err := rows.Scan(&cateID, &cateTitle, &articleID, &articleTitle, &at); err != nil {
			s.contentRPCFailed(w, err)
			return
		}
		i, seen := indexOf[cateID]
		if !seen {
			i = len(out)
			indexOf[cateID] = i
			out = append(out, helpCategoryDoc{Cate: cateID, Title: cateTitle, Items: []contentSummary{}})
		}
		if articleID != nil {
			out[i].Items = append(out[i].Items, contentSummary{ID: *articleID, Title: *articleTitle, CreateTime: contentTime(*at)})
		}
	}
	if err := rows.Err(); err != nil {
		s.contentRPCFailed(w, err)
		return
	}
	contentWriteJSON(w, http.StatusOK, out)
}

// handleContentHelpCategory serves one category's published article
// page, reading order. An unknown category holds no articles — the
// empty page, not an error (the contract's shape).
func (s *Server) handleContentHelpCategory(w http.ResponseWriter, r *http.Request) {
	pageNo, pageSize, err := contentPageParams(r)
	if err != nil {
		writeInternalErr(w, http.StatusBadRequest, err.Error())
		return
	}
	page := contentPage{Content: []contentSummary{}}
	rows, err := s.pool.Query(r.Context(),
		`SELECT id, title, created_at, count(*) OVER()
		   FROM help_articles
		  WHERE category_id = $1 AND published_at IS NOT NULL
		  ORDER BY created_at ASC, id ASC
		  OFFSET $2 LIMIT $3`, r.PathValue("cate"), (pageNo-1)*pageSize, pageSize)
	if err != nil {
		s.contentRPCFailed(w, err)
		return
	}
	defer rows.Close()
	for rows.Next() {
		var id, title string
		var at time.Time
		if err := rows.Scan(&id, &title, &at, &page.TotalElements); err != nil {
			s.contentRPCFailed(w, err)
			return
		}
		page.Content = append(page.Content, contentSummary{ID: id, Title: title, CreateTime: contentTime(at)})
	}
	if err := rows.Err(); err != nil {
		s.contentRPCFailed(w, err)
		return
	}
	contentWriteJSON(w, http.StatusOK, page)
}

// handleContentHelpArticle serves one published help article; unknown
// or draft ids answer 404.
func (s *Server) handleContentHelpArticle(w http.ResponseWriter, r *http.Request) {
	var doc helpArticleDoc
	var createdAt time.Time
	err := s.pool.QueryRow(r.Context(),
		`SELECT id, title, created_at, content, category_id
		   FROM help_articles
		  WHERE id = $1 AND published_at IS NOT NULL`, r.PathValue("id")).
		Scan(&doc.ID, &doc.Title, &createdAt, &doc.Content, &doc.Cate)
	if errors.Is(err, pgx.ErrNoRows) {
		writeInternalErr(w, http.StatusNotFound, "help article not found")
		return
	}
	if err != nil {
		s.contentRPCFailed(w, err)
		return
	}
	doc.CreateTime = contentTime(createdAt)
	contentWriteJSON(w, http.StatusOK, doc)
}

func (s *Server) contentRPCFailed(w http.ResponseWriter, err error) {
	s.log.Error("admin: content rpc failed", "err", err)
	writeInternalErr(w, http.StatusInternalServerError, "Something went wrong. Try again.")
}
