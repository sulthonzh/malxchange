//go:build integration

// Ticker integration tests (ticket #32): real Postgres + the in-process
// broker, on the same stack as the #30 consumer IT. The Ticker is a pure
// function of the Market's own trade history, so a deterministic Fill
// sequence must yield exactly the hand-computed last / 24h high / 24h low
// / turnover — identical on the ticker topic and at internal REST.
package market

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/syaikhipin/malxchange/internal/events"
)

// seedTickerFills produces three funded Fills on one Market at fixed ages
// inside the 24h window: 50000.1 / 49500.001 / 51500.5 quote per unit.
func (s *itStack) seedTickerFills(t *testing.T, at time.Time) {
	t.Helper()
	fills := []struct {
		id       string
		price    string
		amount   string
		turnover string
		age      time.Duration
	}{
		{"ticker-fill-1", "50000.1", "0.0002", "10.00002", 3 * time.Hour},
		{"ticker-fill-2", "49500.001", "0.0001", "4.95", 2 * time.Hour},
		{"ticker-fill-3", "51500.5", "0.001", "51.5005", time.Hour},
	}
	for _, f := range fills {
		s.fundAndFreeze(t, "alice", "bob", "BTC", "USDT", f.amount, f.turnover)
		s.produceFill(t, f.id, at.Add(-f.age), events.TradeFill{
			Market: "BTC/USDT", Seq: 1,
			BuyOrderID: "ord-t", SellOrderID: "ord-m",
			BuyMemberID: "bob", SellMemberID: "alice",
			TakerSide: "buy",
			Price:     f.price, Amount: f.amount, Turnover: f.turnover,
		})
	}
	s.drain(t)
}

// TestTickerRESTMatchesDeterministicFills: the REST answers are the
// hand-computed 24h summary of the deterministic Fill sequence — one
// Market, the all-markets map, and the zero-Fill Market that is known only
// through its last-known Depth (acceptance: REST matches the published
// stream, zero-trade Markets publish empty last/range).
func TestTickerRESTMatchesDeterministicFills(t *testing.T) {
	s := newStack(t)
	s.newConsumer("ticker-rest-1")
	s.seedTickerFills(t, time.Now())

	// A second Market known through config activity only (a Depth publish,
	// the shape every exchange boot bursts): zero Fills, so empty last/range.
	at := time.Now()
	s.producePlate(t, "ticker-plate-eth", at, events.Plate{
		Market: "ETH/USDT",
		Bids:   []events.PlateLevel{{Price: "3000", Amount: "4"}},
	})
	s.drain(t)

	// 10.00002 + 4.95 + 51.5005, decimal-exact.
	wantBTC := events.Ticker{
		Market: "BTC/USDT", Last: "51500.5", High: "51500.5",
		Low: "49500.001", Turnover: "66.45052",
	}
	wantEmpty := events.Ticker{Market: "ETH/USDT", Turnover: "0"}

	status, body := s.getREST(t, "/internal/ticker/BTC/USDT")
	if status != http.StatusOK {
		t.Fatalf("GET /internal/ticker/BTC/USDT = %d, want 200", status)
	}
	var got events.Ticker
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("ticker response is not JSON: %v\n%s", err, body)
	}
	if got != wantBTC {
		t.Errorf("one-Market ticker = %+v, want %+v", got, wantBTC)
	}

	// The all-markets map: keyed by Market symbol, never positional —
	// every known Market present, values identical to the single-Market GET.
	status, body = s.getREST(t, "/internal/tickers")
	if status != http.StatusOK {
		t.Fatalf("GET /internal/tickers = %d, want 200", status)
	}
	var all map[string]events.Ticker
	if err := json.Unmarshal(body, &all); err != nil {
		t.Fatalf("tickers response is not a JSON object: %v\n%s", err, body)
	}
	if all["BTC/USDT"] != wantBTC {
		t.Errorf("map BTC/USDT = %+v, want the same %+v as the single GET", all["BTC/USDT"], wantBTC)
	}
	if all["ETH/USDT"] != wantEmpty {
		t.Errorf("map ETH/USDT = %+v, want empty last/range %+v", all["ETH/USDT"], wantEmpty)
	}
	if len(all) != 2 {
		t.Errorf("map has %d Markets, want exactly the 2 known ones: %+v", len(all), all)
	}

	// The zero-Fill Market's own endpoint answers the same empty summary.
	status, body = s.getREST(t, "/internal/ticker/ETH/USDT")
	if status != http.StatusOK {
		t.Fatalf("GET /internal/ticker/ETH/USDT = %d, want 200", status)
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("zero-fill ticker response is not JSON: %v\n%s", err, body)
	}
	if got != wantEmpty {
		t.Errorf("zero-fill ticker = %+v, want %+v", got, wantEmpty)
	}

	// A Market known to nobody answers the empty summary too — the front
	// never waits for a first tick, and never sees a 404 shape.
	status, body = s.getREST(t, "/internal/ticker/SOL/USDT")
	if status != http.StatusOK {
		t.Fatalf("unknown-Market GET = %d, want 200", status)
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("unknown-Market ticker response is not JSON: %v\n%s", err, body)
	}
	if want := (events.Ticker{Market: "SOL/USDT", Turnover: "0"}); got != want {
		t.Errorf("unknown-Market ticker = %+v, want %+v", got, want)
	}
}

// TestTickerWindowFollowsClock: a Fill older than 24h is outside the
// rolling window — the Ticker summary only ever reflects the window, so
// the same history summarized a day later answers empty.
func TestTickerWindowFollowsClock(t *testing.T) {
	s := newStack(t)
	s.newConsumer("ticker-window-1")

	old := time.Now().Add(-25 * time.Hour)
	s.fundAndFreeze(t, "alice", "bob", "BTC", "USDT", "0.0002", "10.00002")
	s.produceFill(t, "ticker-fill-old", old, events.TradeFill{
		Market: "BTC/USDT", Seq: 1,
		BuyOrderID: "ord-t", SellOrderID: "ord-m",
		BuyMemberID: "bob", SellMemberID: "alice",
		TakerSide: "buy",
		Price:     "50000.1", Amount: "0.0002", Turnover: "10.00002",
	})
	s.drain(t)

	// The old Fill still makes the Market known (it has history), but the
	// 24h window over it is empty: honestly empty last/range, zero Turnover.
	status, body := s.getREST(t, "/internal/ticker/BTC/USDT")
	if status != http.StatusOK {
		t.Fatalf("GET = %d, want 200", status)
	}
	var got events.Ticker
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("ticker response is not JSON: %v\n%s", err, body)
	}
	if want := (events.Ticker{Market: "BTC/USDT", Turnover: "0"}); got != want {
		t.Errorf("rolled-out ticker = %+v, want %+v", got, want)
	}
}
