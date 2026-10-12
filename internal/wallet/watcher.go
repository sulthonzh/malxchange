package wallet

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/syaikhipin/malxchange/internal/money"
	"github.com/syaikhipin/malxchange/internal/wallet/chain"
	"github.com/syaikhipin/malxchange/internal/wallet/keys"
)

// CoinWatcher drives one Coin's deposit watching (spec #22 stories
// 2/3/21/22/28): scan forward from the persisted cursor, refresh the
// live confirmation counter, credit at exactly the Coin's confirmation
// count, re-scan on height regression (reorg), and check the Coin's
// scale against the chain's actual decimals at the boundary.
type CoinWatcher struct {
	Coin Coin
	DB   Pool
	Ch   chain.Watcher
	Log  *slog.Logger

	watch  *keys.Watch // Coin's xpub (address decoding / hot address)
	hot    string      // hot address for UTXO indexing ("" on evm/tron)
	uptime bool        // first successful Height seen
}

// NewCoinWatcher arms a watcher; xpub may be empty (coin not yet
// ceremony-configured → watcher idles with a logged notice).
func NewCoinWatcher(ctx context.Context, db Pool, c Coin, ch chain.Watcher, log *slog.Logger) (*CoinWatcher, error) {
	cw := &CoinWatcher{Coin: c, DB: db, Ch: ch, Log: log}
	if c.XPub == "" {
		return cw, nil
	}
	w, err := keys.ImportXPub(c.Family, WatchNet(c), c.XPub)
	if err != nil {
		return nil, fmt.Errorf("coin %s xpub: %w", c.Name, err)
	}
	cw.watch = w
	if c.Family == chain.BTC || c.Family == chain.LTC {
		hot, err := w.HotAddress()
		if err != nil {
			return nil, err
		}
		cw.hot = hot
	}
	return cw, nil
}

// WatchNet picks the address network for a Coin: simnet in the
// compose test profile (MAL_WALLET_NET=simnet), mainnet otherwise.
func WatchNet(c Coin) string {
	if WalletNet() == keys.SimNet {
		return keys.SimNet
	}
	return keys.MainNet
}

// Tick runs one scan pass. Chain errors return an error (the loop
// backs off; a persistent failure raises a chain_error incident).
func (cw *CoinWatcher) Tick(ctx context.Context) error {
	c := cw.Coin
	tip, err := cw.Ch.Height(ctx)
	if err != nil {
		return fmt.Errorf("height: %w", err)
	}

	// Scale check (spec #22 story 28): config drift is caught at the
	// boundary, once per open incident.
	if err := cw.checkScale(ctx); err != nil {
		return err
	}

	cur, err := loadCursor(ctx, cw.DB, c.Name)
	if err != nil {
		return err
	}

	if tip < cur {
		// Height regression: a reorg replaced our tip. Rewind deep
		// enough to re-verify everything inside the confirmation window
		// and re-scan forward (pruned nodes cannot re-serve older
		// history — the cursor is ours, ADR-0008).
		rewind := tip - int64(2*c.Confirmations) + 1
		if rewind < 0 {
			rewind = 0
		}
		cw.Log.Warn("reorg detected: rescanning", "coin", c.Name, "cursor", cur, "tip", tip, "rewind_to", rewind)
		if err := cw.verifyWindow(ctx, rewind, cur); err != nil {
			return err
		}
		cur = rewind - 1
		if cur < -1 {
			cur = -1
		}
	}

	// Deposit-address map (plus the hot address on UTXO families, so
	// the spendable-output index stays complete; hot outputs never
	// become member deposits — the hot wallet holds exchange funds).
	deposits, err := WatchAddresses(ctx, cw.DB)
	if err != nil {
		return err
	}
	watchMap := make(map[string]string, len(deposits)+1)
	for addr, coin := range deposits {
		if coin == c.Name {
			watchMap[addr] = coin
		}
	}
	if cw.hot != "" {
		watchMap[cw.hot] = c.Name
	}

	for h := cur + 1; h <= tip; h++ {
		dl, spends, err := cw.Ch.ScanBlock(ctx, h, watchMap)
		if err != nil {
			return fmt.Errorf("scan block %d: %w", h, err)
		}
		for _, d := range dl {
			if cw.hot != "" && d.Address == cw.hot {
				if err := recordUtxo(ctx, cw.DB, c.Name, d); err != nil {
					return err
				}
				continue // hot-side funds are not member deposits
			}
			member, ok := memberFor(ctx, cw.DB, c.Name, d.Address)
			if !ok {
				continue // not one of ours (cannot happen; defensive)
			}
			amountStr := money.Format(d.Amount, c.Scale)
			if _, err := RecordDetected(ctx, cw.DB, Deposit{
				Coin: c.Name, Member: member, Address: d.Address,
				TxID: d.TxID, Vout: d.Vout, Amount: amountStr,
				Confs: int(tip - d.Height + 1),
			}, d.Height); err != nil {
				return err
			}
			// UTXO families: every deposit output is a spendable coin.
			if c.Family == chain.BTC || c.Family == chain.LTC {
				if err := recordUtxo(ctx, cw.DB, c.Name, d); err != nil {
					return err
				}
			}
		}
		for _, s := range spends {
			if err := spendUtxo(ctx, cw.DB, c.Name, s); err != nil {
				return err
			}
		}
	}

	if err := UpdateConfirmations(ctx, cw.DB, c.Name, tip); err != nil {
		return err
	}
	n, err := CreditDue(ctx, cw.DB, c.Name, c.Confirmations)
	if err != nil {
		return err
	}
	if n > 0 {
		cw.Log.Info("deposits credited", "coin", c.Name, "count", n)
	}
	return saveCursor(ctx, cw.DB, c.Name, tip)
}

