//go:build integration

// Kline integration tests (spec #23 ticket #31): a deterministic Fill
// sequence produces identical bars at every stored interval, bars open and
// close at exact UTC interval edges, crash redelivery and replay leave
// bars unchanged, and the internal REST serves any stored interval while
// rejecting unknown ones. Runs against real Postgres + in-process kfake.
package market

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/syaikhipin/malxchange/internal/events"
)

// klineSeq is the deterministic Fill sequence every interval folds:
//
//	f1 09:00:00  p=100 a=1 t=100   — opens bars at every interval edge
//	f2 09:00:30  p=110 a=2 t=220
//	f3 09:04:59  p=90  a=3 t=270   — last instant inside the 5m bucket
//	f4 09:05:00  p=105 a=4 t=420   — opens the 5m/1m 09:05 bucket at its edge
//	f5 10:00:00  p=120 a=5 t=600   — opens the 1h 10:00 bucket at its edge
//
// 2026-10-12 is a Monday, so the 1w buckets are Monday-aligned and f1
// opens the week bar at 2026-10-12T00:00Z. All money values are exact
// decimal literals; every expected bar below is hand-worked from this
// table, never recomputed by the fold under test.
func klineSeq() []struct {
	at   time.Time
	seq  uint64
	fill events.TradeFill
} {
	at := func(s string) time.Time { return mustUTC(s) }
	mk := func(id, atS string, seq uint64, p, a, t string) struct {
		at   time.Time
		seq  uint64
		fill events.TradeFill
	} {
		return struct {
			at   time.Time
			seq  uint64
			fill events.TradeFill
		}{at: at(atS), seq: seq, fill: events.TradeFill{
			Market: "BTC/USDT", Seq: seq,
			BuyOrderID: "ord-" + id, SellOrderID: "ord-maker",
			BuyMemberID: "bob", SellMemberID: "alice",
			TakerSide: "buy",
			Price:     p, Amount: a, Turnover: t,
			BuyTurnover: t, BuyFee: "0", SellFee: "0",
		}}
	}
	return []struct {
		at   time.Time
		seq  uint64
		fill events.TradeFill
	}{
		mk("f1", "2026-10-12T09:00:00Z", 1, "100", "1", "100"),
		mk("f2", "2026-10-12T09:00:30Z", 2, "110", "2", "220"),
		mk("f3", "2026-10-12T09:04:59Z", 3, "90", "3", "270"),
		mk("f4", "2026-10-12T09:05:00Z", 4, "105", "4", "420"),
		mk("f5", "2026-10-12T10:00:00Z", 5, "120", "5", "600"),
	}
}

// wantKline is one hand-worked expected bar (openTime, O/H/L/C, amount,
// turnover, fillCount) at one interval.
type wantKline struct {
	interval                     Interval
	open                         string
	o, h, l, c, amount, turnover string
	fills                        int
}

