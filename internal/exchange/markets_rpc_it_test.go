//go:build integration

// The Market-config admin seam over the served internal HTTP surface
// (ticket #61): PUT persists and the engine's config watch live-applies
// it and publishes market.updated; the freeze guard refuses tick/lot/
// scale edits while Orders rest (409 at write time — the row never
// diverges from what the engine will apply); unknown Markets are 404
// and invalid configs are 400; and /engine/status now reports the
// command consumer's lag next to the per-Market Ready/open/Halted view.
package exchange

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/syaikhipin/malxchange/internal/events"
	"github.com/syaikhipin/malxchange/internal/kafka"
)

// serveAdminRPC mirrors run.go's internal wiring: the RPC routes plus
// the engine-status handler behind the boot harness's /internal strip.
func serveAdminRPC(t *testing.T, s *itStack) *httptest.Server {
	t.Helper()
	rpc := &AdminRPC{DB: s.pool, Log: nil}
	root := http.NewServeMux()
	internal := http.NewServeMux()
	internal.HandleFunc("GET /engine/status", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(StatusWithLag(r.Context(), s.engine, s.producer))
	})
	RegisterAdminRPC(internal, rpc)
	root.Handle("/internal/", http.StripPrefix("/internal", internal))
	srv := httptest.NewServer(root)
	t.Cleanup(srv.Close)
	return srv
}

func doJSON(t *testing.T, srv *httptest.Server, method, path, body string) (int, string) {
	t.Helper()
	var rdr *strings.Reader
	if body == "" {
		rdr = strings.NewReader("")
	} else {
		rdr = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, srv.URL+path, rdr)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Mal-Actor", "ops@exchange.example.com")
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, string(raw)
}

// TestMarketConfigEditPersistsAndApplies: the write path lands, the
// engine's config watch applies it within one tick, and market.updated
// carries the new row — the derived-market copy follows (acceptance 2).
func TestMarketConfigEditPersistsAndApplies(t *testing.T) {
	s := newStack(t)
	createExchangeTopics(t, s)
	startRelay(t, s)
	stopWatch := startConfigWatch(t, s, 200*time.Millisecond)
	defer stopWatch()
	f := startUpdatedFeed(t, s, "cfg-edit-it")

	// Drain the boot burst so the next awaited event is the edit's.
	f.await(t, 2, 30*time.Second)

	srv := serveAdminRPC(t, s)

	// The list surfaces the full config rows.
	status, body := doJSON(t, srv, "GET", "/internal/markets", "")
	if status != http.StatusOK {
		t.Fatalf("list markets: %d %s", status, body)
	}
	var listed []marketView
	if err := json.Unmarshal([]byte(body), &listed); err != nil {
		t.Fatalf("list body: %s", body)
	}
	if len(listed) != 2 {
		t.Fatalf("want the two seed Markets, got %s", body)
	}

	// An accepted edit persists and echoes the merged row.
	status, body = doJSON(t, srv, "PUT", "/internal/markets/BTC/USDT",
		`{"makerFeeRate":"0.002","minTurnover":"10","visible":false}`)
	if status != http.StatusOK {
		t.Fatalf("edit: %d %s", status, body)
	}
	var updated marketView
	if err := json.Unmarshal([]byte(body), &updated); err != nil {
		t.Fatalf("edit body: %s", body)
	}
	if updated.MakerFeeRate != "0.002" || updated.MinTurnover != "10" || updated.Visible {
		t.Fatalf("edit echo wrong: %s", body)
	}

	// The row persists (read back through the seam).
	status, body = doJSON(t, srv, "GET", "/internal/markets", "")
	if status != http.StatusOK {
		t.Fatalf("re-list: %d %s", status, body)
	}
	if err := json.Unmarshal([]byte(body), &listed); err != nil {
		t.Fatal(err)
	}
	for _, m := range listed {
		if m.Market != "BTC/USDT" {
			continue
		}
		if m.MakerFeeRate != "0.002" || m.MinTurnover != "10" || m.Visible {
			t.Fatalf("edit did not persist: %s", body)
		}
	}

	// The engine applies and publishes within the watch tick.
	envs := f.await(t, 1, 15*time.Second)
	row := decodeUpdated(t, envs[0])
	if row.Market != "BTC/USDT" || row.MakerFeeRate != "0.002" || row.MinTurnover != "10" || row.Visible {
		t.Fatalf("market.updated payload wrong: %+v", row)
	}

	// Unknown Market → 404; invalid config → 400; collar ordering → 400.
	if status, _ = doJSON(t, srv, "PUT", "/internal/markets/NOPE/USDT", `{"visible":false}`); status != http.StatusNotFound {
		t.Fatalf("unknown market: %d", status)
	}
	if status, _ = doJSON(t, srv, "PUT", "/internal/markets/BTC/USDT", `{"minPrice":"0"}`); status != http.StatusBadRequest {
		t.Fatalf("invalid collar: %d", status)
	}
	if status, body = doJSON(t, srv, "PUT", "/internal/markets/BTC/USDT", `{"minPrice":"10","maxPrice":"5"}`); status != http.StatusBadRequest {
		t.Fatalf("crossed collars: %d %s", status, body)
	}
}