// verifyWindow re-checks every deposit whose detection height falls in
// (from, to]: evicted detected rows are dropped (they may re-detect),
// evicted credited rows are invalidated and raise the admin incident
// for manual adjudication — never an automatic clawback (ADR-0008).
func (cw *CoinWatcher) verifyWindow(ctx context.Context, from, to int64) error {
	rows, err := cw.DB.Query(ctx, `
		SELECT txid, vout, status, detected_height FROM deposits
		WHERE coin = $1 AND detected_height > $2 AND detected_height <= $3`,
		cw.Coin.Name, from, to)
	if err != nil {
		return err
	}
	defer rows.Close()
	type row struct {
		txid   string
		vout   int32
		status string
		height int64
	}
	var check []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.txid, &r.vout, &r.status, &r.height); err != nil {
			return err
		}
		check = append(check, r)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, r := range check {
		ok, err := cw.Ch.TxExists(ctx, r.txid, r.height)
		if err != nil {
			return err
		}
		if ok {
			continue
		}
		switch r.status {
		case "credited":
			cw.Log.Error("deep reorg invalidated a credited deposit — incident raised",
				"coin", cw.Coin.Name, "txid", r.txid, "vout", r.vout)
			if err := InvalidateCredited(ctx, cw.DB, cw.Coin.Name, r.txid, r.vout); err != nil {
				return err
			}
		case "detected":
			if err := InvalidateDetected(ctx, cw.DB, cw.Coin.Name, r.txid, r.vout); err != nil {
				return err
			}
		}
	}
	return nil
}

// checkScale compares the Coin's declared scale with the chain's
// actual decimals (native or token contract) and records a
// scale_mismatch incident when they drift. Amounts stay exact either
// way — this is a config-drift alarm, not a money-safety gate.
func (cw *CoinWatcher) checkScale(ctx context.Context) error {
	if cw.Coin.ChainDecimals == 0 {
		return nil // already alarmed / unset
	}
	dec, err := cw.Ch.Decimals(ctx, cw.Coin.Contract)
	if err != nil {
		return fmt.Errorf("decimals: %w", err)
	}
	if dec == cw.Coin.ChainDecimals {
		return nil
	}
	var open int
	if err := cw.DB.QueryRow(ctx, `
		SELECT count(*) FROM incidents
		WHERE coin = $1 AND kind = 'scale_mismatch' AND status = 'open'`, cw.Coin.Name).Scan(&open); err != nil {
		return err
	}
	if open > 0 {
		return nil
	}
	cw.Log.Error("coin scale drift detected", "coin", cw.Coin.Name,
		"configured", cw.Coin.ChainDecimals, "chain", dec)
	_, err = cw.DB.Exec(ctx, `
		INSERT INTO incidents (id, coin, kind, detail)
		VALUES (gen_random_uuid(), $1, 'scale_mismatch', $2)`,
		cw.Coin.Name,
		fmt.Sprintf("coin chain_decimals %d != on-chain decimals %d — fix coin config", cw.Coin.ChainDecimals, dec))
	return err
}

func loadCursor(ctx context.Context, db DB, coin string) (int64, error) {
	var h int64
	err := db.QueryRow(ctx, `SELECT height FROM watch_cursors WHERE coin = $1`, coin).Scan(&h)
	if errors.Is(err, pgx.ErrNoRows) {
		return -1, nil
	}
	return h, err
}

func saveCursor(ctx context.Context, db DB, coin string, height int64) error {
	_, err := db.Exec(ctx, `
		INSERT INTO watch_cursors (coin, height) VALUES ($1, $2)
		ON CONFLICT (coin) DO UPDATE SET height = EXCLUDED.height, updated_at = now()`,
		coin, height)
	return err
}

