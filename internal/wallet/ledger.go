// Package wallet is the sole Ledger writer (issue #22, ADR-0008): the
// append-only Journal backs every balance movement, deposits credit at
// per-Coin confirmation counts, withdrawals and sweeps run explicit
// state machines, and custody keys live only in the encrypted hot
// keystore.
package wallet

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

	"github.com/syaikhipin/malxchange/internal/money"
)

// Journal kinds. Every Ledger movement is exactly one of these.
const (
	KindDeposit          = "deposit"
	KindWithdrawal       = "withdrawal"
	KindWithdrawalRefund = "withdrawal_refund"
	KindFill             = "fill"
	KindFee              = "fee"
	KindFreeze           = "freeze"
	KindFillDebit        = "fill_debit"
	KindRelease          = "release"
	KindAdjustment       = "adjustment"
)

// Ledger errors, classified from Postgres constraint violations so
// callers (and the RPC surface) can answer precisely.
var (
	// ErrInsufficientFunds means the movement would drive available or
	// frozen below zero (CHECK violation on ledger_accounts).
	ErrInsufficientFunds = errors.New("insufficient funds")
	// ErrAlreadyApplied means the idempotency key was journaled before.
	ErrAlreadyApplied = errors.New("journal entry already applied")
)

// Entry is one Journal append: the signed available/frozen movement,
// its reason, and the idempotency key that makes replay a no-op.
type Entry struct {
	Member      string
	Coin        string
	Kind        string
	AvailDelta  decimal.Decimal
	FrozenDelta decimal.Decimal
	Reason      string
	IdemKey     string
	RefID       string
	Actor       string
}

// Post appends the Journal row and moves the Ledger account, in the
// SAME transaction: the Ledger is always the fold of the Journal
// (spec #22: "balance is always the fold of the Journal"). The account
// row is created on first movement, so balances exist exactly when
// money has moved (member.register pre-opens rows for display).
//
// A repeated IdemKey is ErrAlreadyApplied and moves nothing; a movement
// that would drive available or frozen negative is ErrInsufficientFunds.
func Post(ctx context.Context, tx pgx.Tx, e Entry) error {
	if e.Member == "" || e.Coin == "" || e.Kind == "" || e.Reason == "" || e.IdemKey == "" {
		return fmt.Errorf("journal entry needs member, coin, kind, reason and idem key")
	}
	// A freeze/release moves value between avail and frozen, so the net
	// (avail+frozen) is zero by design; the movement is the gross amount.
	amount := e.AvailDelta.Abs()
	if fa := e.FrozenDelta.Abs(); fa.GreaterThan(amount) {
		amount = fa
	}
	if amount.IsZero() {
		return fmt.Errorf("journal entry %s moves nothing", e.IdemKey)
	}
	_, err := tx.Exec(ctx, `
		INSERT INTO journal (id, member_id, coin, kind, avail_delta, frozen_delta,
		                     amount, reason, idem_key, ref_id, actor)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, NULLIF($10,''), NULLIF($11,''))`,
		uuid.NewString(), e.Member, e.Coin, e.Kind,
		e.AvailDelta.String(), e.FrozenDelta.String(), amount.String(),
		e.Reason, e.IdemKey, e.RefID, e.Actor)
	if err != nil {
		return classifyPost(err)
	}
	// Open the row at zero first, then move it with a plain UPDATE. An
	// INSERT..ON CONFLICT checks the CHECK constraints against the
	// proposed row (the negative delta) before conflict resolution, so a
	// freeze from a funded account would wrongly fail.
	if _, err := tx.Exec(ctx, `
		INSERT INTO ledger_accounts (member_id, coin) VALUES ($1, $2)
		ON CONFLICT (member_id, coin) DO NOTHING`, e.Member, e.Coin); err != nil {
		return classifyPost(err)
	}
	_, err = tx.Exec(ctx, `
		UPDATE ledger_accounts
		SET available = available + $3, frozen = frozen + $4, updated_at = now()
		WHERE member_id = $1 AND coin = $2`,
		e.Member, e.Coin, e.AvailDelta.String(), e.FrozenDelta.String())
	if err != nil {
		return classifyPost(err)
	}
	return nil
}

func classifyPost(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "23514": // check_violation: available/frozen would go negative
			return fmt.Errorf("%w: %s", ErrInsufficientFunds, pgErr.ConstraintName)
		case "23505": // unique_violation on journal.idem_key
			if pgErr.ConstraintName == "journal_idem_key_unique" || pgErr.ConstraintName == "journal_idem_key" {
				return ErrAlreadyApplied
			}
		}
	}
	return err
}

