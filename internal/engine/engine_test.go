package engine

import (
	"fmt"
	"reflect"
	"testing"
)

var btcSpec = MarketSpec{
	Symbol: "BTC/USDT", BaseScale: 8, QuoteScale: 2,
	TickRaw: 1, LotRaw: 10000, // 0.01 USDT tick, 0.0001 BTC lot
}

func place(id, member string, side Side, kind Kind, price, amount string) Command {
	return Command{Place: &PlaceIn{ID: id, Member: member, Side: side, Kind: kind, Price: price, Amount: amount}}
}

func TestGoldenCrossAndPartialFills(t *testing.T) {
	b := NewBook(btcSpec)
	evs := b.Batch([]Command{
		place("m1", "alice", Sell, Limit, "50000.00", "0.0005"), // 5 lots
		place("m2", "bob", Sell, Limit, "50100.00", "0.0010"),   // 10 lots
		place("t1", "carol", Buy, Limit, "50100.00", "0.0007"),  // crosses m1 fully, m2 partially
	})

	var got []string
	for _, ev := range evs {
		for _, f := range ev.Fills {
			got = append(got, fmt.Sprintf("fill %s/%s@%s x%s taker=%s buyTurn=%s",
				f.BuyOrderID, f.SellOrderID, btcSpec.PriceString(f.PriceTicks),
				btcSpec.AmountString(f.AmountLots), f.TakerSide, btcSpec.MoneyString(f.BuyTurnoverRaw)))
			if f.BuyMemberID != "carol" || f.SellMemberID != "alice" && f.SellMemberID != "bob" {
				t.Errorf("fill member ids wrong: %s/%s", f.BuyMemberID, f.SellMemberID)
			}
		}
	}
	want := []string{
		"fill t1/m1@50000 x0.0005 taker=buy buyTurn=25",
		"fill t1/m2@50100 x0.0002 taker=buy buyTurn=10.02",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("fills:\n got %v\nwant %v", got, want)
	}

	// m1 completed; m2 partially filled (2 of 10 lots); t1 completed.
	st := b.State()
	if len(st.OpenLimit) != 1 || st.OpenLimit[0].ID != "m2" {
		t.Errorf("open orders after cross: %+v", st.OpenLimit)
	}
	if st.OpenLimit[0].TradedLots != 2 {
		t.Errorf("m2 traded = %d lots, want 2", st.OpenLimit[0].TradedLots)
	}
}

func TestMarketBuyDustBurn(t *testing.T) {
	b := NewBook(btcSpec)
	// Budget 10.01 USDT: 2 lots cost 10.00, the 0.01 remainder cannot
	// buy another lot → burned into the fill's buy turnover.
	evs := b.Batch([]Command{
		place("m1", "a", Sell, Limit, "50000.00", "0.0002"),
		place("t1", "b", Buy, Market, "", "10.01"),
	})
	var fills []Fill
	for _, ev := range evs {
		fills = append(fills, ev.Fills...)
	}
	if len(fills) != 1 {
		t.Fatalf("fills = %d, want 1 (dust burned): %+v", len(fills), fills)
	}
	f := fills[0]
	if f.AmountLots != 2 {
		t.Errorf("amount = %d lots, want 2", f.AmountLots)
	}
	if btcSpec.MoneyString(f.BuyTurnoverRaw) != "10.01" {
		t.Errorf("buy turnover = %s, want dust folded in (10.01)", btcSpec.MoneyString(f.BuyTurnoverRaw))
	}
	// taker completed exactly on budget
	st := b.State()
	if len(st.OpenMkt) != 0 {
		t.Errorf("market buy should be complete, open: %+v", st.OpenMkt)
	}
}

func TestLimitTakerMatchesRestingMarketQueue(t *testing.T) {
	b := NewBook(btcSpec)
	// A resting market sell has no price; a crossing limit buy fills it
	// at the taker's own price (legacy matchLimitPriceWithMPList).
	evs := b.Batch([]Command{
		place("m1", "a", Sell, Market, "", "0.0003"),
		place("t1", "b", Buy, Limit, "49900.00", "0.0003"),
	})
	var fills []Fill
	for _, ev := range evs {
		fills = append(fills, ev.Fills...)
	}
	if len(fills) != 1 || fills[0].PriceTicks != 4990000 {
		t.Fatalf("fill = %+v, want one fill at taker price 49900.00", fills)
	}
	if b.Plate(Buy, 10) == nil || len(b.Plate(Buy, 10).Items) != 0 {
		t.Error("nothing should rest after full cross")
	}
}

func TestPriceTimePriority(t *testing.T) {
	b := NewBook(btcSpec)
	evs := b.Batch([]Command{
		place("s1", "a", Sell, Limit, "50000.00", "0.0001"),
		place("s2", "b", Sell, Limit, "50000.00", "0.0001"),
		place("s3", "c", Sell, Limit, "49900.00", "0.0001"), // better price, fills first
		place("t1", "d", Buy, Market, "", "20.00"),          // budget for 3 lots at ≤ 5 USDT/lot
	})
	var seq []string
	for _, ev := range evs {
		for _, f := range ev.Fills {
			seq = append(seq, f.SellOrderID)
		}
	}
	want := []string{"s3", "s1", "s2"} // best price first, then FIFO
	if !reflect.DeepEqual(seq, want) {
		t.Errorf("fill sequence %v, want %v", seq, want)
	}
}

