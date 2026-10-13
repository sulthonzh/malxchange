// Member order surface (ticket #42): placing and cancelling Orders
// through the submit pipeline — session auth → decimal validation
// against the Market's tick size, lot size and scales → the wallet
// freeze RPC SYNCHRONOUSLY → order.command enqueued through the outbox
// in one transaction with api's order-intent row. No Order enters the
// engine without frozen funds (spec #24 story 29). Current and history
// lists are proxied from the exchange's internal RPC byte-for-byte, so
// api holds no order-table copy to drift (story 30).
//
// Crash consistency (documented choice): the freeze is a cross-service
// RPC and cannot join api's transaction, so the pipeline is freeze →
// BeginFunc(intent row + outbox enqueue). The window between the
// freeze and that commit fails safe — funds stay frozen and no
// order.command exists, so no Order ever trades unfunded; the orphaned
// hold is recoverable by an operator through the wallet release RPC
// (the orderId is recorded in wallet's own order_holds and Journal).
// The intent row and the outbox row commit atomically, so an accepted
// orderId always maps to exactly one place command. A crash after the
// commit but before the answer reaches the member can duplicate on
// retry: the retry is a new Order with its own freeze, which is the
// honest at-least-once outcome.
package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

	"github.com/syaikhipin/malxchange/internal/config"
	"github.com/syaikhipin/malxchange/internal/events"
	"github.com/syaikhipin/malxchange/internal/money"
	"github.com/syaikhipin/malxchange/internal/outbox"
)

// OrderConfig wires the order path's internal-RPC origins (the wallet
// pattern: service-local group, MAL_API_* names, deploy/env.example is
// the operator surface). Defaults are the compose service names, so a
// compose install needs none of these set.
type OrderConfig struct {
	WalletBaseURL   string        // wallet freeze RPC
	MarketBaseURL   string        // market config for validation (GET /markets behind the /internal mount)
	ExchangeBaseURL string        // exchange member-order RPC (GET /orders/* behind the /internal mount)
	MarketCacheTTL  time.Duration // how long a fetched market listing serves validation
}

// LoadOrderConfig reads the MAL_API_* order knobs. Listed in
// deploy/env.example.
func LoadOrderConfig() OrderConfig {
	return OrderConfig{
		WalletBaseURL:   config.EnvDefault("MAL_API_WALLET_BASE_URL", "http://wallet:8080/internal"),
		MarketBaseURL:   config.EnvDefault("MAL_API_MARKET_BASE_URL", "http://market:8080/internal"),
		ExchangeBaseURL: config.EnvDefault("MAL_API_EXCHANGE_BASE_URL", "http://exchange:8080/internal"),
		MarketCacheTTL:  envDuration("MAL_API_MARKET_CACHE_TTL", 5*time.Second),
	}
}

// orderAPI carries the collaborators of the member order routes.
type orderAPI struct {
	pool *pgxpool.Pool
	env  *APIEnv
	ob   *outbox.Outbox
	log  *slog.Logger
	cfg  OrderConfig
	hc   *http.Client
	mkts *marketCache
}

// newOrderAPI builds the order surface: one shared HTTP client for the
// internal RPCs and the market-config cache.
func newOrderAPI(pool *pgxpool.Pool, env *APIEnv, ob *outbox.Outbox, log *slog.Logger, cfg OrderConfig) *orderAPI {
	hc := &http.Client{Timeout: 10 * time.Second}
	return &orderAPI{
		pool: pool, env: env, ob: ob, log: log, cfg: cfg, hc: hc,
		mkts: &marketCache{base: cfg.MarketBaseURL, ttl: cfg.MarketCacheTTL, hc: hc},
	}
}

// RegisterOrders mounts the member order routes on the PUBLIC mux
// (Caddy's `handle /api/*` proxies without stripping). Place and cancel
// answer 202: the command is accepted for the engine, which owns
// execution — a placement the engine refuses surfaces as
// cancel.rejected on the member's channel, not as an error here.
func (a *orderAPI) RegisterOrders(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/uc/orders", withMemberSession(a.pool, a.env, a.handlePlace))
	mux.HandleFunc("GET /api/uc/orders/current", withMemberSession(a.pool, a.env, a.handleCurrent))
	mux.HandleFunc("GET /api/uc/orders/history", withMemberSession(a.pool, a.env, a.handleHistory))
	mux.HandleFunc("POST /api/uc/orders/{id}/cancel", withMemberSession(a.pool, a.env, a.handleCancel))
}

