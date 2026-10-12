package wallet

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/shopspring/decimal"

	"github.com/syaikhipin/malxchange/internal/money"
)

// Reconciliation (spec #22 story 29): the Ledger equals the Journal
// fold (pure SQL), and the on-chain hot wallet reconciles against
// member balances plus fees. Chain-side balance access is injected at
// boot (watch-only, no keys needed); unknown components are reported
// rather than guessed.

// HotBalanceQuery returns the on-chain hot balance of a Coin as a
// decimal string, or ok=false when no chain client is configured for
// its family. Wired in run.go.
var HotBalanceQuery func(ctx context.Context, c Coin) (string, bool)

// HotBalanceOf adapts HotBalanceQuery with the zero-value default.
func HotBalanceOf(ctx context.Context, c Coin) (string, bool) {
	if HotBalanceQuery == nil {
		return "", false
	}
	return HotBalanceQuery(ctx, c)
}

// CoinRecon is one Coin's reconciliation row.
type CoinRecon struct {
	Coin          string `json:"coin"`
	LedgerSum     string `json:"ledgerSum"`     // member available + frozen
	FeesCollected string `json:"feesCollected"` // journal fee magnitude
	ColdOut       string `json:"coldOut"`       // hot→cold confirmed
	ColdIn        string `json:"coldIn"`        // recorded cold→hot
	InFlight      string `json:"inFlight"`      // withdrawals broadcast, unconfirmed
	ExpectedHot   string `json:"expectedHot"`
	ChainHot      string `json:"chainHot"` // "" when no chain client
	Unswept       string `json:"unswept"`  // deposit-side funds not yet consolidated
	Delta         string `json:"delta"`
	OK            bool   `json:"ok"`
}

// ReconcileCoin computes one Coin's hot-wallet reconciliation:
//
//	expected = ledger + fees + coldIn − coldOut − inFlight − unswept
//	delta    = expected − chainHot
//
// Fees collected stay chain-side in the hot wallet (they are internal
// accounting), so they reconcile positively; unswept deposits and
// in-flight (broadcast, unconfirmed) withdrawals explain the rest.
func ReconcileCoin(ctx context.Context, db DB, c Coin) (CoinRecon, error) {
	rc := CoinRecon{Coin: c.Name}
	one := func(sql string, args ...any) (decimal.Decimal, error) {
		var s string
		if err := db.QueryRow(ctx, sql, args...).Scan(&s); err != nil {
			return decimal.Zero, err
		}
		return money.MustParse(s), nil
	}
	str := func(sql string, args ...any) (string, error) {
		d, err := one(sql, args...)
		return d.String(), err
	}
	var err error
	if rc.LedgerSum, err = str(`SELECT COALESCE(SUM(available + frozen),0) FROM ledger_accounts WHERE coin = $1`, c.Name); err != nil {
		return rc, err
	}
	if rc.FeesCollected, err = str(`SELECT COALESCE(SUM(amount),0) FROM journal WHERE coin = $1 AND kind = 'fee'`, c.Name); err != nil {
		return rc, err
	}
	if rc.ColdOut, err = str(`SELECT COALESCE(SUM(amount),0) FROM hot_transfers WHERE coin = $1 AND state = 'confirmed'`, c.Name); err != nil {
		return rc, err
	}
	if rc.ColdIn, err = str(`SELECT COALESCE(SUM(amount),0) FROM cold_transfers WHERE coin = $1`, c.Name); err != nil {
		return rc, err
	}
	if rc.InFlight, err = str(`SELECT COALESCE(SUM(net),0) FROM withdrawals WHERE coin = $1 AND state = 'broadcast'`, c.Name); err != nil {
		return rc, err
	}
	// Deposit-side unswept funds: UTXO families read the local index;
	// token/native evm+tron coins need the chain (sum over allocations)
	// — report "" (unknown) when no client is wired.
	switch c.Family {
	case "btc", "ltc":
		if rc.Unswept, err = str(`
			SELECT COALESCE(SUM(u.amount),0) FROM utxos u
			JOIN deposit_addresses a ON a.coin = u.coin AND a.address = u.address
			WHERE u.coin = $1 AND NOT u.spent`, c.Name); err != nil {
			return rc, err
		}
	default:
		if HotBalanceQuery != nil {
			sum := decimal.Zero
			rows, err := db.Query(ctx, `SELECT address FROM deposit_addresses WHERE coin = $1`, c.Name)
			if err != nil {
				return rc, err
			}
			defer rows.Close()
			var addrs []string
			for rows.Next() {
				var a string
				if err := rows.Scan(&a); err != nil {
					return rc, err
				}
				addrs = append(addrs, a)
			}
			for _, a := range addrs {
				if bal, ok := AddressBalanceQuery(ctx, c, a); ok {
					sum = sum.Add(money.MustParse(bal))
				}
			}
			rc.Unswept = sum.String()
		}
	}
	expected := money.MustParse(rc.LedgerSum).
		Add(money.MustParse(rc.FeesCollected)).
		Add(money.MustParse(rc.ColdIn)).
		Sub(money.MustParse(rc.ColdOut)).
		Sub(money.MustParse(rc.InFlight)).
		Sub(money.MustParse(rc.Unswept))
	rc.ExpectedHot = expected.String()
	if h, ok := HotBalanceOf(ctx, c); ok {
		rc.ChainHot = h
		delta := expected.Sub(money.MustParse(h))
		rc.Delta = delta.String()
		rc.OK = delta.IsZero()
	} else {
		rc.OK = false // unknown, not broken
	}
	return rc, nil
}

// AddressBalanceQuery returns one deposit address's watch-only Coin
// balance (evm/tron chains); wired alongside HotBalanceQuery.
var AddressBalanceQuery func(ctx context.Context, c Coin, addr string) (string, bool)

// reconcile is the admin surface: fold invariant + per-Coin hot
// reconciliation.
func (r *RPC) reconcile(w http.ResponseWriter, req *http.Request) {
	ctx := req.Context()
	fold, err := VerifyFold(ctx, r.DB, "")
	if err != nil {
		writeErr(w, err)
		return
	}
	coins, err := ListCoins(ctx, r.DB)
	if err != nil {
		writeErr(w, err)
		return
	}
	recon := make([]CoinRecon, 0, len(coins))
	ok := true
	for _, f := range fold {
		ok = ok && f.OK
	}
	for _, c := range coins {
		rc, err := ReconcileCoin(ctx, r.DB, c)
		if err != nil {
			writeErr(w, fmt.Errorf("reconcile %s: %w", c.Name, err))
			return
		}
		ok = ok && rc.OK
		recon = append(recon, rc)
	}
	writeJSON(w, map[string]any{"ok": ok, "fold": fold, "coins": recon, "at": time.Now().UTC()})
}
