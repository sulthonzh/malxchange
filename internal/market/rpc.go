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
// serve at /internal/trades/{market} and /internal/depth/{market}. Market
// symbols contain a slash ("BTC/USDT"), so the wildcard is greedy.
func RegisterRPC(mux *http.ServeMux, r *RPC) {
	mux.HandleFunc("GET /trades/{market...}", r.trades)
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

func writeErr(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	if errors.Is(err, errBadLimit) {
		status = http.StatusBadRequest
	}
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