// ---- place ----

type placeOrderRequest struct {
	Market string `json:"market"`
	Side   string `json:"side"`   // "buy" | "sell"
	Kind   string `json:"kind"`   // "limit" | "market"
	Price  string `json:"price"`  // decimal string; limit only
	Amount string `json:"amount"` // decimal string; base amount, or the quote budget for a market buy
}

func (a *orderAPI) handlePlace(w http.ResponseWriter, r *http.Request, s Session) {
	var req placeOrderRequest
	if !decodeBody(w, r, &req) {
		return
	}
	if req.Market == "" {
		WriteError(w, http.StatusBadRequest, "Choose a market for this order.")
		return
	}
	if req.Side != "buy" && req.Side != "sell" {
		WriteError(w, http.StatusBadRequest, "The side must be buy or sell.")
		return
	}
	if req.Kind != "limit" && req.Kind != "market" {
		WriteError(w, http.StatusBadRequest, "The order kind must be limit or market.")
		return
	}
	mkt, ok := a.lookupMarket(w, r, req.Market)
	if !ok {
		return
	}
	price, amount, ok := validatePlaceAmounts(w, req, mkt)
	if !ok {
		return
	}

	// The freeze target (spec #24 story 16, wire notes §3.4): a sell
	// freezes the base amount; a buy freezes the quoted cost — price ×
	// amount for a limit (the exact product, never floored: fills can
	// only cost less), and the amount itself for a market buy, because
	// the amount IS the quote budget.
	coin, freezeAmt := mkt.QuoteCoin, amount
	if req.Side == "sell" {
		coin, freezeAmt = mkt.BaseCoin, amount
	} else if req.Kind == "limit" {
		freezeAmt = price.Mul(amount)
	}

	orderID := uuid.NewString()
	if err := a.freezeHold(r.Context(), orderID, s.MemberID, req.Market, req.Side, coin, freezeAmt.String()); err != nil {
		switch {
		case errors.Is(err, errInsufficientFunds):
			WriteError(w, http.StatusConflict, "You do not have enough available funds to place this order.")
		case errors.Is(err, errFundsUnavailable):
			a.log.Error("wallet freeze failed", "err", err)
			WriteError(w, http.StatusServiceUnavailable, "The funds service is unavailable. Try again in a moment.")
		default:
			writeErr(w, a.log, err)
		}
		return
	}

	cmd := events.OrderCommand{
		Op: "place", OrderID: orderID, Member: s.MemberID,
		Market: req.Market, Side: req.Side, Kind: req.Kind, Amount: req.Amount,
	}
	if req.Kind == "limit" {
		cmd.Price = req.Price
	}
	err := pgx.BeginFunc(r.Context(), a.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(r.Context(),
			`INSERT INTO order_intents (id, member_id, market, side, kind, price, amount, frozen_coin, frozen_amount)
			 VALUES ($1, $2, $3, $4, $5, NULLIF($6,''), $7, $8, $9)`,
			orderID, s.MemberID, req.Market, req.Side, req.Kind, req.Price,
			req.Amount, coin, freezeAmt.String()); err != nil {
			return fmt.Errorf("api: insert order intent: %w", err)
		}
		return a.enqueueCommand(r.Context(), tx, cmd)
	})
	if err != nil {
		writeErr(w, a.log, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"orderId": orderID})
}