// klineExpectations is the independent source of truth: the bars the
// sequence above MUST produce at every stored interval.
func klineExpectations() map[Interval][]wantKline {
	return map[Interval][]wantKline{
		Interval1m: {
			{"1m", "2026-10-12T09:00:00Z", "100", "110", "100", "110", "3", "320", 2},
			{"1m", "2026-10-12T09:04:00Z", "90", "90", "90", "90", "3", "270", 1},
			{"1m", "2026-10-12T09:05:00Z", "105", "105", "105", "105", "4", "420", 1},
			{"1m", "2026-10-12T10:00:00Z", "120", "120", "120", "120", "5", "600", 1},
		},
		Interval5m: {
			{"5m", "2026-10-12T09:00:00Z", "100", "110", "90", "90", "6", "590", 3},
			{"5m", "2026-10-12T09:05:00Z", "105", "105", "105", "105", "4", "420", 1},
			{"5m", "2026-10-12T10:00:00Z", "120", "120", "120", "120", "5", "600", 1},
		},
		// 15m/30m/1h share one 09:00 bar here: the 09:00–10:00 Fills all
		// land in it, opening at f1's price and closing at f4's.
		Interval15m: {
			{"15m", "2026-10-12T09:00:00Z", "100", "110", "90", "105", "10", "1010", 4},
			{"15m", "2026-10-12T10:00:00Z", "120", "120", "120", "120", "5", "600", 1},
		},
		Interval30m: {
			{"30m", "2026-10-12T09:00:00Z", "100", "110", "90", "105", "10", "1010", 4},
			{"30m", "2026-10-12T10:00:00Z", "120", "120", "120", "120", "5", "600", 1},
		},
		Interval1h: {
			{"1h", "2026-10-12T09:00:00Z", "100", "110", "90", "105", "10", "1010", 4},
			{"1h", "2026-10-12T10:00:00Z", "120", "120", "120", "120", "5", "600", 1},
		},
		// 4h buckets open at 00/04/08/12 UTC: everything from 09:00–10:00
		// lands in the 08:00 bar.
		Interval4h: {
			{"4h", "2026-10-12T08:00:00Z", "100", "120", "90", "120", "15", "1610", 5},
		},
		Interval1d: {
			{"1d", "2026-10-12T00:00:00Z", "100", "120", "90", "120", "15", "1610", 5},
		},
		// 2026-10-12 is a Monday: the week bucket opens exactly there
		// (Monday-start weeks, pinned by TestBucketOpenIntervalEdges).
		Interval1w: {
			{"1w", "2026-10-12T00:00:00Z", "100", "120", "90", "120", "15", "1610", 5},
		},
	}
}

// TestFillSequenceProducesBarsAtEveryStoredInterval: the deterministic
// Fill sequence folds into identical, hand-worked bars at every stored
// interval, served oldest first at internal REST (ticket #31).
func TestFillSequenceProducesBarsAtEveryStoredInterval(t *testing.T) {
	s := newStack(t)
	s.newConsumer("kline-1")

	for _, p := range klineSeq() {
		s.fundAndFreeze(t, "alice", "bob", "BTC", "USDT", p.fill.Amount, p.fill.Turnover)
		s.produceFill(t, fmt.Sprintf("kfill-%d", p.seq), p.at, p.fill)
	}
	s.drain(t)

	for _, iv := range StoredIntervals {
		status, body := s.getREST(t, "/internal/kline/BTC/USDT?interval="+string(iv)+"&limit=1000")
		if status != http.StatusOK {
			t.Fatalf("GET kline %s = %d, want 200: %s", iv, status, body)
		}
		var got []Kline
		if err := json.Unmarshal(body, &got); err != nil {
			t.Fatalf("kline %s: response is not a JSON array: %v\n%s", iv, err, body)
		}
		want := klineExpectations()[iv]
		if len(got) != len(want) {
			t.Fatalf("kline %s = %d bars, want %d\n%s", iv, len(got), len(want), body)
		}
		for i, w := range want {
			g := got[i]
			if g.Interval != w.interval {
				t.Errorf("kline %s[%d].interval = %q, want %q", iv, i, g.Interval, w.interval)
			}
			if open := mustUTC(w.open); !g.OpenTime.Equal(open) {
				t.Errorf("kline %s[%d].openTime = %s, want %s", iv, i, g.OpenTime.Format(time.RFC3339Nano), w.open)
			}
			for _, chk := range []struct {
				name string
				got  string
				want string
			}{
				{"open", g.Open, w.o},
				{"high", g.High, w.h},
				{"low", g.Low, w.l},
				{"close", g.Close, w.c},
				{"amount", g.Amount, w.amount},
				{"turnover", g.Turnover, w.turnover},
			} {
				if chk.got != chk.want {
					t.Errorf("kline %s[%d] (%s) %s = %s, want %s\n%s", iv, i, w.open, chk.name, chk.got, chk.want, body)
				}
			}
			if g.FillCount != int64(w.fills) {
				t.Errorf("kline %s[%d] (%s) fillCount = %d, want %d", iv, i, w.open, g.FillCount, w.fills)
			}
		}

		// Bars carry market-wide values only: the Fills' Member/Order
		// identifiers never reach the wire.
		for _, banned := range []string{"alice", "bob", "ord-", "kfill-", "member", "orderId"} {
			if strings.Contains(string(body), banned) {
				t.Errorf("kline %s response leaks %q:\n%s", iv, banned, body)
			}
		}
	}

	// Unknown and absent intervals are explicit errors, never an empty
	// success.
	for _, q := range []string{"interval=2m", "interval=1M", ""} {
		status, body := s.getREST(t, "/internal/kline/BTC/USDT?"+q)
		if status != http.StatusBadRequest {
			t.Errorf("GET kline with %q = %d, want 400", q, status)
		}
		var errBody struct {
			Error string
		}
		if err := json.Unmarshal(body, &errBody); err != nil || errBody.Error == "" {
			t.Errorf("GET kline with %q: body %s is not a named error", q, body)
		}
	}
}

