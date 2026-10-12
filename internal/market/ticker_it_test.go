//go:build integration

// Ticker integration tests (ticket #32): real Postgres + the in-process
// broker, on the same stack as the #30 consumer IT. The Ticker is a pure
// function of the Market's own trade history, so a deterministic Fill
// sequence must yield exactly the hand-computed last / 24h high / 24h low
// / turnover — identical on the ticker topic and at internal REST.
package market

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/syaikhipin/malxchange/internal/config"
	"github.com/syaikhipin/malxchange/internal/events"
	"github.com/syaikhipin/malxchange/internal/kafka"
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

// tickerFeed reads the ticker topic with its own consumer group from the
// first offset, keeping every envelope it sees.
type tickerFeed struct {
	cl  *kgo.Client
	got []events.Envelope
}

func newTickerFeed(t *testing.T, cfg config.Kafka) *tickerFeed {
	t.Helper()
	cl, err := kafka.NewConsumer(cfg, "ticker-feed-"+uuid.NewString(),
		[]string{events.TopicTicker}, kgo.ConsumeResetOffset(kgo.NewOffset().AtStart()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cl.Close)
	return &tickerFeed{cl: cl}
}

// collect polls until the deadline, appending every decoded ticker
// envelope.
func (f *tickerFeed) collect(t *testing.T, d time.Duration) []events.Envelope {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		pollCtx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
		fetches := f.cl.PollRecords(pollCtx, 100)
		cancel()
		fetches.EachError(func(_ string, _ int32, err error) {
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Errorf("ticker feed error: %v", err)
			}
		})
		fetches.EachRecord(func(r *kgo.Record) {
			env, err := events.Decode(r.Value)
			if err != nil {
				t.Fatalf("decode ticker envelope: %v", err)
			}
			if env.Type != events.TopicTicker {
				t.Fatalf("envelope type %q on the ticker topic", env.Type)
			}
			f.got = append(f.got, env)
		})
		if err := f.cl.CommitMarkedOffsets(context.Background()); err != nil {
			t.Fatalf("commit ticker offsets: %v", err)
		}
	}
	return f.got
}

