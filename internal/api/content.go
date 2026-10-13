package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/syaikhipin/malxchange/internal/config"
)

// ContentConfig wires the member content proxy (ticket #45): the
// read-only announcements and help surfaces. The content itself is
// owned by admin-api (spec #25); api serves members through this
// internal-RPC client, so admin-api plugs in later without api changes.
type ContentConfig struct {
	// AdminBaseURL is the admin-api internal-RPC origin
	// ("http://admin:8080" in compose). Empty = unconfigured: every
	// content route answers as an empty content set, with one warning
	// per process, never per request.
	AdminBaseURL string
	// Log carries the state-change warnings; nil = slog.Default().
	Log *slog.Logger
	// Client is the upstream HTTP client; nil = a client with a 5s
	// timeout, so an unreachable admin-api fails fast instead of
	// hanging the member route.
	Client *http.Client
}

// LoadContentConfig reads MAL_API_ADMIN_BASE_URL (the wallet service's
// EnvDefault pattern): the admin-api origin the content proxy calls.
// Unset = the proxy is off and content routes answer empty. Listed in
// deploy/env.example.
func LoadContentConfig() ContentConfig {
	return ContentConfig{AdminBaseURL: config.EnvDefault("MAL_API_ADMIN_BASE_URL", "")}
}

// RegisterContent mounts the public member content routes (ticket #45):
// the announcements and help surfaces proxied from admin-api. Patterns
// carry the /api prefix — Caddy's `handle /api/*` proxies without
// stripping, so the api listener sees the browser's path. Every route
// is public for anonymous visitors (spec #24 story 26): content is
// operator guidance and gates nothing.
func RegisterContent(mux *http.ServeMux, cfg ContentConfig) {
	c := &contentProxy{
		base:   strings.TrimRight(cfg.AdminBaseURL, "/"),
		client: cfg.Client,
		log:    cfg.Log,
	}
	if c.client == nil {
		c.client = &http.Client{Timeout: 5 * time.Second}
	}
	if c.log == nil {
		c.log = slog.Default()
	}
	mux.HandleFunc("GET /api/announcements", c.announcements)
	mux.HandleFunc("GET /api/announcements/{id}", c.announcementDetail)
	mux.HandleFunc("GET /api/help", c.helpCenter)
	mux.HandleFunc("GET /api/help/categories/{cate}", c.helpList)
	mux.HandleFunc("GET /api/help/articles/{id}", c.helpDetail)
}

// contentProxy is the admin-api client behind the member content
// routes. Graceful degradation is the design (ticket #45): an
// unconfigured, unreachable, or misbehaving upstream answers as an
// empty content set — the front renders empty states, never errors.
// Each degradation is announced with ONE warning per state change, so a
// missing admin-api cannot spam the log on every request.
type contentProxy struct {
	base   string
	client *http.Client
	log    *slog.Logger
	state  atomic.Int32
}

// Upstream states — only transitions log; steady states never do.
const (
	contentFresh = int32(iota) // configured, no call answered yet
	contentUp
	contentDown
	contentOff // unconfigured (terminal: the env is fixed at boot)
)

var (
	// errContentNotFound: the upstream answered 404 — the id is unknown.
	errContentNotFound = errors.New("content not found")
	// errContentDegraded: the upstream is unconfigured or failing —
	// callers answer the empty-content-set response.
	errContentDegraded = errors.New("content upstream degraded")
)

// get fetches one upstream document into out. The internal contract
// (content_CONTRACT.md): 200 = the document, 404 = unknown id, anything
// else — or a transport or decode failure — = degraded.
func (c *contentProxy) get(ctx context.Context, path string, out any) error {
	if c.base == "" {
		c.noteUnconfigured()
		return errContentDegraded
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+path, nil)
	if err != nil {
		c.noteDown(err)
		return errContentDegraded
	}
	resp, err := c.client.Do(req)
	if err != nil {
		c.noteDown(err)
		return errContentDegraded
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusOK:
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			c.noteDown(err)
			return errContentDegraded
		}
		c.noteUp()
		return nil
	case resp.StatusCode == http.StatusNotFound:
		c.noteUp()
		return errContentNotFound
	default:
		c.noteDown(fmt.Errorf("%s answered status %d", path, resp.StatusCode))
		return errContentDegraded
	}
}

