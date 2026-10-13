package api

import (
	"encoding/json"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/syaikhipin/malxchange/internal/events"
	"github.com/syaikhipin/malxchange/internal/market"
)

// Unit seam for the hub: fabricated envelopes pushed straight into
// ingest — the franz-go consumer loop is a thin wrapper over it. The
// buffer policy, fanout order, sanitization, and member isolation are
// asserted here; the WS wire behavior is asserted by ws_it_test.go.

// fakeTarget is a stand-in socket: a bounded queue (exactly the hub's
// deliver-or-kill contract) and a kill flag.
type fakeTarget struct {
	member string
	out    chan []byte
	killed bool
}

func (f *fakeTarget) MemberID() string { return f.member }
func (f *fakeTarget) tryDeliver(b []byte) bool {
	select {
	case f.out <- b:
		return true
	default:
		return false
	}
}
func (f *fakeTarget) kill(string) { f.killed = true }

func newTarget(member string, cap int) *fakeTarget {
	return &fakeTarget{member: member, out: make(chan []byte, cap)}
}

// recv pulls one queued message or fails the test after the deadline.
func (f *fakeTarget) recv(t *testing.T) map[string]any {
	t.Helper()
	select {
	case b := <-f.out:
		return decodeMsg(t, b)
	case <-time.After(2 * time.Second):
		t.Fatal("no message within deadline")
		return nil
	}
}

// quiet asserts nothing arrives for a short window.
func (f *fakeTarget) quiet(t *testing.T) {
	t.Helper()
	select {
	case b := <-f.out:
		t.Fatalf("unexpected message: %s", b)
	case <-time.After(150 * time.Millisecond):
	}
}

func decodeMsg(t *testing.T, b []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("message is not JSON: %v (%s)", err, b)
	}
	return m
}

func testHub() *Hub { return NewHub(slog.New(slog.NewTextHandler(io.Discard, nil))) }

func fillEnv(t *testing.T, id string, at time.Time, f events.TradeFill) events.Envelope {
	t.Helper()
	env, _, err := events.New(id, f.Market, events.TopicTradeFill, at, f)
	if err != nil {
		t.Fatal(err)
	}
	return env
}

func sampleFill(mkt string) events.TradeFill {
	return events.TradeFill{
		Market: mkt, Seq: 7,
		BuyOrderID: "ob1", SellOrderID: "os1",
		BuyMemberID: "alice", SellMemberID: "bob",
		TakerSide: "buy", Price: "50000.10", Amount: "0.0002", Turnover: "10.00002",
		BuyTurnover: "10.00002", BuyFee: "0.0000002", SellFee: "0.01000002",
	}
}

// The trades channel is the sanitized projection of trade.fill: the wire
// shape is market's Trade (backend-wires §2) — no Member or Order
// identifier anywhere.
func TestHubTradesSanitized(t *testing.T) {
	h := testHub()
	pub := newTarget("", 8)
	h.Subscribe(pub, "trades.BTC/USDT")
	at := time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC)

	h.ingest(fillEnv(t, "fill-1", at, sampleFill("BTC/USDT")))

	m := pub.recv(t)
	if m["channel"] != "trades.BTC/USDT" {
		t.Fatalf("channel = %v", m["channel"])
	}
	data, _ := m["data"].(map[string]any)
	want := map[string]any{
		"id": "fill-1", "price": "50000.10", "amount": "0.0002",
		"turnover": "10.00002", "takerSide": "buy",
	}
	for k, v := range want {
		if data[k] != v {
			t.Errorf("data[%q] = %v, want %v", k, data[k], v)
		}
	}
	if len(data) != len(want)+1 { // + time
		t.Errorf("data carries extra fields: %v", data)
	}
	if _, ok := data["time"]; !ok {
		t.Error("data has no time field")
	}
}

