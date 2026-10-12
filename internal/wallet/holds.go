package wallet

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"
	"github.com/syaikhipin/malxchange/internal/money"
)

// Order holds: funds frozen while an Order rests (spec #22 story 6).
// api calls Freeze synchronously so placement fails fast on
// insufficient funds; fills settle the hold; completion releases the
// remainder.

var (
	// ErrHoldNotFound means no open hold for the order.
	ErrHoldNotFound = errors.New("order hold not found")
	// ErrUnknownCoin means the referenced Coin is not configured.
	ErrUnknownCoin = errors.New("unknown coin")
)

// Freeze moves amount from available to frozen and records the hold.
// A repeated call for the same order is a no-op (idempotent RPC).
func Freeze(ctx context.Context, db *pgxpool.Pool, orderID, member, market, side, coin string, amount decimal.Decimal) error {
	return pgx.BeginFunc(ctx, db, func(tx pgx.Tx) error {
		var holdCoin string
		err := tx.QueryRow(ctx, `SELECT coin FROM order_holds WHERE order_id = $1`, orderID).Scan(&holdCoin)
		switch {
		case err == nil:
			if holdCoin != coin {
				return fmt.Errorf("order %s already held in %s", orderID, holdCoin)
			}
			return nil // redelivery of the same placement
		case !errors.Is(err, pgx.ErrNoRows):
			return err
		}
		if err := Post(ctx, tx, Entry{
			Member: member, Coin: coin, Kind: KindFreeze,
			AvailDelta: amount.Neg(), FrozenDelta: amount,
			Reason:  "order placed: " + market + " " + side,
			IdemKey: "freeze:" + orderID,
			RefID:   orderID,
		}); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `
			INSERT INTO order_holds (order_id, member_id, market, side, coin, amount)
			VALUES ($1, $2, $3, $4, $5, $6)`,
			orderID, member, market, side, coin, amount.String())
		if err != nil {
			return err
		}
		return nil
	})
}

// settleHold consumes consumed from the hold and the frozen balance
// (inside the caller's fill transaction).
func settleHold(ctx context.Context, tx pgx.Tx, orderID string, consumed decimal.Decimal) error {
	tag, err := tx.Exec(ctx, `
		UPDATE order_holds SET settled = settled + $2
		WHERE order_id = $1 AND status = 'open'`, orderID, consumed.String())
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		// No open hold: a fill for an order wallet never froze (e.g. the
		// freeze RPC raced the engine). The credit below still applies —
		// the member must receive the proceeds.
		return nil
	}
	// Guard the invariant: settled can never exceed the frozen amount.
	var amount, settled string
	if err := tx.QueryRow(ctx, `SELECT amount, settled FROM order_holds WHERE order_id = $1`, orderID).
		Scan(&amount, &settled); err != nil {
		return err
	}
	if money.MustParse(settled).GreaterThan(money.MustParse(amount)) {
		return fmt.Errorf("order %s: settlement %s exceeds hold %s — engine/wallet drift", orderID, settled, amount)
	}
	return nil
}

// releaseHold returns the un-settled remainder to available and closes
// the hold. Idempotent: a released hold is a no-op.
func releaseHold(ctx context.Context, tx pgx.Tx, orderID, idem, reason string) error {
	var member, coin, amount, settled string
	err := tx.QueryRow(ctx, `
		SELECT member_id, coin, amount, settled FROM order_holds
		WHERE order_id = $1 AND status = 'open' FOR UPDATE`, orderID).
		Scan(&member, &coin, &amount, &settled)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil // never frozen, or already released
	}
	if err != nil {
		return err
	}
	remaining := money.MustParse(amount).Sub(money.MustParse(settled))
	if remaining.IsPositive() {
		if err := Post(ctx, tx, Entry{
			Member: member, Coin: coin, Kind: KindRelease,
			AvailDelta: remaining, FrozenDelta: remaining.Neg(),
			Reason: reason, IdemKey: idem, RefID: orderID,
		}); err != nil && !errors.Is(err, ErrAlreadyApplied) {
			return err
		}
	}
	_, err = tx.Exec(ctx, `
		UPDATE order_holds SET status = 'released', released_at = now()
		WHERE order_id = $1 AND status = 'open'`, orderID)
	return err
}

// ReleaseOrder releases a hold on api's behalf (the order died before
// reaching the engine, e.g. a rejected placement after the freeze).
func ReleaseOrder(ctx context.Context, db *pgxpool.Pool, orderID string) error {
	return pgx.BeginFunc(ctx, db, func(tx pgx.Tx) error {
		return releaseHold(ctx, tx, orderID, "release:"+orderID, "order released before placement")
	})
}
