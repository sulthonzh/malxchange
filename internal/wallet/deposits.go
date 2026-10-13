package wallet

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/syaikhipin/malxchange/internal/money"
	"github.com/syaikhipin/malxchange/internal/wallet/keys"
)

// Deposits: detected with a live confirmation counter, credited to the
// Ledger exactly once at the Coin's confirmation count (spec #22
// stories 2/3). Chain-side identity is (coin, txid, vout).

// ErrNoAllocation is returned when address allocation cannot proceed.
var ErrNoAllocation = errors.New("deposit address allocation failed")

// AddressFor returns the Member's deposit address for coin, allocating
// one lazily on first request (one per Member per Coin). watch is the
// Coin's watch-only xpub; indexes are persisted per Coin.
func AddressFor(ctx context.Context, db Pool, watch *keys.Watch, coin, member string) (string, error) {
	var addr string
	err := db.QueryRow(ctx, `SELECT address FROM deposit_addresses WHERE coin = $1 AND member_id = $2`,
		coin, member).Scan(&addr)
	if err == nil {
		return addr, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return "", err
	}
	// Serialize index allocation per Coin (same-transaction lock; the
	// UNIQUE (coin, idx) constraint is the backstop).
	tx, err := db.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, "deposit-alloc:"+coin); err != nil {
		return "", err
	}
	var nextIdx int
	if err := tx.QueryRow(ctx, `
		SELECT COALESCE(MAX(idx) + 1, 0) FROM deposit_addresses WHERE coin = $1`, coin).Scan(&nextIdx); err != nil {
		return "", err
	}
	newAddr, err := watch.AddressAt(uint32(nextIdx))
	if err != nil {
		return "", fmt.Errorf("derive %s index %d: %w", coin, nextIdx, err)
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO deposit_addresses (coin, member_id, address, path, idx)
		VALUES ($1, $2, $3, $4, $5)`,
		coin, member, newAddr, keys.Path(uint32(nextIdx)), nextIdx)
	if err != nil {
		return "", err
	}
	if err := tx.Commit(ctx); err != nil {
		return "", err
	}
	return newAddr, nil
}

// WatchAddresses loads the current address→coin allocation map the
// chain watchers match outputs against.
func WatchAddresses(ctx context.Context, db DB) (map[string]string, error) {
	rows, err := db.Query(ctx, `SELECT address, coin FROM deposit_addresses`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string]string)
	for rows.Next() {
		var addr, coin string
		if err := rows.Scan(&addr, &coin); err != nil {
			return nil, err
		}
		out[addr] = coin
	}
	return out, rows.Err()
}

// Deposit is one member-visible deposit record.
type Deposit struct {
	ID      string
	Coin    string
	Member  string
	Address string
	TxID    string
	Vout    int32
	Amount  string // decimal string at coin scale
	Confs   int    // live counter
	Need    int    // credit threshold
	Status  string
}

// RecordDetected upserts a detected deposit (idempotent on chain
// identity) and refreshes the live confirmation counter. It returns
// whether the row is new.
func RecordDetected(ctx context.Context, db DB, d Deposit, height int64) (bool, error) {
	// xmax = 0 only for a freshly inserted row; a conflict-update sets
	// it, so this distinguishes a new detection from a redelivered one.
	var inserted bool
	err := db.QueryRow(ctx, `
		INSERT INTO deposits (id, coin, member_id, address, txid, vout, amount,
		                      detected_height, confirmations, status)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, 'detected')
		ON CONFLICT (coin, txid, vout) DO UPDATE
		SET updated_at = now()
		RETURNING (xmax = 0)`,
		uuid.NewString(), d.Coin, d.Member, d.Address, d.TxID, d.Vout, d.Amount,
		height, d.Confs).Scan(&inserted)
	if err != nil {
		return false, err
	}
	return inserted, nil
}

// UpdateConfirmations refreshes the live counter for every detected
// deposit of coin at the given tip.
func UpdateConfirmations(ctx context.Context, db DB, coin string, tip int64) error {
	_, err := db.Exec(ctx, `
		UPDATE deposits SET confirmations = $2 - detected_height + 1, updated_at = now()
		WHERE coin = $1 AND status = 'detected'`, coin, tip)
	return err
}

// CreditDue credits every detected deposit of coin whose confirmation
// counter reached the threshold — exactly once (status transition and
// Journal append share one transaction, and the Journal idempotency
// key is the chain identity).
func CreditDue(ctx context.Context, db Pool, coin string, need int) (int, error) {
	credited := 0
	for {
		var id, member, amount, txid string
		var vout int32
		err := db.QueryRow(ctx, `
			SELECT id, member_id, amount, txid, vout FROM deposits
			WHERE coin = $1 AND status = 'detected' AND confirmations >= $2
			ORDER BY detected_height, created_at LIMIT 1`, coin, need).
			Scan(&id, &member, &amount, &txid, &vout)
		if errors.Is(err, pgx.ErrNoRows) {
			return credited, nil
		}
		if err != nil {
			return credited, err
		}
		err = pgx.BeginFunc(ctx, db, func(tx pgx.Tx) error {
			tag, err := tx.Exec(ctx, `
				UPDATE deposits SET status = 'credited', credited_at = now(), updated_at = now()
				WHERE id = $1 AND status = 'detected'`, id)
			if err != nil {
				return err
			}
			if tag.RowsAffected() == 0 {
				return nil // raced: another worker credited it
			}
			return Post(ctx, tx, Entry{
				Member: member, Coin: coin, Kind: KindDeposit,
				AvailDelta: money.MustParse(amount),
				Reason:     "deposit " + txid + fmt.Sprintf(":%d", vout),
				IdemKey:    fmt.Sprintf("deposit:%s:%s:%d", coin, txid, vout),
				RefID:      id,
			})
		})
		if err != nil {
			return credited, err
		}
		credited++
	}
}

// InvalidateDetected drops a detected deposit the reorg evicted (it
// may reappear and re-detect). Credited deposits are never touched —
// that path is InvalidateCredited + an incident.
func InvalidateDetected(ctx context.Context, db DB, coin, txid string, vout int32) error {
	_, err := db.Exec(ctx, `
		DELETE FROM deposits
		WHERE coin = $1 AND txid = $2 AND vout = $3 AND status = 'detected'`,
		coin, txid, vout)
	return err
}

// InvalidateCredited marks a credited deposit invalidated by a deep
// reorg and raises the admin incident — manual adjudication, never an
// automatic Ledger clawback (ADR-0008).
func InvalidateCredited(ctx context.Context, db Pool, coin, txid string, vout int32) error {
	return pgx.BeginFunc(ctx, db, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `
			UPDATE deposits SET status = 'invalidated', updated_at = now()
			WHERE coin = $1 AND txid = $2 AND vout = $3 AND status = 'credited'`,
			coin, txid, vout)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return nil
		}
		_, err = tx.Exec(ctx, `
			INSERT INTO incidents (id, coin, kind, detail)
			VALUES ($1, $2, 'reorg_invalidated_deposit', $3)`,
			uuid.NewString(), coin,
			fmt.Sprintf("deep reorg evicted credited deposit tx %s vout %d — manual adjudication required, no automatic clawback", txid, vout))
		return err
	})
}

// OpenDeposits lists a member's deposits (Member-visible surface).
type OpenDeposit struct {
	Coin          string `json:"coin"`
	TxID          string `json:"txid"`
	Vout          int32  `json:"vout"`
	Amount        string `json:"amount"`
	Confirmations int    `json:"confirmations"`
	Needed        int    `json:"needed"`
	Status        string `json:"status"`
	DetectedAt    string `json:"detectedAt"`
}

// ListMemberDeposits renders deposits with the live counter and the
// Coin's threshold.
func ListMemberDeposits(ctx context.Context, db DB, member string) ([]OpenDeposit, error) {
	rows, err := db.Query(ctx, `
		SELECT d.coin, d.txid, d.vout, d.amount, d.confirmations,
		       c.confirmations, d.status, d.created_at
		FROM deposits d JOIN coins c ON c.name = d.coin
		WHERE d.member_id = $1 ORDER BY d.created_at DESC LIMIT 200`, member)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []OpenDeposit
	for rows.Next() {
		var d OpenDeposit
		var created time.Time
		if err := rows.Scan(&d.Coin, &d.TxID, &d.Vout, &d.Amount, &d.Confirmations,
			&d.Needed, &d.Status, &created); err != nil {
			return nil, err
		}
		d.DetectedAt = created.UTC().Format(PgTimestampText)
		out = append(out, d)
	}
	return out, rows.Err()
}

// PgTimestampText renders a timestamptz the way Postgres' text
// protocol does ("2026-10-13 09:00:00.123456+00") — the documented
// wire form of the RPC timestamps. pgx's binary protocol cannot scan
// timestamptz into []byte, so the scan goes through time.Time and
// formats back to the same text.
const PgTimestampText = "2006-01-02 15:04:05.999999-07"
