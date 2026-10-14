package wallet

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"
)

// Sweeps consolidate deposit-address funds into the hot wallet
// (ADR-0008): automated above a per-Coin threshold with a minimum
// interval, pausable per Coin, fees absorbed by the exchange. Token
// families pre-fund chain fees from the hot wallet (ERC-20 gas,
// TRON TRX buffer) before moving the tokens.

// Sweep states.
const (
	SPending   = "pending"
	SFunding   = "funding"
	SSigned    = "signed"
	SBroadcast = "broadcast"
	SConfirmed = "confirmed"
	SFailed    = "failed"
)

var sweepNext = map[string]map[string]bool{
	SPending:   {SSigned: true, SFunding: true, SFailed: true},
	SFunding:   {SSigned: true, SFailed: true},
	SSigned:    {SBroadcast: true, SFailed: true},
	SBroadcast: {SConfirmed: true, SFailed: true},
	SFailed:    {SPending: true}, // retry
	SConfirmed: {},
}

// SweepTransition reports whether from → to is legal.
func SweepTransition(from, to string) bool { return sweepNext[from][to] }

// Sweep is one consolidation record.
type Sweep struct {
	ID          string
	Coin        string
	Address     string
	Amount      decimal.Decimal
	Fee         decimal.Decimal
	FundingTxID string
	TxID        string
	State       string
	Error       string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// CreateSweep opens a pending sweep (idempotent per address while one
// is in flight: an open sweep for the same address blocks a new one).
func CreateSweep(ctx context.Context, db Pool, coin, address string, amount decimal.Decimal) (Sweep, error) {
	id := uuid.NewString()
	s := Sweep{ID: id, Coin: coin, Address: address, Amount: amount, State: SPending}
	err := pgx.BeginFunc(ctx, db, func(tx pgx.Tx) error {
		var open int
		if err := tx.QueryRow(ctx, `
			SELECT count(*) FROM sweeps
			WHERE address = $1 AND state IN ('pending','funding','signed','broadcast')`,
			address).Scan(&open); err != nil {
			return err
		}
		if open > 0 {
			return fmt.Errorf("sweep already in flight for %s", address)
		}
		_, err := tx.Exec(ctx, `
			INSERT INTO sweeps (id, coin, address, amount, state)
			VALUES ($1, $2, $3, $4, 'pending')`, id, coin, address, amount.String())
		return err
	})
	if err != nil {
		return Sweep{}, err
	}
	return s, nil
}

func loadSweepTx(ctx context.Context, tx pgx.Tx, id string) (Sweep, error) {
	var s Sweep
	err := tx.QueryRow(ctx, `
		SELECT id, coin, address, amount, fee, COALESCE(funding_txid,''),
		       COALESCE(txid,''), state, COALESCE(error,''), created_at, updated_at
		FROM sweeps WHERE id = $1 FOR UPDATE`, id).
		Scan(&s.ID, &s.Coin, &s.Address, &s.Amount, &s.Fee, &s.FundingTxID,
			&s.TxID, &s.State, &s.Error, &s.CreatedAt, &s.UpdatedAt)
	return s, err
}

func moveSweep(ctx context.Context, db Pool, id, from, to, setSQL string, args ...any) error {
	return moveState(ctx, db, "sweeps", "sweep", id, from, to, setSQL, args...)
}

// MarkSweepFunding records the fee pre-fund transaction of a token
// sweep (ERC-20 gas / TRON TRX).
func MarkSweepFunding(ctx context.Context, db Pool, id, fundingTxid string) error {
	return moveSweep(ctx, db, id, SPending, SFunding, ", funding_txid = NULLIF($4,'')", fundingTxid)
}

// MarkSweepSigned/Broadcast/Confirmed/Failed advance the machine.

func MarkSweepBroadcast(ctx context.Context, db Pool, id, txid string) error {
	return moveSweep(ctx, db, id, SSigned, SBroadcast, ", txid = NULLIF($4,'')", txid)
}

func MarkSweepConfirmed(ctx context.Context, db Pool, id string) error {
	return moveSweep(ctx, db, id, SBroadcast, SConfirmed, ", confirmed_at = now()")
}

func MarkSweepFailed(ctx context.Context, db Pool, id, from, detail string) error {
	return moveSweep(ctx, db, id, from, SFailed, ", error = $4", detail)
}

// RetrySweep re-arms a failed sweep.
func RetrySweep(ctx context.Context, db Pool, id string) error {
	return moveSweep(ctx, db, id, SFailed, SPending, ", error = NULL")
}

// LastSweepAt reports the newest sweep timestamp of coin (the minimum
// interval gate).
func LastSweepAt(ctx context.Context, db DB, coin string) (time.Time, error) {
	var t time.Time
	err := db.QueryRow(ctx, `SELECT COALESCE(max(created_at), to_timestamp(0)) FROM sweeps WHERE coin = $1`, coin).Scan(&t)
	return t, err
}

// MarkSweepSigned records the signed consolidation tx (from pending
// for UTXO/native sweeps, from funding once token fees are pre-funded).
func MarkSweepSigned(ctx context.Context, db Pool, id, from, txid string, fee decimal.Decimal) error {
	return moveSweep(ctx, db, id, from, SSigned, ", txid = NULLIF($4,''), fee = $5", txid, fee.String())
}

// ListSweeps filters sweep records for the admin surface.
func ListSweeps(ctx context.Context, db DB, coin, state string, limit int) ([]map[string]any, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := db.Query(ctx, `
		SELECT id, coin, address, amount, fee, COALESCE(funding_txid,''),
		       COALESCE(txid,''), state, COALESCE(error,''), created_at
		FROM sweeps
		WHERE ($1 = '' OR coin = $1) AND ($2 = '' OR state = $2)
		ORDER BY created_at DESC LIMIT $3`, coin, state, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []map[string]any
	for rows.Next() {
		var id, c, addr, amount, fee, funding, txid, state, errMsg string
		var created time.Time
		if err := rows.Scan(&id, &c, &addr, &amount, &fee, &funding, &txid, &state, &errMsg, &created); err != nil {
			return nil, err
		}
		out = append(out, map[string]any{
			"id": id, "coin": c, "address": addr, "amount": amount, "fee": fee,
			"fundingTxid": funding, "txid": txid, "state": state, "error": errMsg,
			"createdAt": created.UTC().Format(PgTimestampText),
		})
	}
	return out, rows.Err()
}

// SetSweepPaused toggles a Coin's sweeps (admin, per-Coin pause).
func SetSweepPaused(ctx context.Context, db DB, coin string, paused bool) error {
	tag, err := db.Exec(ctx, `UPDATE coins SET sweep_paused = $2, updated_at = now() WHERE name = $1`, coin, paused)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("%w: %s", ErrUnknownCoin, coin)
	}
	return nil
}

// --- Hot↔cold custody transfers (ADR-0008) ---

// HotTransfer states mirror withdrawals (same signing path).
const (
	HPending   = "pending"
	HSigned    = "signed"
	HBroadcast = "broadcast"
	HConfirmed = "confirmed"
	HFailed    = "failed"
)

var hotTransferNext = map[string]map[string]bool{
	HPending:   {HSigned: true, HFailed: true},
	HSigned:    {HBroadcast: true, HFailed: true},
	HBroadcast: {HConfirmed: true, HFailed: true},
	HFailed:    {HPending: true},
	HConfirmed: {},
}

// HotTransferTransition reports whether from → to is legal.
func HotTransferTransition(from, to string) bool { return hotTransferNext[from][to] }

// HotTransfer is an admin-triggered hot→cold movement.
type HotTransfer struct {
	ID        string
	Coin      string
	Amount    decimal.Decimal
	Address   string // configured cold address
	State     string
	TxID      string
	Error     string
	Actor     string
	CreatedAt time.Time
}

// CreateHotTransfer validates the Coin has a cold address and opens a
// pending transfer (suggested by the dashboard when hot > ceiling).
func CreateHotTransfer(ctx context.Context, db Pool, coin Coin, amount decimal.Decimal, actor string) (HotTransfer, error) {
	if coin.ColdAddress == "" {
		return HotTransfer{}, fmt.Errorf("%w: %s has no cold address", ErrUnknownCoin, coin.Name)
	}
	id := uuid.NewString()
	h := HotTransfer{ID: id, Coin: coin.Name, Amount: amount, Address: coin.ColdAddress, State: HPending, Actor: actor}
	_, err := db.Exec(ctx, `
		INSERT INTO hot_transfers (id, coin, amount, address, state, actor)
		VALUES ($1, $2, $3, $4, 'pending', NULLIF($5,''))`,
		id, coin.Name, amount.String(), coin.ColdAddress, actor)
	if err != nil {
		return HotTransfer{}, err
	}
	return h, nil
}

func moveHotTransfer(ctx context.Context, db Pool, id, from, to, setSQL string, args ...any) error {
	return moveState(ctx, db, "hot_transfers", "hot transfer", id, from, to, setSQL, args...)
}

func MarkHotTransferSigned(ctx context.Context, db Pool, id, txid string) error {
	return moveHotTransfer(ctx, db, id, HPending, HSigned, ", txid = NULLIF($4,'')", txid)
}

func MarkHotTransferBroadcast(ctx context.Context, db Pool, id, txid string) error {
	return moveHotTransfer(ctx, db, id, HSigned, HBroadcast, ", txid = NULLIF($4,'')", txid)
}

func MarkHotTransferConfirmed(ctx context.Context, db Pool, id string) error {
	return moveHotTransfer(ctx, db, id, HBroadcast, HConfirmed, ", confirmed_at = now()")
}

func MarkHotTransferFailed(ctx context.Context, db Pool, id, from, detail string) error {
	return moveHotTransfer(ctx, db, id, from, HFailed, ", error = $4", detail)
}

// RetryHotTransfer re-arms a failed hot→cold transfer.
func RetryHotTransfer(ctx context.Context, db Pool, id string) error {
	return moveHotTransfer(ctx, db, id, HFailed, HPending, ", error = NULL")
}

// ListHotTransfers lists hot→cold records for the custody trail.
func ListHotTransfers(ctx context.Context, db DB, coin string) ([]map[string]any, error) {
	rows, err := db.Query(ctx, `
		SELECT id, coin, amount, address, state, COALESCE(txid,''),
		       COALESCE(error,''), COALESCE(actor,''), created_at
		FROM hot_transfers WHERE ($1 = '' OR coin = $1)
		ORDER BY created_at DESC LIMIT 200`, coin)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []map[string]any
	for rows.Next() {
		var id, c, amount, addr, state, txid, errMsg, actor string
		var created time.Time
		if err := rows.Scan(&id, &c, &amount, &addr, &state, &txid, &errMsg, &actor, &created); err != nil {
			return nil, err
		}
		out = append(out, map[string]any{
			"id": id, "coin": c, "amount": amount, "address": addr, "state": state,
			"txid": txid, "error": errMsg, "actor": actor,
			"createdAt": created.UTC().Format(PgTimestampText),
		})
	}
	return out, rows.Err()
}

// RecordColdTransfer is the offline cold→hot procedure's paper trail:
// the Employee performs it offline and records the txid (spec #22
// story 13).
func RecordColdTransfer(ctx context.Context, db DB, coin string, amount decimal.Decimal, txid, actor string) error {
	if txid == "" {
		return errors.New("cold transfer needs the chain txid")
	}
	_, err := db.Exec(ctx, `
		INSERT INTO cold_transfers (id, coin, amount, txid, actor)
		VALUES ($1, $2, $3, $4, NULLIF($5,''))`,
		uuid.NewString(), coin, amount.String(), txid, actor)
	return err
}

// ListColdTransfers lists recorded cold→hot movements.
func ListColdTransfers(ctx context.Context, db DB, coin string) ([]map[string]any, error) {
	rows, err := db.Query(ctx, `
		SELECT id, coin, amount, txid, COALESCE(actor,''), noted_at
		FROM cold_transfers WHERE ($1 = '' OR coin = $1)
		ORDER BY noted_at DESC LIMIT 200`, coin)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []map[string]any
	for rows.Next() {
		var id, c, amount, txid, actor string
		var noted time.Time
		if err := rows.Scan(&id, &c, &amount, &txid, &actor, &noted); err != nil {
			return nil, err
		}
		out = append(out, map[string]any{
			"id": id, "coin": c, "amount": amount, "txid": txid, "actor": actor,
			"notedAt": noted.UTC().Format(PgTimestampText),
		})
	}
	return out, rows.Err()
}
