package wallet

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"
	"log/slog"

	"github.com/syaikhipin/malxchange/internal/money"
	"github.com/syaikhipin/malxchange/internal/wallet/keys"
)

// RPC is the wallet's internal surface: member-side seams the api
// service calls (freeze/release sync, balances, deposit addresses,
// withdrawals, journal — spec #22 stories 4/6/8/26) and admin-side
// seams admin-api fronts with RBAC + Step-up (stories 9-16). Auth is
// the compose network boundary: Caddy never proxies /internal.
type RPC struct {
	DB       *pgxpool.Pool
	Log      *slog.Logger
	WatchFor func(ctx context.Context, coin string) (*keys.Watch, error)
}

// RegisterRPC mounts every route on the service's internal mux. The
// boot harness serves that mux under /internal/ with the prefix
// stripped (internal/service), so patterns are prefix-free — like
// market's and exchange's registrations — and serve at
// /internal/freeze, /internal/release, /internal/accounts/{member}, …
func RegisterRPC(mux *http.ServeMux, r *RPC) {
	// member seams (api service)
	mux.HandleFunc("POST /freeze", r.freeze)
	mux.HandleFunc("POST /release", r.release)
	mux.HandleFunc("GET /accounts/{member}", r.accounts)
	mux.HandleFunc("POST /deposit-addresses", r.depositAddress)
	mux.HandleFunc("GET /deposits", r.deposits)
	mux.HandleFunc("POST /withdrawals", r.withdrawalRequest)
	mux.HandleFunc("GET /journal/{member}", r.journalEntries)

	// admin seams (admin-api front; it enforces permission + step-up)
	mux.HandleFunc("GET /withdrawals", r.listWithdrawals)
	mux.HandleFunc("GET /withdrawals/{id}", r.withdrawalDetail)
	mux.HandleFunc("POST /withdrawals/{id}/approve", r.withdrawalApprove)
	mux.HandleFunc("POST /withdrawals/{id}/reject", r.withdrawalReject)
	mux.HandleFunc("POST /withdrawals/{id}/retry", r.withdrawalRetry)
	mux.HandleFunc("POST /withdrawals/{id}/refund", r.withdrawalRefund)
	mux.HandleFunc("GET /sweeps", r.listSweeps)
	mux.HandleFunc("POST /sweeps/{id}/retry", r.sweepRetry)
	mux.HandleFunc("GET /coins", r.listCoins)
	mux.HandleFunc("GET /coins/{name}", r.getCoin)
	mux.HandleFunc("PUT /coins/{name}", r.putCoin)
	mux.HandleFunc("POST /coins/{name}/sweep-pause", r.sweepPause)
	mux.HandleFunc("GET /hot-transfers", r.listHotTransfers)
	mux.HandleFunc("POST /hot-transfers", r.hotTransfer)
	mux.HandleFunc("GET /cold-transfers", r.listColdTransfers)
	mux.HandleFunc("POST /cold-transfers", r.coldTransfer)
	mux.HandleFunc("POST /adjustments", r.adjustment)
	mux.HandleFunc("GET /incidents", r.listIncidents)
	mux.HandleFunc("POST /incidents/{id}/resolve", r.resolveIncident)
	mux.HandleFunc("GET /ops/summary", r.opsSummary)
	mux.HandleFunc("GET /reconcile", r.reconcile)
}

func writeErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrInsufficientFunds):
		w.WriteHeader(http.StatusConflict)
	case errors.Is(err, ErrInvalidState), errors.Is(err, ErrBelowMinimum),
		errors.Is(err, ErrNotWithdrawable), errors.Is(err, ErrFeeExceeds),
		errors.Is(err, ErrUnknownCoin), errors.Is(err, ErrHoldNotFound):
		w.WriteHeader(http.StatusBadRequest)
	default:
		w.WriteHeader(http.StatusInternalServerError)
	}
	_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func decode[T any](r *http.Request) (T, error) {
	var v T
	err := json.NewDecoder(r.Body).Decode(&v)
	return v, err
}