// Credit appends a plain available-balance credit (deposit, refund).
func Credit(ctx context.Context, db *pgxpool.Pool, member, coin, kind, reason, idem string, amount decimal.Decimal, refID, actor string) error {
	return pgx.BeginFunc(ctx, db, func(tx pgx.Tx) error {
		return Post(ctx, tx, Entry{
			Member: member, Coin: coin, Kind: kind,
			AvailDelta: amount, Reason: reason, IdemKey: idem,
			RefID: refID, Actor: actor,
		})
	})
}

// Account is one member's per-Coin balance row.
type Account struct {
	Member    string          `json:"member"`
	Coin      string          `json:"coin"`
	Available decimal.Decimal `json:"-"`
	Frozen    decimal.Decimal `json:"-"`
	Scale     int32           `json:"-"`
}

// API renders the balance as decimal strings at the Coin's scale.
func (a Account) API() map[string]string {
	return map[string]string{
		"coin":      a.Coin,
		"available": money.Format(a.Available, a.Scale),
		"frozen":    money.Format(a.Frozen, a.Scale),
	}
}

// Accounts lists a member's balances (all Coins, opened or not, with
// the Coin's scale so callers can render exactly).
func Accounts(ctx context.Context, db DB, member string) ([]Account, error) {
	rows, err := db.Query(ctx, `
		SELECT c.name, COALESCE(a.available, 0), COALESCE(a.frozen, 0), c.scale
		FROM coins c
		LEFT JOIN ledger_accounts a ON a.coin = c.name AND a.member_id = $1
		ORDER BY c.name`, member)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Account
	for rows.Next() {
		var a Account
		var avail, frozen string
		if err := rows.Scan(&a.Coin, &avail, &frozen, &a.Scale); err != nil {
			return nil, err
		}
		a.Member = member
		a.Available = money.MustParse(avail)
		a.Frozen = money.MustParse(frozen)
		out = append(out, a)
	}
	return out, rows.Err()
}

// OpenAccounts inserts Ledger account rows for every Coin (used by the
// member-register consumer so balances exist before the first deposit).
// Existing rows are left untouched — an operator edit to a balance via
// adjustment must survive re-registration.
func OpenAccounts(ctx context.Context, tx pgx.Tx, member string) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO ledger_accounts (member_id, coin)
		SELECT $1, name FROM coins
		ON CONFLICT (member_id, coin) DO NOTHING`, member)
	return err
}

// FoldCheck is the audit invariant of spec #22: per Coin, the Ledger
// balances equal the Journal fold. Coin "" checks every Coin.
type FoldCheck struct {
	Coin          string `json:"coin"`
	LedgerAvail   string `json:"ledgerAvail"`
	LedgerFrozen  string `json:"ledgerFrozen"`
	JournalAvail  string `json:"journalAvail"`
	JournalFrozen string `json:"journalFrozen"`
	OK            bool   `json:"ok"`
}

// VerifyFold asserts Ledger == Journal fold and returns the per-Coin
// comparison (the reconciliation query, not an expedition).
func VerifyFold(ctx context.Context, db DB, coin string) ([]FoldCheck, error) {
	rows, err := db.Query(ctx, `
		WITH folded AS (
			SELECT coin, COALESCE(SUM(avail_delta),0) AS avail, COALESCE(SUM(frozen_delta),0) AS frozen
			FROM journal WHERE ($1 = '' OR coin = $1) GROUP BY coin
		), held AS (
			SELECT coin, COALESCE(SUM(available),0) AS avail, COALESCE(SUM(frozen),0) AS frozen
			FROM ledger_accounts WHERE ($1 = '' OR coin = $1) GROUP BY coin
		)
		SELECT c.name, COALESCE(h.avail,0), COALESCE(h.frozen,0), COALESCE(f.avail,0), COALESCE(f.frozen,0)
		FROM coins c
		LEFT JOIN folded f ON f.coin = c.name
		LEFT JOIN held   h ON h.coin = c.name
		WHERE ($1 = '' OR c.name = $1)
		ORDER BY c.name`, coin)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []FoldCheck
	for rows.Next() {
		var fc FoldCheck
		var la, lf, ja, jf string
		if err := rows.Scan(&fc.Coin, &la, &lf, &ja, &jf); err != nil {
			return nil, err
		}
		fc.LedgerAvail, fc.LedgerFrozen = la, lf
		fc.JournalAvail, fc.JournalFrozen = ja, jf
		fc.OK = money.MustParse(la).Equal(money.MustParse(ja)) &&
			money.MustParse(lf).Equal(money.MustParse(jf))
		out = append(out, fc)
	}
	return out, rows.Err()
}
