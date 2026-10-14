//go:build integration

// The CMS over the served seams (ticket #63): the support journeys
// through the registry-gated admin surface (every mutation audited),
// the ADR-0012 matrix (support manages; finance/compliance — and
// super-admin — cannot), and the internal read RPC's byte shape, both
// directly and round-tripped through api's real member content proxy.
// The contract literals mirror internal/api/content_CONTRACT.md and
// api's newStubAdminAPI — the same bytes, now from real tables.
package admin

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/syaikhipin/malxchange/internal/api"
)

// contentStaffs provisions the root super-admin and a support Employee
// through the management surface — the exact journey the app drives —
// and returns their live session cookies.
func contentStaffs(t *testing.T, s *itStack) (root provisioned, rootCookie, supportCookie *http.Cookie) {
	t.Helper()
	root = s.fullyProvisioned(t, "root@exchange.example.com")
	rootCookie = root.freshCookie(t, s)
	support := s.staff(t, root, rootCookie, "support@exchange.example.com", RoleSupport)
	return root, rootCookie, support.freshCookie(t, s)
}

// rowPause separates two content inserts by a beat: created_at order
// IS the member surface's news/reading order, and the tests must not
// depend on two statements sharing a microsecond.
func rowPause(t *testing.T) {
	t.Helper()
	time.Sleep(2 * time.Millisecond)
}

// createAnnouncement posts one announcement and fails the test on any
// non-200, returning the stored row.
func createAnnouncement(t *testing.T, s *itStack, cookie *http.Cookie, title, content string, published bool) announcementRow {
	t.Helper()
	body := fmt.Sprintf(`{"title":%q,"content":%q,"published":%t}`, title, content, published)
	status, _, raw := s.post(t, "/api/announcements", body, cookie)
	if status != http.StatusOK {
		t.Fatalf("create announcement %q: %d %s", title, status, raw)
	}
	var row announcementRow
	if err := json.Unmarshal([]byte(raw), &row); err != nil {
		t.Fatalf("decode announcement row: %v (%s)", err, raw)
	}
	if row.ID == "" || row.Title != title || row.Published != published {
		t.Fatalf("stored row = %+v", row)
	}
	return row
}

func decodeCategoryRow(t *testing.T, raw string) helpCategoryRow {
	t.Helper()
	var row helpCategoryRow
	if err := json.Unmarshal([]byte(raw), &row); err != nil {
		t.Fatalf("decode category row: %v (%s)", err, raw)
	}
	return row
}

// createArticle posts one help article and fails the test on any
// non-200, returning the stored row.
func createArticle(t *testing.T, s *itStack, cookie *http.Cookie, cate, title, content string, published bool) helpArticleRow {
	t.Helper()
	body := fmt.Sprintf(`{"category_id":%q,"title":%q,"content":%q,"published":%t}`, cate, title, content, published)
	status, _, raw := s.post(t, "/api/help/articles", body, cookie)
	if status != http.StatusOK {
		t.Fatalf("create article %q: %d %s", title, status, raw)
	}
	var row helpArticleRow
	if err := json.Unmarshal([]byte(raw), &row); err != nil {
		t.Fatalf("decode article row: %v (%s)", err, raw)
	}
	if row.ID == "" || row.CategoryID != cate {
		t.Fatalf("stored article row = %+v", row)
	}
	return row
}

// getBody GETs a path without a session (the internal RPC's caller is
// the compose network) and returns status + body.
func (s *itStack) getBody(t *testing.T, path string) (int, string) {
	t.Helper()
	status, _, raw := s.do(t, "GET", path, "", nil)
	return status, raw
}

// wantBody asserts one exact response body — the contract's byte
// shape, not a decoded twin.
func wantBody(t *testing.T, status int, raw string, wantStatus int, want string) {
	t.Helper()
	if status != wantStatus {
		t.Fatalf("status = %d, want %d (body %s)", status, wantStatus, raw)
	}
	if strings.TrimSpace(raw) != want {
		t.Errorf("body =\n%s\nwant\n%s", raw, want)
	}
}

// contractSeed lays the fixture both contract tests read: two
// published announcements (older then newer), a draft announcement,
// two categories, and a published plus a draft article in the first
// category.
type contractSeed struct {
	cat1, cat2            helpCategoryRow
	older, newer, draft   announcementRow
	article, draftArticle helpArticleRow
	olderCT               string // createTime as the wire carries it (RFC3339 UTC)
	newerCT               string
	articleCT             string
}

