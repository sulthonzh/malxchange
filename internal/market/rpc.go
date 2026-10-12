package market

import (
	"cmp"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/syaikhipin/malxchange/internal/events"
)

// RPC is the market service's internal surface: the market-data seams the
// api service fronts for public REST (ADR-0011 — api is the only public
// member surface; market stays headless, no sessions, no public ports).
// Auth is the compose network boundary: Caddy never proxies /internal.
type RPC struct {
	DB Pool
}

// RegisterRPC mounts every route on the service's internal mux. The boot
// harness strips the /internal prefix onto this mux, so these patterns
// serve at /internal/markets, /internal/trades/{market...},
// /internal/depth/{market...}, /internal/kline/{market...},
// /internal/ticker/{market...} and /internal/tickers. Market symbols
// contain a slash ("BTC/USDT"),
// so the wildcard is greedy.
func RegisterRPC(mux *http.ServeMux, r *RPC) {
	mux.HandleFunc("GET /markets", r.markets)
	mux.HandleFunc("GET /trades/{market...}", r.trades)
	mux.HandleFunc("GET /depth/{market...}", r.depth)
	mux.HandleFunc("GET /kline/{market...}", r.klines)
	mux.HandleFunc("GET /ticker/{market...}", r.ticker)
	mux.HandleFunc("GET /tickers", r.tickers)
}

// MarketSummary is one visible Market of the internal listing (ticket #33):
// the row api proxies for the front's market list (ADR-0011 — api is the
// only public member surface). Money values are decimal strings (ADR-0005).
type MarketSummary struct {
	Market     string `json:"market"`
	BaseCoin   string `json:"baseCoin"`
	QuoteCoin  string `json:"quoteCoin"`
	Zone       string `json:"zone"`
	Sort       int    `json:"sort"`
	Featured   bool   `json:"featured"`
	BaseScale  int    `json:"baseScale"`
	QuoteScale int    `json:"quoteScale"`
	TickSize   string `json:"tickSize"` // price quantum, decimal string
	LotSize    string `json:"lotSize"`  // amount quantum, decimal string
}

// markets serves the VISIBLE listing (ticket #33): only Markets with
// visible set, so a Market flipped invisible drops out here while its
// other endpoints keep answering — nothing else gates on visibility. The
// empty listing answers [] never null.
func (r *RPC) markets(w http.ResponseWriter, req *http.Request) {
	rows, err := r.DB.Query(req.Context(), `
		SELECT market, base_coin, quote_coin, zone, sort, featured,
		       base_scale, quote_scale, tick_size::text, lot_size::text
		FROM markets
		WHERE visible`)
	if err != nil {
		writeErr(w, err)
		return
	}
	defer rows.Close()

	listing := make([]MarketSummary, 0)
	for rows.Next() {
		var m MarketSummary
		if err := rows.Scan(&m.Market, &m.BaseCoin, &m.QuoteCoin, &m.Zone, &m.Sort,
			&m.Featured, &m.BaseScale, &m.QuoteScale, &m.TickSize, &m.LotSize); err != nil {
			writeErr(w, err)
			return
		}
		listing = append(listing, m)
	}
	if err := rows.Err(); err != nil {
		writeErr(w, err)
		return
	}
	sortMarkets(listing)
	writeJSON(w, listing)
}

// sortMarkets pins the listing's determinism promise at the publish seam:
// zone (lexicographic), then sort, then the Market as the tie-break —
// never map or heap order (review checklist: keyed reads, stable publish
// order).
func sortMarkets(ms []MarketSummary) {
	slices.SortFunc(ms, func(a, b MarketSummary) int {
		return cmp.Or(
			strings.Compare(a.Zone, b.Zone),
			cmp.Compare(a.Sort, b.Sort),
			strings.Compare(a.Market, b.Market),
		)
	})
}

// Trade is the sanitized market-wide record of a Fill (GLOSSARY): the
// underlying Fill's Member and Order identifiers stay on trade.fill and
// exist nowhere in the derived schema or on this wire. Money values are
// decimal strings (ADR-0005).
type Trade struct {
	ID        string    `json:"id"` // fill ID; clients dedupe on reconnect (ADR-0011)
	Price     string    `json:"price"`
	Amount    string    `json:"amount"`
	Turnover  string    `json:"turnover"`
	TakerSide string    `json:"takerSide"`
	Time      time.Time `json:"time"`
}

