package wallet

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"time"

	"github.com/shopspring/decimal"

	"github.com/syaikhipin/malxchange/internal/money"
	"github.com/syaikhipin/malxchange/internal/wallet/chain"
	"github.com/syaikhipin/malxchange/internal/wallet/chain/btc"
	"github.com/syaikhipin/malxchange/internal/wallet/chain/evm"
	"github.com/syaikhipin/malxchange/internal/wallet/chain/tron"
	"github.com/syaikhipin/malxchange/internal/wallet/keys"
)

// Senders bundles the family chain clients and the decrypted hot
// accounts. A nil client (or missing account) means the family is in
// watch-only mode: watching and crediting work, signing fails with a
// clear error that surfaces on the Wallet Ops page.
type Senders struct {
	UTXO map[string]*btc.RPC // "btc" and/or "ltc"
	EVM  *evm.Client
	TRON *tron.Client
	Acct map[string]*keys.Account // family → hot account
}

func (s *Senders) rpcFor(family string) (*btc.RPC, error) {
	if r, ok := s.UTXO[family]; ok && r != nil {
		return r, nil
	}
	return nil, fmt.Errorf("no %s chain client configured (watch-only mode)", family)
}

func (s *Senders) accountFor(family string) (*keys.Account, error) {
	if a, ok := s.Acct[family]; ok && a != nil {
		return a, nil
	}
	return nil, fmt.Errorf("no hot keystore account for %s — signing unavailable", family)
}

// RunExecutors drives the withdrawal, sweep, and hot-transfer state
// machines until the context ends (spec #22 story 23). Failures mark
// the record failed with error detail and are retried or refunded by
// an Employee; nothing auto-crawls back.
func RunExecutors(ctx context.Context, db Pool, s *Senders, log *slog.Logger, interval time.Duration) error {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		if err := execPass(ctx, db, s, log); err != nil {
			log.Error("executor pass failed", "err", err)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		}
	}
}

func execPass(ctx context.Context, db Pool, s *Senders, log *slog.Logger) error {
	if err := execWithdrawals(ctx, db, s, log); err != nil {
		return fmt.Errorf("withdrawals: %w", err)
	}
	if err := execHotTransfers(ctx, db, s, log); err != nil {
		return fmt.Errorf("hot transfers: %w", err)
	}
	if err := execSweeps(ctx, db, s, log); err != nil {
		return fmt.Errorf("sweeps: %w", err)
	}
	return nil
}

func execWithdrawals(ctx context.Context, db Pool, s *Senders, log *slog.Logger) error {
	// approved → signed (→ broadcast in the same pass; a crash between
	// the marks resumes from signed).
	list, err := TakeApprovedWithdrawals(ctx, db, "", 10)
	if err != nil {
		return err
	}
	for _, w := range list {
		coin, err := GetCoin(ctx, db, w.Coin)
		if err != nil {
			continue
		}
		txid, err := signAndBroadcast(ctx, db, s, coin, w.Address, w.Net)
		if err != nil {
			_ = MarkWithdrawalFailed(ctx, db, w.ID, truncErr(err))
			log.Error("withdrawal sign/broadcast failed", "id", w.ID, "err", err)
			continue
		}
		if err := MarkWithdrawalSigned(ctx, db, w.ID, txid); err != nil {
			return err
		}
		if err := MarkWithdrawalBroadcast(ctx, db, w.ID, txid); err != nil {
			return err
		}
		log.Info("withdrawal broadcast", "id", w.ID, "coin", w.Coin, "txid", txid)
	}
	// signed (crash recovery) → re-broadcast.
	stuck, err := takeWithdrawalsIn(ctx, db, WSigned)
	if err != nil {
		return err
	}
	for _, w := range stuck {
		coin, _ := GetCoin(ctx, db, w.Coin)
		txid, err := signAndBroadcast(ctx, db, s, coin, w.Address, w.Net)
		if err != nil {
			_ = MarkWithdrawalBroadcast(ctx, db, w.ID, "") // force error path below
			_ = failFrom(ctx, db, "withdrawals", w.ID, WSigned, truncErr(err))
			continue
		}
		_ = MarkWithdrawalBroadcast(ctx, db, w.ID, txid)
	}
	// broadcast → confirmed at the Coin's confirmation count.
	out, err := takeWithdrawalsIn(ctx, db, WBroadcast)
	if err != nil {
		return err
	}
	for _, w := range out {
		coin, err := GetCoin(ctx, db, w.Coin)
		if err != nil {
			continue
		}
		if err := confirmTx(ctx, db, s, coin, w.TxID, func() error {
			return MarkWithdrawalConfirmed(ctx, db, w.ID)
		}); err != nil {
			log.Error("withdrawal confirm failed", "id", w.ID, "err", err)
		}
	}
	return nil
}

