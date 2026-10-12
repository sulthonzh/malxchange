package market

import (
	"testing"
	"time"

	"github.com/syaikhipin/malxchange/internal/events"
)

// TestFoldTickerEmptyWindow: a Market whose 24h window holds no Fills
// publishes an honest empty summary — no invented last price, no invented
// range, zero Turnover (ticket #32: the front never waits for a first tick).
func TestFoldTickerEmptyWindow(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC)
	got := FoldTicker("BTC/USDT", now, nil)
	want := events.Ticker{
		Market: "BTC/USDT", Last: "", High: "", Low: "", Turnover: "0",
	}
	if got != want {
		t.Errorf("empty window fold = %+v, want %+v", got, want)
	}
}
