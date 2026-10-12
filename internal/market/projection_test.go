package market

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/syaikhipin/malxchange/internal/events"
)

// TestTradeWireStripsIdentifiers pins the sanitized wire contract: a Trade
// carries exactly fill ID, price, amount, turnover, taker side, time — no
// Member/Order identifier field exists to leak (ticket #30, ADR-0011).
func TestTradeWireStripsIdentifiers(t *testing.T) {
	tr := Trade{
		ID:        "fill-0001",
		Price:     "50000.10",
		Amount:    "0.0002",
		Turnover:  "10.00002",
		TakerSide: "buy",
		Time:      time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC),
	}
	b, err := json.Marshal(tr)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(b, &fields); err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{
		"id": true, "price": true, "amount": true, "turnover": true, "takerSide": true, "time": true,
	}
	if len(fields) != len(want) {
		t.Errorf("Trade wire fields = %v, want exactly %v", keys(fields), want)
	}
	for k := range want {
		if _, ok := fields[k]; !ok {
			t.Errorf("Trade wire missing field %q", k)
		}
	}
	for _, banned := range []string{"member", "order"} {
		if strings.Contains(string(b), banned) {
			t.Errorf("Trade wire carries %q:\n%s", banned, b)
		}
	}
}

func keys(m map[string]json.RawMessage) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// TestTradeLimitParsing: absent limit = the default; the accepted range is
// explicit, anything else is a bad request.
func TestTradeLimitParsing(t *testing.T) {
	t.Parallel()
	cases := []struct {
		raw  string
		want int
		bad  bool
	}{
		{raw: "", want: defaultTradeLimit},
		{raw: "1", want: 1},
		{raw: "1000", want: 1000},
		{raw: "0", bad: true},
		{raw: "-5", bad: true},
		{raw: "1001", bad: true},
		{raw: "abc", bad: true},
		{raw: "1.5", bad: true},
	}
	for _, tc := range cases {
		req := httptest.NewRequest(http.MethodGet, "/trades/BTC/USDT?limit="+url.QueryEscape(tc.raw), nil)
		got, err := tradeLimit(req)
		if tc.bad {
			if !errors.Is(err, errBadLimit) {
				t.Errorf("limit %q: err = %v, want errBadLimit", tc.raw, err)
			}
			continue
		}
		if err != nil {
			t.Errorf("limit %q: %v", tc.raw, err)
		} else if got != tc.want {
			t.Errorf("limit %q = %d, want %d", tc.raw, got, tc.want)
		}
	}
}

// TestValidateFill: payload-level contract enforcement keeps poisoned
// messages out of the transactions (they park at the consumer instead).
func TestValidateFill(t *testing.T) {
	t.Parallel()
	valid := events.TradeFill{
		Market: "BTC/USDT", TakerSide: "buy",
		Price: "50000.10", Amount: "0.0002", Turnover: "10.00002",
	}
	if err := validateFill(valid); err != nil {
		t.Errorf("valid fill rejected: %v", err)
	}

	noMarket := valid
	noMarket.Market = ""
	if err := validateFill(noMarket); err == nil {
		t.Error("empty market accepted")
	}

	badSide := valid
	badSide.TakerSide = "left"
	if err := validateFill(badSide); err == nil {
		t.Error("unknown taker side accepted")
	}

	badPrice := valid
	badPrice.Price = "10,00"
	if err := validateFill(badPrice); err == nil {
		t.Error("non-decimal price accepted")
	}
}

// TestValidatePlate: every published level must carry decimal strings.
func TestValidatePlate(t *testing.T) {
	t.Parallel()
	valid := events.Plate{
		Market: "BTC/USDT",
		Bids:   []events.PlateLevel{{Price: "49999.90", Amount: "1.5"}},
	}
	if err := validatePlate(valid); err != nil {
		t.Errorf("valid plate rejected: %v", err)
	}

	noMarket := valid
	noMarket.Market = ""
	if err := validatePlate(noMarket); err == nil {
		t.Error("empty market accepted")
	}

	badLevel := valid
	badLevel.Bids[0].Amount = "lots"
	if err := validatePlate(badLevel); err == nil {
		t.Error("non-decimal level amount accepted")
	}
}

// TestPayloadOfRoutesTypes: envelope type decides the applier; anything
// else is an error so a misrouted envelope surfaces loudly.
func TestPayloadOfRoutesTypes(t *testing.T) {
	t.Parallel()
	_, body, err := events.New("f1", "BTC/USDT", events.TopicTradeFill,
		time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC), events.TradeFill{
			Market: "BTC/USDT", TakerSide: "sell",
			Price: "1", Amount: "2", Turnover: "2",
		})
	if err != nil {
		t.Fatal(err)
	}
	decodedFill, err := payloadOf(mustEnvelope(t, body))
	if err != nil {
		t.Fatalf("trade.fill rejected: %v", err)
	}
	if decodedFill.fill == nil || decodedFill.plate != nil {
		t.Errorf("trade.fill routed to fill=%v plate=%v", decodedFill.fill != nil, decodedFill.plate != nil)
	}
	if decodedFill.inboxKey() != "fill:BTC/USDT" {
		t.Errorf("inbox key = %q, want fill:BTC/USDT", decodedFill.inboxKey())
	}

	_, body, err = events.New("p1", "BTC/USDT", events.TopicMarketPlate,
		time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC), events.Plate{
			Market: "BTC/USDT",
			Asks:   []events.PlateLevel{{Price: "50000.10", Amount: "0.7"}},
		})
	if err != nil {
		t.Fatal(err)
	}
	decodedPlate, err := payloadOf(mustEnvelope(t, body))
	if err != nil {
		t.Fatalf("market.plate rejected: %v", err)
	}
	if decodedPlate.plate == nil || decodedPlate.fill != nil {
		t.Errorf("market.plate routed to fill=%v plate=%v", decodedPlate.fill != nil, decodedPlate.plate != nil)
	}
	if decodedPlate.inboxKey() != "plate:BTC/USDT" {
		t.Errorf("inbox key = %q, want plate:BTC/USDT", decodedPlate.inboxKey())
	}
	if _, err := payloadOf(mustEnvelope(t, []byte(`{"id":"x","key":"BTC/USDT","type":"ticker","data":{}}`))); err == nil {
		t.Error("unexpected topic accepted")
	}
}

// mustEnvelope decodes wire bytes the way the consumer does, returning the
// Envelope payloadOf receives.
func mustEnvelope(t *testing.T, body []byte) events.Envelope {
	t.Helper()
	env, err := events.Decode(body)
	if err != nil {
		t.Fatalf("test envelope invalid: %v", err)
	}
	return env
}