func (r *RPC) freeze(w http.ResponseWriter, req *http.Request) {
	in, err := decode[struct {
		OrderID string `json:"orderId"`
		Member  string `json:"member"`
		Market  string `json:"market"`
		Side    string `json:"side"`
		Coin    string `json:"coin"`
		Amount  string `json:"amount"`
	}](req)
	if err != nil || in.OrderID == "" || in.Member == "" || in.Coin == "" || in.Amount == "" {
		http.Error(w, "need orderId, member, market, side, coin, amount", http.StatusBadRequest)
		return
	}
	amount, err := money.Parse(in.Amount)
	if err != nil {
		http.Error(w, "amount: "+err.Error(), http.StatusBadRequest)
		return
	}
	if err := Freeze(req.Context(), r.DB, in.OrderID, in.Member, in.Market, in.Side, in.Coin, amount); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, map[string]string{"ok": "true", "orderId": in.OrderID})
}

func (r *RPC) release(w http.ResponseWriter, req *http.Request) {
	in, err := decode[struct {
		OrderID string `json:"orderId"`
	}](req)
	if err != nil || in.OrderID == "" {
		http.Error(w, "need orderId", http.StatusBadRequest)
		return
	}
	if err := ReleaseOrder(req.Context(), r.DB, in.OrderID); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, map[string]string{"ok": "true"})
}

func (r *RPC) accounts(w http.ResponseWriter, req *http.Request) {
	member := req.PathValue("member")
	accts, err := Accounts(req.Context(), r.DB, member)
	if err != nil {
		writeErr(w, err)
		return
	}
	out := make([]map[string]string, 0, len(accts))
	for _, a := range accts {
		out = append(out, a.API())
	}
	writeJSON(w, out)
}

func (r *RPC) depositAddress(w http.ResponseWriter, req *http.Request) {
	in, err := decode[struct {
		Member string `json:"member"`
		Coin   string `json:"coin"`
	}](req)
	if err != nil || in.Member == "" || in.Coin == "" {
		http.Error(w, "need member and coin", http.StatusBadRequest)
		return
	}
	watch, err := r.WatchFor(req.Context(), in.Coin)
	if err != nil {
		writeErr(w, err)
		return
	}
	addr, err := AddressFor(req.Context(), r.DB, watch, in.Coin, in.Member)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, map[string]string{"coin": in.Coin, "address": addr})
}

func (r *RPC) deposits(w http.ResponseWriter, req *http.Request) {
	member := req.URL.Query().Get("member")
	if member == "" {
		http.Error(w, "need member", http.StatusBadRequest)
		return
	}
	list, err := ListMemberDeposits(req.Context(), r.DB, member)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, list)
}

func (r *RPC) withdrawalRequest(w http.ResponseWriter, req *http.Request) {
	in, err := decode[struct {
		Member  string `json:"member"`
		Coin    string `json:"coin"`
		Address string `json:"address"`
		Amount  string `json:"amount"`
	}](req)
	if err != nil || in.Member == "" || in.Coin == "" || in.Address == "" || in.Amount == "" {
		http.Error(w, "need member, coin, address, amount", http.StatusBadRequest)
		return
	}
	amount, err := money.Parse(in.Amount)
	if err != nil {
		http.Error(w, "amount: "+err.Error(), http.StatusBadRequest)
		return
	}
	coin, err := GetCoin(req.Context(), r.DB, in.Coin)
	if err != nil {
		writeErr(w, fmt.Errorf("%w: %s", ErrUnknownCoin, in.Coin))
		return
	}
	wd, err := RequestWithdrawal(req.Context(), r.DB, coin, in.Member, in.Address, amount)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, map[string]string{
		"id": wd.ID, "coin": wd.Coin, "amount": money.Format(wd.Amount, coin.Scale),
		"fee": money.Format(wd.Fee, coin.Scale), "net": money.Format(wd.Net, coin.Scale),
		"state": wd.State,
	})
}

func (r *RPC) listWithdrawals(w http.ResponseWriter, req *http.Request) {
	limit, _ := strconv.Atoi(req.URL.Query().Get("limit"))
	list, err := ListWithdrawals(req.Context(), r.DB,
		req.URL.Query().Get("member"), req.URL.Query().Get("state"), limit)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, list)
}