func (c *contentProxy) noteUnconfigured() {
	if c.state.Swap(contentOff) != contentOff {
		c.log.Warn("admin-api is not configured (MAL_API_ADMIN_BASE_URL is unset); announcements and help answer as empty until it is set")
	}
}

func (c *contentProxy) noteDown(err error) {
	if c.state.Swap(contentDown) != contentDown {
		c.log.Warn("admin-api content is unavailable; announcements and help answer as empty", "base", c.base, "error", err)
	}
}

func (c *contentProxy) noteUp() {
	if c.state.Swap(contentUp) == contentDown {
		c.log.Info("admin-api content is available again", "base", c.base)
	}
}

// contentSummary is one row of a content listing: the identifying and
// browsing fields, without the body. IDs are opaque strings set by
// admin-api; createTime is RFC3339 UTC.
type contentSummary struct {
	ID         string `json:"id"`
	Title      string `json:"title"`
	CreateTime string `json:"createTime"`
}

// contentPage is the listings' wire shape (announcements and help
// alike): the page rows plus the total across pages, so the front can
// render pagination. Content is never null — an empty page is [].
type contentPage struct {
	Content       []contentSummary `json:"content"`
	TotalElements int64            `json:"totalElements"`
}

const (
	defaultContentPageNo   = 1
	defaultContentPageSize = 10
	maxContentPageSize     = 50
)

var errBadContentPage = errors.New("pageNo must be an integer of at least 1 and pageSize an integer between 1 and 50")

// contentPageParams parses the shared pagination parameters: absent =
// defaults, anything malformed or out of range is the one named bad
// request (the market service's limit discipline — never a silent
// clamp).
func contentPageParams(req *http.Request) (pageNo, pageSize int, err error) {
	pageNo = defaultContentPageNo
	if raw := req.URL.Query().Get("pageNo"); raw != "" {
		pageNo, err = strconv.Atoi(raw)
		if err != nil || pageNo < 1 {
			return 0, 0, errBadContentPage
		}
	}
	pageSize = defaultContentPageSize
	if raw := req.URL.Query().Get("pageSize"); raw != "" {
		pageSize, err = strconv.Atoi(raw)
		if err != nil || pageSize < 1 || pageSize > maxContentPageSize {
			return 0, 0, errBadContentPage
		}
	}
	return pageNo, pageSize, nil
}

// announcements serves the announcements listing. A degraded upstream
// answers the empty page — 200, because the front must render its empty
// state, not an error (ticket #45).
func (c *contentProxy) announcements(w http.ResponseWriter, req *http.Request) {
	pageNo, pageSize, err := contentPageParams(req)
	if err != nil {
		writeContentErr(w, http.StatusBadRequest, err)
		return
	}
	var page contentPage
	if err := c.get(req.Context(), contentAnnouncementsPath(pageNo, pageSize), &page); err != nil {
		page = contentPage{} // degraded or unknown: the empty listing
	}
	if page.Content == nil {
		page.Content = []contentSummary{}
	}
	writeContentJSON(w, page)
}

// announcementBody is an announcement's full document: the summary
// fields plus the HTML body the CMS stored.
type announcementBody struct {
	ID         string `json:"id"`
	Title      string `json:"title"`
	CreateTime string `json:"createTime"`
	Content    string `json:"content"`
}

// announcementDetail is one announcement with the adjacent summaries
// for prev/next navigation (the legacy NoticeItem page's shape); Back
// and Next are null at the ends of the listing.
type announcementDetail struct {
	Info announcementBody `json:"info"`
	Back *contentSummary  `json:"back"`
	Next *contentSummary  `json:"next"`
}

var errAnnouncementNotFound = errors.New("announcement not found")