func contractSeedOf(t *testing.T, s *itStack, cookie *http.Cookie) contractSeed {
	t.Helper()
	var seed contractSeed

	seed.older = createAnnouncement(t, s, cookie, "Listing announced", "<p>BTC/USDT is live.</p>", true)
	seed.olderCT = seed.older.CreatedAt.UTC().Format(time.RFC3339)
	rowPause(t)
	seed.newer = createAnnouncement(t, s, cookie, "Second maintenance window", "<p>Read-only mode on Sunday.</p>", true)
	seed.newerCT = seed.newer.CreatedAt.UTC().Format(time.RFC3339)
	rowPause(t)
	seed.draft = createAnnouncement(t, s, cookie, "Draft only", "<p>Nobody can see this yet.</p>", false)

	status, _, raw := s.post(t, "/api/help/categories", `{"title":"Getting started"}`, cookie)
	if status != http.StatusOK {
		t.Fatalf("create category 1: %d %s", status, raw)
	}
	seed.cat1 = decodeCategoryRow(t, raw)
	rowPause(t)
	status, _, raw = s.post(t, "/api/help/categories", `{"title":"Fees"}`, cookie)
	if status != http.StatusOK {
		t.Fatalf("create category 2: %d %s", status, raw)
	}
	seed.cat2 = decodeCategoryRow(t, raw)

	seed.article = createArticle(t, s, cookie, seed.cat1.ID, "How to deposit",
		"<p>Deposits credit after the Coin's confirmation count.</p>", true)
	seed.articleCT = seed.article.CreatedAt.UTC().Format(time.RFC3339)
	rowPause(t)
	seed.draftArticle = createArticle(t, s, cookie, seed.cat1.ID, "Draft guide", "<p>Unpublished.</p>", false)
	return seed
}