// Fanout order per trade.fill is pinned: trades first, then the Kline
// bars in stored-interval order, then the personal orders events. One
// hub goroutine sends, so per-socket order is deterministic.
func TestHubFanoutOrdering(t *testing.T) {
	h := testHub()
	sub := newTarget("alice", 16)
	h.Subscribe(sub, "orders")
	h.Subscribe(sub, "kline.BTC/USDT.1d")
	h.Subscribe(sub, "kline.BTC/USDT.1m")
	h.Subscribe(sub, "trades.BTC/USDT")

	h.ingest(fillEnv(t, "fill-1", time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC), sampleFill("BTC/USDT")))

	got := []string{sub.recv(t)["channel"].(string), sub.recv(t)["channel"].(string),
		sub.recv(t)["channel"].(string), sub.recv(t)["channel"].(string)}
	want := []string{"trades.BTC/USDT", "kline.BTC/USDT.1m", "kline.BTC/USDT.1d", "orders"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("fanout order = %v, want %v", got, want)
		}
	}
}

// One fill feeds a bar on EVERY stored interval channel.
func TestHubKlineEveryStoredInterval(t *testing.T) {
	h := testHub()
	subs := map[string]*fakeTarget{}
	for _, iv := range market.StoredIntervals {
		name := "kline.BTC/USDT." + string(iv)
		subs[name] = newTarget("", 2)
		h.Subscribe(subs[name], name)
	}
	at := time.Date(2026, 10, 12, 9, 0, 30, 0, time.UTC)

	h.ingest(fillEnv(t, "fill-1", at, sampleFill("BTC/USDT")))

	for name, sub := range subs {
		m := sub.recv(t)
		if m["channel"] != name {
			t.Errorf("%s: wrong channel %v", name, m["channel"])
		}
		data, _ := m["data"].(map[string]any)
		if data["interval"] != name[len("kline.BTC/USDT."):] {
			t.Errorf("%s: interval = %v", name, data["interval"])
		}
		// Computed bar money is rendered by decimal.String(): exact
		// canonical form, so the trailing zero of "50000.10" is gone.
		for _, k := range []string{"open", "high", "low", "close"} {
			if data[k] != "50000.1" {
				t.Errorf("%s: %s = %v, want the fill price", name, k, data[k])
			}
		}
		if data["amount"] != "0.0002" || data["turnover"] != "10.00002" || data["fillCount"] != float64(1) {
			t.Errorf("%s: bar totals wrong: %v", name, data)
		}
	}
}

// Bars accumulate inside a bucket and roll when the bucket passes.
func TestHubKlineBucketAccumulateAndRoll(t *testing.T) {
	h := testHub()
	sub := newTarget("", 8)
	h.Subscribe(sub, "kline.BTC/USDT.1m")
	base := time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC)

	f1 := sampleFill("BTC/USDT")
	f1.Price = "100"
	h.ingest(fillEnv(t, "f1", base, f1))

	f2 := sampleFill("BTC/USDT")
	f2.Price = "300"
	f2.Amount = "2"
	f2.Turnover = "600"
	h.ingest(fillEnv(t, "f2", base.Add(30*time.Second), f2))

	f3 := sampleFill("BTC/USDT")
	f3.Price = "200"
	h.ingest(fillEnv(t, "f3", base.Add(61*time.Second), f3))

	// One message per fill: the bar as it stood after each fold.
	b1 := sub.recv(t)["data"].(map[string]any)
	if b1["openTime"] != "2026-10-12T09:00:00Z" || b1["open"] != "100" || b1["close"] != "100" || b1["fillCount"] != float64(1) {
		t.Errorf("first bar wrong: %v", b1)
	}
	b2 := sub.recv(t)["data"].(map[string]any)
	if b2["openTime"] != "2026-10-12T09:00:00Z" || b2["open"] != "100" || b2["close"] != "300" ||
		b2["high"] != "300" || b2["low"] != "100" || b2["fillCount"] != float64(2) {
		t.Errorf("accumulated bar wrong: %v", b2)
	}
	if b2["amount"] != "2.0002" || b2["turnover"] != "610.00002" {
		t.Errorf("accumulated totals wrong: %v", b2)
	}
	b3 := sub.recv(t)["data"].(map[string]any)
	if b3["openTime"] != "2026-10-12T09:01:00Z" || b3["open"] != "200" || b3["close"] != "200" || b3["fillCount"] != float64(1) {
		t.Errorf("rolled bar wrong: %v", b3)
	}
}

