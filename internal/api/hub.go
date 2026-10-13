// The in-process channel→subscribers hub (ADR-0011, ticket #44). ONE
// franz-go consumer group feeds every socket: fanout cost is flat in
// connections, never per-connection consumers. The hub owns the two
// projections the wire needs — the sanitized Trade (no Member or Order
// identifiers) and the in-memory Kline bars derived from trade.fill
// (market publishes Kline REST only; there is no kline topic today, so
// the boring option is deriving bars where the fill passes through
// anyway) — and the personal-orders filter by the member IDs the events
// already carry. A member ID never appears in a channel name.
package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"time"

	"github.com/shopspring/decimal"
	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/syaikhipin/malxchange/internal/events"
	"github.com/syaikhipin/malxchange/internal/market"
	"github.com/syaikhipin/malxchange/internal/money"
)

// wsConsumerGroup names the hub's consumer group: one group for the whole
// service, named <service>-<purpose> like market-projection.
const wsConsumerGroup = "api-ws-hub"

// wsTopics is everything the hub consumes — the public market-data topics
// plus the personal order-flow topics, all through the single shared
// consumer (never one consumer per socket).
var wsTopics = []string{
	events.TopicTradeFill,
	events.TopicOrderCompleted,
	events.TopicCancelRejected,
	events.TopicMarketPlate,
	events.TopicTicker,
}

// wsMessage is the server→client envelope: channel plus data for events,
// channel plus error for rejected subscribes, error alone for protocol
// mistakes. There is no ack protocol (ADR-0011) — errors are the only
// replies.
type wsMessage struct {
	Channel string `json:"channel,omitempty"`
	Data    any    `json:"data,omitempty"`
	Error   string `json:"error,omitempty"`
}

// wsTarget is what the hub needs from one socket: its bound Member (""
// for anonymous), a non-blocking bounded outbound queue, and an
// idempotent kill.
type wsTarget interface {
	MemberID() string
	tryDeliver(msg []byte) bool
	kill(reason string)
}

// Hub routes envelopes to channel subscribers. Run drives it from the
// consumer; ingest itself is single-goroutine, so per-socket delivery
// order is exactly ingest order.
type Hub struct {
	log *slog.Logger

	mu   sync.Mutex
	subs map[string]map[wsTarget]struct{}

	// bars is the derived-Kline state: the single open bar per Market
	// and interval (a bucket older than the arriving fill has closed
	// and was already published; state is O(markets × intervals)).
	bars map[barKey]*klineBar
}

// barKey identifies one derived bar series.
type barKey struct {
	market string
	iv     market.Interval
}

// klineBar accumulates one open bucket's OHLCV in exact decimals.
type klineBar struct {
	openTime             time.Time
	open, high, low, clz decimal.Decimal
	amount, turnover     decimal.Decimal
	fills                int64
}

// NewHub builds an empty hub.
func NewHub(log *slog.Logger) *Hub {
	return &Hub{
		log:  log,
		subs: map[string]map[wsTarget]struct{}{},
		bars: map[barKey]*klineBar{},
	}
}

// Subscribe adds a socket to a channel (already validated by the
// socket's router). Double subscribes are idempotent.
func (h *Hub) Subscribe(t wsTarget, channel string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	set, ok := h.subs[channel]
	if !ok {
		set = map[wsTarget]struct{}{}
		h.subs[channel] = set
	}
	set[t] = struct{}{}
}

// Unsubscribe removes one channel subscription; missing ones are no-ops.
func (h *Hub) Unsubscribe(t wsTarget, channel string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if set := h.subs[channel]; set != nil {
		delete(set, t)
		if len(set) == 0 {
			delete(h.subs, channel)
		}
	}
}

// Remove drops a socket from every channel (socket teardown).
func (h *Hub) Remove(t wsTarget) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch, set := range h.subs {
		delete(set, t)
		if len(set) == 0 {
			delete(h.subs, ch)
		}
	}
}

