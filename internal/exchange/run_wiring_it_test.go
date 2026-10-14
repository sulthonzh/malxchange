//go:build integration

// Regression for ticket #65: exchange.Run never mounted the member-order
// RPC — RegisterRPC (ticket #41) was only ever called from test wiring —
// so in the compose stack the api proxy's /api/uc/orders/current|history
// hit an unmounted /internal route, while order place/cancel (riding
// order.command) worked and hid the gap. This test boots the production
// Run itself behind the boot harness's mux shape (root → /internal/ →
// StripPrefix — internal/service) and drives a member journey through
// the served surface the api proxy fronts: place a resting order, see it
// on current; cancel it, see it on history. No fills, so no wallet holds
// (the review checklist's funding rule is about fills).
package exchange

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/twmb/franz-go/pkg/kfake"
	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/syaikhipin/malxchange/internal/config"
	"github.com/syaikhipin/malxchange/internal/events"
	"github.com/syaikhipin/malxchange/internal/kafka"
	"github.com/syaikhipin/malxchange/internal/migrations"
	"github.com/syaikhipin/malxchange/internal/pgmig"
	"github.com/syaikhipin/malxchange/internal/service"
)

// runStack: the real Run against a throwaway exchange-schema database
// and an in-process broker. Unlike the orders stack there is no manual
// engine: Run's own consumer applies the commands — that wiring is
// exactly what is under test.
type runStack struct {
	t        *testing.T
	producer *kgo.Client
	srv      *httptest.Server
	runErr   chan error
	n        int // envelope-id counter: fresh ids make re-produces safe
}