// TestMarketConfigFreezeGuardRefusesWhileOrdersRest: a quantization
// edit with resting Orders is refused at write time (409) and the row
// keeps the working spec; once the book empties the same edit lands
// (acceptance 2, the guard half).
func TestMarketConfigFreezeGuardRefusesWhileOrdersRest(t *testing.T) {
	s := newStack(t)
	srv := serveAdminRPC(t, s)
	ctx := context.Background()

	// Fixture: one resting order, exactly the row the engine persists.
	_, err := s.pool.Exec(ctx, `
		INSERT INTO orders (id, market, member_id, side, kind, price, amount, filled_amount, turnover, status, seq)
		VALUES ('ord-freeze-1', 'BTC/USDT', 'm-1', 'buy', 'limit', 50000, 0.5, 0, 0, 'open', 1)`)
	if err != nil {
		t.Fatal(err)
	}

	status, body := doJSON(t, srv, "PUT", "/internal/markets/BTC/USDT", `{"tickSize":"0.01"}`)
	if status != http.StatusConflict {
		t.Fatalf("frozen edit: %d %s", status, body)
	}
	if !strings.Contains(body, "open orders") {
		t.Fatalf("refusal should name the resting orders: %s", body)
	}

	// The refusal is real: the stored tick is untouched.
	cfgs, err := LoadMarkets(ctx, s.pool)
	if err != nil {
		t.Fatal(err)
	}
	if cfgs["BTC/USDT"].TickSize != "0.10" {
		t.Fatalf("refused edit leaked into the table: %s", cfgs["BTC/USDT"].TickSize)
	}

	// With the book empty the same edit is admitted.
	if _, err := s.pool.Exec(ctx, `UPDATE orders SET status='canceled', canceled_at=now() WHERE id='ord-freeze-1'`); err != nil {
		t.Fatal(err)
	}
	status, body = doJSON(t, srv, "PUT", "/internal/markets/BTC/USDT", `{"tickSize":"0.01"}`)
	if status != http.StatusOK {
		t.Fatalf("edit with empty book: %d %s", status, body)
	}
	cfgs, err = LoadMarkets(ctx, s.pool)
	if err != nil {
		t.Fatal(err)
	}
	if cfgs["BTC/USDT"].TickSize != "0.01" {
		t.Fatalf("accepted quantization edit not stored: %s", cfgs["BTC/USDT"].TickSize)
	}
}

// TestEngineStatusReportsConsumerLag: the status RPC surfaces the
// command consumer's lag — null before the engine's group exists (an
// honest unknown, never a lying zero), a number once the broker
// answers — alongside the per-Market Ready/open/Halted view
// (acceptance 3). The exact committed-vs-end arithmetic is the unit
// suite's; the in-process broker cannot demonstrate group-commit
// persistence, so the served assertion here is the wire contract.
func TestEngineStatusReportsConsumerLag(t *testing.T) {
	s := newStack(t)
	srv := serveAdminRPC(t, s)

	// Three unconsumed commands sit on the topic.
	s.produce(
		events.OrderCommand{Op: "place", OrderID: "lag-1", Member: "m-1", Market: "BTC/USDT", Side: "buy", Kind: "limit", Price: "50000", Amount: "1"},
		events.OrderCommand{Op: "place", OrderID: "lag-2", Member: "m-1", Market: "BTC/USDT", Side: "sell", Kind: "limit", Price: "51000", Amount: "1"},
		events.OrderCommand{Op: "place", OrderID: "lag-3", Member: "m-2", Market: "ETH/USDT", Side: "buy", Kind: "limit", Price: "3000", Amount: "1"},
	)

	// Before the engine group exists the broker cannot answer → the
	// page shows unknown.
	status, body := doJSON(t, srv, "GET", "/internal/engine/status", "")
	if status != http.StatusOK {
		t.Fatalf("status: %d %s", status, body)
	}
	var unknown struct {
		Markets []StatusSnapshot `json:"markets"`
		Lag     *int64           `json:"consumerLag"`
	}
	if err := json.Unmarshal([]byte(body), &unknown); err != nil {
		t.Fatalf("status body: %s", body)
	}
	if unknown.Lag != nil {
		t.Fatalf("consumerLag before the group exists should be null, got %d", *unknown.Lag)
	}
	if len(unknown.Markets) != 2 {
		t.Fatalf("per-Market view wrong: %s", body)
	}
	for _, m := range unknown.Markets {
		if !m.Ready || m.Halted || m.OpenOrders != 0 {
			t.Fatalf("seed market snapshot wrong: %+v", m)
		}
	}

	// The engine group joins and consumes (autocommit off, the house
	// discipline) — from then on the broker knows the group and the
	// page carries a number.
	cl, err := kafka.NewConsumer(s.cfg, ConsumerGroup, []string{events.TopicOrderCommand},
		kgo.ConsumeResetOffset(kgo.NewOffset().AtStart()))
	if err != nil {
		t.Fatal(err)
	}
	defer cl.Close()
	pollCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	saw := 0
	for saw < 1 {
		fetches := cl.PollRecords(pollCtx, 10)
		fetches.EachRecord(func(*kgo.Record) { saw++ })
		if saw == 0 && pollCtx.Err() != nil {
			t.Fatal("timed out polling order.command")
		}
	}

	deadline := time.Now().Add(15 * time.Second)
	for {
		status, body = doJSON(t, srv, "GET", "/internal/engine/status", "")
		if status != http.StatusOK {
			t.Fatalf("status: %d %s", status, body)
		}
		var got struct {
			Lag *int64 `json:"consumerLag"`
		}
		if err := json.Unmarshal([]byte(body), &got); err != nil {
			t.Fatalf("status body: %s", body)
		}
		if got.Lag != nil && *got.Lag >= 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("consumerLag never became a number once the group existed: %s", body)
		}
		time.Sleep(150 * time.Millisecond)
	}
}