// The 1w bucket edge: ISO weeks open Monday 00:00 UTC (the same pinned
// boundary as market's projection — see wsHubBucketOpen).
func TestHubKlineWeekBucketOpensMonday(t *testing.T) {
	h := testHub()
	sub := newTarget("", 2)
	h.Subscribe(sub, "kline.BTC/USDT.1w")
	// 2026-10-12 is a Monday; 09:00 lands inside that week's bucket.
	h.ingest(fillEnv(t, "f1", time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC), sampleFill("BTC/USDT")))
	bar := sub.recv(t)["data"].(map[string]any)
	if bar["openTime"] != "2026-10-12T00:00:00Z" {
		t.Errorf("1w openTime = %v, want Monday 00:00 UTC", bar["openTime"])
	}
}

// Personal order events are filtered by the member IDs the events carry:
// each side of a fill sees only its own projection, with its own order
// and fee and no Member identifier on the wire.
func TestHubOrdersMemberIsolation(t *testing.T) {
	h := testHub()
	alice := newTarget("alice", 8)
	bob := newTarget("bob", 8)
	h.Subscribe(alice, "orders")
	h.Subscribe(bob, "orders")

	h.ingest(fillEnv(t, "fill-1", time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC), sampleFill("BTC/USDT")))

	a := alice.recv(t)["data"].(map[string]any)
	if a["orderId"] != "ob1" || a["side"] != "buy" || a["fee"] != "0.0000002" || a["turnover"] != "10.00002" {
		t.Errorf("alice's own fill wrong: %v", a)
	}
	b := bob.recv(t)["data"].(map[string]any)
	if b["orderId"] != "os1" || b["side"] != "sell" || b["fee"] != "0.01000002" {
		t.Errorf("bob's own fill wrong: %v", b)
	}
	for _, m := range []map[string]any{a, b} {
		for _, leak := range []string{"buyMemberId", "sellMemberId", "buyOrderId", "sellOrderId", "buyFee", "sellFee", "buyTurnover"} {
			if _, ok := m[leak]; ok {
				t.Errorf("personal fill leaks %q: %v", leak, m)
			}
		}
	}

	// order.completed and cancel.rejected carry exactly one member: only
	// that member's socket receives them, payload as-is.
	completed := events.OrderCompleted{Market: "BTC/USDT", OrderID: "os1", MemberID: "bob",
		Side: "sell", Kind: "limit", Status: "filled", FilledAmount: "1", Turnover: "2"}
	env, _, _ := events.New("c1", completed.Market, events.TopicOrderCompleted, time.Now().UTC(), completed)
	h.ingest(env)
	alice.quiet(t)
	got := bob.recv(t)["data"].(map[string]any)
	if got["orderId"] != "os1" || got["memberId"] != "bob" {
		t.Errorf("bob's completion wrong: %v", got)
	}

	rejected := events.CancelRejected{Market: "BTC/USDT", OrderID: "ob1", Member: "alice", Reason: "order is not on the book"}
	env, _, _ = events.New("r1", rejected.Market, events.TopicCancelRejected, time.Now().UTC(), rejected)
	h.ingest(env)
	bob.quiet(t)
	got = alice.recv(t)["data"].(map[string]any)
	if got["reason"] != "order is not on the book" {
		t.Errorf("alice's rejection wrong: %v", got)
	}
}

