package market

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"
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
// serve at /internal/trades/{market...}, /internal/depth/{market...}, and
// /internal/kline/{market...}. Market symbols contain a slash ("BTC/USDT"),
// so the wildcard is greedy.
func RegisterRPC(mux *http.ServeMux, r *RPC) {
	mux.HandleFunc("GET /trades/{market...}", r.trades)
	mux.HandleFunc("GET /depth/{market...}", r.depth)
	mux.HandleFunc("GET /kline/{market...}", r.klines)
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
