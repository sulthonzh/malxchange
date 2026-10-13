// Member order queries over the internal RPC (ticket #41): the api
// service fronts these for its member REST, so api holds no order-table
// copy to drift (spec #24 story 30). Current = open orders with live
// progress; history = terminal orders (filled/canceled) newest-placed
// first, paged, each carrying the member's per-fill fees — the buy fee
// (base Coin) on fills the member bought, the sell fee (quote Coin) on
// fills the member sold (ADR-0005 fee sides).
package exchange

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"
)

// RPC serves the exchange service's internal surface. Compose keeps the
// port off Caddy; the network boundary is the auth.
type RPC struct {
	DB *pgxpool.Pool
}

// RegisterRPC mounts the member-order routes on the service's internal
// mux (the boot harness serves it under /internal/ with the prefix
// stripped — see internal/service).
func RegisterRPC(mux *http.ServeMux, r *RPC) {
	mux.HandleFunc("GET /orders/current", r.current)
	mux.HandleFunc("GET /orders/history", r.history)
}

var (
	errNeedMember  = errors.New("need member")
	errBadLimit    = errors.New("limit must be an integer between 1 and 1000")
	errBadOffset   = errors.New("offset must be a non-negative integer")
	errOrdersQuery = errors.New("order query failed")
)

// memberOrder is one row of the member's current or history list. Money
// is decimal strings (ADR-0005); price is empty for market orders; fills
// ride history only.
type memberOrder struct {
	OrderID      string `json:"orderId"`
	Market       string `json:"market"`
	Side         string `json:"side"`
	Kind         string `json:"kind"`
	Price        string `json:"price,omitempty"`
	Amount       string `json:"amount"`
	FilledAmount string `json:"filledAmount"`
	Turnover     string `json:"turnover"`
	Status       string `json:"status"`
	CreatedAt    string `json:"createdAt"`
	CanceledAt   string `json:"canceledAt,omitempty"`
}

// historyOrder is a terminal order carrying its fills; the array is
// always present (empty when nothing filled). Current orders carry no
// fills key at all — the split keeps the two wires explicit.
type historyOrder struct {
	memberOrder
	Fills []memberFill `json:"fills"`
}

type memberFill struct {
	Seq      int64  `json:"seq"`
	Price    string `json:"price"`
	Amount   string `json:"amount"`
	Turnover string `json:"turnover"`
	Fee      string `json:"fee"`
	Time     string `json:"time"`
}

// num normalizes a NUMERIC's text form to the decimal-string wire form
// (trailing zeros gone: "50000.10" → "50000.1").
func num(s string) string {
	d, err := decimal.NewFromString(s)
	if err != nil {
		return s
	}
	return d.String()
}

