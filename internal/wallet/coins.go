package wallet

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

	"github.com/syaikhipin/malxchange/internal/money"
)

// Coin is the wallet-owned chain configuration row (spec #22: scale,
// confirmation count, fees, sweep config, withdrawability).
type Coin struct {
	Name            string
	Family          string // btc | ltc | evm | tron
	Native          bool
	Contract        string
	ChainDecimals   int32 // on-chain dp; 0 = unknown until checked
	Scale           int32
	Confirmations   int
	WithdrawFee     decimal.Decimal
	MinWithdraw     decimal.Decimal
	Withdrawable    bool
	SweepThreshold  decimal.Decimal
	SweepMinSeconds int
	SweepPaused     bool
	XPub            string
	ColdAddress     string
	HotCeiling      decimal.Decimal // zero = unset
	HotFloor        decimal.Decimal
	UpdatedAt       time.Time
}

func scanCoin(row pgx.Row) (Coin, error) {
	var c Coin
	var contract, xpub, cold *string
	var ceiling, floor, fee, minw, thresh *string
	var chainDec *int32
	err := row.Scan(&c.Name, &c.Family, &c.Native, &contract, &chainDec, &c.Scale,
		&c.Confirmations, &fee, &minw, &c.Withdrawable, &thresh, &c.SweepMinSeconds,
		&c.SweepPaused, &xpub, &cold, &ceiling, &floor, &c.UpdatedAt)
	if err != nil {
		return c, err
	}
	deref := func(s *string) string {
		if s == nil {
			return ""
		}
		return *s
	}
	if contract != nil {
		c.Contract = *contract
	}
	if chainDec != nil {
		c.ChainDecimals = *chainDec
	}
	c.XPub, c.ColdAddress = deref(xpub), deref(cold)
	num := func(s *string) decimal.Decimal {
		if s == nil {
			return decimal.Zero
		}
		return money.MustParse(*s)
	}
	c.WithdrawFee, c.MinWithdraw = num(fee), num(minw)
	c.SweepThreshold = num(thresh)
	c.HotCeiling, c.HotFloor = num(ceiling), num(floor)
	return c, nil
}

const coinCols = `name, family, native, contract, chain_decimals, scale,
	confirmations, withdraw_fee, min_withdraw, withdrawable,
	sweep_threshold, sweep_min_seconds, sweep_paused, xpub, cold_address,
	hot_ceiling, hot_floor, updated_at`

// GetCoin loads one Coin row.
func GetCoin(ctx context.Context, db DB, name string) (Coin, error) {
	return scanCoin(db.QueryRow(ctx, `SELECT `+coinCols+` FROM coins WHERE name = $1`, name))
}

// ListCoins loads every Coin.
func ListCoins(ctx context.Context, db DB) ([]Coin, error) {
	rows, err := db.Query(ctx, `SELECT `+coinCols+` FROM coins ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Coin
	for rows.Next() {
		c, err := scanCoin(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// CoinUpdate is the admin-editable subset (everything an Employee may
// change without a deploy; name/family/contract are immutable).
type CoinUpdate struct {
	Scale           *int32
	Confirmations   *int
	WithdrawFee     *decimal.Decimal
	MinWithdraw     *decimal.Decimal
	Withdrawable    *bool
	SweepThreshold  *decimal.Decimal
	SweepMinSeconds *int
	SweepPaused     *bool
	ColdAddress     *string
	HotCeiling      *decimal.Decimal
	HotFloor        *decimal.Decimal
	Xpub            *string
}

// UpdateCoin applies the editable fields and returns the new row.
func UpdateCoin(ctx context.Context, db *pgxpool.Pool, name string, u CoinUpdate) (Coin, error) {
	tag, err := db.Exec(ctx, `
		UPDATE coins SET
			scale             = COALESCE($2, scale),
			confirmations     = COALESCE($3, confirmations),
			withdraw_fee      = COALESCE($4, withdraw_fee),
			min_withdraw      = COALESCE($5, min_withdraw),
			withdrawable      = COALESCE($6, withdrawable),
			sweep_threshold   = COALESCE($7, sweep_threshold),
			sweep_min_seconds = COALESCE($8, sweep_min_seconds),
			sweep_paused      = COALESCE($9, sweep_paused),
			cold_address      = COALESCE($10, cold_address),
			hot_ceiling       = COALESCE($11, hot_ceiling),
			hot_floor         = COALESCE($12, hot_floor),
			xpub              = COALESCE($13, xpub),
			updated_at        = now()
		WHERE name = $1`,
		name, u.Scale, u.Confirmations, decPtr(u.WithdrawFee), decPtr(u.MinWithdraw),
		u.Withdrawable, decPtr(u.SweepThreshold), u.SweepMinSeconds, u.SweepPaused,
		u.ColdAddress, decPtr(u.HotCeiling), decPtr(u.HotFloor), u.Xpub)
	if err != nil {
		return Coin{}, err
	}
	if tag.RowsAffected() == 0 {
		return Coin{}, ErrUnknownCoin
	}
	return GetCoin(ctx, db, name)
}

func decPtr(d *decimal.Decimal) any {
	if d == nil {
		return nil
	}
	return d.String()
}

// SetCoinXPub registers the watch-only account key from the key
// ceremony (also used by tests to arm simnet coins).
func SetCoinXPub(ctx context.Context, db DB, coin, xpub string) error {
	_, err := db.Exec(ctx, `UPDATE coins SET xpub = $2, updated_at = now() WHERE name = $1`, coin, xpub)
	return err
}
