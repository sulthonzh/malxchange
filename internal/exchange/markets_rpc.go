// Market-config admin seam (ticket #61, ADR-0009): the write path the
// markets table never had. admin-api fronts PUT with RBAC + Step-up +
// the Audit log; this RPC is the engine-side truth and the single
// writer. Edits cover the two state flags (Halted follows enabled=off,
// cancels still flow), maker/taker fees, price/amount collars, the
// turnover minimum, the market-order toggle, and the visibility/sort
// fields (zone, featured). Quantization edits (tick/lot/scale) are
// REFUSED while Orders rest — the same freeze guard WatchConfig
// enforces, moved up to write time so a refused edit never lands in
// the table. This RPC deliberately does NOT live-apply or publish:
// the engine's WatchConfig picks the row up within its 5s tick,
// applies it, and publishes market.updated — this ticket rides those
// mechanics, it does not rebuild them.
package exchange

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/syaikhipin/malxchange/internal/money"
)

// AdminRPC serves the exchange's config-admin internal surface. The
// compose network boundary is the auth; attribution rides X-Mal-Actor
// (admin-api sets it on every mutating call) for the engine log.
type AdminRPC struct {
	DB  *pgxpool.Pool
	Log *slog.Logger
}

// logger never returns nil (a nil Log is legal in tests).
func (r *AdminRPC) logger() *slog.Logger {
	if r.Log == nil {
		return slog.Default()
	}
	return r.Log
}

// RegisterAdminRPC mounts the Market-config routes on the service's
// internal mux (the boot harness serves it under /internal/ with the
// prefix stripped). Markets are named with a slash ("BTC/USDT"), so the
// edit route takes the multi-segment form.
func RegisterAdminRPC(mux *http.ServeMux, r *AdminRPC) {
	mux.HandleFunc("GET /markets", r.listMarkets)
	mux.HandleFunc("PUT /markets/{market...}", r.putMarket)
}

// marketView is the full Market row as decimal strings (ADR-0005) —
// the wire shape of both the list and the PUT answer. Null collars are
// JSON null (unbounded), not empty strings.
type marketView struct {
	Market              string  `json:"market"`
	BaseCoin            string  `json:"baseCoin"`
	QuoteCoin           string  `json:"quoteCoin"`
	BaseScale           int     `json:"baseScale"`
	QuoteScale          int     `json:"quoteScale"`
	TickSize            string  `json:"tickSize"`
	LotSize             string  `json:"lotSize"`
	MakerFeeRate        string  `json:"makerFeeRate"`
	TakerFeeRate        string  `json:"takerFeeRate"`
	MinPrice            *string `json:"minPrice"`
	MaxPrice            *string `json:"maxPrice"`
	MinAmount           string  `json:"minAmount"`
	MaxAmount           *string `json:"maxAmount"`
	MinTurnover         string  `json:"minTurnover"`
	MarketOrdersEnabled bool    `json:"marketOrdersEnabled"`
	Enabled             bool    `json:"enabled"`
	Visible             bool    `json:"visible"`
	Sort                int     `json:"sort"`
	Zone                string  `json:"zone"`
	Featured            bool    `json:"featured"`
}

// strPtr normalizes the config's "" = unbounded convention to JSON null.
func strPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func viewOf(m MarketCfg) marketView {
	return marketView{
		Market: m.Market, BaseCoin: m.BaseCoin, QuoteCoin: m.QuoteCoin,
		BaseScale: m.BaseScale, QuoteScale: m.QuoteScale,
		TickSize: m.TickSize, LotSize: m.LotSize,
		MakerFeeRate: m.MakerFee, TakerFeeRate: m.TakerFee,
		MinPrice: strPtr(m.MinPrice), MaxPrice: strPtr(m.MaxPrice),
		MinAmount: m.MinAmount, MaxAmount: strPtr(m.MaxAmount),
		MinTurnover: m.MinTurnover, MarketOrdersEnabled: m.MarketOrders,
		Enabled: m.Enabled, Visible: m.Visible, Sort: m.Sort,
		Zone: m.Zone, Featured: m.Featured,
	}
}

func (r *AdminRPC) listMarkets(w http.ResponseWriter, req *http.Request) {
	cfgs, err := LoadMarkets(req.Context(), r.DB)
	if err != nil {
		writeMarketErr(w, r.Log, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]marketView, 0, len(cfgs))
	for _, cfg := range cfgs {
		out = append(out, viewOf(cfg))
	}
	slices.SortFunc(out, func(a, b marketView) int { return strings.Compare(a.Market, b.Market) })
	writeMarketsJSON(w, out)
}