// withdrawalDetail is the audit queue's drill-down seam (spec #59):
// one full withdrawal record; anything unresolvable — unknown id or a
// malformed one — answers 404 (the queue only ever links real ids).
func (r *RPC) withdrawalDetail(w http.ResponseWriter, req *http.Request) {
	v, err := LoadWithdrawalDetail(req.Context(), r.DB, req.PathValue("id"))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) || errors.Is(err, errMalformedID) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "no such withdrawal"})
			return
		}
		writeErr(w, err)
		return
	}
	writeJSON(w, v)
}

func (r *RPC) withdrawalApprove(w http.ResponseWriter, req *http.Request) {
	if err := AuditWithdrawal(req.Context(), r.DB, req.PathValue("id"), true, req.Header.Get("X-Mal-Actor")); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, map[string]string{"ok": "true"})
}

func (r *RPC) withdrawalReject(w http.ResponseWriter, req *http.Request) {
	if err := AuditWithdrawal(req.Context(), r.DB, req.PathValue("id"), false, req.Header.Get("X-Mal-Actor")); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, map[string]string{"ok": "true"})
}

func (r *RPC) withdrawalRetry(w http.ResponseWriter, req *http.Request) {
	if err := RetryWithdrawal(req.Context(), r.DB, req.PathValue("id")); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, map[string]string{"ok": "true"})
}

func (r *RPC) withdrawalRefund(w http.ResponseWriter, req *http.Request) {
	if err := RefundWithdrawal(req.Context(), r.DB, req.PathValue("id"), req.Header.Get("X-Mal-Actor")); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, map[string]string{"ok": "true"})
}

func (r *RPC) listSweeps(w http.ResponseWriter, req *http.Request) {
	limit, _ := strconv.Atoi(req.URL.Query().Get("limit"))
	list, err := ListSweeps(req.Context(), r.DB,
		req.URL.Query().Get("coin"), req.URL.Query().Get("state"), limit)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, list)
}

func (r *RPC) sweepRetry(w http.ResponseWriter, req *http.Request) {
	if err := RetrySweep(req.Context(), r.DB, req.PathValue("id")); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, map[string]string{"ok": "true"})
}

func (r *RPC) listCoins(w http.ResponseWriter, req *http.Request) {
	coins, err := ListCoins(req.Context(), r.DB)
	if err != nil {
		writeErr(w, err)
		return
	}
	out := make([]map[string]any, 0, len(coins))
	for _, c := range coins {
		out = append(out, map[string]any{
			"name": c.Name, "family": c.Family, "native": c.Native,
			"contract": c.Contract, "scale": c.Scale,
			"chainDecimals": c.ChainDecimals, "confirmations": c.Confirmations,
			"withdrawFee": c.WithdrawFee.String(), "minWithdraw": c.MinWithdraw.String(),
			"withdrawable":   c.Withdrawable,
			"sweepThreshold": c.SweepThreshold.String(), "sweepMinSeconds": c.SweepMinSeconds,
			"sweepPaused": c.SweepPaused, "coldAddress": c.ColdAddress,
			"hotCeiling": c.HotCeiling.String(), "hotFloor": c.HotFloor.String(),
		})
	}
	writeJSON(w, out)
}

func (r *RPC) getCoin(w http.ResponseWriter, req *http.Request) {
	c, err := GetCoin(req.Context(), r.DB, req.PathValue("name"))
	if err != nil {
		writeErr(w, fmt.Errorf("%w: %s", ErrUnknownCoin, req.PathValue("name")))
		return
	}
	writeJSON(w, map[string]any{
		"name": c.Name, "family": c.Family, "scale": c.Scale,
		"confirmations": c.Confirmations, "withdrawFee": c.WithdrawFee.String(),
		"minWithdraw": c.MinWithdraw.String(), "withdrawable": c.Withdrawable,
		"sweepThreshold": c.SweepThreshold.String(), "sweepMinSeconds": c.SweepMinSeconds,
		"sweepPaused": c.SweepPaused, "coldAddress": c.ColdAddress,
		"hotCeiling": c.HotCeiling.String(), "hotFloor": c.HotFloor.String(),
	})
}

func (r *RPC) putCoin(w http.ResponseWriter, req *http.Request) {
	in, err := decode[CoinUpdateWire](req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	up, err := in.update()
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	c, err := UpdateCoin(req.Context(), r.DB, req.PathValue("name"), up)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, map[string]string{"name": c.Name, "updated": "true"})
}