// announcementDetail serves one announcement. A degraded upstream is
// indistinguishable from an empty content set, so it and an unknown id
// both answer not found — the front renders its empty state (ticket #45).
func (c *contentProxy) announcementDetail(w http.ResponseWriter, req *http.Request) {
	var detail announcementDetail
	path := "/internal/content/announcements/" + url.PathEscape(req.PathValue("id"))
	if err := c.get(req.Context(), path, &detail); err != nil {
		writeContentErr(w, http.StatusNotFound, errAnnouncementNotFound)
		return
	}
	writeContentJSON(w, detail)
}

// helpCategory is one section of the help center: the category's id
// (the legacy cate), its title, and its articles in reading order.
type helpCategory struct {
	Cate  string           `json:"cate"`
	Title string           `json:"title"`
	Items []contentSummary `json:"items"`
}

// helpCenter serves the help-center overview: every category with its
// articles. A degraded upstream answers the empty overview (the front
// renders its empty state), and a null items array never reaches the
// wire — api answers [] even if the upstream violates the contract.
func (c *contentProxy) helpCenter(w http.ResponseWriter, req *http.Request) {
	var categories []helpCategory
	if err := c.get(req.Context(), "/internal/content/help", &categories); err != nil {
		categories = nil
	}
	if categories == nil {
		categories = []helpCategory{}
	}
	for i := range categories {
		if categories[i].Items == nil {
			categories[i].Items = []contentSummary{}
		}
	}
	writeContentJSON(w, categories)
}

// helpList serves one category's article listing.
func (c *contentProxy) helpList(w http.ResponseWriter, req *http.Request) {
	pageNo, pageSize, err := contentPageParams(req)
	if err != nil {
		writeContentErr(w, http.StatusBadRequest, err)
		return
	}
	prefix := "/internal/content/help/categories/" + url.PathEscape(req.PathValue("cate"))
	var page contentPage
	if err := c.get(req.Context(), contentListPath(prefix, pageNo, pageSize), &page); err != nil {
		page = contentPage{} // degraded or unknown: the empty listing
	}
	if page.Content == nil {
		page.Content = []contentSummary{}
	}
	writeContentJSON(w, page)
}

// helpArticle is one help document: the summary fields, the HTML body,
// and the category it belongs to.
type helpArticle struct {
	ID         string `json:"id"`
	Title      string `json:"title"`
	CreateTime string `json:"createTime"`
	Content    string `json:"content"`
	Cate       string `json:"cate"`
}

var errHelpArticleNotFound = errors.New("help article not found")

// helpDetail serves one help article, with the same not-found rule as
// announcement detail.
func (c *contentProxy) helpDetail(w http.ResponseWriter, req *http.Request) {
	var article helpArticle
	path := "/internal/content/help/articles/" + url.PathEscape(req.PathValue("id"))
	if err := c.get(req.Context(), path, &article); err != nil {
		writeContentErr(w, http.StatusNotFound, errHelpArticleNotFound)
		return
	}
	writeContentJSON(w, article)
}

func contentAnnouncementsPath(pageNo, pageSize int) string {
	return contentListPath("/internal/content/announcements", pageNo, pageSize)
}

func contentListPath(prefix string, pageNo, pageSize int) string {
	q := url.Values{"pageNo": {strconv.Itoa(pageNo)}, "pageSize": {strconv.Itoa(pageSize)}}
	return prefix + "?" + q.Encode()
}

// writeContentErr answers the public error envelope {"message":"..."},
// the shape apps/web's useApi reads. Ticket #37's shared WriteError in
// internal/api/errors.go emits the same body; this local twin keeps the
// names distinct so both tickets compile independently and the merger
// can collapse the calls.
func writeContentErr(w http.ResponseWriter, status int, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(struct {
		Message string `json:"message"`
	}{Message: err.Error()})
}

func writeContentJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	enc := json.NewEncoder(w)
	// CMS bodies are HTML; pass them through literally instead of
	// \u003c-escaping — the contract (content_CONTRACT.md) pins the
	// upstream's bytes, and both decode identically for clients.
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
}