// TestInternalContentContractByteShape proves the internal read RPC
// serves the pinned wire shapes byte-for-byte from real tables: the
// listing page, the detail with back/next neighbors (null at the
// ends), the help overview with draft-free items, the category page,
// and the article document with its cate. Drafts are invisible
// everywhere; unknown ids answer 404; malformed pagination is the one
// named bad request.
func TestInternalContentContractByteShape(t *testing.T) {
	s := newStack(t)
	_, _, cookie := contentStaffs(t, s)
	seed := contractSeedOf(t, s, cookie)

	// Announcements page, newest first, drafts never.
	status, raw := s.getBody(t, "/internal/content/announcements?pageNo=1&pageSize=10")
	wantBody(t, status, raw, http.StatusOK, fmt.Sprintf(
		`{"content":[{"id":%q,"title":"Second maintenance window","createTime":%q},`+
			`{"id":%q,"title":"Listing announced","createTime":%q}],"totalElements":2}`,
		seed.newer.ID, seed.newerCT, seed.older.ID, seed.olderCT))

	// The defaults are the contract's (pageNo 1, pageSize 10).
	status, raw = s.getBody(t, "/internal/content/announcements")
	wantBody(t, status, raw, http.StatusOK, fmt.Sprintf(
		`{"content":[{"id":%q,"title":"Second maintenance window","createTime":%q},`+
			`{"id":%q,"title":"Listing announced","createTime":%q}],"totalElements":2}`,
		seed.newer.ID, seed.newerCT, seed.older.ID, seed.olderCT))

	// Explicit pagination slices the same published set; the total
	// counts across pages.
	status, raw = s.getBody(t, "/internal/content/announcements?pageNo=2&pageSize=1")
	wantBody(t, status, raw, http.StatusOK, fmt.Sprintf(
		`{"content":[{"id":%q,"title":"Listing announced","createTime":%q}],"totalElements":2}`,
		seed.older.ID, seed.olderCT))

	// Detail: back is the next-older summary, next null at the newest end.
	status, raw = s.getBody(t, "/internal/content/announcements/"+seed.newer.ID)
	wantBody(t, status, raw, http.StatusOK, fmt.Sprintf(
		`{"info":{"id":%q,"title":"Second maintenance window","createTime":%q,`+
			`"content":"<p>Read-only mode on Sunday.</p>"},`+
			`"back":{"id":%q,"title":"Listing announced","createTime":%q},"next":null}`,
		seed.newer.ID, seed.newerCT, seed.older.ID, seed.olderCT))

	// Detail at the oldest end: back null, next the newer summary.
	status, raw = s.getBody(t, "/internal/content/announcements/"+seed.older.ID)
	wantBody(t, status, raw, http.StatusOK, fmt.Sprintf(
		`{"info":{"id":%q,"title":"Listing announced","createTime":%q,`+
			`"content":"<p>BTC/USDT is live.</p>"},"back":null,`+
			`"next":{"id":%q,"title":"Second maintenance window","createTime":%q}}`,
		seed.older.ID, seed.olderCT, seed.newer.ID, seed.newerCT))

	// A draft is a 404 to members — indistinguishable from unknown.
	status, raw = s.getBody(t, "/internal/content/announcements/"+seed.draft.ID)
	wantBody(t, status, raw, http.StatusNotFound, `{"error":"announcement not found"}`)
	status, _ = s.getBody(t, "/internal/content/announcements/not-an-id")
	if status != http.StatusNotFound {
		t.Fatalf("unknown announcement: status %d, want 404", status)
	}

	// Help overview: every category in creation order, published
	// articles only, items never null.
	status, raw = s.getBody(t, "/internal/content/help")
	wantBody(t, status, raw, http.StatusOK, fmt.Sprintf(
		`[{"cate":%q,"title":"Getting started","items":[{"id":%q,"title":"How to deposit","createTime":%q}]},`+
			`{"cate":%q,"title":"Fees","items":[]}]`,
		seed.cat1.ID, seed.article.ID, seed.articleCT, seed.cat2.ID))

	// One category's article page; unknown categories hold no articles.
	status, raw = s.getBody(t, "/internal/content/help/categories/"+seed.cat1.ID+"?pageNo=1&pageSize=10")
	wantBody(t, status, raw, http.StatusOK, fmt.Sprintf(
		`{"content":[{"id":%q,"title":"How to deposit","createTime":%q}],"totalElements":1}`,
		seed.article.ID, seed.articleCT))
	status, raw = s.getBody(t, "/internal/content/help/categories/not-a-cate")
	wantBody(t, status, raw, http.StatusOK, `{"content":[],"totalElements":0}`)

	// The article document repeats the owning category.
	status, raw = s.getBody(t, "/internal/content/help/articles/"+seed.article.ID)
	wantBody(t, status, raw, http.StatusOK, fmt.Sprintf(
		`{"id":%q,"title":"How to deposit","createTime":%q,`+
			`"content":"<p>Deposits credit after the Coin's confirmation count.</p>","cate":%q}`,
		seed.article.ID, seed.articleCT, seed.cat1.ID))
	status, _ = s.getBody(t, "/internal/content/help/articles/"+seed.draftArticle.ID)
	if status != http.StatusNotFound {
		t.Fatalf("draft article: status %d, want 404", status)
	}
	status, raw = s.getBody(t, "/internal/content/help/articles/not-an-id")
	wantBody(t, status, raw, http.StatusNotFound, `{"error":"help article not found"}`)

	// Malformed pagination is the named bad request (api never sends
	// one — its own parser refuses first — and would degrade on it).
	status, raw = s.getBody(t, "/internal/content/announcements?pageNo=0")
	wantBody(t, status, raw, http.StatusBadRequest,
		`{"error":"pageNo must be an integer of at least 1 and pageSize an integer between 1 and 50"}`)
}