// CoinUpdateWire is the admin-editable Coin config (decimal strings).
type CoinUpdateWire struct {
	Scale           *int32  `json:"scale"`
	Confirmations   *int    `json:"confirmations"`
	WithdrawFee     *string `json:"withdrawFee"`
	MinWithdraw     *string `json:"minWithdraw"`
	Withdrawable    *bool   `json:"withdrawable"`
	SweepThreshold  *string `json:"sweepThreshold"`
	SweepMinSeconds *int    `json:"sweepMinSeconds"`
	SweepPaused     *bool   `json:"sweepPaused"`
	ColdAddress     *string `json:"coldAddress"`
	HotCeiling      *string `json:"hotCeiling"`
	HotFloor        *string `json:"hotFloor"`
	Xpub            *string `json:"xpub"`
}

func (cw *CoinUpdateWire) update() (CoinUpdate, error) {
	out := CoinUpdate{
		Scale: cw.Scale, Confirmations: cw.Confirmations, Withdrawable: cw.Withdrawable,
		SweepMinSeconds: cw.SweepMinSeconds, SweepPaused: cw.SweepPaused, ColdAddress: cw.ColdAddress,
		Xpub: cw.Xpub,
	}
	for _, p := range []struct {
		s   *string
		dst **decimal.Decimal
		n   string
	}{
		{cw.WithdrawFee, &out.WithdrawFee, "withdrawFee"},
		{cw.MinWithdraw, &out.MinWithdraw, "minWithdraw"},
		{cw.SweepThreshold, &out.SweepThreshold, "sweepThreshold"},
		{cw.HotCeiling, &out.HotCeiling, "hotCeiling"},
		{cw.HotFloor, &out.HotFloor, "hotFloor"},
	} {
		if p.s == nil {
			continue
		}
		d, err := money.Parse(*p.s)
		if err != nil {
			return out, fmt.Errorf("%s: %w", p.n, err)
		}
		*p.dst = &d
	}
	return out, nil
}

func (r *RPC) sweepPause(w http.ResponseWriter, req *http.Request) {
	in, err := decode[struct {
		Paused *bool `json:"paused"`
	}](req)
	if err != nil || in.Paused == nil {
		http.Error(w, "need paused", http.StatusBadRequest)
		return
	}
	if err := SetSweepPaused(req.Context(), r.DB, req.PathValue("name"), *in.Paused); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, map[string]bool{"ok": true})
}