func TestCancelByIDAndUnknown(t *testing.T) {
	b := NewBook(btcSpec)
	b.Batch([]Command{place("m1", "a", Sell, Limit, "50000.00", "0.0001")})
	evs := b.Batch([]Command{
		{CancelID: "m1"},
		{CancelID: "nope"}, // unknown → empty event (caller emits cancel.rejected)
	})
	if evs[0].Canceled == nil || evs[0].Canceled.ID != "m1" {
		t.Errorf("cancel event = %+v", evs[0])
	}
	if evs[1].Canceled != nil {
		t.Errorf("unknown cancel must produce no Canceled event: %+v", evs[1])
	}
	if items := b.Plate(Sell, 10).Items; len(items) != 0 {
		t.Errorf("book not empty after cancel: %+v", items)
	}
}

func TestHaltRejectsPlacesAllowsCancels(t *testing.T) {
	b := NewBook(btcSpec)
	b.Batch([]Command{
		place("m1", "a", Sell, Limit, "50000.00", "0.0001"),
		{Halt: true},
	})
	evs := b.Batch([]Command{
		place("t1", "b", Buy, Limit, "50000.00", "0.0001"),
		{CancelID: "m1"},
	})
	if evs[0].Rejected == nil || evs[0].Rejected.Reason != "trading is halted" {
		t.Errorf("halted place should reject: %+v", evs[0])
	}
	if evs[1].Canceled == nil {
		t.Error("cancel must still work while halted")
	}
}