// fanout delivers one channel message to every subscriber. Delivery is
// non-blocking: a full outbound queue means the slow-client policy fires
// — that socket is killed and removed, the hub never blocks.
func (h *Hub) fanout(channel string, data any) {
	msg, err := json.Marshal(wsMessage{Channel: channel, Data: data})
	if err != nil {
		h.log.Error("ws hub: marshal fanout", "channel", channel, "err", err)
		return
	}
	h.mu.Lock()
	targets := make([]wsTarget, 0, len(h.subs[channel]))
	for t := range h.subs[channel] {
		targets = append(targets, t)
	}
	h.mu.Unlock()
	for _, t := range targets {
		if !t.tryDeliver(msg) {
			h.drop(t, channel)
		}
	}
}

// fanoutOrders delivers a personal event to the "orders" subscribers
// bound to exactly this member — the carried member ID is the filter, so
// no other socket can ever receive it.
func (h *Hub) fanoutOrders(memberID string, data any) {
	msg, err := json.Marshal(wsMessage{Channel: "orders", Data: data})
	if err != nil {
		h.log.Error("ws hub: marshal orders event", "err", err)
		return
	}
	h.mu.Lock()
	targets := make([]wsTarget, 0, len(h.subs["orders"]))
	for t := range h.subs["orders"] {
		targets = append(targets, t)
	}
	h.mu.Unlock()
	for _, t := range targets {
		if t.MemberID() != memberID {
			continue
		}
		if !t.tryDeliver(msg) {
			h.drop(t, "orders")
		}
	}
}

// drop severs one overflowed socket: removed from every channel first
// (no further delivery attempts), then killed exactly once.
func (h *Hub) drop(t wsTarget, from string) {
	h.mu.Lock()
	for ch, set := range h.subs {
		if _, dead := set[t]; dead {
			delete(set, t)
		}
		if len(set) == 0 {
			delete(h.subs, ch)
		}
	}
	h.mu.Unlock()
	h.log.Warn("ws: closing slow consumer (outbound buffer overflow)", "channel", from)
	t.kill("outbound buffer overflow")
}

// ingest routes one envelope. Malformed or contract-violating payloads
// are logged and skipped — the hub is a relay with no durable state, so
// wedging the loop or broadcasting garbage are the only two wrong
// answers, and neither is taken.
func (h *Hub) ingest(env events.Envelope) {
	switch env.Type {
	case events.TopicTicker:
		var tk events.Ticker
		if !decodePayload(h.log, env, &tk) || tk.Market == "" {
			return
		}
		h.fanout("ticker", tk)
		h.fanout("ticker."+tk.Market, tk)
	case events.TopicMarketPlate:
		var p events.Plate
		if !decodePayload(h.log, env, &p) || p.Market == "" {
			return
		}
		// Depth relays market.plate as-is (ADR-0011): the engine's
		// once-per-batch publish already paces it.
		h.fanout("depth."+p.Market, p)
	case events.TopicTradeFill:
		var f events.TradeFill
		if !decodePayload(h.log, env, &f) {
			return
		}
		h.ingestFill(env, f)
	case events.TopicOrderCompleted:
		var oc events.OrderCompleted
		if !decodePayload(h.log, env, &oc) || oc.MemberID == "" {
			return
		}
		h.fanoutOrders(oc.MemberID, oc)
	case events.TopicCancelRejected:
		var cr events.CancelRejected
		if !decodePayload(h.log, env, &cr) || cr.Member == "" {
			return
		}
		h.fanoutOrders(cr.Member, cr)
	}
}