func memberFor(ctx context.Context, db DB, coin, addr string) (string, bool) {
	var member string
	err := db.QueryRow(ctx, `SELECT member_id FROM deposit_addresses WHERE coin = $1 AND address = $2`,
		coin, addr).Scan(&member)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false
	}
	return member, err == nil
}

func recordUtxo(ctx context.Context, db DB, coin string, d chain.Deposit) error {
	_, err := db.Exec(ctx, `
		INSERT INTO utxos (coin, txid, vout, address, amount, height)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (coin, txid, vout) DO NOTHING`,
		coin, d.TxID, d.Vout, d.Address, d.Amount.String(), d.Height)
	return err
}

func spendUtxo(ctx context.Context, db DB, coin string, s chain.Spend) error {
	_, err := db.Exec(ctx, `
		UPDATE utxos SET spent = TRUE, spent_by = $3
		WHERE coin = $1 AND txid = $2 AND vout = $4 AND NOT spent`,
		coin, s.TxID, s.SpentBy, s.Vout)
	return err
}

// UtxoRow is one spendable output.
type UtxoRow struct {
	TxID    string
	Vout    int32
	Address string
	Amount  string // decimal string
	Height  int64
}

// HotUtxos selects unspent hot-address outputs for withdrawal/hot
// transfer signing, oldest first.
func HotUtxos(ctx context.Context, db DB, coin, hot string, minSum string) ([]UtxoRow, error) {
	rows, err := db.Query(ctx, `
		SELECT txid, vout, address, amount, height FROM utxos
		WHERE coin = $1 AND address = $2 AND NOT spent
		ORDER BY height, txid`, coin, hot)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []UtxoRow
	sum := money.MustParse("0")
	need := money.MustParse(minSum)
	for rows.Next() {
		var u UtxoRow
		if err := rows.Scan(&u.TxID, &u.Vout, &u.Address, &u.Amount, &u.Height); err != nil {
			return nil, err
		}
		out = append(out, u)
		sum = sum.Add(money.MustParse(u.Amount))
		if sum.GreaterThanOrEqual(need) {
			break
		}
	}
	if sum.LessThan(need) {
		return nil, fmt.Errorf("hot %s utxos %s < needed %s", coin, sum.String(), need)
	}
	return out, rows.Err()
}

// AddressUtxos selects one address's unspent outputs (sweeps).
func AddressUtxos(ctx context.Context, db DB, coin, addr string) ([]UtxoRow, error) {
	rows, err := db.Query(ctx, `
		SELECT txid, vout, address, amount, height FROM utxos
		WHERE coin = $1 AND address = $2 AND NOT spent
		ORDER BY height, txid`, coin, addr)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []UtxoRow
	for rows.Next() {
		var u UtxoRow
		if err := rows.Scan(&u.TxID, &u.Vout, &u.Address, &u.Amount, &u.Height); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// RunWatchers runs every Coin's watcher on a fixed cadence until the
// context ends. One coin failing backs off that coin only.
func RunWatchers(ctx context.Context, db Pool, mk func(Coin) (*CoinWatcher, error), log *slog.Logger, interval time.Duration) error {
	type run struct {
		w    *CoinWatcher
		next time.Time
		back time.Duration
	}
	var runs []*run
	refresh := func() error {
		coins, err := ListCoins(ctx, db)
		if err != nil {
			return err
		}
		seen := map[string]bool{}
		for _, r := range runs {
			seen[r.w.Coin.Name] = true
		}
		for _, c := range coins {
			if seen[c.Name] {
				continue
			}
			w, err := mk(c)
			if err != nil {
				log.Error("watcher setup failed", "coin", c.Name, "err", err)
				continue
			}
			runs = append(runs, &run{w: w})
		}
		return nil
	}
	if err := refresh(); err != nil {
		return err
	}
	t := time.NewTicker(10 * time.Second)
	defer t.Stop()
	for {
		for _, r := range runs {
			if time.Now().Before(r.next) {
				continue
			}
			if err := r.w.Tick(ctx); err != nil {
				r.back = nextBackoff(r.back)
				r.next = time.Now().Add(r.back)
				log.Error("watcher tick failed — backing off", "coin", r.w.Coin.Name, "err", err, "backoff", r.back)
			} else {
				r.back = 0
				r.next = time.Now().Add(interval)
			}
		}
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
			if err := refresh(); err != nil {
				return err
			}
		}
	}
}

func nextBackoff(cur time.Duration) time.Duration {
	if cur == 0 {
		return 2 * time.Second
	}
	if cur >= time.Minute {
		return time.Minute
	}
	return cur * 2
}