// validatePlaceAmounts enforces the engine's exact quantization rules
// (internal/engine Book.quantize) fail-fast at the member seam: price
// positive, within the quote scale, a whole multiple of the tick size;
// amount positive, within the base scale, a whole multiple of the lot
// size — except a market buy, whose amount is the quote budget and only
// needs the quote scale. The strings members sent ride the command
// verbatim (trailing zeros and all), so validation parses but never
// rewrites them.
func validatePlaceAmounts(w http.ResponseWriter, req placeOrderRequest, mkt marketSummary) (price, amount decimal.Decimal, ok bool) {
	if req.Kind == "limit" {
		if req.Price == "" {
			WriteError(w, http.StatusBadRequest, "A limit order needs a price.")
			return decimal.Decimal{}, decimal.Decimal{}, false
		}
		parsedPrice, err := money.Parse(req.Price)
		price = parsedPrice
		if err != nil {
			WriteError(w, http.StatusBadRequest, "The price must be a decimal number like 50000.10.")
			return decimal.Decimal{}, decimal.Decimal{}, false
		}
		if !price.IsPositive() {
			WriteError(w, http.StatusBadRequest, "The price must be greater than zero.")
			return decimal.Decimal{}, decimal.Decimal{}, false
		}
		if !money.WithinScale(price, int32(mkt.QuoteScale)) {
			WriteError(w, http.StatusBadRequest,
				fmt.Sprintf("The price %s has more decimal places than the quote scale %d.", req.Price, mkt.QuoteScale))
			return decimal.Decimal{}, decimal.Decimal{}, false
		}
		if !money.Conforms(price, money.MustParse(mkt.TickSize)) {
			WriteError(w, http.StatusBadRequest,
				fmt.Sprintf("The price %s must be a multiple of the tick size %s.", req.Price, mkt.TickSize))
			return decimal.Decimal{}, decimal.Decimal{}, false
		}
	}
	amount, err := money.Parse(req.Amount)
	if err != nil {
		WriteError(w, http.StatusBadRequest, "The amount must be a decimal number like 0.0001.")
		return decimal.Decimal{}, decimal.Decimal{}, false
	}
	if !amount.IsPositive() {
		WriteError(w, http.StatusBadRequest, "The amount must be greater than zero.")
		return decimal.Decimal{}, decimal.Decimal{}, false
	}
	if req.Kind == "market" && req.Side == "buy" {
		if !money.WithinScale(amount, int32(mkt.QuoteScale)) {
			WriteError(w, http.StatusBadRequest,
				fmt.Sprintf("The budget %s has more decimal places than the quote scale %d.", req.Amount, mkt.QuoteScale))
			return decimal.Decimal{}, decimal.Decimal{}, false
		}
		return decimal.Decimal{}, amount, true
	}
	if !money.WithinScale(amount, int32(mkt.BaseScale)) {
		WriteError(w, http.StatusBadRequest,
			fmt.Sprintf("The amount %s has more decimal places than the base scale %d.", req.Amount, mkt.BaseScale))
		return decimal.Decimal{}, decimal.Decimal{}, false
	}
	if !money.Conforms(amount, money.MustParse(mkt.LotSize)) {
		WriteError(w, http.StatusBadRequest,
			fmt.Sprintf("The amount %s must be a multiple of the lot size %s.", req.Amount, mkt.LotSize))
		return decimal.Decimal{}, decimal.Decimal{}, false
	}
	return price, amount, true
}

// lookupMarket answers the Market's validation config or writes the
// named error (unknown market, or market info unavailable — validation
// without config would guess, so a fetch failure fails closed).
func (a *orderAPI) lookupMarket(w http.ResponseWriter, r *http.Request, name string) (marketSummary, bool) {
	mkt, err := a.mkts.market(r.Context(), name)
	switch {
	case errors.Is(err, errUnknownMarket):
		WriteError(w, http.StatusBadRequest, "This market is not available for trading.")
		return marketSummary{}, false
	case err != nil:
		a.log.Error("market listing fetch failed", "err", err)
		WriteError(w, http.StatusServiceUnavailable, "Market information is unavailable. Try again in a moment.")
		return marketSummary{}, false
	}
	return mkt, true
}

// ---- wallet freeze (synchronous, before any event) ----

var (
	// errInsufficientFunds maps wallet's 409 freeze answer.
	errInsufficientFunds = errors.New("insufficient funds")
	// errFundsUnavailable covers every other non-200 (transport, 5xx,
	// shape): the order is rejected and nothing is published.
	errFundsUnavailable = errors.New("funds service unavailable")
)

