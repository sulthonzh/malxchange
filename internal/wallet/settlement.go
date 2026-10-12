package wallet

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/syaikhipin/malxchange/internal/events"
	"github.com/syaikhipin/malxchange/internal/inbox"
	"github.com/syaikhipin/malxchange/internal/money"
)

// Fill settlement (spec #22 stories 7/25): consume trade.fill through
// the idempotent inbox; gross credit plus an explicit fee entry per
// side so the journal is queryable line by line. Redelivery can never
// double-credit — the whole settlement is one transaction behind one
// inbox row.

// ApplyFill settles one TradeFill inside tx. Idempotent: each Journal
// entry carries its own key, so a partial replay is a no-op.
func ApplyFill(ctx context.Context, tx pgx.Tx, f events.TradeFill) error {
	base, quote, err := marketCoins(f.Market)
	if err != nil {
		return err
	}
	amount := money.MustParse(f.Amount)
	turnover := money.MustParse(f.Turnover)
	buyTurnover := money.MustParse(f.BuyTurnover)
	buyFee := money.MustParse(f.BuyFee)
	sellFee := money.MustParse(f.SellFee)

	// Buyer: held the quote Coin, receives base; fee in base (ADR-0009).
	if err := settleSide(ctx, tx, f, "buy", f.BuyOrderID, f.BuyMemberID,
		quote, base, buyTurnover, amount, buyFee); err != nil {
		return err
	}
	// Seller: held the base Coin, receives quote; fee in quote.
	return settleSide(ctx, tx, f, "sell", f.SellOrderID, f.SellMemberID,
		base, quote, amount, turnover, sellFee)
}

func settleSide(ctx context.Context, tx pgx.Tx, f events.TradeFill, side, orderID, member,
	holdCoin, creditCoin string, consumed, gross, fee decimal.Decimal) error {
	prefix := fmt.Sprintf("fill:%s:%d:%s", f.Market, f.Seq, side)

	// Consume the hold: frozen down by what this fill took.
	if err := Post(ctx, tx, Entry{
		Member: member, Coin: holdCoin, Kind: KindFillDebit,
		FrozenDelta: consumed.Neg(),
		Reason:      fmt.Sprintf("%s fill seq %d", f.Market, f.Seq),
		IdemKey:     prefix + ":hold", RefID: orderID,
	}); err != nil && !errors.Is(err, ErrAlreadyApplied) {
		return err
	}
	if err := settleHold(ctx, tx, orderID, consumed); err != nil {
		return err
	}
	// Gross credit in the received Coin.
	if err := Post(ctx, tx, Entry{
		Member: member, Coin: creditCoin, Kind: KindFill,
		AvailDelta: gross,
		Reason:     fmt.Sprintf("%s fill seq %d gross", f.Market, f.Seq),
		IdemKey:    prefix + ":credit", RefID: orderID,
	}); err != nil && !errors.Is(err, ErrAlreadyApplied) {
		return err
	}
	// Explicit fee entry (journal line), same Coin as the credit.
	if fee.IsPositive() {
		if err := Post(ctx, tx, Entry{
			Member: member, Coin: creditCoin, Kind: KindFee,
			AvailDelta: fee.Neg(),
			Reason:     fmt.Sprintf("%s fill seq %d fee", f.Market, f.Seq),
			IdemKey:    prefix + ":fee", RefID: orderID,
		}); err != nil && !errors.Is(err, ErrAlreadyApplied) {
			return err
		}
	}
	return nil
}

// ApplyCompleted releases a finished order's hold remainder (canceled:
// everything not settled; filled: any dust remainder).
func ApplyCompleted(ctx context.Context, tx pgx.Tx, oc events.OrderCompleted) error {
	idem := "complete:" + oc.OrderID
	reason := "order " + oc.Status
	return releaseHold(ctx, tx, oc.OrderID, idem, reason)
}

// ApplyMemberRegistered opens Ledger accounts for a new Member so
// balances exist before the first deposit (spec #22 story 27).
func ApplyMemberRegistered(ctx context.Context, tx pgx.Tx, env events.Envelope) error {
	var reg events.MemberRegistered
	if err := json.Unmarshal(env.Data, &reg); err != nil {
		return fmt.Errorf("member.register payload: %w", err)
	}
	if reg.MemberID == "" {
		return fmt.Errorf("member.register payload: empty memberId")
	}
	return OpenAccounts(ctx, tx, reg.MemberID)
}

// SettleEnvelope routes one consumed envelope to its applier, all
// inside the caller's transaction and behind the inbox dedupe.
func SettleEnvelope(ctx context.Context, tx pgx.Tx, env events.Envelope) error {
	switch env.Type {
	case events.TopicTradeFill:
		var f events.TradeFill
		if err := json.Unmarshal(env.Data, &f); err != nil {
			return fmt.Errorf("trade.fill payload: %w", err)
		}
		return inbox.Apply(ctx, tx, "fill:"+f.Market, env.ID, func(tx pgx.Tx) error {
			return ApplyFill(ctx, tx, f)
		})
	case events.TopicOrderCompleted:
		var oc events.OrderCompleted
		if err := json.Unmarshal(env.Data, &oc); err != nil {
			return fmt.Errorf("order.completed payload: %w", err)
		}
		return inbox.Apply(ctx, tx, "complete:"+oc.OrderID, env.ID, func(tx pgx.Tx) error {
			return ApplyCompleted(ctx, tx, oc)
		})
	case events.TopicMemberRegister:
		return inbox.Apply(ctx, tx, "register:"+env.Key, env.ID, func(tx pgx.Tx) error {
			return ApplyMemberRegistered(ctx, tx, env)
		})
	default:
		return fmt.Errorf("wallet: unexpected topic %s", env.Type)
	}
}

// marketCoins splits a Market symbol into its base and quote Coin
// names. Symbols are "<base>/<quote>" by construction (markets table).
func marketCoins(symbol string) (base, quote string, err error) {
	i := strings.IndexByte(symbol, '/')
	if i <= 0 || i == len(symbol)-1 {
		return "", "", fmt.Errorf("market symbol %q is not <base>/<quote>", symbol)
	}
	return symbol[:i], symbol[i+1:], nil
}