// tradeListDefaults pins how many trades a plain GET answers with; the
// accepted range is explicit so clients cannot silently shrink or explode
// a response.
const (
	defaultTradeLimit = 50
	maxTradeLimit     = 1000
)

// trades serves the latest trades of one Market, newest Fill first, so a
// reconnecting client dedupes against the head of the list (the fill ID).
func (r *RPC) trades(w http.ResponseWriter, req *http.Request) {
	market := req.PathValue("market")
	limit, err := tradeLimit(req)
	if err != nil {
		writeErr(w, err)
		return
	}
	rows, err := r.DB.Query(req.Context(), `
		SELECT fill_id, taker_side, price::text, amount::text, turnover::text, traded_at
		FROM trades
		WHERE market = $1
		ORDER BY traded_at DESC, fill_id DESC
		LIMIT $2`, market, limit)
	if err != nil {
		writeErr(w, err)
		return
	}
	defer rows.Close()

	// A Market with no trades yet answers an empty list, never null.
	trades := make([]Trade, 0)
	for rows.Next() {
		var t Trade
		if err := rows.Scan(&t.ID, &t.TakerSide, &t.Price, &t.Amount, &t.Turnover, &t.Time); err != nil {
			writeErr(w, err)
			return
		}
		trades = append(trades, t)
	}
	if err := rows.Err(); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, trades)
}

