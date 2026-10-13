// The WebSocket channel-name router (ADR-0011, ticket #44). One pure
// function decides what a channel name means and whether this socket may
// subscribe — the personal channel is the fixed word "orders" bound to
// the socket's session, so a member ID never appears in any channel name.
package api

import (
	"errors"
	"strings"

	"github.com/syaikhipin/malxchange/internal/market"
)

// channelKind enumerates the routable channel families.
type channelKind int

const (
	chTicker       channelKind = iota // "ticker" — every Market, relayed from the ticker topic
	chTickerMarket                    // "ticker.{market}" — one Market's ticker
	chDepth                           // "depth.{market}" — market.plate relayed as-is
	chTrades                          // "trades.{market}" — sanitized trade.fill projection
	chKline                           // "kline.{market}.{interval}" — bars derived from trade.fill
	chOrders                          // "orders" — member-only personal events
)

// Named subscribe errors, mapped to member-facing English sentences by
// the socket (ADR-0013). errors.Is-identity is the contract the socket
// and the tests rely on.
var (
	errUnknownWSChannel   = errors.New("unknown channel")
	errBadChannelInterval = errors.New("unknown kline interval")
	errSignInRequired     = errors.New("sign in required")
)

// channelErrorMessage maps a named parse error to the sentence the member
// sees in the error envelope. Unknown errors stay generic.
func channelErrorMessage(err error) string {
	switch {
	case errors.Is(err, errSignInRequired):
		return "Sign in to use the orders channel."
	case errors.Is(err, errBadChannelInterval):
		return "Unknown Kline interval."
	default:
		return "Unknown channel."
	}
}

// wsChannel is one parsed channel name.
type wsChannel struct {
	name     string
	kind     channelKind
	market   string          // Depth/Trades/Kline/Ticker-per-Market target
	interval market.Interval // Kline only; always a stored interval
}

// parseChannel splits a channel name into its routing parts. member tells
// whether the socket is bound to a Member session; the member-only
// "orders" channel is the sole auth decision made here. A Market that is
// not configured is NOT a parse error: the hub knows no Market list, so
// such a channel simply stays quiet until events for it exist.
func parseChannel(name string, member bool) (wsChannel, error) {
	parts := strings.Split(name, ".")
	rest := parts[1:]

	okMarket := func(s string) bool {
		halves := strings.Split(s, "/")
		return len(halves) == 2 && halves[0] != "" && halves[1] != ""
	}

	switch {
	case name == "ticker":
		return wsChannel{name: name, kind: chTicker}, nil
	case name == "orders":
		if !member {
			return wsChannel{}, errSignInRequired
		}
		return wsChannel{name: name, kind: chOrders}, nil
	case len(parts) == 2 && parts[0] == "ticker":
		if !okMarket(rest[0]) {
			return wsChannel{}, errUnknownWSChannel
		}
		return wsChannel{name: name, kind: chTickerMarket, market: rest[0]}, nil
	case len(parts) == 2 && parts[0] == "depth":
		if !okMarket(rest[0]) {
			return wsChannel{}, errUnknownWSChannel
		}
		return wsChannel{name: name, kind: chDepth, market: rest[0]}, nil
	case len(parts) == 2 && parts[0] == "trades":
		if !okMarket(rest[0]) {
			return wsChannel{}, errUnknownWSChannel
		}
		return wsChannel{name: name, kind: chTrades, market: rest[0]}, nil
	case len(parts) >= 2 && parts[0] == "kline":
		if !okMarket(rest[0]) {
			return wsChannel{}, errUnknownWSChannel
		}
		if len(parts) != 3 {
			// A kline name missing its interval segment reads as a bad
			// interval, not a bad channel: the family is named right.
			return wsChannel{}, errBadChannelInterval
		}
		iv, err := market.ParseInterval(rest[1])
		if err != nil {
			return wsChannel{}, errBadChannelInterval
		}
		return wsChannel{name: name, kind: chKline, market: rest[0], interval: iv}, nil
	default:
		return wsChannel{}, errUnknownWSChannel
	}
}