func TestQuantizationRejections(t *testing.T) {
	// tickedSpec: tick 0.1 (coarser than the 0.01 scale quantum) so
	// off-tick prices are observable at quote scale 2.
	tickedSpec := MarketSpec{Symbol: "T/USDT", BaseScale: 8, QuoteScale: 2, TickRaw: 10, LotRaw: 10000}
	cases := []struct {
		name             string
		side             Side
		kind             Kind
		price, amount    string
		wantRejectionMsg string
	}{
		{"off-tick price", Buy, Limit, "50000.15", "0.0001", "multiple of the tick size"},
		{"off-lot amount", Buy, Limit, "50000.10", "0.00015", "multiple of the lot size"},
		{"sub-scale price", Buy, Limit, "50000.001", "0.0001", "positive decimal"},
		{"zero price", Buy, Limit, "0", "0.0001", "positive decimal"},
		{"negative amount", Sell, Limit, "50000.10", "-1", "positive decimal"},
	}
	for _, c := range cases {
		b := NewBook(tickedSpec)
		evs := b.Batch([]Command{place("x", "m", c.side, c.kind, c.price, c.amount)})
		if evs[0].Rejected == nil {
			t.Errorf("%s: expected rejection", c.name)
			continue
		}
		if !contains(evs[0].Rejected.Reason, c.wantRejectionMsg) {
			t.Errorf("%s: reason %q lacks %q", c.name, evs[0].Rejected.Reason, c.wantRejectionMsg)
		}
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(s) > 0 && indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

func TestDeterministicFold(t *testing.T) {
	cmds := goldenStream(500)
	a := NewBook(btcSpec).Batch(cloneCommands(cmds))
	bk := NewBook(btcSpec).Batch(cloneCommands(cmds))
	if !reflect.DeepEqual(normalize(a), normalize(bk)) {
		t.Fatal("same command stream produced different event streams")
	}
}

func TestConservationNoMoneyCreation(t *testing.T) {
	cmds := goldenStream(2000)
	b := NewBook(btcSpec)
	budgets := map[string]int64{}
	for _, c := range cmds {
		if c.Place != nil && c.Place.Kind == Market && c.Place.Side == Buy {
			raw, ok := parseRaw(c.Place.Amount, btcSpec.QuoteScale)
			if !ok {
				t.Fatalf("bad budget %q", c.Place.Amount)
			}
			budgets[c.Place.ID] = raw
		}
	}
	evs := b.Batch(cmds)
	spent := map[string]int64{}
	for _, ev := range evs {
		for _, f := range ev.Fills {
			if f.AmountLots <= 0 || f.PriceTicks <= 0 {
				t.Fatalf("non-positive fill: %+v", f)
			}
			// turnover must equal price × amount exactly (no rounding drift)
			want := btcSpec.turnoverRaw(f.PriceTicks, f.AmountLots)
			if f.TurnoverRaw != want {
				t.Fatalf("turnover drift: %d != %d", f.TurnoverRaw, want)
			}
			// buy turnover ≥ sell turnover only by burned dust
			if f.BuyTurnoverRaw < f.TurnoverRaw {
				t.Fatalf("buy turnover below fill turnover: %+v", f)
			}
			spent[f.BuyOrderID] += f.BuyTurnoverRaw
		}
	}
	for id, budget := range budgets {
		if spent[id] > budget {
			t.Fatalf("market buy %s spent %d > budget %d", id, spent[id], budget)
		}
	}
}

func TestRestoreRebuildsIdenticalBook(t *testing.T) {
	cmds := goldenStream(300)
	pre := NewBook(btcSpec)
	evs := pre.Batch(cloneCommands(cmds))

	// persisted state: open orders + last seq
	st := pre.State()
	var restored []RestoredOrder
	var lastSeq uint64
	for _, o := range append(append([]*PlacedOrder{}, st.OpenLimit...), st.OpenMkt...) {
		restored = append(restored, RestoredOrder{
			ID: o.ID, MemberID: o.MemberID, Side: o.Side, Kind: o.Kind,
			PriceTicks: o.PriceTicks, AmountLots: o.AmountLots, BudgetRaw: o.BudgetRaw,
			TradedLots: o.TradedLots, TurnoverRaw: o.TurnoverRaw, Seq: o.Seq,
		})
		if o.Seq > lastSeq {
			lastSeq = o.Seq
		}
	}
	for _, ev := range evs {
		for _, f := range ev.Fills {
			if f.Seq > lastSeq {
				lastSeq = f.Seq
			}
		}
	}

	post := NewBook(btcSpec)
	if err := post.Restore(restored, lastSeq); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if !reflect.DeepEqual(pre.State(), post.State()) {
		t.Fatal("restored book state differs from pre-restart book")
	}

	// Post-restore commands produce the same events on both books.
	tail := []Command{
		place("zz1", "x", Buy, Limit, "60000.00", "0.0007"),
		{CancelID: st.OpenLimit[0].ID},
	}
	evPre := pre.Batch(cloneCommands(tail))
	evPost := post.Batch(cloneCommands(tail))
	if !reflect.DeepEqual(normalize(evPre), normalize(evPost)) {
		t.Fatal("post-restore behavior differs")
	}
}

func TestRestoreRejectsBadSeq(t *testing.T) {
	b := NewBook(btcSpec)
	err := b.Restore([]RestoredOrder{{ID: "o", MemberID: "m", Side: Sell, Kind: Limit, PriceTicks: 100, AmountLots: 1, Seq: 5}}, 3)
	if err == nil {
		t.Fatal("seq above lastSeq must fail")
	}
}

// goldenStream builds a deterministic mixed workload: seeded ladder of
// makers, crossing takers, dust-prone market buys, cancels.
func goldenStream(n int) []Command {
	cmds := make([]Command, 0, n)
	placed := 0
	nextID := func() string { placed++; return fmt.Sprintf("O%d", placed) }
	for i := 0; i < 40 && len(cmds) < n; i++ {
		lvl := int64(1 + i%13)
		cmds = append(cmds,
			place(nextID(), "maker", Sell, Limit, btcSpec.PriceString(50000_00+lvl*7), btcSpec.AmountString(int64(1+i%4))),
			place(nextID(), "maker", Buy, Limit, btcSpec.PriceString(50000_00-lvl*7), btcSpec.AmountString(int64(1+i%4))),
		)
	}
	for i := 0; len(cmds) < n; i++ {
		switch i % 7 {
		case 0, 1, 2: // crossing limit takers
			cmds = append(cmds, place(nextID(), "taker", Buy, Limit,
				btcSpec.PriceString(50000_00+int64(i%23)), btcSpec.AmountString(int64(1+i%3))))
		case 3: // dust-prone market buys
			budget := fmt.Sprintf("%d.%02d", 20+i%40, 14) // 2dp: dust-prone at ~5 USDT/lot
			cmds = append(cmds, place(nextID(), "taker", Buy, Market, "", budget))
		case 4: // market sells
			cmds = append(cmds, place(nextID(), "taker", Sell, Market, "", btcSpec.AmountString(int64(1+i%2))))
		case 5: // cancels of earlier orders (some unknown)
			cmds = append(cmds, Command{CancelID: fmt.Sprintf("O%d", i*3%max(placed, 1))})
		case 6: // resting far-from-touch limits
			cmds = append(cmds, place(nextID(), "maker", Sell, Limit,
				btcSpec.PriceString(90000_00+int64(i%11)), btcSpec.AmountString(int64(1))))
		}
	}
	return cmds
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// normalize strips pointer fields so DeepEqual compares values.
func normalize(evs []Event) []Event {
	out := make([]Event, 0, len(evs))
	for _, ev := range evs {
		if ev.Placed != nil {
			p := *ev.Placed
			ev.Placed = &p
		}
		if ev.Rejected != nil {
			r := *ev.Rejected
			ev.Rejected = &r
		}
		if ev.Canceled != nil {
			c := *ev.Canceled
			ev.Canceled = &c
		}
		var comps []*PlacedOrder
		for _, c := range ev.Completed {
			p := *c
			comps = append(comps, &p)
		}
		ev.Completed = comps
		out = append(out, ev)
	}
	return out
}

func cloneCommands(cmds []Command) []Command {
	out := make([]Command, len(cmds))
	copy(out, cmds)
	return out
}
