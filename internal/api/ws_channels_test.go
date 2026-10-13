package api

import (
	"errors"
	"testing"

	"github.com/syaikhipin/malxchange/internal/market"
)

// The channel-name router (ADR-0011): one parse function decides what a
// client may subscribe to. A member ID never appears in a channel name —
// the personal channel is the fixed word "orders", bound to the socket's
// session, so this parser is the whole authorization matrix for names.
func TestParseChannel(t *testing.T) {
	cases := []struct {
		name     string
		member   bool
		kind     channelKind
		market   string
		interval market.Interval
		err      error
	}{
		{name: "ticker", kind: chTicker},
		{name: "ticker.BTC/USDT", kind: chTickerMarket, market: "BTC/USDT"},
		{name: "depth.BTC/USDT", kind: chDepth, market: "BTC/USDT"},
		{name: "trades.ETH/USDT", kind: chTrades, market: "ETH/USDT"},
		{name: "kline.BTC/USDT.1m", kind: chKline, market: "BTC/USDT", interval: market.Interval1m},
		{name: "kline.BTC/USDT.5m", kind: chKline, market: "BTC/USDT", interval: market.Interval5m},
		{name: "kline.BTC/USDT.15m", kind: chKline, market: "BTC/USDT", interval: market.Interval15m},
		{name: "kline.BTC/USDT.30m", kind: chKline, market: "BTC/USDT", interval: market.Interval30m},
		{name: "kline.BTC/USDT.1h", kind: chKline, market: "BTC/USDT", interval: market.Interval1h},
		{name: "kline.BTC/USDT.4h", kind: chKline, market: "BTC/USDT", interval: market.Interval4h},
		{name: "kline.BTC/USDT.1d", kind: chKline, market: "BTC/USDT", interval: market.Interval1d},
		{name: "kline.BTC/USDT.1w", kind: chKline, market: "BTC/USDT", interval: market.Interval1w},
		{name: "orders", member: true, kind: chOrders},

		// The orders channel is member-only (ADR-0011).
		{name: "orders", err: errSignInRequired},
		// Unknown prefixes and bare words.
		{name: "foo", err: errUnknownWSChannel},
		{name: "", err: errUnknownWSChannel},
		{name: "tickers", err: errUnknownWSChannel},
		{name: "depths.BTC/USDT", err: errUnknownWSChannel},
		// Empty or malformed market segments.
		{name: "depth.", err: errUnknownWSChannel},
		{name: "trades.BTCUSDT", err: errUnknownWSChannel},
		{name: "kline..1m", err: errUnknownWSChannel},
		// Interval must be one of the stored intervals (ticket #44:
		// kline channels exist for every stored interval, nothing else).
		{name: "kline.BTC/USDT.2h", err: errBadChannelInterval},
		{name: "kline.BTC/USDT", err: errBadChannelInterval},
		// Dots are separators: a market or interval may not carry one.
		{name: "ticker.B.TC/USDT", err: errUnknownWSChannel},
	}
	for _, tc := range cases {
		ch, err := parseChannel(tc.name, tc.member)
		if tc.err != nil {
			if !errors.Is(err, tc.err) {
				t.Errorf("parseChannel(%q, member=%v) error = %v, want %v", tc.name, tc.member, err, tc.err)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseChannel(%q, member=%v) unexpected error %v", tc.name, tc.member, err)
			continue
		}
		if ch.kind != tc.kind || ch.market != tc.market || ch.interval != tc.interval {
			t.Errorf("parseChannel(%q, member=%v) = %+v, want kind=%v market=%q interval=%q",
				tc.name, tc.member, ch, tc.kind, tc.market, tc.interval)
		}
	}
}