// TestMemberContentProxyServesAdminContent is the end-to-end contract
// proof: api's real member content proxy (internal/api/content.go, no
// api-side changes) reading this stack's internal RPC and re-serving
// the identical bytes to members.
func TestMemberContentProxyServesAdminContent(t *testing.T) {
	s := newStack(t)
	_, _, cookie := contentStaffs(t, s)
	seed := contractSeedOf(t, s, cookie)

	mux := http.NewServeMux()
	api.RegisterContent(mux, api.ContentConfig{AdminBaseURL: s.rest.URL})
	member := httptest.NewServer(mux)
	t.Cleanup(member.Close)
	get := func(path string) (int, string) {
		t.Helper()
		resp, err := member.Client().Get(member.URL + path)
		if err != nil {
			t.Fatalf("get %s: %v", path, err)
		}
		defer resp.Body.Close()
		b, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		return resp.StatusCode, string(b)
	}

	status, raw := get("/api/announcements")
	wantBody(t, status, raw, http.StatusOK, fmt.Sprintf(
		`{"content":[{"id":%q,"title":"Second maintenance window","createTime":%q},`+
			`{"id":%q,"title":"Listing announced","createTime":%q}],"totalElements":2}`,
		seed.newer.ID, seed.newerCT, seed.older.ID, seed.olderCT))
	status, raw = get("/api/announcements/" + seed.newer.ID)
	wantBody(t, status, raw, http.StatusOK, fmt.Sprintf(
		`{"info":{"id":%q,"title":"Second maintenance window","createTime":%q,`+
			`"content":"<p>Read-only mode on Sunday.</p>"},`+
			`"back":{"id":%q,"title":"Listing announced","createTime":%q},"next":null}`,
		seed.newer.ID, seed.newerCT, seed.older.ID, seed.olderCT))
	status, raw = get("/api/help")
	wantBody(t, status, raw, http.StatusOK, fmt.Sprintf(
		`[{"cate":%q,"title":"Getting started","items":[{"id":%q,"title":"How to deposit","createTime":%q}]},`+
			`{"cate":%q,"title":"Fees","items":[]}]`,
		seed.cat1.ID, seed.article.ID, seed.articleCT, seed.cat2.ID))
	status, raw = get("/api/help/categories/" + seed.cat1.ID)
	wantBody(t, status, raw, http.StatusOK, fmt.Sprintf(
		`{"content":[{"id":%q,"title":"How to deposit","createTime":%q}],"totalElements":1}`,
		seed.article.ID, seed.articleCT))
	status, raw = get("/api/help/articles/" + seed.article.ID)
	wantBody(t, status, raw, http.StatusOK, fmt.Sprintf(
		`{"id":%q,"title":"How to deposit","createTime":%q,`+
			`"content":"<p>Deposits credit after the Coin's confirmation count.</p>","cate":%q}`,
		seed.article.ID, seed.articleCT, seed.cat1.ID))

	// An unknown id degrades to the member envelope's not-found.
	status, raw = get("/api/announcements/not-an-id")
	wantBody(t, status, raw, http.StatusNotFound, `{"message":"announcement not found"}`)
	status, raw = get("/api/help/articles/not-an-id")
	wantBody(t, status, raw, http.StatusNotFound, `{"message":"help article not found"}`)
}