// ingestFill fans one trade.fill out as, in order: the sanitized Trade
// (market's wire shape — fill ID kept for reconnect dedupe), the derived
// Kline bar per stored interval (StoredIntervals order), then each
// side's personal event.
func (h *Hub) ingestFill(env events.Envelope, f events.TradeFill) {
	if f.Market == "" || (f.TakerSide != "buy" && f.TakerSide != "sell") {
		h.log.Error("ws hub: trade.fill violates contract", "id", env.ID, "market", f.Market)
		return
	}
	price, err := money.Parse(f.Price)
	if err != nil {
		h.log.Error("ws hub: trade.fill price", "id", env.ID, "err", err)
		return
	}
	amount, err := money.Parse(f.Amount)
	if err != nil {
		h.log.Error("ws hub: trade.fill amount", "id", env.ID, "err", err)
		return
	}
	turnover, err := money.Parse(f.Turnover)
	if err != nil {
		h.log.Error("ws hub: trade.fill turnover", "id", env.ID, "err", err)
		return
	}

	// The public Trade: market's REST shape, sanitized by construction —
	// Member and Order identifiers stay on trade.fill (GLOSSARY: a Trade
	// is the market-wide record of a Fill pair).
	h.fanout("trades."+f.Market, market.Trade{
		ID:        env.ID,
		Price:     f.Price,
		Amount:    f.Amount,
		Turnover:  f.Turnover,
		TakerSide: f.TakerSide,
		Time:      env.OccurredAt,
	})

	for _, iv := range market.StoredIntervals {
		if bar, ok := h.foldBar(f.Market, iv, env.OccurredAt, price, amount, turnover); ok {
			h.fanout("kline."+f.Market+"."+string(iv), bar)
		}
	}

	if f.BuyMemberID != "" {
		h.fanoutOrders(f.BuyMemberID, wsOwnFill{
			Market: f.Market, Seq: f.Seq, OrderID: f.BuyOrderID, Side: "buy",
			Price: f.Price, Amount: f.Amount,
			Turnover: f.BuyTurnover, // includes burned dust on the buy side
			Fee:      f.BuyFee,      // charged in the base Coin
		})
	}
	if f.SellMemberID != "" && f.SellMemberID != f.BuyMemberID {
		h.fanoutOrders(f.SellMemberID, wsOwnFill{
			Market: f.Market, Seq: f.Seq, OrderID: f.SellOrderID, Side: "sell",
			Price: f.Price, Amount: f.Amount,
			Turnover: f.Turnover,
			Fee:      f.SellFee, // charged in the quote Coin
		})
	}
}

// wsOwnFill is the personal orders projection of a fill for one side:
// the member's own order, their own fee in the Coin their side receives,
// and no Member identifier or counterparty detail on the wire.
type wsOwnFill struct {
	Market   string `json:"market"`
	Seq      uint64 `json:"seq"`
	OrderID  string `json:"orderId"`
	Side     string `json:"side"`
	Price    string `json:"price"`
	Amount   string `json:"amount"`
	Turnover string `json:"turnover"`
	Fee      string `json:"fee"`
}

// foldBar folds one fill into the open bar of a Market+interval and
// returns the resulting wire bar. ok=false means the fill belongs to a
// bucket that already rolled closed (a late event): the live feed drops
// it — REST bootstrap stays the source of history.
func (h *Hub) foldBar(mkt string, iv market.Interval, at time.Time, price, amount, turnover decimal.Decimal) (market.Kline, bool) {
	key := barKey{market: mkt, iv: iv}
	open := wsHubBucketOpen(at, iv)
	b, live := h.bars[key]
	switch {
	case !live || open.After(b.openTime):
		b = &klineBar{
			openTime: open,
			open:     price, high: price, low: price, clz: price,
			amount: amount, turnover: turnover, fills: 1,
		}
		h.bars[key] = b
	case open.Equal(b.openTime):
		if price.GreaterThan(b.high) {
			b.high = price
		}
		if price.LessThan(b.low) {
			b.low = price
		}
		b.clz = price
		b.amount = b.amount.Add(amount)
		b.turnover = b.turnover.Add(turnover)
		b.fills++
	default: // open.Before(b.openTime): the bucket already closed
		return market.Kline{}, false
	}
	return market.Kline{
		Interval:  iv,
		OpenTime:  b.openTime,
		Open:      b.open.String(),
		High:      b.high.String(),
		Low:       b.low.String(),
		Close:     b.clz.String(),
		Amount:    b.amount.String(),
		Turnover:  b.turnover.String(),
		FillCount: b.fills,
	}, true
}