// freezeHold calls the wallet freeze RPC and waits for it: placement
// must fail fast on insufficient funds, before any command exists
// (holds.go contract). The freeze is idempotent per orderId.
func (a *orderAPI) freezeHold(ctx context.Context, orderID, member, market, side, coin, amount string) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	body, _ := json.Marshal(map[string]string{
		"orderId": orderID, "member": member, "market": market,
		"side": side, "coin": coin, "amount": amount,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		a.cfg.WalletBaseURL+"/freeze", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("freeze request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := a.hc.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %v", errFundsUnavailable, err)
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
		return nil
	case http.StatusConflict:
		return errInsufficientFunds
	default:
		detail, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("%w: status %d: %s", errFundsUnavailable, resp.StatusCode, detail)
	}
}

// ---- cancel ----

func (a *orderAPI) handleCancel(w http.ResponseWriter, r *http.Request, s Session) {
	id := r.PathValue("id")
	var market string
	err := a.pool.QueryRow(r.Context(),
		`SELECT market FROM order_intents WHERE id = $1 AND member_id = $2`, id, s.MemberID).Scan(&market)
	if errors.Is(err, pgx.ErrNoRows) {
		WriteError(w, http.StatusNotFound, "Order not found.")
		return
	}
	if err != nil {
		writeErr(w, a.log, err)
		return
	}
	cmd := events.OrderCommand{Op: "cancel", OrderID: id, Member: s.MemberID, Market: market}
	if err := pgx.BeginFunc(r.Context(), a.pool, func(tx pgx.Tx) error {
		return a.enqueueCommand(r.Context(), tx, cmd)
	}); err != nil {
		writeErr(w, a.log, err)
		return
	}
	// No release call here: the engine cancels the Order and publishes
	// order.completed, and wallet releases the hold remainder from that
	// event (settlement contract). If the engine never knew the Order,
	// cancel.rejected answers on the member's channel.
	writeJSON(w, http.StatusAccepted, map[string]string{"orderId": id})
}

// enqueueCommand writes one order.command envelope into the outbox
// inside the caller's transaction. Exactly one op per message; the
// envelope key is the Market symbol (events contract).
func (a *orderAPI) enqueueCommand(ctx context.Context, tx pgx.Tx, cmd events.OrderCommand) error {
	_, body, err := events.New(uuid.NewString(), cmd.Market, events.TopicOrderCommand, time.Now().UTC(), cmd)
	if err != nil {
		return fmt.Errorf("api: build order command: %w", err)
	}
	return a.ob.Enqueue(ctx, tx, events.TopicOrderCommand, cmd.Market, body)
}

// ---- current/history: the exchange proxy ----

// proxyOrders fronts the exchange service's member-order RPC (ticket
// #41) at the given path ("/orders/current" or
// "/orders/history"). api adds no drift: the upstream body streams
// through byte-for-byte with its status and content type, the member
// comes from the session (never the query string), and the member's
// paging parameters ride along verbatim after api's own bounds check.
// An unreachable or misbehaving upstream is one named 503 — order
// information is unavailable, never guessed at.
func (a *orderAPI) proxyOrders(w http.ResponseWriter, r *http.Request, path, member string, extra url.Values) {
	q := url.Values{"member": {member}}
	for k, vs := range extra {
		for _, v := range vs {
			q.Add(k, v)
		}
	}
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet,
		a.cfg.ExchangeBaseURL+path+"?"+q.Encode(), nil)
	if err != nil {
		writeErr(w, a.log, err)
		return
	}
	resp, err := a.hc.Do(req)
	if err != nil {
		a.log.Error("exchange orders rpc failed", "err", err)
		WriteError(w, http.StatusServiceUnavailable, "Order information is unavailable. Try again in a moment.")
		return
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		a.log.Error("exchange orders rpc read failed", "err", err)
		WriteError(w, http.StatusServiceUnavailable, "Order information is unavailable. Try again in a moment.")
		return
	}
	if resp.StatusCode != http.StatusOK {
		a.log.Error("exchange orders rpc answered non-200", "status", resp.StatusCode, "body", string(body))
		WriteError(w, http.StatusServiceUnavailable, "Order information is unavailable. Try again in a moment.")
		return
	}
	ct := resp.Header.Get("Content-Type")
	if ct == "" {
		ct = "application/json"
	}
	w.Header().Set("Content-Type", ct)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

