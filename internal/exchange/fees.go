package exchange

import (
	"github.com/shopspring/decimal"

	"github.com/syaikhipin/malxchange/internal/engine"
	"github.com/syaikhipin/malxchange/internal/money"
)

// Fees computed at fill time (ADR-0009): charged in the Coin the side
// receives — buy side receives base (amount × rate), sell side receives
// quote (turnover × rate) — floored to the fee Coin's scale per fill; a
// fee may floor to zero (dust stays with the member); no exemptions.
type fillFees struct {
	BuyFeeRaw  int64 // base raw
	SellFeeRaw int64 // quote raw
}

func feesForFill(cfg MarketCfg, f engine.Fill) fillFees {
	buyRate, sellRate := cfg.MakerFee, cfg.TakerFee
	if f.TakerSide == engine.Buy {
		buyRate, sellRate = cfg.TakerFee, cfg.MakerFee
	}
	amountRaw := f.AmountLots * cfg.LotRaw // base raw
	return fillFees{
		BuyFeeRaw:  floorMul(amountRaw, buyRate, cfg.BaseScale),
		SellFeeRaw: floorMul(f.TurnoverRaw, sellRate, cfg.QuoteScale),
	}
}

// floorMul = floor(raw × rate) at the fee Coin's scale. raw is an integer
// at the fee scale, so the product's exact scale is already the coin's;
// the floor is just integer truncation of the rate product.
func floorMul(raw int64, rateStr string, scale int) int64 {
	if raw <= 0 {
		return 0
	}
	rate, err := money.Parse(rateStr)
	if err != nil {
		return 0 // config validated at load; unreachable in practice
	}
	rawD := decimal.New(raw, -int32(scale))
	fee := money.FloorTo(rawD.Mul(rate), int32(scale))
	v := fee.Shift(int32(scale)).IntPart()
	if v < 0 {
		return 0
	}
	return v
}