func (r *RPC) hotTransfer(w http.ResponseWriter, req *http.Request) {
	in, err := decode[struct {
		Coin   string `json:"coin"`
		Amount string `json:"amount"`
	}](req)
	if err != nil || in.Coin == "" || in.Amount == "" {
		http.Error(w, "need coin and amount", http.StatusBadRequest)
		return
	}
	amount, err := money.Parse(in.Amount)
	if err != nil {
		http.Error(w, "amount: "+err.Error(), http.StatusBadRequest)
		return
	}
	coin, err := GetCoin(req.Context(), r.DB, in.Coin)
	if err != nil {
		writeErr(w, fmt.Errorf("%w: %s", ErrUnknownCoin, in.Coin))
		return
	}
	h, err := CreateHotTransfer(req.Context(), r.DB, coin, amount, req.Header.Get("X-Mal-Actor"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, map[string]string{"id": h.ID, "coin": h.Coin, "state": h.State})
}

func (r *RPC) listHotTransfers(w http.ResponseWriter, req *http.Request) {
	list, err := ListHotTransfers(req.Context(), r.DB, req.URL.Query().Get("coin"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, list)
}

func (r *RPC) coldTransfer(w http.ResponseWriter, req *http.Request) {
	in, err := decode[struct {
		Coin   string `json:"coin"`
		Amount string `json:"amount"`
		TxID   string `json:"txid"`
	}](req)
	if err != nil || in.Coin == "" || in.Amount == "" || in.TxID == "" {
		http.Error(w, "need coin, amount, txid", http.StatusBadRequest)
		return
	}
	amount, err := money.Parse(in.Amount)
	if err != nil {
		http.Error(w, "amount: "+err.Error(), http.StatusBadRequest)
		return
	}
	if err := RecordColdTransfer(req.Context(), r.DB, in.Coin, amount, in.TxID, req.Header.Get("X-Mal-Actor")); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, map[string]bool{"ok": true})
}

func (r *RPC) listColdTransfers(w http.ResponseWriter, req *http.Request) {
	list, err := ListColdTransfers(req.Context(), r.DB, req.URL.Query().Get("coin"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, list)
}

// adjustment is the support-case path: a signed movement with a
// mandatory reason, landing as an auditable Journal entry (spec #22
// story 15) — never a SQL edit. The caller supplies the idempotency
// key (wallet convention: producers own their keys, every journal row
// carries a UNIQUE idem_key), so a retried HTTP call is a no-op
// instead of a second movement; a fresh key from the admin app marks
// each new submission. Replaying a key answers applied:false and
// moves nothing.
func (r *RPC) adjustment(w http.ResponseWriter, req *http.Request) {
	in, err := decode[struct {
		Member string `json:"member"`
		Coin   string `json:"coin"`
		Amount string `json:"amount"` // signed
		Reason string `json:"reason"`
		Key    string `json:"key"`
	}](req)
	if err != nil || in.Member == "" || in.Coin == "" || in.Amount == "" || in.Reason == "" || in.Key == "" {
		http.Error(w, "need member, coin, signed amount, reason, key", http.StatusBadRequest)
		return
	}
	amount, err := money.Parse(in.Amount)
	if err != nil {
		http.Error(w, "amount: "+err.Error(), http.StatusBadRequest)
		return
	}
	if amount.IsZero() {
		http.Error(w, "adjustment of zero", http.StatusBadRequest)
		return
	}
	err = Credit(req.Context(), r.DB, in.Member, in.Coin, KindAdjustment,
		"adjustment: "+in.Reason, "adjust:"+in.Key,
		amount, "", req.Header.Get("X-Mal-Actor"))
	if errors.Is(err, ErrAlreadyApplied) {
		writeJSON(w, map[string]bool{"ok": true, "applied": false})
		return
	}
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, map[string]bool{"ok": true, "applied": true})
}

func (r *RPC) listIncidents(w http.ResponseWriter, req *http.Request) {
	rows, err := r.DB.Query(req.Context(), `
		SELECT id, coin, kind, detail, status, created_at FROM incidents
		WHERE ($1 = '' OR status = $1) ORDER BY created_at DESC LIMIT 200`,
		req.URL.Query().Get("status"))
	if err != nil {
		writeErr(w, err)
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, coin, kind, detail, status string
		var created []byte
		if err := rows.Scan(&id, &coin, &kind, &detail, &status, &created); err != nil {
			writeErr(w, err)
			return
		}
		out = append(out, map[string]any{
			"id": id, "coin": coin, "kind": kind, "detail": detail,
			"status": status, "createdAt": string(created),
		})
	}
	writeJSON(w, out)
}

func (r *RPC) resolveIncident(w http.ResponseWriter, req *http.Request) {
	_, err := r.DB.Exec(req.Context(), `
		UPDATE incidents SET status = 'resolved', resolved_at = now()
		WHERE id = $1 AND status = 'open'`, req.PathValue("id"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, map[string]bool{"ok": true})
}

// journalEntries exports a member's full history with exact decimals
// (spec #22 story 8): JSON by default, CSV for the transfer download.
func (r *RPC) journalEntries(w http.ResponseWriter, req *http.Request) {
	member := req.PathValue("member")
	q := req.URL.Query()
	rows, err := r.DB.Query(req.Context(), `
		SELECT coin, kind, amount, avail_delta, frozen_delta, reason,
		       COALESCE(ref_id,''), created_at
		FROM journal
		WHERE member_id = $1
		  AND ($2 = '' OR coin = $2)
		  AND created_at >= COALESCE(NULLIF($3,'')::timestamptz, to_timestamp(0))
		  AND created_at <= COALESCE(NULLIF($4,'')::timestamptz, now())
		ORDER BY created_at, id`, member, q.Get("coin"), q.Get("from"), q.Get("to"))
	if err != nil {
		writeErr(w, err)
		return
	}
	defer rows.Close()
	type line struct {
		Coin, Kind, Amount, AvailDelta, FrozenDelta, Reason, Ref, At string
	}
	var lines []line
	for rows.Next() {
		var l line
		var at time.Time
		if err := rows.Scan(&l.Coin, &l.Kind, &l.Amount, &l.AvailDelta, &l.FrozenDelta,
			&l.Reason, &l.Ref, &at); err != nil {
			writeErr(w, err)
			return
		}
		l.At = at.UTC().Format(PgTimestampText)
		lines = append(lines, l)
	}
	if q.Get("format") != "csv" {
		writeJSON(w, lines)
		return
	}
	w.Header().Set("Content-Type", "text/csv")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=transfer-%s.csv", member))
	cw := csv.NewWriter(w)
	_ = cw.Write([]string{"at", "coin", "kind", "amount", "availDelta", "frozenDelta", "reason", "ref"})
	for _, l := range lines {
		_ = cw.Write([]string{l.At, l.Coin, l.Kind, l.Amount, l.AvailDelta, l.FrozenDelta, l.Reason, l.Ref})
	}
	cw.Flush()
}

// opsSummary is the Wallet Ops page feed: alerts (ceiling/floor),
// failed operations with error detail, open incidents, sweep pauses
// (spec #22 stories 10/11/14).
func (r *RPC) opsSummary(w http.ResponseWriter, req *http.Request) {
	ctx := req.Context()
	coins, err := ListCoins(ctx, r.DB)
	if err != nil {
		writeErr(w, err)
		return
	}
	var failedW, failedS, failedH, openIncidents int
	for _, q := range []struct {
		sql  string
		dst  *int
		args []any
	}{
		{`SELECT count(*) FROM withdrawals WHERE state = 'failed'`, &failedW, nil},
		{`SELECT count(*) FROM sweeps WHERE state = 'failed'`, &failedS, nil},
		{`SELECT count(*) FROM hot_transfers WHERE state = 'failed'`, &failedH, nil},
		{`SELECT count(*) FROM incidents WHERE status = 'open'`, &openIncidents, nil},
	} {
		if err := r.DB.QueryRow(ctx, q.sql, q.args...).Scan(q.dst); err != nil {
			writeErr(w, err)
			return
		}
	}
	var paused []string
	hotBalances := map[string]string{}
	alerts := []map[string]any{}
	for _, c := range coins {
		if c.SweepPaused {
			paused = append(paused, c.Name)
		}
		if h, ok := HotBalanceOf(ctx, c); ok {
			hotBalances[c.Name] = h
			if !c.HotCeiling.IsZero() && c.HotCeiling.IsPositive() &&
				money.MustParse(h).GreaterThan(c.HotCeiling) {
				alerts = append(alerts, map[string]any{
					"coin": c.Name, "kind": "hot_ceiling",
					"detail": fmt.Sprintf("hot balance %s above ceiling %s — move excess to cold", h, c.HotCeiling.String()),
				})
			}
			if !c.HotFloor.IsZero() && c.HotFloor.IsPositive() &&
				money.MustParse(h).LessThan(c.HotFloor) {
				alerts = append(alerts, map[string]any{
					"coin": c.Name, "kind": "hot_floor",
					"detail": fmt.Sprintf("hot balance %s below floor %s — withdrawal liquidity at risk", h, c.HotFloor.String()),
				})
			}
		}
	}
	fold, err := VerifyFold(ctx, r.DB, "")
	if err != nil {
		writeErr(w, err)
		return
	}
	foldOK := true
	for _, f := range fold {
		foldOK = foldOK && f.OK
	}
	writeJSON(w, map[string]any{
		"alerts": alerts, "hotBalances": hotBalances,
		"failed":        map[string]int{"withdrawals": failedW, "sweeps": failedS, "hotTransfers": failedH},
		"openIncidents": openIncidents, "sweepsPaused": paused,
		"auditPending": map[string]int{},
		"foldOK":       foldOK,
		"at":           time.Now().UTC(),
	})
}

// fileModeOK reports path's mode bits (keystore permission check for
// run.go; test seam).
func fileModeOK(path string, want fs.FileMode) bool {
	st, err := os.Stat(path)
	return err == nil && st.Mode().Perm() == want
}