func (a *orderAPI) handleCurrent(w http.ResponseWriter, r *http.Request, s Session) {
	a.proxyOrders(w, r, "/orders/current", s.MemberID, nil)
}

func (a *orderAPI) handleHistory(w http.ResponseWriter, r *http.Request, s Session) {
	q := r.URL.Query()
	// Exchange's own bounds, checked here so a member's bad query gets
	// api's named message instead of the internal error envelope.
	if raw := q.Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > 1000 {
			WriteError(w, http.StatusBadRequest, "The limit must be an integer between 1 and 1000.")
			return
		}
	}
	if raw := q.Get("offset"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 0 {
			WriteError(w, http.StatusBadRequest, "The offset must be a non-negative integer.")
			return
		}
	}
	a.proxyOrders(w, r, "/orders/history", s.MemberID, q)
}

// ---- market config cache ----

// marketSummary is one row of the market service's GET /markets (behind the /internal mount)
// listing (internal/market MarketSummary); the validation fields are
// tickSize, lotSize, baseScale, quoteScale.
type marketSummary struct {
	Market     string `json:"market"`
	BaseCoin   string `json:"baseCoin"`
	QuoteCoin  string `json:"quoteCoin"`
	Zone       string `json:"zone"`
	Sort       int    `json:"sort"`
	Featured   bool   `json:"featured"`
	BaseScale  int    `json:"baseScale"`
	QuoteScale int    `json:"quoteScale"`
	TickSize   string `json:"tickSize"`
	LotSize    string `json:"lotSize"`
}

var (
	errUnknownMarket         = errors.New("unknown market")
	errMarketInfoUnavailable = errors.New("market info unavailable")
)

// marketCache is a read-through cache of the market listing with a
// short TTL (MAL_API_MARKET_CACHE_TTL, default 5s) — the boring option:
// no boot-time snapshot to invalidate and no extra event subscription;
// tick/lot/scale changes take effect within the TTL, and the engine
// re-validates authoritatively anyway (a change that invalidates a
// submitted Order surfaces as cancel.rejected on the member's channel).
// An expired cache that cannot be refetched fails closed.
type marketCache struct {
	base string
	ttl  time.Duration
	hc   *http.Client

	mu     sync.Mutex
	at     time.Time
	byName map[string]marketSummary
}

func (c *marketCache) market(ctx context.Context, name string) (marketSummary, error) {
	c.mu.Lock()
	if c.byName != nil && time.Since(c.at) < c.ttl {
		m, ok := c.byName[name]
		c.mu.Unlock()
		if !ok {
			return marketSummary{}, errUnknownMarket
		}
		return m, nil
	}
	c.mu.Unlock()

	mkt, err := c.fetch(ctx, name)
	if err != nil {
		return marketSummary{}, err
	}
	return mkt, nil
}

// fetch reloads the listing outside the lock (a slow market service
// must not serialize placements) and stores the fresh copy.
func (c *marketCache) fetch(ctx context.Context, name string) (marketSummary, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+"/markets", nil)
	if err != nil {
		return marketSummary{}, fmt.Errorf("%w: %v", errMarketInfoUnavailable, err)
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return marketSummary{}, fmt.Errorf("%w: %v", errMarketInfoUnavailable, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return marketSummary{}, fmt.Errorf("%w: status %d", errMarketInfoUnavailable, resp.StatusCode)
	}
	var list []marketSummary
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		return marketSummary{}, fmt.Errorf("%w: decode: %v", errMarketInfoUnavailable, err)
	}
	byName := make(map[string]marketSummary, len(list))
	for _, m := range list {
		byName[m.Market] = m
	}
	c.mu.Lock()
	c.byName, c.at = byName, time.Now()
	c.mu.Unlock()
	m, ok := byName[name]
	if !ok {
		return marketSummary{}, errUnknownMarket
	}
	return m, nil
}
