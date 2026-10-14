package wallet

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/shopspring/decimal"

	"github.com/syaikhipin/malxchange/internal/money"
)

// Withdrawals: request (fee deducted from the requested amount), admin
// audit, then the explicit state machine pending → signed → broadcast
// → confirmed, plus failed with error detail (ADR-0008). Money leaves
// the available balance at request time; a rejection or refund puts it
// back through the Journal.

// Withdrawal states.
const (
	WAudit     = "audit"
	WApproved  = "approved"
	WSigned    = "signed"
	WBroadcast = "broadcast"
	WConfirmed = "confirmed"
	WRejected  = "rejected"
	WFailed    = "failed"
	WRefunded  = "refunded"
)

// withdrawalNext is the legal transition table — the state machine is
// enforced in SQL (UPDATE ... WHERE state = expected) and pinned here
// for the unit test.
var withdrawalNext = map[string]map[string]bool{
	WAudit:     {WApproved: true, WRejected: true},
	WApproved:  {WSigned: true, WFailed: true},
	WSigned:    {WBroadcast: true, WFailed: true},
	WBroadcast: {WConfirmed: true, WFailed: true},
	WFailed:    {WApproved: true, WRefunded: true}, // retry | refund
	WRejected:  {},
	WConfirmed: {},
	WRefunded:  {},
}

// WithdrawalTransition reports whether from → to is legal.
func WithdrawalTransition(from, to string) bool {
	return withdrawalNext[from][to]
}

var (
	// ErrInvalidState is an illegal state-machine transition.
	ErrInvalidState = errors.New("illegal state transition")
	// ErrBelowMinimum is a withdrawal under the Coin's minimum.
	ErrBelowMinimum = errors.New("below minimum withdrawal")
	// ErrNotWithdrawable is a Coin with withdrawals disabled.
	ErrNotWithdrawable = errors.New("coin is not withdrawable")
	// ErrFeeExceeds is fee >= requested amount.
	ErrFeeExceeds = errors.New("withdrawal fee exceeds amount")
)

// Withdrawal is a withdrawal record (admin- and member-visible).
type Withdrawal struct {
	ID        string
	Coin      string
	Member    string
	Address   string
	Amount    decimal.Decimal // requested, debited
	Fee       decimal.Decimal
	Net       decimal.Decimal // broadcast on chain
	State     string
	TxID      string
	Error     string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// RequestWithdrawal validates, debits, and records a withdrawal in
// state audit — the only state an Employee can approve from.
func RequestWithdrawal(ctx context.Context, db Pool, coin Coin, member, address string, amount decimal.Decimal) (Withdrawal, error) {
	if !coin.Withdrawable {
		return Withdrawal{}, fmt.Errorf("%w: %s", ErrNotWithdrawable, coin.Name)
	}
	if !money.WithinScale(amount, coin.Scale) {
		return Withdrawal{}, fmt.Errorf("amount %s exceeds scale %d of %s", amount.String(), coin.Scale, coin.Name)
	}
	if amount.LessThan(coin.MinWithdraw) || amount.IsZero() {
		return Withdrawal{}, fmt.Errorf("%w: %s < %s", ErrBelowMinimum, amount.String(), coin.MinWithdraw.String())
	}
	if amount.LessThanOrEqual(coin.WithdrawFee) {
		return Withdrawal{}, fmt.Errorf("%w: %s <= fee %s", ErrFeeExceeds, amount.String(), coin.WithdrawFee.String())
	}
	id := uuid.NewString()
	net := amount.Sub(coin.WithdrawFee)
	w := Withdrawal{ID: id, Coin: coin.Name, Member: member, Address: address,
		Amount: amount, Fee: coin.WithdrawFee, Net: net, State: WAudit}
	err := pgx.BeginFunc(ctx, db, func(tx pgx.Tx) error {
		if err := Post(ctx, tx, Entry{
			Member: member, Coin: coin.Name, Kind: KindWithdrawal,
			AvailDelta: amount.Neg(),
			Reason:     "withdrawal request to " + address,
			IdemKey:    "withdrawal:" + id, RefID: id,
		}); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `
			INSERT INTO withdrawals (id, coin, member_id, address, amount, fee, net, state)
			VALUES ($1, $2, $3, $4, $5, $6, $7, 'audit')`,
			id, coin.Name, member, address, amount.String(), w.Fee.String(), net.String())
		return err
	})
	if err != nil {
		return Withdrawal{}, err
	}
	return w, nil
}

