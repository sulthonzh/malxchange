//go:build integration

// market.updated integration tests (ticket #29): real Postgres + the
// in-process Kafka broker. The seam under test is the topic: exchange
// must surface the full Market row at boot (after Book recovery) and on
// every accepted config-watch change, keyed by Market symbol, through
// the transactional outbox.
package exchange

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/syaikhipin/malxchange/internal/events"
	"github.com/syaikhipin/malxchange/internal/kafka"
	"github.com/syaikhipin/malxchange/internal/outbox"
)

// startRelay runs the outbox relay against the in-process broker so
// enqueued events actually land on their topics.
func startRelay(t *testing.T, s *itStack) context.CancelFunc {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	go func() { _ = outbox.NewRelay(s.pool, kafka.Publisher{Client: s.producer}).Run(ctx) }()
	t.Cleanup(cancel)
	return cancel
}

// updatedFeed reads market.updated with its own consumer group, from the
// first offset, committing every accepted envelope.
type updatedFeed struct{ cl *kgo.Client }

func startUpdatedFeed(t *testing.T, s *itStack, group string) *updatedFeed {
	t.Helper()
	if err := s.broker.CreateTopic(events.TopicMarketUpdated, 1, nil); err != nil {
		t.Fatal(err)
	}
	cl, err := kafka.NewConsumer(s.cfg, group, []string{events.TopicMarketUpdated},
		kgo.ConsumeResetOffset(kgo.NewOffset().AtStart()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cl.Close)
	return &updatedFeed{cl: cl}
}

// await returns the next n market.updated envelopes, committing as they
// land; it fails the test when they do not all arrive within timeout.
func (f *updatedFeed) await(t *testing.T, n int, timeout time.Duration) []events.Envelope {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var got []events.Envelope
	var pending []*kgo.Record
	commit := func() {
		if len(pending) == 0 {
			return
		}
		f.cl.MarkCommitRecords(pending...)
		if err := f.cl.CommitMarkedOffsets(context.Background()); err != nil {
			t.Fatalf("commit market.updated offsets: %v", err)
		}
		pending = pending[:0]
	}
	defer commit()
	for len(got) < n {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %d market.updated events (got %d)", n, len(got))
		}
		pollCtx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
		fetches := f.cl.PollRecords(pollCtx, 100)
		fetches.EachError(func(_ string, _ int32, err error) {
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Errorf("market.updated fetch error: %v", err)
			}
		})
		fetches.EachRecord(func(r *kgo.Record) {
			env, err := events.Decode(r.Value)
			if err != nil {
				t.Fatalf("decode market.updated: %v", err)
			}
			if env.Type != events.TopicMarketUpdated {
				t.Fatalf("envelope type %q on the market.updated topic", env.Type)
			}
			got = append(got, env)
			pending = append(pending, r)
		})
		commit()
		cancel()
		time.Sleep(10 * time.Millisecond)
	}
	return got
}

// awaitNone asserts the topic stays quiet for d — a refused or unchanged
// config must not publish.
func (f *updatedFeed) awaitNone(t *testing.T, d time.Duration) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		pollCtx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		fetches := f.cl.PollRecords(pollCtx, 100)
		n := 0
		fetches.EachRecord(func(*kgo.Record) { n++ })
		cancel()
		if n > 0 {
			t.Fatalf("expected no market.updated events, got %d", n)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// decodeUpdated unmarshals one envelope's full-row payload.
func decodeUpdated(t *testing.T, env events.Envelope) events.MarketUpdated {
	t.Helper()
	var got events.MarketUpdated
	if err := json.Unmarshal(env.Data, &got); err != nil {
		t.Fatalf("unmarshal market.updated payload: %v", err)
	}
	if got.Market != env.Key {
		t.Errorf("payload market %q does not match envelope key %q", got.Market, env.Key)
	}
	return got
}

// wantSeedMarket is the full-row expectation for the 0002 migration seeds,
// as decimal strings exactly as Postgres normalizes NUMERIC text.
func wantSeedMarket(market, base, tick, lot string, sort int, featured bool) events.MarketUpdated {
	return events.MarketUpdated{
		Market: market, BaseCoin: base, QuoteCoin: "USDT",
		BaseScale: 8, QuoteScale: 2,
		TickSize: tick, LotSize: lot,
		MakerFeeRate: "0.001", TakerFeeRate: "0.001",
		MinPrice: "", MaxPrice: "", MinAmount: "0", MaxAmount: "", MinTurnover: "5",
		MarketOrdersEnabled: true,
		Enabled:             true,
		Visible:             true,
		Sort:                sort,
		Zone:                "main",
		Featured:            featured,
	}
}

// TestBootPublishesMarketUpdatedPerMarket: on exchange boot one
// market.updated per known Market lands on the topic, carrying the full
// config row as decimal strings, keyed by Market symbol (acceptance 1).
func TestBootPublishesMarketUpdatedPerMarket(t *testing.T) {
	s := newStack(t)
	startRelay(t, s)

	f := startUpdatedFeed(t, s, "boot-burst-it")
	envs := f.await(t, 2, 30*time.Second)

	byMarket := map[string]events.Envelope{}
	for _, env := range envs {
		if _, dup := byMarket[env.Key]; dup {
			t.Errorf("duplicate envelope key %q in the boot burst", env.Key)
		}
		byMarket[env.Key] = env
	}
	want := map[string]events.MarketUpdated{
		"BTC/USDT": wantSeedMarket("BTC/USDT", "BTC", "0.10", "0.0001", 1, true),
		"ETH/USDT": wantSeedMarket("ETH/USDT", "ETH", "0.01", "0.001", 2, false),
	}
	for market, wantRow := range want {
		env, ok := byMarket[market]
		if !ok {
			t.Fatalf("no market.updated for %s (got keys %v)", market, keysOf(byMarket))
		}
		if env.ID == "" {
			t.Errorf("%s: empty envelope id", market)
		}
		if got := decodeUpdated(t, env); got != wantRow {
			t.Errorf("%s payload:\n got %+v\nwant %+v", market, got, wantRow)
		}
	}
}

// keysOf exists only to make a failed assertion name the markets seen.
func keysOf(m map[string]events.Envelope) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