// optString distinguishes "field absent" (Set=false — keep the stored
// value) from an explicit JSON null (Set=true, Value=nil — clear the
// collar) and a value (Set=true, Value set).
type optString struct {
	Set   bool
	Value *string
}

func (o *optString) UnmarshalJSON(b []byte) error {
	o.Set = true
	if string(b) == "null" {
		return nil
	}
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	o.Value = &s
	return nil
}

// marketUpdate is the editable subset of a Market row. Base/quote Coin
// are immutable (a live Book cannot change denomination); everything
// else an Employee may change without a deploy.
type marketUpdate struct {
	BaseScale           *int      `json:"baseScale"`
	QuoteScale          *int      `json:"quoteScale"`
	TickSize            *string   `json:"tickSize"`
	LotSize             *string   `json:"lotSize"`
	MakerFeeRate        *string   `json:"makerFeeRate"`
	TakerFeeRate        *string   `json:"takerFeeRate"`
	MinPrice            optString `json:"minPrice"`
	MaxPrice            optString `json:"maxPrice"`
	MinAmount           *string   `json:"minAmount"`
	MaxAmount           optString `json:"maxAmount"`
	MinTurnover         *string   `json:"minTurnover"`
	MarketOrdersEnabled *bool     `json:"marketOrdersEnabled"`
	Enabled             *bool     `json:"enabled"`
	Visible             *bool     `json:"visible"`
	Sort                *int      `json:"sort"`
	Zone                *string   `json:"zone"`
	Featured            *bool     `json:"featured"`
}

var (
	errUnknownMarket = errors.New("unknown market")
	errBadMarketCfg  = errors.New("bad market config")
	errFrozenMarket  = errors.New("market config frozen while orders rest")

	decimalOne = money.MustParse("1")
)

func (r *AdminRPC) putMarket(w http.ResponseWriter, req *http.Request) {
	market := req.PathValue("market")
	var in marketUpdate
	if err := json.NewDecoder(http.MaxBytesReader(w, req.Body, 1<<16)).Decode(&in); err != nil {
		writeMarketErr(w, r.Log, http.StatusBadRequest, "The request body is not valid JSON.")
		return
	}
	actor := req.Header.Get("X-Mal-Actor")

	var updated marketView
	err := pgx.BeginFunc(req.Context(), r.DB, func(tx pgx.Tx) error {
		cfg, err := loadMarketForUpdate(req.Context(), tx, market)
		if err != nil {
			return err
		}
		next, err := cfg.applyUpdate(in)
		if err != nil {
			return err
		}
		// The freeze guard, at write time: a quantization move with
		// resting Orders is refused here so the table never diverges
		// from what the engine would apply.
		if next.quant() != cfg.quant() {
			open, err := openOrderCount(req.Context(), tx, market)
			if err != nil {
				return err
			}
			if open > 0 {
				return fmt.Errorf("%w: %s has %d open orders", errFrozenMarket, market, open)
			}
		}
		if err := saveMarket(req.Context(), tx, next); err != nil {
			return err
		}
		updated = viewOf(next)
		return nil
	})
	switch {
	case err == nil:
	case errors.Is(err, errUnknownMarket):
		writeMarketErr(w, r.Log, http.StatusNotFound, fmt.Sprintf("Unknown Market %q.", market))
		return
	case errors.Is(err, errFrozenMarket):
		r.logger().Warn("market config edit refused", "market", market, "actor", actor, "err", err)
		writeMarketErr(w, r.Log, http.StatusConflict, err.Error())
		return
	case errors.Is(err, errBadMarketCfg):
		writeMarketErr(w, r.Log, http.StatusBadRequest, err.Error())
		return
	default:
		writeMarketErr(w, r.Log, http.StatusInternalServerError, err.Error())
		return
	}
	r.logger().Info("market config edited", "market", market, "actor", actor)
	writeMarketsJSON(w, updated)
}

func writeMarketsJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func writeMarketErr(w http.ResponseWriter, log *slog.Logger, status int, message string) {
	if log != nil && status >= http.StatusInternalServerError {
		log.Error("market config rpc", "status", status, "err", message)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": message})
}

const marketCols = `symbol, base_coin, quote_coin, base_scale, quote_scale,
	tick_size::text, lot_size::text,
	maker_fee_rate::text, taker_fee_rate::text,
	COALESCE(min_price::text,''), COALESCE(max_price::text,''),
	min_amount::text, COALESCE(max_amount::text,''), min_turnover::text,
	market_orders_enabled, enabled, visible, sort, zone, featured`

// loadMarketForUpdate locks one Market row and returns it resolved the
// same way LoadMarkets resolves rows (quantization raws included), so
// the edit math and the engine's apply math cannot disagree.
func loadMarketForUpdate(ctx context.Context, tx pgx.Tx, market string) (MarketCfg, error) {
	var m MarketCfg
	// Waiver: the WHERE clause keeps the spec-#21 storage column name
	// `symbol`; the Go field and the wire/glossary name are Market.
	err := tx.QueryRow(ctx, `SELECT `+marketCols+` FROM markets WHERE symbol = $1 FOR UPDATE`, market).
		Scan(&m.Market, &m.BaseCoin, &m.QuoteCoin, &m.BaseScale, &m.QuoteScale,
			&m.TickSize, &m.LotSize, &m.MakerFee, &m.TakerFee,
			&m.MinPrice, &m.MaxPrice, &m.MinAmount, &m.MaxAmount, &m.MinTurnover,
			&m.MarketOrders, &m.Enabled, &m.Visible, &m.Sort, &m.Zone, &m.Featured)
	if errors.Is(err, pgx.ErrNoRows) {
		return m, fmt.Errorf("%w: %s", errUnknownMarket, market)
	}
	if err != nil {
		return m, err
	}
	tick, err := rawAtScale(m.TickSize, m.QuoteScale)
	if err != nil || tick <= 0 {
		return m, fmt.Errorf("%w: %s: bad tick_size %q", errBadMarketCfg, market, m.TickSize)
	}
	lot, err := rawAtScale(m.LotSize, m.BaseScale)
	if err != nil || lot <= 0 {
		return m, fmt.Errorf("%w: %s: bad lot_size %q", errBadMarketCfg, market, m.LotSize)
	}
	m.TickRaw, m.LotRaw = tick, lot
	return m, nil
}

func openOrderCount(ctx context.Context, tx pgx.Tx, market string) (int, error) {
	var n int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM orders WHERE market = $1 AND status = 'open'`, market).Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}

// applyUpdate folds the wire edit onto the stored row and validates the
// result as a whole: every decimal parses with the money contract, fees
// stay in [0,1) at 4 dp, collars stay ordered, and quantization fields
// resolve exactly at the (possibly new) scales. Invalid input never
// reaches the table.
func (m MarketCfg) applyUpdate(in marketUpdate) (MarketCfg, error) {
	next := m
	var err error

	if in.BaseScale != nil {
		if *in.BaseScale < 0 || *in.BaseScale > 18 {
			return next, fmt.Errorf("%w: baseScale must be between 0 and 18", errBadMarketCfg)
		}
		next.BaseScale = *in.BaseScale
	}
	if in.QuoteScale != nil {
		if *in.QuoteScale < 0 || *in.QuoteScale > 18 {
			return next, fmt.Errorf("%w: quoteScale must be between 0 and 18", errBadMarketCfg)
		}
		next.QuoteScale = *in.QuoteScale
	}
	if in.TickSize != nil {
		if next.TickSize, err = decimalField(*in.TickSize, "tickSize", true); err != nil {
			return next, err
		}
	}
	if in.LotSize != nil {
		if next.LotSize, err = decimalField(*in.LotSize, "lotSize", true); err != nil {
			return next, err
		}
	}
	if in.MakerFeeRate != nil {
		if next.MakerFee, err = feeField(*in.MakerFeeRate, "makerFeeRate"); err != nil {
			return next, err
		}
	}
	if in.TakerFeeRate != nil {
		if next.TakerFee, err = feeField(*in.TakerFeeRate, "takerFeeRate"); err != nil {
			return next, err
		}
	}
	if in.MinPrice.Set {
		if next.MinPrice, err = collarField(in.MinPrice.Value, "minPrice"); err != nil {
			return next, err
		}
	}
	if in.MaxPrice.Set {
		if next.MaxPrice, err = collarField(in.MaxPrice.Value, "maxPrice"); err != nil {
			return next, err
		}
	}
	if in.MinAmount != nil {
		if next.MinAmount, err = decimalField(*in.MinAmount, "minAmount", false); err != nil {
			return next, err
		}
	}
	if in.MaxAmount.Set {
		if next.MaxAmount, err = collarField(in.MaxAmount.Value, "maxAmount"); err != nil {
			return next, err
		}
	}
	if in.MinTurnover != nil {
		if next.MinTurnover, err = decimalField(*in.MinTurnover, "minTurnover", false); err != nil {
			return next, err
		}
	}
	if in.MarketOrdersEnabled != nil {
		next.MarketOrders = *in.MarketOrdersEnabled
	}
	if in.Enabled != nil {
		next.Enabled = *in.Enabled
	}
	if in.Visible != nil {
		next.Visible = *in.Visible
	}
	if in.Sort != nil {
		if *in.Sort < 0 {
			return next, fmt.Errorf("%w: sort must be a non-negative integer", errBadMarketCfg)
		}
		next.Sort = *in.Sort
	}
	if in.Zone != nil {
		zone := strings.TrimSpace(*in.Zone)
		if zone == "" || len(zone) > 64 {
			return next, fmt.Errorf("%w: zone must be 1-64 characters", errBadMarketCfg)
		}
		next.Zone = zone
	}
	if in.Featured != nil {
		next.Featured = *in.Featured
	}

	// Whole-row validity, on the merged result.
	tick, err := rawAtScale(next.TickSize, next.QuoteScale)
	if err != nil || tick <= 0 {
		return next, fmt.Errorf("%w: tickSize %q is not a positive decimal at quote scale %d", errBadMarketCfg, next.TickSize, next.QuoteScale)
	}
	lot, err := rawAtScale(next.LotSize, next.BaseScale)
	if err != nil || lot <= 0 {
		return next, fmt.Errorf("%w: lotSize %q is not a positive decimal at base scale %d", errBadMarketCfg, next.LotSize, next.BaseScale)
	}
	if err := orderedCollars(next.MinPrice, next.MaxPrice, "price"); err != nil {
		return next, err
	}
	if err := orderedCollars(next.MinAmount, next.MaxAmount, "amount"); err != nil {
		return next, err
	}
	next.TickRaw, next.LotRaw = tick, lot
	return next, nil
}

func decimalField(s, name string, positive bool) (string, error) {
	s = strings.TrimSpace(s)
	d, err := money.Parse(s)
	if err != nil {
		return "", fmt.Errorf("%w: %s must be a decimal string", errBadMarketCfg, name)
	}
	if positive && d.Sign() <= 0 {
		return "", fmt.Errorf("%w: %s must be positive", errBadMarketCfg, name)
	}
	if !positive && d.Sign() < 0 {
		return "", fmt.Errorf("%w: %s must not be negative", errBadMarketCfg, name)
	}
	return s, nil
}

// feeField pins the ADR-0009 shape: decimal, 0 ≤ rate < 1, at most 4 dp.
func feeField(s, name string) (string, error) {
	s = strings.TrimSpace(s)
	d, err := money.Parse(s)
	if err != nil {
		return "", fmt.Errorf("%w: %s must be a decimal string", errBadMarketCfg, name)
	}
	if d.Sign() < 0 || !d.LessThan(decimalOne) {
		return "", fmt.Errorf("%w: %s must be at least 0 and less than 1", errBadMarketCfg, name)
	}
	if !money.WithinScale(d, 4) {
		return "", fmt.Errorf("%w: %s carries more than 4 decimal places", errBadMarketCfg, name)
	}
	return s, nil
}

// collarField accepts a positive decimal or JSON null (unbounded).
func collarField(s *string, name string) (string, error) {
	if s == nil {
		return "", nil
	}
	return decimalField(*s, name, true)
}

func orderedCollars(minS, maxS, what string) error {
	if minS == "" || maxS == "" {
		return nil
	}
	lo, err := money.Parse(minS)
	if err != nil {
		return fmt.Errorf("%w: min %s must be a decimal string", errBadMarketCfg, what)
	}
	hi, err := money.Parse(maxS)
	if err != nil {
		return fmt.Errorf("%w: max %s must be a decimal string", errBadMarketCfg, what)
	}
	if lo.GreaterThan(hi) {
		return fmt.Errorf("%w: min %s exceeds max %s", errBadMarketCfg, what, what)
	}
	return nil
}

// saveMarket writes every editable column of the resolved row. The
// updated_at touch is the operator-visible "when did this change" —
// the Audit log carries who and what moved.
func saveMarket(ctx context.Context, tx pgx.Tx, m MarketCfg) error {
	_, err := tx.Exec(ctx, `
		UPDATE markets SET
			base_scale = $2, quote_scale = $3,
			tick_size = $4, lot_size = $5,
			maker_fee_rate = $6, taker_fee_rate = $7,
			min_price = NULLIF($8,'')::numeric, max_price = NULLIF($9,'')::numeric,
			min_amount = $10, max_amount = NULLIF($11,'')::numeric,
			min_turnover = $12, market_orders_enabled = $13,
			enabled = $14, visible = $15,
			sort = $16, zone = $17, featured = $18,
			updated_at = now()
		WHERE symbol = $1`,
		m.Market, m.BaseScale, m.QuoteScale,
		m.TickSize, m.LotSize,
		m.MakerFee, m.TakerFee,
		m.MinPrice, m.MaxPrice,
		m.MinAmount, m.MaxAmount,
		m.MinTurnover, m.MarketOrders,
		m.Enabled, m.Visible,
		m.Sort, m.Zone, m.Featured)
	return err
}