// TestTickerStreamAndRESTMatchUnderLoad: the full seam (ticket #32) —
// deterministic Fills land on the topic through the real ≤1s publish loop;
// a zero-Fill Market publishes its empty summary within the cadence; under
// sustained Fill load no Market exceeds one message per second; and REST
// answers exactly what the stream published.
func TestTickerStreamAndRESTMatchUnderLoad(t *testing.T) {
	s := newStack(t)
	s.newConsumer("ticker-stream-1")
	if err := s.broker.CreateTopic(events.TopicTicker, 1, nil); err != nil {
		t.Fatal(err)
	}
	feed := newTickerFeed(t, s.cfg)

	// Seed state: three Fills on BTC/USDT, a Depth-only (zero-Fill)
	// publish on ETH/USDT — the shape every exchange boot bursts.
	s.seedTickerFills(t, time.Now())
	s.producePlate(t, "ticker-plate-eth", time.Now(), events.Plate{
		Market: "ETH/USDT",
		Bids:   []events.PlateLevel{{Price: "3000", Amount: "4"}},
	})
	s.drain(t)

	// The real publish loop.
	loopCtx, cancel := context.WithCancel(context.Background())
	loopDone := make(chan error, 1)
	go func() {
		loopDone <- PublishTickers(loopCtx, s.pool, slog.New(slog.NewTextHandler(io.Discard, nil)), s.producer)
	}()
	defer func() {
		cancel()
		if err := <-loopDone; err != nil {
			t.Errorf("publish loop: %v", err)
		}
	}()

	// Sustained Fill load: 40 priced Fills over ~2s, each funded both
	// sides. Price rises 50000+i, turnover a flat 10. Together with the
	// three seeded Fills above (50000.1 / 49500.001 / 51500.5, turnover
	// 66.45052) the final summary is hand-derived: the newest Fill is the
	// last load Fill (50039), the seeded 51500.5 stays the high, the
	// seeded 49500.001 the low, turnover 66.45052 + 400 = 466.45052.
	const load = 40
	for i := range load {
		price := fmt.Sprintf("%d", 50000+i)
		s.fundAndFreeze(t, "alice", "bob", "BTC", "USDT", "0.0002", "10")
		s.produceFill(t, fmt.Sprintf("ticker-load-%d", i), time.Now(), events.TradeFill{
			Market: "BTC/USDT", Seq: uint64(i + 1),
			BuyOrderID: "ord-t", SellOrderID: "ord-m",
			BuyMemberID: "bob", SellMemberID: "alice",
			TakerSide: "buy",
			Price:     price, Amount: "0.0002", Turnover: "10",
		})
		time.Sleep(50 * time.Millisecond)
	}
	s.drain(t)

	// Let one pass fold the final state before the loop stops, so the
	// last published value is the current one (≤1s cadence).
	time.Sleep(1500 * time.Millisecond)
	cancel()
	if err := <-loopDone; err != nil {
		t.Fatalf("publish loop: %v", err)
	}

	envs := feed.collect(t, 2*time.Second)
	if len(envs) == 0 {
		t.Fatal("no ticker messages published")
	}

	// Cadence bound: at most one message per Market per second, counted by
	// the envelope's own publish stamp, under sustained load.
	perSecond := map[string]int{}
	var btcCount, ethCount int
	for _, env := range envs {
		perSecond[fmt.Sprintf("%s@%d", env.Key, env.OccurredAt.Unix())]++
		switch env.Key {
		case "BTC/USDT":
			btcCount++
		case "ETH/USDT":
			ethCount++
		default:
			t.Errorf("unknown Market %q on the ticker topic", env.Key)
		}
	}
	for k, n := range perSecond {
		if n > 1 {
			t.Errorf("cadence bound broken: %q published %d messages in one second", k, n)
		}
	}
	// The load actually changed the value pass over pass (the bound is not
	// passing vacuously), and the zero-Fill Market published within it.
	if btcCount < 2 {
		t.Errorf("BTC/USDT published %d messages under load, want several changing passes", btcCount)
	}
	if ethCount < 1 {
		t.Errorf("ETH/USDT never published its empty summary")
	}

	// The stream's final values and REST agree, decimal-exact: the last
	// published envelope equals both REST endpoints.
	var lastBTC, lastETH events.Ticker
	for _, env := range envs {
		var payload events.Ticker
		if err := json.Unmarshal(env.Data, &payload); err != nil {
			t.Fatalf("ticker payload: %v\n%s", err, env.Data)
		}
		if payload.Market != env.Key {
			t.Errorf("payload market %q does not match record key %q", payload.Market, env.Key)
		}
		switch env.Key {
		case "BTC/USDT":
			lastBTC = payload
		case "ETH/USDT":
			lastETH = payload
		}
	}
	wantBTC := events.Ticker{
		Market: "BTC/USDT", Last: "50039", High: "51500.5",
		Low: "49500.001", Turnover: "466.45052",
	}
	if lastBTC != wantBTC {
		t.Errorf("final stream ticker = %+v, want the hand-derived %+v", lastBTC, wantBTC)
	}
	wantETH := events.Ticker{Market: "ETH/USDT", Turnover: "0"}
	if lastETH != wantETH {
		t.Errorf("final stream ticker = %+v, want the empty %+v", lastETH, wantETH)
	}

	status, body := s.getREST(t, "/internal/ticker/BTC/USDT")
	if status != http.StatusOK {
		t.Fatalf("GET ticker = %d, want 200", status)
	}
	var restBTC events.Ticker
	if err := json.Unmarshal(body, &restBTC); err != nil {
		t.Fatalf("REST ticker: %v\n%s", err, body)
	}
	if restBTC != lastBTC {
		t.Errorf("REST %+v does not match the published stream %+v", restBTC, lastBTC)
	}

	status, body = s.getREST(t, "/internal/tickers")
	if status != http.StatusOK {
		t.Fatalf("GET tickers = %d, want 200", status)
	}
	var all map[string]events.Ticker
	if err := json.Unmarshal(body, &all); err != nil {
		t.Fatalf("tickers map: %v\n%s", err, body)
	}
	if all["BTC/USDT"] != lastBTC || all["ETH/USDT"] != lastETH {
		t.Errorf("all-markets map %+v does not match the published stream (BTC %+v ETH %+v)",
			all, lastBTC, lastETH)
	}
}