func newRunStack(t *testing.T) *runStack {
	t.Helper()
	ctx := context.Background()

	// Throwaway DB via the shared mintITDB helper; exchange schema only —
	// this journey rests and cancels, never fills.
	dbName := mintITDB(t, "r")

	exURL := ordersPGURL(t, dbName, "exchange")
	pool, err := pgxpool.New(ctx, exURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	_, _ = pool.Exec(ctx, `CREATE SCHEMA IF NOT EXISTS exchange`)
	if err := pgmig.Run(exURL, migrations.FS, "exchange"); err != nil {
		t.Fatalf("exchange migrations: %v", err)
	}

	broker, err := kfake.NewCluster(kfake.NumBrokers(1))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(broker.Close)
	addrs := broker.ListenAddrs()
	if len(addrs) == 0 {
		t.Fatal("kfake gave no listen addresses")
	}
	cfg := config.Kafka{Seeds: []string{addrs[0]}} // no SASL against kfake
	producer, err := kafka.NewProducer(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(producer.Close)
	if err := broker.CreateTopic(events.TopicOrderCommand, 1, nil); err != nil {
		t.Fatal(err)
	}

	// The production mux shape (internal/service): the boot harness
	// mounts Deps.Internal under /internal/ with the prefix stripped.
	internal := http.NewServeMux()
	root := http.NewServeMux()
	root.Handle("/internal/", http.StripPrefix("/internal", internal))
	srv := httptest.NewServer(root)
	t.Cleanup(srv.Close)

	s := &runStack{t: t, producer: producer, srv: srv, runErr: make(chan error, 1)}
	runCtx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() {
		s.runErr <- Run(runCtx, service.Deps{
			Cfg:      config.Config{Kafka: cfg},
			Pool:     pool,
			Producer: producer,
			Log:      slog.New(slog.NewTextHandler(io.Discard, nil)),
			Internal: internal,
		})
	}()
	return s
}

// produce publishes one command with a fresh envelope id: the inbox
// dedupes on (key, envelope id) and the orders row conflicts do nothing,
// so re-producing a command can never double-apply it.
func (s *runStack) produce(cmd events.OrderCommand) {
	s.t.Helper()
	s.n++
	ctx := context.Background()
	_, body, err := events.New(fmt.Sprintf("runw-%s-%d", cmd.OrderID, s.n), cmd.Market,
		events.TopicOrderCommand, time.Now().UTC(), cmd)
	if err != nil {
		s.t.Fatal(err)
	}
	if err := s.producer.ProduceSync(ctx, &kgo.Record{
		Topic: events.TopicOrderCommand, Key: []byte(cmd.Market), Value: body,
	}).FirstErr(); err != nil {
		s.t.Fatal(err)
	}
}

// get performs one GET against the Run-wired surface.
func (s *runStack) get(path string) (int, []byte) {
	s.t.Helper()
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Get(s.srv.URL + path)
	if err != nil {
		s.t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		s.t.Fatal(err)
	}
	return resp.StatusCode, body
}

// awaitOrder polls the member surface until orderID surfaces and returns
// it. A 404 fails the test only after a boot grace window: Run registers
// the RPC after Recover and the consumer dial, so on a correctly wired
// tree the first poll of a cold run can legitimately see the mux still
// empty; 404s past the window are the unmounted-route symptom this
// ticket fixes. The command is re-produced periodically: Run's consumer
// group starts at the fresh topic's end offset, so a command produced
// before the group assigned can be skipped — a fresh envelope id applies
// it safely (see produce's dedupe note).
func (s *runStack) awaitOrder(cmd events.OrderCommand, path, orderID string) map[string]any {
	s.t.Helper()
	start := time.Now()
	fastFail := start.Add(10 * time.Second)
	deadline := start.Add(30 * time.Second)
	var last []byte
	for i := 0; ; i++ {
		select {
		case err := <-s.runErr:
			s.t.Fatalf("Run exited early: %v", err)
		default:
		}
		if i%30 == 0 {
			s.produce(cmd)
		}
		code, body := s.get(path)
		last = body
		if code == http.StatusNotFound && time.Now().After(fastFail) {
			s.t.Fatalf("GET %s = 404 — Run did not mount the member-order RPC (ticket #65)", path)
		}
		if code == http.StatusOK {
			for _, o := range asOrders(s.t, code, body) {
				if o["orderId"] == orderID {
					return o
				}
			}
		}
		if time.Now().After(deadline) {
			s.t.Fatalf("order %s never surfaced on %s (last body: %s)", orderID, path, last)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// awaitDrained polls the path until it answers an empty array.
func (s *runStack) awaitDrained(path string) {
	s.t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		code, body := s.get(path)
		if code == http.StatusOK && len(asOrders(s.t, code, body)) == 0 {
			return
		}
		if time.Now().After(deadline) {
			s.t.Fatalf("%s never drained (last body: %s)", path, body)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// TestRunWiresMemberOrderRPC: the production Run serves the member-order
// surface the api proxy fronts — a placed order rests on current, a
// cancel lands it in history with its (empty) fills array, and current
// drains.
func TestRunWiresMemberOrderRPC(t *testing.T) {
	s := newRunStack(t)

	current := s.awaitOrder(rpcPlace("runw-1", "greg", "sell", "limit", "999999", "0.5"),
		"/internal/orders/current?member=greg", "runw-1")
	checkOrder(t, current, map[string]any{
		"orderId": "runw-1", "market": "BTC/USDT", "side": "sell", "kind": "limit",
		"price": "999999", "amount": "0.5", "filledAmount": "0", "turnover": "0",
		"status": "open", "createdAt": nonEmpty,
	})
	absent(t, current, "fills", "canceledAt")

	hist := s.awaitOrder(events.OrderCommand{Op: "cancel", OrderID: "runw-1",
		Member: "greg", Market: "BTC/USDT"},
		"/internal/orders/history?member=greg", "runw-1")
	checkOrder(t, hist, map[string]any{
		"orderId": "runw-1", "market": "BTC/USDT", "side": "sell", "kind": "limit",
		"price": "999999", "amount": "0.5", "filledAmount": "0", "turnover": "0",
		"status": "canceled", "createdAt": nonEmpty, "canceledAt": nonEmpty,
	})
	// History carries fills; nothing filled here, so an empty array.
	checkFills(t, hist, []map[string]any{})

	// The cancel and the history move are one apply, so current drains.
	s.awaitDrained("/internal/orders/current?member=greg")
}
