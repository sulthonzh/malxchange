package exchange

import (
	"testing"

	"github.com/syaikhipin/malxchange/internal/engine"
)

// Status() publishes one snapshot per loaded Market in stable market
// order (#26): callers index the slice, and map iteration order is random.
func TestStatusStableMarketOrder(t *testing.T) {
	eth := MarketCfg{Market: "ETH/USDT", BaseScale: 8, QuoteScale: 2, TickRaw: 1, LotRaw: 1}
	btc := MarketCfg{Market: "BTC/USDT", BaseScale: 8, QuoteScale: 2, TickRaw: 1, LotRaw: 1}
	e := &Engine{
		cfgs:  map[string]MarketCfg{btc.Market: btc, eth.Market: eth},
		books: map[string]*engine.Book{btc.Market: engine.NewBook(btc.Spec()), eth.Market: engine.NewBook(eth.Spec())},
		ready: map[string]bool{btc.Market: true, eth.Market: true},
	}
	got := e.Status()
	if len(got) != 2 {
		t.Fatalf("status markets = %d, want 2", len(got))
	}
	if got[0].Market != "BTC/USDT" || got[1].Market != "ETH/USDT" {
		t.Errorf("status order = [%s, %s], want ascending [BTC/USDT, ETH/USDT]", got[0].Market, got[1].Market)
	}
}