// LoadWithdrawal fetches one record.
func LoadWithdrawal(ctx context.Context, db DB, id string) (Withdrawal, error) {
	var w Withdrawal
	err := db.QueryRow(ctx, `
		SELECT id, coin, member_id, address, amount, fee, net, state,
		       COALESCE(txid,''), COALESCE(error,''), created_at, updated_at
		FROM withdrawals WHERE id = $1`, id).
		Scan(&w.ID, &w.Coin, &w.Member, &w.Address, &w.Amount, &w.Fee, &w.Net, &w.State,
			&w.TxID, &w.Error, &w.CreatedAt, &w.UpdatedAt)
	return w, err
}

// moveWithdrawal performs a guarded state transition; extra SQL
// (txid/error columns) rides along via SET clauses.
func moveWithdrawal(ctx context.Context, db Pool, id, from, to, setSQL string, args ...any) error {
	return moveState(ctx, db, "withdrawals", "withdrawal", id, from, to, setSQL, args...)
}

// moveState is the single guarded state-machine transition primitive
// shared by withdrawals, sweeps, and hot transfers: it moves a row from
// `from` to `to` only if it is currently in `from`, wrapping a zero-row
// update as ErrInvalidState. setSQL is an internal, code-authored column
// fragment (never user input); kind is the noun in the error message.
func moveState(ctx context.Context, db Pool, table, kind, id, from, to, setSQL string, args ...any) error {
	all := append([]any{id, from, to}, args...)
	tag, err := db.Exec(ctx, `
		UPDATE `+table+` SET state = $3, updated_at = now()`+setSQL+`
		WHERE id = $1 AND state = $2`, all...)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("%w: %s %s %s → %s", ErrInvalidState, kind, id, from, to)
	}
	return nil
}

// AuditWithdrawal is the Employee decision point: approve sends the
// withdrawal to the signer; reject refunds the Ledger.
func AuditWithdrawal(ctx context.Context, db Pool, id string, approve bool, actor string) error {
	if approve {
		return moveWithdrawal(ctx, db, id, WAudit, WApproved, ", error = NULL")
	}
	return pgx.BeginFunc(ctx, db, func(tx pgx.Tx) error {
		w, err := loadWithdrawalTx(ctx, tx, id)
		if err != nil {
			return err
		}
		if w.State != WAudit {
			return fmt.Errorf("%w: withdrawal %s is %s, not audit", ErrInvalidState, id, w.State)
		}
		if err := Post(ctx, tx, Entry{
			Member: w.Member, Coin: w.Coin, Kind: KindWithdrawalRefund,
			AvailDelta: w.Amount,
			Reason:     "withdrawal rejected",
			IdemKey:    "withdrawal-reject:" + id, RefID: id, Actor: actor,
		}); err != nil && !errors.Is(err, ErrAlreadyApplied) {
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE withdrawals SET state='rejected', updated_at=now() WHERE id=$1 AND state='audit'`, id)
		return err
	})
}

// RefundWithdrawal returns failed funds to the Member (Employee
// decides a failed withdrawal will not be retried).
func RefundWithdrawal(ctx context.Context, db Pool, id, actor string) error {
	return pgx.BeginFunc(ctx, db, func(tx pgx.Tx) error {
		w, err := loadWithdrawalTx(ctx, tx, id)
		if err != nil {
			return err
		}
		if w.State != WFailed {
			return fmt.Errorf("%w: withdrawal %s is %s, not failed", ErrInvalidState, id, w.State)
		}
		if err := Post(ctx, tx, Entry{
			Member: w.Member, Coin: w.Coin, Kind: KindWithdrawalRefund,
			AvailDelta: w.Amount,
			Reason:     "withdrawal failed and refunded",
			IdemKey:    "withdrawal-refund:" + id, RefID: id, Actor: actor,
		}); err != nil && !errors.Is(err, ErrAlreadyApplied) {
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE withdrawals SET state='refunded', updated_at=now() WHERE id=$1 AND state='failed'`, id)
		return err
	})
}

// RetryWithdrawal re-arms a failed withdrawal for signing.
func RetryWithdrawal(ctx context.Context, db Pool, id string) error {
	return moveWithdrawal(ctx, db, id, WFailed, WApproved, ", error = NULL")
}

// MarkWithdrawalSigned/Broadcast/Confirmed/Failed advance the machine
// from the executor loops.
func MarkWithdrawalSigned(ctx context.Context, db Pool, id, txid string) error {
	return moveWithdrawal(ctx, db, id, WApproved, WSigned, ", txid = NULLIF($4,'')", txid)
}

func MarkWithdrawalBroadcast(ctx context.Context, db Pool, id, txid string) error {
	return moveWithdrawal(ctx, db, id, WSigned, WBroadcast, ", txid = NULLIF($4,'')", txid)
}

func MarkWithdrawalConfirmed(ctx context.Context, db Pool, id string) error {
	return moveWithdrawal(ctx, db, id, WBroadcast, WConfirmed, ", confirmed_at = now()")
}

func MarkWithdrawalFailed(ctx context.Context, db Pool, id, detail string) error {
	return moveWithdrawal(ctx, db, id, WApproved, WFailed, ", error = $4", detail)
}