// tradeLimit parses the limit query parameter: absent = default, anything
// outside [1, max] is a bad request.
func tradeLimit(req *http.Request) (int, error) {
	raw := req.URL.Query().Get("limit")
	if raw == "" {
		return defaultTradeLimit, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 || n > maxTradeLimit {
		return 0, errBadLimit
	}
	return n, nil
}

var errBadLimit = errors.New("limit must be an integer between 1 and 1000")

// DepthLevel is one aggregated price level of the last-known Depth.
type DepthLevel struct {
	Price  string `json:"price"` // decimal string (ADR-0005)
	Amount string `json:"amount"`
}

// Depth is the Market's last-known full Depth, both sides best first. It
// answers from the last-known state even when plates stop flowing (engine
// down / Warmup, ADR-0011); it is empty only before any publish for that
// Market (true cold start).
type Depth struct {
	Market string       `json:"market"`
	Bids   []DepthLevel `json:"bids"`
	Asks   []DepthLevel `json:"asks"`
}

// depth serves the last-known Depth of one Market. Levels come back in one
// query (single snapshot, no side straddles a plate commit), ordered by
// price descending and split: bids stay best-first (highest price), asks
// reverse to best-first (lowest price).
func (r *RPC) depth(w http.ResponseWriter, req *http.Request) {
	market := req.PathValue("market")
	rows, err := r.DB.Query(req.Context(), `
		SELECT side, price::text, amount::text
		FROM depth_levels
		WHERE market = $1
		ORDER BY price DESC`, market)
	if err != nil {
		writeErr(w, err)
		return
	}
	defer rows.Close()

	// A Market never published to answers two empty sides, never null.
	d := Depth{Market: market, Bids: make([]DepthLevel, 0), Asks: make([]DepthLevel, 0)}
	for rows.Next() {
		var side string
		var lv DepthLevel
		if err := rows.Scan(&side, &lv.Price, &lv.Amount); err != nil {
			writeErr(w, err)
			return
		}
		if side == "buy" {
			d.Bids = append(d.Bids, lv)
		} else {
			d.Asks = append(d.Asks, lv)
		}
	}
	if err := rows.Err(); err != nil {
		writeErr(w, err)
		return
	}
	for i, j := 0, len(d.Asks)-1; i < j; i, j = i+1, j-1 {
		d.Asks[i], d.Asks[j] = d.Asks[j], d.Asks[i]
	}
	writeJSON(w, d)
}

// Kline is one OHLCV bar of a Market at a stored interval (GLOSSARY:
// Kline). Money values are decimal strings (ADR-0005); OpenTime is the
// bar's exact UTC bucket open. Bars carry market-wide values only — no
// Member/Order identifier exists on this wire (ticket #31).
type Kline struct {
	Interval  Interval  `json:"interval"`
	OpenTime  time.Time `json:"openTime"`
	Open      string    `json:"open"`
	High      string    `json:"high"`
	Low       string    `json:"low"`
	Close     string    `json:"close"`
	Amount    string    `json:"amount"`
	Turnover  string    `json:"turnover"`
	FillCount int64     `json:"fillCount"`
}

// klineDefaults pins how many bars a plain GET answers with; the accepted
// range is explicit so clients cannot silently shrink or explode a
// response (same discipline as the trade list).
const (
	defaultKlineLimit = 500
	maxKlineLimit     = 1000
)

// klines serves the latest Kline bars of one Market at one stored
// interval, oldest first — charts draw left to right, and a bar is
// identified by its bucket open, so unlike trades no reconnect-dedupe head
// is needed. Every stored interval is served (the legacy 1-minute-only
// restriction is dead, ticket #31); an absent or unknown interval is an
// explicit bad request (errBadInterval), never an empty success.
func (r *RPC) klines(w http.ResponseWriter, req *http.Request) {
	market := req.PathValue("market")
	iv, err := klineInterval(req)
	if err != nil {
		writeErr(w, err)
		return
	}
	limit, err := klineLimit(req)
	if err != nil {
		writeErr(w, err)
		return
	}
	rows, err := r.DB.Query(req.Context(), `
		SELECT "interval", bucket_open, open::text, high::text, low::text, close::text,
		       amount::text, turnover::text, fill_count
		FROM klines
		WHERE market = $1 AND "interval" = $2
		ORDER BY bucket_open DESC
		LIMIT $3`, market, string(iv), limit)
	if err != nil {
		writeErr(w, err)
		return
	}
	defer rows.Close()

	// A Market with no bars yet answers an empty list, never null. Rows
	// arrive latest first (the index order); reverse to oldest first.
	klines := make([]Kline, 0)
	for rows.Next() {
		var k Kline
		if err := rows.Scan(&k.Interval, &k.OpenTime, &k.Open, &k.High, &k.Low, &k.Close,
			&k.Amount, &k.Turnover, &k.FillCount); err != nil {
			writeErr(w, err)
			return
		}
		klines = append(klines, k)
	}
	if err := rows.Err(); err != nil {
		writeErr(w, err)
		return
	}
	for i, j := 0, len(klines)-1; i < j; i, j = i+1, j-1 {
		klines[i], klines[j] = klines[j], klines[i]
	}
	writeJSON(w, klines)
}

// klineInterval parses the interval query parameter: it must name a stored
// interval, and its absence is the same named error as an unknown one.
func klineInterval(req *http.Request) (Interval, error) {
	return ParseInterval(req.URL.Query().Get("interval"))
}

// klineLimit parses the limit query parameter: absent = default, anything
// outside [1, max] is a bad request.
func klineLimit(req *http.Request) (int, error) {
	raw := req.URL.Query().Get("limit")
	if raw == "" {
		return defaultKlineLimit, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 || n > maxKlineLimit {
		return 0, errBadLimit
	}
	return n, nil
}

// ticker serves one Market's current rolling 24h summary (last, high,
// low, turnover). It answers from the same computation the publish loop
// puts on the ticker topic (ComputeTicker → FoldTicker), so REST-first
// bootstrap and the stream can never disagree (ADR-0011). A Market with
// no in-window Fills — known or unknown — answers the empty summary, and
// a Market known only through its Depth publishes too.
func (r *RPC) ticker(w http.ResponseWriter, req *http.Request) {
	market := req.PathValue("market")
	t, err := ComputeTicker(req.Context(), r.DB, market, time.Now().UTC())
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, t)
}

// tickers serves the all-markets map keyed by Market symbol — every known
// Market (trade history or last-known Depth), each value identical to the
// single-Market endpoint. Keyed by symbol, never positional.
func (r *RPC) tickers(w http.ResponseWriter, req *http.Request) {
	markets, err := knownMarkets(req.Context(), r.DB)
	if err != nil {
		writeErr(w, err)
		return
	}
	now := time.Now().UTC()
	out := make(map[string]events.Ticker, len(markets))
	for _, m := range markets {
		t, err := ComputeTicker(req.Context(), r.DB, m, now)
		if err != nil {
			writeErr(w, err)
			return
		}
		out[m] = t
	}
	writeJSON(w, out)
}

func writeErr(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	if errors.Is(err, errBadLimit) || errors.Is(err, errBadInterval) {
		status = http.StatusBadRequest
	}
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