// TestContentCRUDJourneysAuditEverything walks the management
// journeys: create (draft and straight-to-published), edit (including
// the publish switch both ways), delete — each landing exactly one
// audit row whose action is the endpoint's permission string — plus
// the input refusals (400), the unknown ids (404), and the
// non-empty-category guard (409).
func TestContentCRUDJourneysAuditEverything(t *testing.T) {
	s := newStack(t)
	_, _, cookie := contentStaffs(t, s)
	seed := contractSeedOf(t, s, cookie)

	// --- announcements ---
	draft := createAnnouncement(t, s, cookie, "Third window", "<p>Later.</p>", false)
	rows := s.auditRows(t, "create:announcement")
	if len(rows) != 4 { // the seed's three + this one
		t.Fatalf("create:announcement rows = %d, want 4", len(rows))
	}
	last := rows[len(rows)-1]
	if last.Actor != "support@exchange.example.com" || last.Target != draft.ID {
		t.Fatalf("audit row = actor %q target %q", last.Actor, last.Target)
	}

	status, _, raw := s.do(t, "PUT", "/api/announcements/"+draft.ID,
		`{"title":"Third window","content":"<p>Now.</p>","published":true}`, cookie)
	if status != http.StatusOK {
		t.Fatalf("edit announcement: %d %s", status, raw)
	}
	rows = s.auditRows(t, "edit:announcement")
	if len(rows) != 1 {
		t.Fatalf("edit:announcement rows = %d, want 1", len(rows))
	}
	// The audit rows come back as Postgres-rendered JSONB (spaces after
	// the colons); assert the publish flags, not the exact bytes.
	if !strings.Contains(string(rows[0].Before), `"published": false`) ||
		!strings.Contains(string(rows[0].After), `"published": true`) {
		t.Fatalf("edit audit before/after = %s / %s", rows[0].Before, rows[0].After)
	}

	// The management list shows drafts and published alike, newest first.
	status, _, raw = s.do(t, "GET", "/api/announcements", "", cookie)
	if status != http.StatusOK {
		t.Fatalf("list announcements: %d %s", status, raw)
	}
	var list struct {
		Content []announcementRow `json:"content"`
		Total   int               `json:"total"`
	}
	if err := json.Unmarshal([]byte(raw), &list); err != nil {
		t.Fatalf("decode list: %v (%s)", err, raw)
	}
	if list.Total != 4 || len(list.Content) != 4 || list.Content[0].ID != draft.ID {
		t.Fatalf("list = total %d, first %s", list.Total, list.Content[0].ID)
	}

	status, _, raw = s.do(t, "DELETE", "/api/announcements/"+draft.ID, "", cookie)
	if status != http.StatusOK {
		t.Fatalf("delete announcement: %d %s", status, raw)
	}
	if rows = s.auditRows(t, "delete:announcement"); len(rows) != 1 || rows[0].Target != draft.ID {
		t.Fatalf("delete:announcement rows = %+v", rows)
	}
	if status, _ := s.getBody(t, "/internal/content/announcements/"+draft.ID); status != http.StatusNotFound {
		t.Fatalf("deleted announcement still served: %d", status)
	}

	// Refusals: oversized title, blank body, unknown ids, bad page.
	status, _, raw = s.do(t, "POST", "/api/announcements",
		fmt.Sprintf(`{"title":%q,"content":"<p>x</p>","published":false}`, strings.Repeat("t", 201)), cookie)
	wantBody(t, status, raw, http.StatusBadRequest, `{"message":"Enter a title of at most 200 characters."}`)
	status, _, raw = s.do(t, "POST", "/api/announcements",
		`{"title":"No body","content":"   ","published":false}`, cookie)
	wantBody(t, status, raw, http.StatusBadRequest, `{"message":"Enter a body of at most 50000 characters."}`)
	status, _, raw = s.do(t, "PUT", "/api/announcements/not-an-id",
		`{"title":"x","content":"<p>x</p>","published":false}`, cookie)
	wantBody(t, status, raw, http.StatusNotFound, `{"message":"Announcement not found."}`)
	status, _, _ = s.do(t, "DELETE", "/api/announcements/not-an-id", "", cookie)
	if status != http.StatusNotFound {
		t.Fatalf("delete unknown announcement: %d, want 404", status)
	}
	status, _, _ = s.do(t, "GET", "/api/announcements?pageNo=0", "", cookie)
	if status != http.StatusBadRequest {
		t.Fatalf("bad admin page: %d, want 400", status)
	}

	// --- help center ---
	// Renaming a category propagates to the member overview.
	status, _, raw = s.do(t, "PUT", "/api/help/categories/"+seed.cat2.ID,
		`{"title":"Trading fees"}`, cookie)
	if status != http.StatusOK {
		t.Fatalf("rename category: %d %s", status, raw)
	}
	if status, raw = s.getBody(t, "/internal/content/help"); !strings.Contains(raw, `"title":"Trading fees"`) {
		t.Fatalf("renamed category not served: %s", raw)
	}

	// Moving an article between categories and unpublishing it.
	status, _, raw = s.do(t, "PUT", "/api/help/articles/"+seed.article.ID,
		fmt.Sprintf(`{"category_id":%q,"title":"How to deposit","content":"<p>Moved.</p>","published":false}`, seed.cat2.ID), cookie)
	if status != http.StatusOK {
		t.Fatalf("move article: %d %s", status, raw)
	}
	var moved helpArticleRow
	if err := json.Unmarshal([]byte(raw), &moved); err != nil {
		t.Fatalf("decode moved article: %v (%s)", err, raw)
	}
	if moved.CategoryID != seed.cat2.ID || moved.Published {
		t.Fatalf("moved article = %+v", moved)
	}
	if status, _ = s.getBody(t, "/internal/content/help/articles/"+seed.article.ID); status != http.StatusNotFound {
		t.Fatal("unpublished article still served to members")
	}

	// The categories list counts every article, drafts included.
	status, _, raw = s.do(t, "GET", "/api/help/categories", "", cookie)
	if status != http.StatusOK {
		t.Fatalf("list categories: %d %s", status, raw)
	}
	cats := []helpCategoryRow{}
	if err := json.Unmarshal([]byte(raw), &cats); err != nil {
		t.Fatalf("decode categories: %v (%s)", err, raw)
	}
	if len(cats) != 2 || cats[1].ID != seed.cat2.ID || cats[1].Articles != 1 {
		t.Fatalf("categories = %+v", cats)
	}

	// A category with articles is refused — drafts count too.
	status, _, raw = s.do(t, "DELETE", "/api/help/categories/"+seed.cat1.ID, "", cookie)
	wantBody(t, status, raw, http.StatusConflict,
		`{"message":"The category still has articles. Delete or move them first."}`)
	status, _, _ = s.do(t, "DELETE", "/api/help/categories/not-an-id", "", cookie)
	if status != http.StatusNotFound {
		t.Fatalf("delete unknown category: %d, want 404", status)
	}

	// Deleting the last article frees the category; the member
	// overview shrinks to the renamed survivor.
	status, _, _ = s.do(t, "DELETE", "/api/help/articles/"+seed.draftArticle.ID, "", cookie)
	if status != http.StatusOK {
		t.Fatalf("delete draft article: %d", status)
	}
	status, _, _ = s.do(t, "DELETE", "/api/help/categories/"+seed.cat1.ID, "", cookie)
	if status != http.StatusOK {
		t.Fatalf("delete emptied category: %d", status)
	}
	if rows = s.auditRows(t, "delete:help-category"); len(rows) != 1 || rows[0].Target != seed.cat1.ID {
		t.Fatalf("delete:help-category rows = %+v", rows)
	}
	status, raw = s.getBody(t, "/internal/content/help")
	wantBody(t, status, raw, http.StatusOK, fmt.Sprintf(
		`[{"cate":%q,"title":"Trading fees","items":[]}]`, seed.cat2.ID))

	// Unknown article ids and unknown categories refuse.
	status, _, raw = s.do(t, "PUT", "/api/help/articles/not-an-id",
		fmt.Sprintf(`{"category_id":%q,"title":"x","content":"<p>x</p>","published":false}`, seed.cat2.ID), cookie)
	wantBody(t, status, raw, http.StatusNotFound, `{"message":"Help article not found."}`)
	status, _, raw = s.do(t, "POST", "/api/help/articles",
		`{"category_id":"ghost","title":"x","content":"<p>x</p>","published":false}`, cookie)
	wantBody(t, status, raw, http.StatusBadRequest, `{"message":"Choose a category from the list."}`)

	// Every audited content action carried the support actor.
	for _, action := range []string{
		"create:announcement", "edit:announcement", "delete:announcement",
		"create:help-category", "edit:help-category", "delete:help-category",
		"create:help-article", "edit:help-article", "delete:help-article",
	} {
		rows := s.auditRows(t, action)
		if len(rows) == 0 {
			t.Errorf("no audit rows for %s", action)
			continue
		}
		for _, row := range rows {
			if row.Actor != "support@exchange.example.com" {
				t.Errorf("%s audited actor %q, want the support Employee", action, row.Actor)
			}
		}
	}
}