// MarkWithdrawalSignedFrom etc. handle the broadcast→failed retry
// path (re-signing a broadcast withdrawal after failure).
func MarkWithdrawalSignedFromBroadcast(ctx context.Context, db Pool, id, txid string) error {
	return moveWithdrawal(ctx, db, id, WBroadcast, WSigned, ", txid = NULLIF($4,'')", txid)
}

func loadWithdrawalTx(ctx context.Context, tx pgx.Tx, id string) (Withdrawal, error) {
	var w Withdrawal
	err := tx.QueryRow(ctx, `
		SELECT id, coin, member_id, address, amount, fee, net, state,
		       COALESCE(txid,''), COALESCE(error,''), created_at, updated_at
		FROM withdrawals WHERE id = $1 FOR UPDATE`, id).
		Scan(&w.ID, &w.Coin, &w.Member, &w.Address, &w.Amount, &w.Fee, &w.Net, &w.State,
			&w.TxID, &w.Error, &w.CreatedAt, &w.UpdatedAt)
	return w, err
}

// WithdrawalView is the admin wire shape of one withdrawal (the list
// row; detail adds updatedAt). Money values are decimal strings at the
// Coin's scale, timestamps Postgres text.
type WithdrawalView struct {
	ID        string `json:"id"`
	Coin      string `json:"coin"`
	Member    string `json:"member"`
	Address   string `json:"address"`
	Amount    string `json:"amount"`
	Fee       string `json:"fee"`
	Net       string `json:"net"`
	State     string `json:"state"`
	TxID      string `json:"txid,omitempty"`
	Error     string `json:"error,omitempty"`
	CreatedAt string `json:"createdAt"`
	UpdatedAt string `json:"updatedAt,omitempty"` // detail only
}

// errMalformedID marks an id that cannot resolve (e.g. not a UUID for
// a UUID-keyed table) — Postgres' invalid_input_syntax class.
var errMalformedID = errors.New("wallet: malformed id")

// LoadWithdrawalDetail fetches one record in the detail shape — the
// list row plus updatedAt (spec #59: the audit queue drills into one
// withdrawal). pgx.ErrNoRows and errMalformedID pass through for the
// caller's 404.
func LoadWithdrawalDetail(ctx context.Context, db DB, id string) (WithdrawalView, error) {
	var v WithdrawalView
	var created, updated time.Time
	err := db.QueryRow(ctx, `
		SELECT id, coin, member_id, address, amount, fee, net, state,
		       COALESCE(txid,''), COALESCE(error,''), created_at, updated_at
		FROM withdrawals WHERE id = $1`, id).
		Scan(&v.ID, &v.Coin, &v.Member, &v.Address, &v.Amount, &v.Fee, &v.Net,
			&v.State, &v.TxID, &v.Error, &created, &updated)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "22P02" {
			return WithdrawalView{}, fmt.Errorf("%w: %s", errMalformedID, id)
		}
		return WithdrawalView{}, err
	}
	v.CreatedAt = created.UTC().Format(PgTimestampText)
	v.UpdatedAt = updated.UTC().Format(PgTimestampText)
	return v, nil
}

// ListWithdrawals filters records (member or admin surface).
func ListWithdrawals(ctx context.Context, db DB, member, state string, limit int) ([]WithdrawalView, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := db.Query(ctx, `
		SELECT id, coin, member_id, address, amount, fee, net, state,
		       COALESCE(txid,''), COALESCE(error,''), created_at
		FROM withdrawals
		WHERE ($1 = '' OR member_id = $1) AND ($2 = '' OR state = $2)
		ORDER BY created_at DESC LIMIT $3`, member, state, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []WithdrawalView
	for rows.Next() {
		var v WithdrawalView
		var created time.Time
		if err := rows.Scan(&v.ID, &v.Coin, &v.Member, &v.Address, &v.Amount, &v.Fee, &v.Net,
			&v.State, &v.TxID, &v.Error, &created); err != nil {
			return nil, err
		}
		v.CreatedAt = created.UTC().Format(PgTimestampText)
		out = append(out, v)
	}
	return out, rows.Err()
}

// TakeApprovedWithdrawals claims up to n approved withdrawals for
// signing (the executor works them one at a time).
func TakeApprovedWithdrawals(ctx context.Context, db DB, coin string, n int) ([]Withdrawal, error) {
	rows, err := db.Query(ctx, `
		SELECT id, coin, member_id, address, amount, fee, net, state,
		       COALESCE(txid,''), COALESCE(error,''), created_at, updated_at
		FROM withdrawals
		WHERE state = 'approved' AND ($1 = '' OR coin = $1)
		ORDER BY created_at LIMIT $2`, coin, n)
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