func writeOrdersErr(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	switch {
	case errors.Is(err, errNeedMember), errors.Is(err, errBadLimit), errors.Is(err, errBadOffset):
		status = http.StatusBadRequest
	case errors.Is(err, errOrdersQuery):
		status = http.StatusInternalServerError
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
}

func (r *RPC) current(w http.ResponseWriter, req *http.Request) {
	member := req.URL.Query().Get("member")
	if member == "" {
		writeOrdersErr(w, errNeedMember)
		return
	}
	rows, err := r.DB.Query(req.Context(),
		`SELECT id, market, side, kind, price::text, amount::text, filled_amount::text,
		        turnover::text, status, created_at
		   FROM orders
		  WHERE member_id = $1 AND status = 'open'
		  -- Market first, then newest placement inside the Market: the
		  -- answer must not depend on how the consumer grouped the
		  -- placements into apply transactions (created_at can tie or
		  -- differ across runs for one command batch).
		  ORDER BY market, seq DESC`, member)
	if err != nil {
		writeOrdersErr(w, fmt.Errorf("%w: %v", errOrdersQuery, err))
		return
	}
	defer rows.Close()
	out := make([]memberOrder, 0, 8)
	for rows.Next() {
		var o memberOrder
		var price any
		var created time.Time
		if err := rows.Scan(&o.OrderID, &o.Market, &o.Side, &o.Kind, &price,
			&o.Amount, &o.FilledAmount, &o.Turnover, &o.Status, &created); err != nil {
			writeOrdersErr(w, fmt.Errorf("%w: %v", errOrdersQuery, err))
			return
		}
		if s, ok := price.(string); ok {
			o.Price = num(s)
		}
		o.Amount, o.FilledAmount, o.Turnover = num(o.Amount), num(o.FilledAmount), num(o.Turnover)
		o.CreatedAt = created.UTC().Format(time.RFC3339Nano)
		out = append(out, o)
	}
	if err := rows.Err(); err != nil {
		writeOrdersErr(w, fmt.Errorf("%w: %v", errOrdersQuery, err))
		return
	}
	writeOrdersJSON(w, out)
}

func (r *RPC) history(w http.ResponseWriter, req *http.Request) {
	q := req.URL.Query()
	member := q.Get("member")
	if member == "" {
		writeOrdersErr(w, errNeedMember)
		return
	}
	limit := 50
	if raw := q.Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > 1000 {
			writeOrdersErr(w, errBadLimit)
			return
		}
		limit = n
	}
	offset := 0
	if raw := q.Get("offset"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 0 {
			writeOrdersErr(w, errBadOffset)
			return
		}
		offset = n
	}
	rows, err := r.DB.Query(req.Context(),
		`SELECT id, market, side, kind, price::text, amount::text, filled_amount::text,
		        turnover::text, status, created_at, canceled_at
		   FROM orders
		  WHERE member_id = $1 AND status IN ('filled', 'canceled')
		  ORDER BY created_at DESC, seq DESC
		  LIMIT $2 OFFSET $3`, member, limit, offset)
	if err != nil {
		writeOrdersErr(w, fmt.Errorf("%w: %v", errOrdersQuery, err))
		return
	}
	defer rows.Close()
	out := make([]historyOrder, 0, 8)
	ids := make([]string, 0, 8)
	for rows.Next() {
		var o historyOrder
		var price *string
		var created, canceled *time.Time
		if err := rows.Scan(&o.OrderID, &o.Market, &o.Side, &o.Kind, &price,
			&o.Amount, &o.FilledAmount, &o.Turnover, &o.Status, &created, &canceled); err != nil {
			writeOrdersErr(w, fmt.Errorf("%w: %v", errOrdersQuery, err))
			return
		}
		if price != nil {
			o.Price = num(*price)
		}
		o.Amount, o.FilledAmount, o.Turnover = num(o.Amount), num(o.FilledAmount), num(o.Turnover)
		if created != nil {
			o.CreatedAt = created.UTC().Format(time.RFC3339Nano)
		}
		if canceled != nil {
			o.CanceledAt = canceled.UTC().Format(time.RFC3339Nano)
		}
		o.Fills = []memberFill{} // every history order carries the array, empty when nothing filled
		out = append(out, o)
		ids = append(ids, o.OrderID)
	}
	if err := rows.Err(); err != nil {
		writeOrdersErr(w, fmt.Errorf("%w: %v", errOrdersQuery, err))
		return
	}
	if len(ids) > 0 {
		if err := r.attachFills(req.Context(), out, ids); err != nil {
			writeOrdersErr(w, fmt.Errorf("%w: %v", errOrdersQuery, err))
			return
		}
	}
	writeOrdersJSON(w, out)
}

// attachFills loads the member's executions for the given order ids and
// folds them into the orders, seq-ascending. The fee the member paid on
// a fill depends on their side: buy_fee (base Coin) when they were the
// buyer, sell_fee (quote Coin) when the seller.
func (r *RPC) attachFills(ctx context.Context, orders []historyOrder, ids []string) error {
	rows, err := r.DB.Query(ctx,
		`SELECT buy_order_id, sell_order_id, seq, price::text, amount::text,
		        turnover::text, buy_fee::text, sell_fee::text, created_at
		   FROM fills
		  WHERE buy_order_id = ANY($1) OR sell_order_id = ANY($1)
		  ORDER BY market, seq`, ids)
	if err != nil {
		return err
	}
	defer rows.Close()
	byID := make(map[string]*historyOrder, len(orders))
	for i := range orders {
		byID[orders[i].OrderID] = &orders[i]
	}
	for rows.Next() {
		var buyID, sellID, rawBuyFee, rawSellFee string
		var f memberFill
		var at time.Time
		if err := rows.Scan(&buyID, &sellID, &f.Seq, &f.Price, &f.Amount,
			&f.Turnover, &rawBuyFee, &rawSellFee, &at); err != nil {
			return err
		}
		f.Price, f.Amount, f.Turnover = num(f.Price), num(f.Amount), num(f.Turnover)
		f.Time = at.UTC().Format(time.RFC3339Nano)
		if o := byID[buyID]; o != nil {
			fill := f
			fill.Fee = num(rawBuyFee)
			o.Fills = append(o.Fills, fill)
		}
		if o := byID[sellID]; o != nil {
			fill := f
			fill.Fee = num(rawSellFee)
			o.Fills = append(o.Fills, fill)
		}
	}
	return rows.Err()
}

func writeOrdersJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