// wsHubBucketOpen mirrors market's bucketOpen (internal/market/kline.go)
// — unexported there, so the ~20 lines are duplicated here on purpose:
// the WS bars MUST land on the same bucket edges as the REST Kline the
// client bootstraps from, or the deltas cannot stitch (ADR-0011
// REST-first bootstrap). Week buckets open Monday 00:00 UTC.
func wsHubBucketOpen(at time.Time, iv market.Interval) time.Time {
	u := at.Unix()
	var open int64
	switch iv {
	case market.Interval1w:
		days := wsFloorDiv(u, wsSecondsPerDay)
		weeks := wsFloorDiv(days-wsWeekStartOffsetDays, 7)
		open = (weeks*7 + wsWeekStartOffsetDays) * wsSecondsPerDay
	case market.Interval1d:
		open = wsFloorDiv(u, wsSecondsPerDay) * wsSecondsPerDay
	default:
		open = u - wsFloorMod(u, wsIntervalSeconds(iv))
	}
	return time.Unix(open, 0).UTC()
}

const (
	wsSecondsPerDay       = 24 * 60 * 60
	wsWeekStartOffsetDays = 4 // Unix day 0 was a Thursday; weeks open Monday
)

func wsIntervalSeconds(iv market.Interval) int64 {
	switch iv {
	case market.Interval1m:
		return 60
	case market.Interval5m:
		return 5 * 60
	case market.Interval15m:
		return 15 * 60
	case market.Interval30m:
		return 30 * 60
	case market.Interval1h:
		return 60 * 60
	case market.Interval4h:
		return 4 * 60 * 60
	default:
		panic("api: " + string(iv) + " has no second width")
	}
}

func wsFloorDiv(a, b int64) int64 {
	q := a / b
	if a%b != 0 && (a < 0) != (b < 0) {
		q--
	}
	return q
}

func wsFloorMod(a, b int64) int64 { return a - wsFloorDiv(a, b)*b }

func decodePayload(log *slog.Logger, env events.Envelope, v any) bool {
	if err := json.Unmarshal(env.Data, v); err != nil {
		log.Error("ws hub: payload violates contract", "type", env.Type, "id", env.ID, "err", err)
		return false
	}
	return true
}

// hubConsumer is the franz-go surface the hub drives; *kgo.Client
// satisfies it (the same seam market's projector uses).
type hubConsumer interface {
	PollRecords(ctx context.Context, maxPollRecords int) kgo.Fetches
	MarkCommitRecords(recs ...*kgo.Record)
	CommitMarkedOffsets(ctx context.Context) error
}

// Run is the hub's consume loop: poll → ingest → commit. The hub holds
// no durable state (sockets REST-bootstrap on connect and dedupe trades
// by fill ID), so broker hiccups cost only live delivery, never
// correctness — log and keep polling. Returns when ctx is done.
func (h *Hub) Run(ctx context.Context, cl hubConsumer) {
	const pollWait = 500 * time.Millisecond
	for {
		pollCtx, cancel := context.WithTimeout(ctx, pollWait)
		fetches := cl.PollRecords(pollCtx, 100)
		cancel()
		if ctx.Err() != nil {
			return
		}
		var recs []*kgo.Record
		for rec := range fetches.RecordsAll() {
			recs = append(recs, rec)
			env, err := events.Decode(rec.Value)
			if err != nil {
				h.log.Error("ws hub: malformed envelope", "topic", rec.Topic, "err", err)
				continue
			}
			h.ingest(env)
		}
		for _, fe := range fetches.Errors() {
			h.log.Error("ws hub: consumer error", "topic", fe.Topic, "partition", fe.Partition, "err", fe.Err)
		}
		if len(recs) > 0 {
			cl.MarkCommitRecords(recs...)
			if err := cl.CommitMarkedOffsets(ctx); err != nil {
				h.log.Error("ws hub: commit offsets", "err", err)
			}
		}
	}
}