// TestContentMatrixHolds pins the ticket criterion on the served
// surface: support manages content; finance and compliance are refused
// everywhere (super-admin too — the ADR-0012 matrix gives content to
// support alone; the registry matrix IT walks every refusal
// exhaustively).
func TestContentMatrixHolds(t *testing.T) {
	s := newStack(t)
	root, rootCookie, supportCookie := contentStaffs(t, s)
	finance := s.staff(t, root, rootCookie, "finance@exchange.example.com", RoleFinance)
	compliance := s.staff(t, root, rootCookie, "compliance@exchange.example.com", RoleCompliance)

	// Support's positive journey.
	status, _, raw := s.do(t, "GET", "/api/announcements", "", supportCookie)
	if status != http.StatusOK {
		t.Fatalf("support list: %d %s", status, raw)
	}
	createAnnouncement(t, s, supportCookie, "From support", "<p>Hello.</p>", true)

	// Refused roles, on both a read and a write.
	for _, user := range []struct {
		name   string
		cookie *http.Cookie
	}{{"finance", finance.freshCookie(t, s)}, {"compliance", compliance.freshCookie(t, s)}, {"super-admin", rootCookie}} {
		status, _, raw = s.do(t, "GET", "/api/announcements", "", user.cookie)
		if status != http.StatusForbidden || !strings.Contains(raw, "You do not have permission to do that.") {
			t.Fatalf("%s list: %d %s", user.name, status, raw)
		}
		status, _, raw = s.do(t, "POST", "/api/announcements",
			`{"title":"Nope","content":"<p>x</p>","published":false}`, user.cookie)
		if status != http.StatusForbidden {
			t.Fatalf("%s create: %d %s", user.name, status, raw)
		}
		status, _, raw = s.do(t, "GET", "/api/help/categories", "", user.cookie)
		if status != http.StatusForbidden {
			t.Fatalf("%s categories: %d %s", user.name, status, raw)
		}
		status, _, raw = s.do(t, "POST", "/api/help/articles",
			`{"category_id":"x","title":"t","content":"<p>c</p>","published":false}`, user.cookie)
		if status != http.StatusForbidden {
			t.Fatalf("%s article create: %d %s", user.name, status, raw)
		}
	}
}