// klineSnapshot fetches the full bar set at every stored interval as one
// comparable string.
func klineSnapshot(t *testing.T, s *itStack) string {
	t.Helper()
	out := ""
	for _, iv := range StoredIntervals {
		status, body := s.getREST(t, "/internal/kline/BTC/USDT?interval="+string(iv)+"&limit=1000")
		if status != http.StatusOK {
			t.Fatalf("snapshot kline %s = %d, want 200", iv, status)
		}
		out += string(iv) + "=" + string(body) + "\n"
	}
	return out
}

// TestKlineCrashRedeliveryAndReplayLeaveBarsUnchanged: the crash window
// between the Postgres commit and the offset commit redelivers the same
// Fill batch — the inbox dedupes it and the natural-key upsert keeps every
// bar identical. Replaying the identical envelopes again (the rebuild
// shape) changes nothing either (ticket #31).
func TestKlineCrashRedeliveryAndReplayLeaveBarsUnchanged(t *testing.T) {
	s := newStack(t)
	s.newConsumer("kline-crash-1")

	for _, p := range klineSeq() {
		s.fundAndFreeze(t, "alice", "bob", "BTC", "USDT", p.fill.Amount, p.fill.Turnover)
		s.produceFill(t, fmt.Sprintf("kfill-%d", p.seq), p.at, p.fill)
	}
	s.crashAfter(t) // applies the batch, never commits offsets
	before := klineSnapshot(t, s)

	// Restarted consumer rejoins the same group: everything redelivers
	// from the group offset and must land exactly once.
	s.consumer.Close()
	s.newConsumer("kline-crash-1")
	s.drain(t)
	if after := klineSnapshot(t, s); after != before {
		t.Errorf("bars changed after crash redelivery:\nbefore=%s\nafter=%s", before, after)
	}

	// Rebuild replay shape: the identical envelopes again, from scratch.
	for _, p := range klineSeq() {
		s.produceFill(t, fmt.Sprintf("kfill-%d", p.seq), p.at, p.fill)
	}
	s.drain(t)
	if after := klineSnapshot(t, s); after != before {
		t.Errorf("bars changed after replaying the same Fill batch:\nbefore=%s\nafter=%s", before, after)
	}

	// The trade-side count is the double-count tripwire: exactly five
	// fills, not ten.
	var klineFills int64
	if err := s.pool.QueryRow(context.Background(),
		`SELECT sum(fill_count) FROM klines WHERE "interval" = '1m'`).Scan(&klineFills); err != nil {
		t.Fatal(err)
	}
	if klineFills != 5 {
		t.Errorf("sum(fill_count) at 1m = %d, want 5 (no double-count)", klineFills)
	}
}
