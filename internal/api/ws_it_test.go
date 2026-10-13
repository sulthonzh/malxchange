//go:build integration

// Direct WebSocket-client ITs for the member socket (ticket #44, ADR
// -0011): the production mux wiring on an httptest listener, the real
// hub consuming the five topics off an in-process kfake broker, and
// sessions minted through the real mintSession seam. Postgres runs in a
// throwaway database with a unique name (sibling IT suites share the
// dev instance). Everything is asserted through the socket.
package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/twmb/franz-go/pkg/kfake"
	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/syaikhipin/malxchange/internal/config"
	"github.com/syaikhipin/malxchange/internal/events"
	"github.com/syaikhipin/malxchange/internal/kafka"
	"github.com/syaikhipin/malxchange/internal/market"
	"github.com/syaikhipin/malxchange/internal/migrations"
	"github.com/syaikhipin/malxchange/internal/pgmig"
)

// wsStack is one hub + listener + broker, fresh per test.
type wsStack struct {
	pool     *pgxpool.Pool
	cfg      config.Kafka
	producer *kgo.Client
	broker   *kfake.Cluster
	hub      *Hub
	rest     *httptest.Server
	env      *APIEnv
	t        *testing.T
}

func newWSStack(t *testing.T, env *APIEnv) *wsStack {
	t.Helper()
	if env == nil {
		env = &APIEnv{SessionTTL: time.Hour, WsBuffer: 256}
	}
	ctx := context.Background()

	suffix := make([]byte, 6)
	if _, err := rand.Read(suffix); err != nil {
		t.Fatal(err)
	}
	dbName := "malxchange_ita_" + hex.EncodeToString(suffix)
	admin, err := pgxpool.New(ctx, fmt.Sprintf("postgres://%s:%s@%s:5432/postgres",
		os.Getenv("MAL_TEST_PG_SUPERUSER"), itPGPW, itPGHost))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(ctx, fmt.Sprintf(`DROP DATABASE IF EXISTS %s WITH (FORCE)`, dbName)); err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(ctx, fmt.Sprintf(`CREATE DATABASE %s`, dbName)); err != nil {
		t.Fatal(err)
	}
	admin.Close()
	t.Cleanup(func() {
		admin, err := pgxpool.New(ctx, fmt.Sprintf("postgres://%s:%s@%s:5432/postgres",
			os.Getenv("MAL_TEST_PG_SUPERUSER"), itPGPW, itPGHost))
		if err != nil {
			return
		}
		defer admin.Close()
		_, _ = admin.Exec(ctx, fmt.Sprintf(`DROP DATABASE IF EXISTS %s WITH (FORCE)`, dbName))
	})

	pool, err := pgxpool.New(ctx, fmt.Sprintf("postgres://%s:%s@%s:5432/%s?sslmode=disable&search_path=api",
		os.Getenv("MAL_TEST_PG_SUPERUSER"), itPGPW, itPGHost, dbName))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if _, err := pool.Exec(ctx, `CREATE SCHEMA IF NOT EXISTS api`); err != nil {
		t.Fatal(err)
	}
	u := url.URL{
		Scheme:   "postgres",
		User:     url.UserPassword(os.Getenv("MAL_TEST_PG_SUPERUSER"), itPGPW),
		Host:     itPGHost + ":5432",
		Path:     dbName,
		RawQuery: "sslmode=disable&search_path=api",
	}
	if err := pgmig.Run(u.String(), migrations.FS, "api"); err != nil {
		t.Fatalf("api migrations: %v", err)
	}

	broker, err := kfake.NewCluster(kfake.NumBrokers(1))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(broker.Close)
	cfg := config.Kafka{Seeds: broker.ListenAddrs()[:1]}
	producer, err := kafka.NewProducer(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(producer.Close)
	for _, topic := range wsTopics {
		if err := broker.CreateTopic(topic, 1, nil); err != nil {
			t.Fatal(err)
		}
	}

	runCtx, cancel := context.WithCancel(ctx)
	t.Cleanup(cancel)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	// AtStart pins the IT against the group-join race: whatever the
	// test publishes is consumed even if the join lands a beat later.
	consumer, err := kafka.NewConsumer(cfg, wsConsumerGroup, wsTopics, kgo.ConsumeResetOffset(kgo.NewOffset().AtStart()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(consumer.Close)
	hub := NewHub(log)
	go hub.Run(runCtx, consumer)

	root := http.NewServeMux()
	RegisterWS(runCtx, root, hub, pool, env, log)
	rest := httptest.NewServer(root)
	t.Cleanup(rest.Close)

	return &wsStack{pool: pool, cfg: cfg, producer: producer, broker: broker, hub: hub, rest: rest, env: env, t: t}
}

// ---- helpers ----

// wsDial connects a client socket; token "" is anonymous. The query
// parameter is the browser path (the WS API cannot set headers).
func wsDial(t *testing.T, s *wsStack, token string) *websocket.Conn {
	t.Helper()
	q := url.Values{}
	if token != "" {
		q.Set("token", token)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, s.rest.URL+"/ws?"+q.Encode(), nil)
	if err != nil {
		t.Fatalf("ws dial: %v", err)
	}
	t.Cleanup(func() { _ = c.CloseNow() })
	return c
}

func wsSend(t *testing.T, c *websocket.Conn, op, channel string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	body, _ := json.Marshal(map[string]string{"op": op, "channel": channel})
	if err := c.Write(ctx, websocket.MessageText, body); err != nil {
		t.Fatalf("ws write %s %s: %v", op, channel, err)
	}
}

func wsNext(t *testing.T, c *websocket.Conn) map[string]any {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	_, r, err := c.Reader(ctx)
	if err != nil {
		t.Fatalf("ws read: %v", err)
	}
	b, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("ws read body: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("ws message not JSON: %s", b)
	}
	return m
}

// wsNextOn pulls the next message asserting its channel (skip-free: any
// message on another channel is a routing bug).
func wsNextOn(t *testing.T, c *websocket.Conn, channel string) map[string]any {
	t.Helper()
	m := wsNext(t, c)
	if m["channel"] != channel {
		t.Fatalf("message arrived on %v, want channel %q: %v", m["channel"], channel, m)
	}
	data, _ := m["data"].(map[string]any)
	return data
}

func wsQuiet(t *testing.T, c *websocket.Conn) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
	defer cancel()
	_, _, err := c.Reader(ctx)
	if err == nil {
		t.Fatal("unexpected message inside the quiet window")
	}
}

// wsExpectError pulls the next message and asserts it is an error
// envelope for the channel.
func wsExpectError(t *testing.T, c *websocket.Conn, channel string) string {
	t.Helper()
	m := wsNext(t, c)
	if msg, ok := m["error"].(string); ok && msg != "" {
		if channel != "" && m["channel"] != channel {
			t.Fatalf("error named channel %v, want %q", m["channel"], channel)
		}
		return msg
	}
	t.Fatalf("expected an error envelope, got %v", m)
	return ""
}

// publish writes one envelope on a hub topic, waiting for the broker
// ack, and returns the envelope id.
func (s *wsStack) publish(topic, key string, payload any) string {
	s.t.Helper()
	id := uuid.NewString()
	_, body, err := events.New(id, key, topic, time.Now().UTC(), payload)
	if err != nil {
		s.t.Fatal(err)
	}
	done := make(chan error, 1)
	s.producer.Produce(context.Background(), &kgo.Record{Topic: topic, Key: []byte(key), Value: body},
		func(_ *kgo.Record, err error) { done <- err })
	if err := <-done; err != nil {
		s.t.Fatal(err)
	}
	return id
}

// settle lets the read pump register the subscribe before the event.
func settle() { time.Sleep(500 * time.Millisecond) }

func (s *wsStack) token(member string) string {
	s.t.Helper()
	// Sessions reference members (FK): the fixture member row comes
	// first, with a throwaway argon2id-looking hash (never logged in
	// here — the session is minted directly).
	if _, err := s.pool.Exec(context.Background(),
		`INSERT INTO members (id, email, password_hash) VALUES ($1, $2, 'x')
		 ON CONFLICT (id) DO NOTHING`, member, member+"@example.com"); err != nil {
		s.t.Fatal(err)
	}
	tok, _, err := mintSession(context.Background(), s.pool, member, "it device", s.env.SessionTTL)
	if err != nil {
		s.t.Fatal(err)
	}
	return tok
}

// ---- tests ----

// Anonymous sockets get every public channel; the orders channel is
// member-only; a presented-but-dead token refuses the connection.
func TestWSAnonymousPublicChannelsIT(t *testing.T) {
	s := newWSStack(t, nil)
	c := wsDial(t, s, "")

	wsSend(t, c, "subscribe", "ticker")
	wsSend(t, c, "subscribe", "ticker.BTC/USDT")
	wsSend(t, c, "subscribe", "depth.BTC/USDT")
	wsSend(t, c, "subscribe", "trades.BTC/USDT")
	wsSend(t, c, "subscribe", "kline.BTC/USDT.1m")
	wsSend(t, c, "subscribe", "orders")
	wsSend(t, c, "subscribe", "nosuch.channel")
	wsSend(t, c, "subscribe", "kline.BTC/USDT.7m")
	wsSend(t, c, "subscribe", "")
	settle()

	if msg := wsExpectError(t, c, "orders"); msg == "" {
		t.Fatal("orders for anonymous must answer an error")
	}
	wsExpectError(t, c, "nosuch.channel")
	wsExpectError(t, c, "kline.BTC/USDT.7m")
	wsExpectError(t, c, "")

	s.publish(events.TopicTicker, "BTC/USDT", events.Ticker{Market: "BTC/USDT", Last: "50000.10", Turnover: "12345.67"})
	s.publish(events.TopicTicker, "ETH/USDT", events.Ticker{Market: "ETH/USDT", Last: "3000", Turnover: "9"})
	s.publish(events.TopicMarketPlate, "BTC/USDT", events.Plate{Market: "BTC/USDT",
		Bids: []events.PlateLevel{{Price: "50000.10", Amount: "0.0002"}},
		Asks: []events.PlateLevel{{Price: "50010.20", Amount: "0.0003"}}})

	fill := events.TradeFill{Market: "BTC/USDT", Seq: 7, BuyOrderID: "ob1", SellOrderID: "os1",
		BuyMemberID: "alice", SellMemberID: "bob", TakerSide: "buy",
		Price: "50000.10", Amount: "0.0002", Turnover: "10.00002",
		BuyTurnover: "10.00002", BuyFee: "0.0000002", SellFee: "0.01000002"}
	s.publish(events.TopicTradeFill, "BTC/USDT", fill)

	// Per envelope the hub fans the all-Markets ticker first, then the
	// per-Market one — so the wire order is ticker(BTC),
	// ticker.BTC/USDT, ticker(ETH): the all-Markets channel carries both
	// Markets, the per-Market one only its own.
	if d := wsNextOn(t, c, "ticker"); d["market"] != "BTC/USDT" {
		t.Errorf("ticker channel delivered %v, want BTC/USDT first", d["market"])
	}
	if d := wsNextOn(t, c, "ticker.BTC/USDT"); d["last"] != "50000.10" {
		t.Errorf("per-market ticker wrong: %v", d)
	}
	if d := wsNextOn(t, c, "ticker"); d["market"] != "ETH/USDT" {
		t.Errorf("ticker channel delivered %v, want ETH/USDT second", d["market"])
	}
	d := wsNextOn(t, c, "depth.BTC/USDT")
	if bid := d["bids"].([]any)[0].(map[string]any); bid["price"] != "50000.10" || bid["amount"] != "0.0002" {
		t.Errorf("depth relay wrong: %v", d)
	}
	tr := wsNextOn(t, c, "trades.BTC/USDT")
	for _, k := range []string{"price", "amount", "turnover", "takerSide"} {
		if _, ok := tr[k]; !ok {
			t.Errorf("trades data missing %q: %v", k, tr)
		}
	}
	for _, leak := range []string{"buyMemberId", "sellMemberId", "buyOrderId", "sellOrderId", "buyFee", "sellFee"} {
		if _, ok := tr[leak]; ok {
			t.Errorf("trades channel leaks %q: %v", leak, tr)
		}
	}
	bar := wsNextOn(t, c, "kline.BTC/USDT.1m")
	if bar["interval"] != "1m" || bar["open"] != "50000.1" || bar["fillCount"] != float64(1) {
		t.Errorf("kline bar wrong: %v", bar)
	}

	// A presented-but-dead token is refused at the handshake, not
	// silently downgraded to anonymous.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, resp, err := websocket.Dial(ctx, s.rest.URL+"/ws?token=garbage", nil)
	if err == nil {
		t.Fatal("dial with a dead token must fail")
	}
	if resp == nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("dead-token dial status = %v, want 401", resp)
	}
}

// The kline channel family exists for EVERY stored interval, live.
func TestWSKlineEveryStoredIntervalIT(t *testing.T) {
	s := newWSStack(t, nil)
	c := wsDial(t, s, "")
	for _, iv := range market.StoredIntervals {
		wsSend(t, c, "subscribe", "kline.BTC/USDT."+string(iv))
	}
	settle()

	s.publish(events.TopicTradeFill, "BTC/USDT", events.TradeFill{Market: "BTC/USDT", Seq: 1,
		BuyMemberID: "a", SellMemberID: "b", TakerSide: "buy",
		Price: "100", Amount: "1", Turnover: "100"})

	bars := map[string]map[string]any{}
	for range market.StoredIntervals {
		m := wsNext(t, c)
		ch, _ := m["channel"].(string)
		data, _ := m["data"].(map[string]any)
		if bars[ch] != nil {
			t.Fatalf("duplicate bar on %s", ch)
		}
		bars[ch] = data
	}
	for _, iv := range market.StoredIntervals {
		bar := bars["kline.BTC/USDT."+string(iv)]
		if bar == nil {
			t.Fatalf("no bar arrived for interval %s", iv)
		}
		if bar["interval"] != string(iv) || bar["close"] != "100" || bar["fillCount"] != float64(1) {
			t.Errorf("%s bar wrong: %v", iv, bar)
		}
	}
}

// Member isolation on the orders channel: a member sees only the events
// their own member ID carries; the counterparty's ID never reaches them.
func TestWSOrdersMemberIsolationIT(t *testing.T) {
	s := newWSStack(t, nil)
	alice := wsDial(t, s, s.token("alice"))
	// A second socket for the same Member carries the quiet checks: a
	// timed-out read closes a coder/websocket connection, so the socket
	// that must keep living never reads with a deadline.
	aliceQuiet := wsDial(t, s, s.token("alice"))
	bob := wsDial(t, s, s.token("bob"))
	wsSend(t, alice, "subscribe", "orders")
	wsSend(t, aliceQuiet, "subscribe", "orders")
	wsSend(t, bob, "subscribe", "orders")
	settle()

	s.publish(events.TopicTradeFill, "BTC/USDT", events.TradeFill{Market: "BTC/USDT", Seq: 9,
		BuyOrderID: "ob9", SellOrderID: "os9", BuyMemberID: "alice", SellMemberID: "bob",
		TakerSide: "buy", Price: "50000.10", Amount: "0.0002", Turnover: "10.00002",
		BuyTurnover: "10.00003", BuyFee: "0.0000002", SellFee: "0.01"})

	a := wsNextOn(t, alice, "orders")
	if a["orderId"] != "ob9" || a["side"] != "buy" || a["fee"] != "0.0000002" || a["turnover"] != "10.00003" {
		t.Errorf("alice's own fill wrong: %v", a)
	}
	b := wsNextOn(t, bob, "orders")
	if b["orderId"] != "os9" || b["side"] != "sell" || b["fee"] != "0.01" {
		t.Errorf("bob's own fill wrong: %v", b)
	}
	// The second alice socket got her side too (same Member binding).
	if aq := wsNextOn(t, aliceQuiet, "orders"); aq["orderId"] != "ob9" {
		t.Errorf("second alice socket's own fill wrong: %v", aq)
	}

	// Another member's terminal and rejection events must not arrive.
	s.publish(events.TopicOrderCompleted, "BTC/USDT", events.OrderCompleted{Market: "BTC/USDT",
		OrderID: "os9", MemberID: "bob", Side: "sell", Status: "filled", FilledAmount: "1", Turnover: "2"})
	wsQuiet(t, aliceQuiet)
	if d := wsNextOn(t, bob, "orders"); d["orderId"] != "os9" {
		t.Errorf("bob's completion wrong: %v", d)
	}

	s.publish(events.TopicCancelRejected, "BTC/USDT", events.CancelRejected{Market: "BTC/USDT",
		OrderID: "ob9", Member: "bob", Reason: "order is not on the book"})
	wsQuiet(t, aliceQuiet)
	if d := wsNextOn(t, bob, "orders"); d["reason"] != "order is not on the book" {
		t.Errorf("bob's rejection wrong: %v", d)
	}

	// A member-bound socket still reads every public channel too.
	wsSend(t, alice, "subscribe", "trades.BTC/USDT")
	settle()
	s.publish(events.TopicTradeFill, "BTC/USDT", events.TradeFill{Market: "BTC/USDT", Seq: 10,
		BuyMemberID: "carol", SellMemberID: "dave", TakerSide: "sell",
		Price: "1", Amount: "1", Turnover: "1"})
	if tr := wsNextOn(t, alice, "trades.BTC/USDT"); tr["price"] != "1" {
		t.Errorf("member's public trades wrong: %v", tr)
	}
	wsQuiet(t, aliceQuiet) // carol/dave's fill is not alice's order event
}

// The slow-client policy end to end: a socket that stops reading is
// closed once its outbound buffer overflows; the hub and every other
// socket are unaffected.
func TestWSOverflowSlowClientIT(t *testing.T) {
	s := newWSStack(t, &APIEnv{SessionTTL: time.Hour, WsBuffer: 256})
	healthy := wsDial(t, s, "")
	slow := wsDial(t, s, "")
	wsSend(t, healthy, "subscribe", "ticker")
	wsSend(t, slow, "subscribe", "ticker")
	settle()

	const burst = 6000
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { // the healthy reader drains the burst concurrently
		defer wg.Done()
		for range burst {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			_, r, err := healthy.Reader(ctx)
			if err == nil {
				// The body must be consumed: unread bodies fill the
				// receive window and the hub would (correctly) kill
				// this socket as a slow consumer too.
				_, err = io.Copy(io.Discard, r)
			}
			cancel()
			if err != nil {
				return // a healthy-socket failure fails the tail assertions
			}
		}
	}()

	// The slow socket subscribes but never reads: its TCP window closes,
	// its write pump stalls, the bounded buffer fills, the hub kills it.
	// The burst is produced in one batch (per-message ack waits would
	// make the test slower than the policy it proves).
	for i := range burst {
		id := uuid.NewString()
		_, body, err := events.New(id, "BTC/USDT", events.TopicTicker, time.Now().UTC(),
			events.Ticker{Market: "BTC/USDT", Last: fmt.Sprint(i), Turnover: "1"})
		if err != nil {
			t.Fatal(err)
		}
		s.producer.Produce(context.Background(), &kgo.Record{
			Topic: events.TopicTicker, Key: []byte("BTC/USDT"), Value: body}, nil)
		// Paced: the healthy socket must be able to keep up (its death
		// would prove nothing), while the slow socket's window still
		// fills and stalls its pump.
		if i%10 == 9 {
			time.Sleep(2 * time.Millisecond)
		}
	}
	if err := s.producer.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}

	// The slow client must be dead: buffered frames may drain first, but
	// reads must error out well inside the deadline.
	slowDead := false
	for drainDeadline := time.Now().Add(30 * time.Second); time.Now().Before(drainDeadline); {
		deadCtx, deadCancel := context.WithTimeout(context.Background(), 5*time.Second)
		_, _, err := slow.Reader(deadCtx)
		deadCancel()
		if err != nil {
			slowDead = true
			break
		}
	}
	if !slowDead {
		t.Fatal("slow consumer was not closed")
	}

	// The healthy socket survived the whole burst: wait for its drain
	// to finish (two concurrent readers on one socket would corrupt
	// each other), then prove it keeps receiving.
	wg.Wait()
	s.publish(events.TopicTicker, "BTC/USDT", events.Ticker{Market: "BTC/USDT", Last: "final", Turnover: "1"})
	gotFinal := false
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		m := wsNext(t, healthy)
		if d, _ := m["data"].(map[string]any); d != nil && d["last"] == "final" {
			gotFinal = true
			break
		}
	}
	if !gotFinal {
		t.Fatal("healthy socket did not receive the post-kill ticker")
	}
}