// Ticker and Depth are verbatim relays: the ticker topic feeds both the
// all-Markets channel and the per-Market one; market.plate feeds the
// matching Depth channel only.
func TestHubTickerAndDepthRelay(t *testing.T) {
	h := testHub()
	all := newTarget("", 4)
	one := newTarget("", 4)
	depth := newTarget("", 4)
	other := newTarget("", 4)
	h.Subscribe(all, "ticker")
	h.Subscribe(one, "ticker.BTC/USDT")
	h.Subscribe(depth, "depth.BTC/USDT")
	h.Subscribe(other, "depth.ETH/USDT")

	tk := events.Ticker{Market: "BTC/USDT", Last: "50000.10", High: "50100", Low: "49900", Turnover: "12345.67"}
	env, _, _ := events.New("t1", tk.Market, events.TopicTicker, time.Now().UTC(), tk)
	h.ingest(env)

	for _, sub := range []*fakeTarget{all, one} {
		data := sub.recv(t)["data"].(map[string]any)
		if data["market"] != "BTC/USDT" || data["last"] != "50000.10" || data["turnover"] != "12345.67" {
			t.Errorf("ticker relay wrong: %v", data)
		}
	}

	plate := events.Plate{Market: "BTC/USDT", Bids: []events.PlateLevel{{Price: "50000.10", Amount: "0.0002"}}}
	env, _, _ = events.New("p1", plate.Market, events.TopicMarketPlate, time.Now().UTC(), plate)
	h.ingest(env)

	data := depth.recv(t)["data"].(map[string]any)
	bids := data["bids"].([]any)[0].(map[string]any)
	if bids["price"] != "50000.10" || bids["amount"] != "0.0002" {
		t.Errorf("depth relay wrong: %v", data)
	}
	other.quiet(t)
}

// The bounded-buffer policy: a full outbound queue kills that socket —
// once, immediately, and out of every channel — while other sockets keep
// receiving. The hub never blocks on a slow consumer.
func TestHubBufferOverflowPolicy(t *testing.T) {
	h := testHub()
	slow := newTarget("", 1)
	fast := newTarget("", 8)
	h.Subscribe(slow, "ticker")
	h.Subscribe(slow, "trades.BTC/USDT")
	h.Subscribe(fast, "ticker")
	slow.out <- []byte("occupied") // queue already full

	tk := events.Ticker{Market: "BTC/USDT", Last: "1", Turnover: "0"}
	env, _, _ := events.New("t1", tk.Market, events.TopicTicker, time.Now().UTC(), tk)
	h.ingest(env)

	if !slow.killed {
		t.Error("overflowed target was not killed")
	}
	if got := <-slow.out; string(got) != "occupied" {
		t.Errorf("slow target's queue was drained: %s", got)
	}
	// Killed means removed everywhere: a later fill must not even try
	// the trades channel.
	h.ingest(fillEnv(t, "f1", time.Now().UTC(), sampleFill("BTC/USDT")))
	slow.quiet(t)

	data := fast.recv(t)["data"].(map[string]any)
	if data["last"] != "1" {
		t.Errorf("healthy target missed the ticker: %v", data)
	}
	env, _, _ = events.New("t2", tk.Market, events.TopicTicker, time.Now().UTC(), tk)
	h.ingest(env)
	fast.recv(t) // still delivers after the neighbor died
}

// Unsubscribe stops delivery; a malformed envelope is skipped without
// wedging the hub.
func TestHubUnsubscribeAndMalformedSkip(t *testing.T) {
	h := testHub()
	sub := newTarget("", 4)
	h.Subscribe(sub, "ticker")
	h.Unsubscribe(sub, "ticker")

	env, _, _ := events.New("bad", "BTC/USDT", events.TopicTicker, time.Now().UTC(), json.RawMessage(`{"not":"a ticker"}`))
	h.ingest(env) // payload violates the contract: logged and skipped

	tk := events.Ticker{Market: "BTC/USDT", Last: "2"}
	env, _, _ = events.New("t2", tk.Market, events.TopicTicker, time.Now().UTC(), tk)
	h.ingest(env)
	sub.quiet(t)
}