func takeWithdrawalsIn(ctx context.Context, db DB, state string) ([]Withdrawal, error) {
	rows, err := db.Query(ctx, `
		SELECT id, coin, member_id, address, amount, fee, net, state,
		       COALESCE(txid,''), COALESCE(error,''), created_at, updated_at
		FROM withdrawals WHERE state = $1 ORDER BY created_at LIMIT 10`, state)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Withdrawal
	for rows.Next() {
		var w Withdrawal
		if err := rows.Scan(&w.ID, &w.Coin, &w.Member, &w.Address, &w.Amount, &w.Fee, &w.Net, &w.State,
			&w.TxID, &w.Error, &w.CreatedAt, &w.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

// signAndBroadcast signs amount to address with the hot keys of the
// Coin's family and broadcasts, returning the chain txid.
func signAndBroadcast(ctx context.Context, db Pool, s *Senders, coin Coin, address string, amount decimal.Decimal) (string, error) {
	switch coin.Family {
	case chain.BTC, chain.LTC:
		rpc, err := s.rpcFor(coin.Family)
		if err != nil {
			return "", err
		}
		acct, err := s.accountFor(coin.Family)
		if err != nil {
			return "", err
		}
		hot, err := acct.HotAddress()
		if err != nil {
			return "", err
		}
		rate, err := rpc.FeeRate(ctx)
		if err != nil {
			return "", err
		}
		// needed = amount + a generous fee allowance for input selection
		feeEst := decimal.NewFromInt(300 * 68 * rate).Shift(-8) // ~300 vB at rate, worst case
		rows, err := HotUtxos(ctx, db, coin.Name, hot, amount.Add(feeEst).String())
		if err != nil {
			return "", err
		}
		inputs := make([]btc.Utxo, 0, len(rows))
		sat := func(d decimal.Decimal) int64 { return d.Shift(8).IntPart() }
		for _, u := range rows {
			inputs = append(inputs, btc.Utxo{TxID: u.TxID, Vout: uint32(u.Vout), Satoshis: sat(money.MustParse(u.Amount)), Branch: 1, Idx: 0})
		}
		built, err := rpc.BuildSign(ctx, acct, inputs, []btc.Output{{Address: address, Satoshis: sat(amount)}}, rate, hot)
		if err != nil {
			return "", err
		}
		return rpc.Broadcast(ctx, built.RawHex)
	case chain.EVM:
		if s.EVM == nil {
			return "", errors.New("no evm chain client configured (watch-only mode)")
		}
		acct, err := s.accountFor(chain.EVM)
		if err != nil {
			return "", err
		}
		key, err := acct.KeyAt(1, 0)
		if err != nil {
			return "", err
		}
		if coin.Native {
			return s.EVM.TransferNative(ctx, key, address, amount)
		}
		return s.EVM.TransferToken(ctx, coin.Contract, key, address, amount, coin.ChainDecimals)
	case chain.TRON:
		if s.TRON == nil {
			return "", errors.New("no tron chain client configured (watch-only mode)")
		}
		acct, err := s.accountFor(chain.TRON)
		if err != nil {
			return "", err
		}
		key, err := acct.KeyAt(1, 0)
		if err != nil {
			return "", err
		}
		if coin.Native {
			return s.TRON.TRXTransfer(ctx, key, address, amount)
		}
		return s.TRON.TRC20Transfer(ctx, coin.Contract, key, address, amount, coin.ChainDecimals)
	}
	return "", fmt.Errorf("%w: %s", ErrUnknownFamily, coin.Family)
}

// ErrUnknownFamily is a Coin outside the four v1 chain families.
var ErrUnknownFamily = errors.New("unknown chain family")

// confirmTx checks the tx depth against the Coin's confirmation count
// and marks confirmed; an evicted tx (-1) fails the record.
func confirmTx(ctx context.Context, db Pool, s *Senders, coin Coin, txid string, mark func() error) error {
	if txid == "" {
		return errors.New("no txid to confirm")
	}
	depth, err := s.depthFor(ctx, coin, txid)
	if err != nil {
		return err
	}
	switch {
	case depth >= int64(coin.Confirmations):
		return mark()
	case depth < 0:
		return failEvicted(ctx, db, coin, txid)
	}
	return nil // still waiting
}

func (s *Senders) depthFor(ctx context.Context, coin Coin, txid string) (int64, error) {
	switch coin.Family {
	case chain.BTC, chain.LTC:
		rpc, err := s.rpcFor(coin.Family)
		if err != nil {
			return 0, err
		}
		return rpc.Depth(ctx, txid)
	case chain.EVM:
		if s.EVM == nil {
			return 0, errors.New("no evm client")
		}
		return s.EVM.Depth(ctx, txid)
	case chain.TRON:
		if s.TRON == nil {
			return 0, errors.New("no tron client")
		}
		return s.TRON.Depth(ctx, txid)
	}
	return 0, fmt.Errorf("%w: %s", ErrUnknownFamily, coin.Family)
}

// failEvicted records a chain eviction (reorg after broadcast) as a
// chain_error incident — the funds are stuck chain-side; manual
// adjudication via Wallet Ops.
func failEvicted(ctx context.Context, db Pool, coin Coin, txid string) error {
	_, err := db.Exec(ctx, `
		INSERT INTO incidents (id, coin, kind, detail)
		VALUES (gen_random_uuid(), $1, 'chain_error', $2)`,
		coin.Name, fmt.Sprintf("broadcast tx %s evicted by reorg after broadcast — adjudicate manually", txid))
	return err
}

func failFrom(ctx context.Context, db Pool, table, id, from, detail string) error {
	_, err := db.Exec(ctx, fmt.Sprintf(
		`UPDATE %s SET state='failed', error=$3, updated_at=now() WHERE id=$1 AND state=$2`, table),
		id, from, detail)
	return err
}

func truncErr(err error) string {
	s := err.Error()
	if len(s) > 500 {
		s = s[:500]
	}
	return s
}

// execHotTransfers moves excess hot funds to cold when an Employee
// triggers it (signing path identical to a withdrawal).
func execHotTransfers(ctx context.Context, db Pool, s *Senders, log *slog.Logger) error {
	rows, err := db.Query(ctx, `
		SELECT id, coin, amount, address FROM hot_transfers WHERE state = 'pending' LIMIT 10`)
	if err != nil {
		return err
	}
	defer rows.Close()
	type ht struct {
		id, coin, address string
		amount            decimal.Decimal
	}
	var pending []ht
	for rows.Next() {
		var h ht
		var amt string
		if err := rows.Scan(&h.id, &h.coin, &amt, &h.address); err != nil {
			return err
		}
		h.amount = money.MustParse(amt)
		pending = append(pending, h)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, h := range pending {
		coin, err := GetCoin(ctx, db, h.coin)
		if err != nil {
			continue
		}
		txid, err := signAndBroadcast(ctx, db, s, coin, h.address, h.amount)
		if err != nil {
			_ = MarkHotTransferFailed(ctx, db, h.id, HPending, truncErr(err))
			log.Error("hot transfer failed", "id", h.id, "err", err)
			continue
		}
		_ = MarkHotTransferSigned(ctx, db, h.id, txid)
		_ = MarkHotTransferBroadcast(ctx, db, h.id, txid)
	}
	out, err := db.Query(ctx, `
		SELECT h.id, h.coin, h.txid FROM hot_transfers h WHERE h.state = 'broadcast' LIMIT 10`)
	if err != nil {
		return err
	}
	defer out.Close()
	type bc struct{ id, coin, txid string }
	var waiting []bc
	for out.Next() {
		var b bc
		if err := out.Scan(&b.id, &b.coin, &b.txid); err != nil {
			return err
		}
		waiting = append(waiting, b)
	}
	for _, b := range waiting {
		coin, err := GetCoin(ctx, db, b.coin)
		if err != nil {
			continue
		}
		if err := confirmTx(ctx, db, s, coin, b.txid, func() error {
			return MarkHotTransferConfirmed(ctx, db, b.id)
		}); err != nil {
			log.Error("hot transfer confirm failed", "id", b.id, "err", err)
		}
	}
	return nil
}

// execSweeps detects threshold-exceeding deposit addresses and drives
// their consolidation into the hot wallet (fees absorbed; token
// families pre-fund chain fees from the hot wallet — ERC-20 gas,
// TRON's TRX burn buffer).
func execSweeps(ctx context.Context, db Pool, s *Senders, log *slog.Logger) error {
	coins, err := ListCoins(ctx, db)
	if err != nil {
		return err
	}
	for _, coin := range coins {
		if coin.SweepPaused || coin.SweepThreshold.IsZero() || !coin.SweepThreshold.IsPositive() {
			continue
		}
		last, err := LastSweepAt(ctx, db, coin.Name)
		if err != nil {
			return err
		}
		if time.Since(last) < time.Duration(coin.SweepMinSeconds)*time.Second {
			continue
		}
		if err := sweepCoin(ctx, db, s, coin, log); err != nil {
			log.Error("sweep pass failed", "coin", coin.Name, "err", err)
		}
	}
	return resumeSweeps(ctx, db, s, log)
}

func sweepCoin(ctx context.Context, db Pool, s *Senders, coin Coin, log *slog.Logger) error {
	addrs, err := depositAddressesOf(ctx, db, coin.Name)
	if err != nil {
		return err
	}
	for _, addr := range addrs {
		bal, err := addressCoinBalance(ctx, db, s, coin, addr)
		if err != nil {
			continue
		}
		if bal.LessThan(coin.SweepThreshold) {
			continue
		}
		if _, err := CreateSweep(ctx, db, coin.Name, addr, bal); err != nil {
			continue // in flight or raced
		}
		log.Info("sweep created", "coin", coin.Name, "address", addr, "amount", bal.String())
	}
	return nil
}

func depositAddressesOf(ctx context.Context, db DB, coin string) ([]string, error) {
	rows, err := db.Query(ctx, `SELECT address FROM deposit_addresses WHERE coin = $1`, coin)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var a string
		if err := rows.Scan(&a); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// addressCoinBalance is the watch-only Coin balance of one deposit
// address (UTXO families read the local index; evm/tron ask the
// chain). ok=false when the family client is missing.
func addressCoinBalance(ctx context.Context, db DB, s *Senders, coin Coin, addr string) (decimal.Decimal, error) {
	switch coin.Family {
	case chain.BTC, chain.LTC:
		rows, err := AddressUtxos(ctx, db, coin.Name, addr)
		if err != nil {
			return decimal.Zero, err
		}
		sum := decimal.Zero
		for _, u := range rows {
			sum = sum.Add(money.MustParse(u.Amount))
		}
		return sum, nil
	case chain.EVM:
		if s.EVM == nil {
			return decimal.Zero, nil
		}
		if coin.Native {
			return s.EVM.HotBalance(ctx, addr)
		}
		return s.EVM.TokenBalance(ctx, coin.Contract, addr)
	case chain.TRON:
		if s.TRON == nil {
			return decimal.Zero, nil
		}
		if coin.Native {
			return s.TRON.TRXBalance(ctx, addr)
		}
		return s.TRON.TRC20Balance(ctx, coin.Contract, addr)
	}
	return decimal.Zero, fmt.Errorf("%w: %s", ErrUnknownFamily, coin.Family)
}

// resumeSweeps advances in-flight sweeps (funding → signed →
// broadcast → confirmed).
func resumeSweeps(ctx context.Context, db Pool, s *Senders, log *slog.Logger) error {
	rows, err := db.Query(ctx, `
		SELECT id, coin, address, amount, fee, COALESCE(funding_txid,''),
		       COALESCE(txid,''), state FROM sweeps
		WHERE state IN ('pending','funding','signed','broadcast') LIMIT 50`)
	if err != nil {
		return err
	}
	defer rows.Close()
	type sw struct {
		id, coin, address, fundingTxid, txid, state string
		amount                                      decimal.Decimal
	}
	var live []sw
	for rows.Next() {
		var x sw
		var amount, fee string
		if err := rows.Scan(&x.id, &x.coin, &x.address, &amount, &fee, &x.fundingTxid, &x.txid, &x.state); err != nil {
			return err
		}
		x.amount = money.MustParse(amount)
		live = append(live, x)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, x := range live {
		coin, err := GetCoin(ctx, db, x.coin)
		if err != nil {
			continue
		}
		switch x.state {
		case SPending, SFunding:
			if err := driveSweep(ctx, db, s, coin, x.id, x.address, x.amount, x.state, log); err != nil {
				log.Error("sweep failed", "id", x.id, "err", err)
			}
		case SSigned:
			// crash recovery: re-sign + broadcast a fresh consolidation
			if err := driveSweep(ctx, db, s, coin, x.id, x.address, x.amount, SSigned, log); err != nil {
				log.Error("sweep resume failed", "id", x.id, "err", err)
			}
		case SBroadcast:
			if err := confirmTx(ctx, db, s, coin, x.txid, func() error {
				return MarkSweepConfirmed(ctx, db, x.id)
			}); err != nil {
				log.Error("sweep confirm failed", "id", x.id, "err", err)
			}
		}
	}
	return nil
}

// driveSweep performs the family-specific consolidation for one sweep.
func driveSweep(ctx context.Context, db Pool, s *Senders, coin Coin, id, address string, amount decimal.Decimal, from string, log *slog.Logger) error {
	switch coin.Family {
	case chain.BTC, chain.LTC:
		rpc, err := s.rpcFor(coin.Family)
		if err != nil {
			return failSweep(ctx, db, id, from, err)
		}
		acct, err := s.accountFor(coin.Family)
		if err != nil {
			return failSweep(ctx, db, id, from, err)
		}
		hot, err := acct.HotAddress()
		if err != nil {
			return failSweep(ctx, db, id, from, err)
		}
		idx, err := addressIndex(ctx, db, coin.Name, address)
		if err != nil {
			return failSweep(ctx, db, id, from, err)
		}
		rate, err := rpc.FeeRate(ctx)
		if err != nil {
			return failSweep(ctx, db, id, from, err)
		}
		rows, err := AddressUtxos(ctx, db, coin.Name, address)
		if err != nil {
			return failSweep(ctx, db, id, from, err)
		}
		inputs := make([]btc.Utxo, 0, len(rows))
		for _, u := range rows {
			inputs = append(inputs, btc.Utxo{
				TxID: u.TxID, Vout: uint32(u.Vout),
				Satoshis: money.MustParse(u.Amount).Shift(8).IntPart(),
				Branch:   0, Idx: uint32(idx),
			})
		}
		built, err := rpc.BuildSign(ctx, acct, inputs, []btc.Output{{Address: hot, Satoshis: amount.Shift(8).IntPart()}}, rate, hot)
		if err != nil {
			return failSweep(ctx, db, id, from, err)
		}
		if from == SSigned || from == SPending {
			// amount includes the whole balance; rebuild the output to
			// balance-minus-fee is handled inside BuildSign via change
			_ = built
		}
		txid, err := rpc.Broadcast(ctx, built.RawHex)
		if err != nil {
			return failSweep(ctx, db, id, from, err)
		}
		fee := decimal.NewFromInt(built.FeeSat).Shift(-8)
		if err := MarkSweepSigned(ctx, db, id, from, txid, fee); err != nil {
			return err
		}
		return MarkSweepBroadcast(ctx, db, id, txid)
	case chain.EVM, chain.TRON:
		acct, err := s.accountFor(coin.Family)
		if err != nil {
			return failSweep(ctx, db, id, from, err)
		}
		idx, err := addressIndex(ctx, db, coin.Name, address)
		if err != nil {
			return failSweep(ctx, db, id, from, err)
		}
		key, err := acct.KeyAt(0, uint32(idx))
		if err != nil {
			return failSweep(ctx, db, id, from, err)
		}
		hot, err := acct.HotAddress()
		if err != nil {
			return failSweep(ctx, db, id, from, err)
		}
		if coin.Native {
			// native sweep: send balance minus a fee allowance
			send := amount.Sub(coin.NativeSweepFeeAllowance())
			if !send.IsPositive() {
				return failSweep(ctx, db, id, from, errors.New("balance too small to sweep after fee"))
			}
			var txid string
			if coin.Family == chain.EVM {
				txid, err = s.EVM.TransferNative(ctx, key, hot, send)
			} else {
				txid, err = s.TRON.TRXTransfer(ctx, key, hot, send)
			}
			if err != nil {
				return failSweep(ctx, db, id, from, err)
			}
			if err := MarkSweepSigned(ctx, db, id, from, txid, decimal.Zero); err != nil {
				return err
			}
			return MarkSweepBroadcast(ctx, db, id, txid)
		}
		// token sweep: pre-fund chain fees from the hot wallet first
		if from == SPending {
			var fundingTxid string
			if coin.Family == chain.EVM {
				hotKey, err := acct.KeyAt(1, 0)
				if err != nil {
					return failSweep(ctx, db, id, from, err)
				}
				if err := s.EVM.EnsureGas(ctx, hotKey, address, big.NewInt(3_000_000_000_000_000)); err != nil { // ~0.003 ETH
					return failSweep(ctx, db, id, from, err)
				}
			} else {
				hotKey, err := acct.KeyAt(1, 0)
				if err != nil {
					return failSweep(ctx, db, id, from, err)
				}
				// TRX buffer burn (ADR-0008 v1)
				if _, err := s.TRON.TRXTransfer(ctx, hotKey, address, money.MustParse("30")); err != nil {
					return failSweep(ctx, db, id, from, err)
				}
			}
			_ = fundingTxid
			if err := MarkSweepFunding(ctx, db, id, fundingTxid); err != nil {
				return err
			}
			return nil // next pass moves the tokens once fees landed
		}
		var txid string
		if coin.Family == chain.EVM {
			txid, err = s.EVM.TransferToken(ctx, coin.Contract, key, hot, amount, coin.ChainDecimals)
		} else {
			txid, err = s.TRON.TRC20Transfer(ctx, coin.Contract, key, hot, amount, coin.ChainDecimals)
		}
		if err != nil {
			return failSweep(ctx, db, id, from, err)
		}
		if err := MarkSweepSigned(ctx, db, id, from, txid, decimal.Zero); err != nil {
			return err
		}
		return MarkSweepBroadcast(ctx, db, id, txid)
	}
	return failSweep(ctx, db, id, from, fmt.Errorf("%w: %s", ErrUnknownFamily, coin.Family))
}

func failSweep(ctx context.Context, db Pool, id, from string, err error) error {
	return MarkSweepFailed(ctx, db, id, from, truncErr(err))
}

func addressIndex(ctx context.Context, db DB, coin, addr string) (int, error) {
	var idx int
	err := db.QueryRow(ctx, `SELECT idx FROM deposit_addresses WHERE coin = $1 AND address = $2`, coin, addr).Scan(&idx)
	return idx, err
}

// NativeSweepFeeAllowance is the fee reserve subtracted from a native
// evm/tron sweep amount (the actual fee is burned on chain; UTXO
// families compute the exact fee from the signed transaction).
func (c Coin) NativeSweepFeeAllowance() decimal.Decimal {
	switch c.Family {
	case chain.EVM:
		return money.MustParse("0.001")
	case chain.TRON:
		return money.MustParse("1")
	}
	return decimal.Zero
}
