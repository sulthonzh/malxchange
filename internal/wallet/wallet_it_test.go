//go:build integration

// Wallet integration tests (spec #22): real Postgres + the in-process
// Kafka broker (kfake). Covers the Ledger discipline end to end —
// member.register account opening, freeze/settle/release through the
// idempotent inbox, deposit credit exactly once at the confirmation
// count, and the withdrawal state machine with its fee semantics.
package wallet

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/twmb/franz-go/pkg/kfake"
	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/syaikhipin/malxchange/internal/config"
	"github.com/syaikhipin/malxchange/internal/events"
	"github.com/syaikhipin/malxchange/internal/kafka"
	"github.com/syaikhipin/malxchange/internal/migrations"
	"github.com/syaikhipin/malxchange/internal/money"
	"github.com/syaikhipin/malxchange/internal/pgmig"
)

var (
	itPGHost = os.Getenv("MAL_TEST_PG_HOST")
	itPGPW   = os.Getenv("MAL_TEST_PG_SUPERUSER_PASSWORD")
)

func TestMain(m *testing.M) {
	if itPGHost == "" || itPGPW == "" {
		fmt.Println("skipping wallet integration tests: set MAL_TEST_PG_* (make test-integration)")
		os.Exit(0)
	}
	os.Exit(m.Run())
}

type itStack struct {
	pool     *pgxpool.Pool
	cfg      config.Kafka
	producer *kgo.Client
	consumer *kgo.Client
	log      *slog.Logger
	t        *testing.T
}

func newStack(t *testing.T) *itStack {
	t.Helper()
	ctx := context.Background()

	admin, err := pgxpool.New(ctx, fmt.Sprintf("postgres://%s:%s@%s:5432/postgres",
		os.Getenv("MAL_TEST_PG_SUPERUSER"), itPGPW, itPGHost))
	if err != nil {
		t.Fatal(err)
	}
	_, _ = admin.Exec(ctx, `DROP DATABASE IF EXISTS malxchange_itw WITH (FORCE)`)
	_, _ = admin.Exec(ctx, `CREATE DATABASE malxchange_itw`)
	admin.Close()

	pool, err := pgxpool.New(ctx, fmt.Sprintf("postgres://%s:%s@%s:5432/malxchange_itw?sslmode=disable&search_path=wallet",
		os.Getenv("MAL_TEST_PG_SUPERUSER"), itPGPW, itPGHost))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	_, _ = pool.Exec(ctx, `CREATE SCHEMA IF NOT EXISTS wallet`)
	u := url.URL{
		Scheme:   "postgres",
		User:     url.UserPassword(os.Getenv("MAL_TEST_PG_SUPERUSER"), itPGPW),
		Host:     itPGHost + ":5432",
		Path:     "/malxchange_itw",
		RawQuery: "sslmode=disable&search_path=wallet",
	}
	if err := pgmig.Run(u.String(), migrations.FS, "wallet"); err != nil {
		t.Fatalf("migrations: %v", err)
	}

	broker, err := kfake.NewCluster(kfake.NumBrokers(1))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(broker.Close)
	addrs := broker.ListenAddrs()
	cfg := config.Kafka{Seeds: []string{addrs[0]}}
	producer, err := kafka.NewProducer(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(producer.Close)
	for _, topic := range []string{events.TopicTradeFill, events.TopicOrderCompleted, events.TopicMemberRegister} {
		if err := broker.CreateTopic(topic, 1, nil); err != nil {
			t.Fatal(err)
		}
	}

	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	c, err := kafka.NewConsumer(cfg, "wallet-it", []string{
		events.TopicTradeFill, events.TopicOrderCompleted, events.TopicMemberRegister,
	}, kgo.ConsumeResetOffset(kgo.NewOffset().AtStart()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)
	return &itStack{pool: pool, cfg: cfg, producer: producer, consumer: c, log: log, t: t}
}

func (s *itStack) produce(topic, key string, data any) string {
	s.t.Helper()
	envID := uuid.NewString()
	_, body, err := events.New(envID, key, topic, time.Now().UTC(), data)
	if err != nil {
		s.t.Fatal(err)
	}
	if err := s.producer.ProduceSync(context.Background(), &kgo.Record{
		Topic: topic, Key: []byte(key), Value: body,
	}).FirstErr(); err != nil {
		s.t.Fatal(err)
	}
	return envID
}

// drain applies polled batches until quiet.
func (s *itStack) drain(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	quiet := 0
	for quiet < 3 {
		fetches := s.consumer.PollRecords(ctx, 100)
		n := 0
		for _, f := range fetches {
			for _, tp := range f.Topics {
				n += len(tp.Partitions[0].Records)
			}
		}
		if n == 0 {
			quiet++
			continue
		}
		quiet = 0
		if err := applyLedgerFetches(ctx, s.pool, s.log, s.consumer, fetches); err != nil {
			t.Fatalf("apply: %v", err)
		}
	}
}

// crashAfter applies one poll batch but skips the offset commit — the
// crash window between the Postgres commit and the offset commit;
// Redpanda re-delivers on restart.
func (s *itStack) crashAfter(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for {
		fetches := s.consumer.PollRecords(ctx, 100)
		n := 0
		for _, f := range fetches {
			for _, tp := range f.Topics {
				n += len(tp.Partitions[0].Records)
			}
		}
		if n == 0 {
			return
		}
		if err := applyLedgerFetches(ctx, s.pool, s.log, s.consumer, fetches); err != nil {
			t.Fatalf("apply: %v", err)
		}
		// offsets NOT committed
	}
}

func (s *itStack) balance(t *testing.T, member, coin string) (avail, frozen string) {
	t.Helper()
	err := s.pool.QueryRow(context.Background(),
		`SELECT available::text, frozen::text FROM ledger_accounts WHERE member_id=$1 AND coin=$2`,
		member, coin).Scan(&avail, &frozen)
	if err != nil {
		t.Fatal(err)
	}
	return
}

func (s *itStack) mustEqual(t *testing.T, what, got, want string) {
	t.Helper()
	if !money.MustParse(got).Equal(money.MustParse(want)) {
		t.Errorf("%s = %s, want %s", what, got, want)
	}
}

// TestSettlementFoldsExactlyOnce is the spec #22 settlement seam:
// register members, freeze both sides, settle a fill through the
// inbox, kill the wallet mid-settlement (offsets not committed), and
// assert redelivery never double-credits and the fold holds.
func TestSettlementFoldsExactlyOnce(t *testing.T) {
	s := newStack(t)

	// member.register opens accounts for every Coin
	s.produce(events.TopicMemberRegister, "m-alice", events.MemberRegistered{MemberID: "alice"})
	s.produce(events.TopicMemberRegister, "m-bob", events.MemberRegistered{MemberID: "bob"})
	s.drain(t)
	for _, coin := range []string{"BTC", "ETH", "LTC", "TRX", "USDT", "USDT-TRC"} {
		var n int
		if err := s.pool.QueryRow(context.Background(),
			`SELECT count(*) FROM ledger_accounts WHERE coin=$1`, coin).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 2 {
			t.Fatalf("coin %s has %d accounts, want 2", coin, n)
		}
	}

	// fund both members via adjustments (the Ledger-only test faucet)
	for _, f := range []struct{ m, c, amt string }{
		{"alice", "USDT", "1000"}, {"bob", "BTC", "1"},
	} {
		if err := Credit(context.Background(), s.pool, f.m, f.c, KindAdjustment,
			"test faucet", "it-faucet:"+f.m, money.MustParse(f.amt), "", "test"); err != nil {
			t.Fatal(err)
		}
	}

	// order placement freezes: buy freezes quote, sell freezes base
	if err := Freeze(context.Background(), s.pool, "ob1", "alice", "BTC/USDT", "buy", "USDT", money.MustParse("10.00002")); err != nil {
		t.Fatal(err)
	}
	if err := Freeze(context.Background(), s.pool, "os1", "bob", "BTC/USDT", "sell", "BTC", money.MustParse("0.0002")); err != nil {
		t.Fatal(err)
	}
	// freeze is idempotent per order
	if err := Freeze(context.Background(), s.pool, "ob1", "alice", "BTC/USDT", "buy", "USDT", money.MustParse("10.00002")); err != nil {
		t.Fatal(err)
	}
	// insufficient funds rejected
	err := Freeze(context.Background(), s.pool, "ob2", "alice", "BTC/USDT", "buy", "USDT", money.MustParse("9999"))
	if err == nil || !errors.Is(err, ErrInsufficientFunds) {
		t.Fatalf("over-freeze err = %v, want ErrInsufficientFunds", err)
	}

	fill := events.TradeFill{
		Market: "BTC/USDT", Seq: 7,
		BuyOrderID: "ob1", SellOrderID: "os1",
		BuyMemberID: "alice", SellMemberID: "bob", TakerSide: "buy",
		Price: "50000.10", Amount: "0.0002", Turnover: "10.00002", BuyTurnover: "10.00002",
		BuyFee: "0.0000002", SellFee: "0.01",
	}
	s.produce(events.TopicTradeFill, "BTC/USDT", fill)
	s.crashAfter(t) // commit + "death"; offsets not committed
	s.drain(t)      // restart: redelivery applies against the inbox

	// buyer: USDT frozen consumed exactly, base credited gross minus fee
	aUSDTagail, aUSDTfrozen := s.balance(t, "alice", "USDT")
	s.mustEqual(t, "alice USDT avail", aUSDTagail, "989.99998")
	s.mustEqual(t, "alice USDT frozen", aUSDTfrozen, "0")
	aAvail, aFrozen := s.balance(t, "alice", "BTC")
	s.mustEqual(t, "alice BTC avail", aAvail, "0.0001998")
	s.mustEqual(t, "alice BTC frozen", aFrozen, "0")

	// seller: base hold consumed, quote credited gross minus fee
	bAvail, bFrozen := s.balance(t, "bob", "BTC")
	s.mustEqual(t, "bob BTC avail", bAvail, "0.9998")
	s.mustEqual(t, "bob BTC frozen", bFrozen, "0")
	bUSDT, _ := s.balance(t, "bob", "USDT")
	s.mustEqual(t, "bob USDT avail", bUSDT, "9.99002")

	// the Journal carries gross credit + explicit fee per side, and
	// exactly six fill entries — redelivery did not duplicate
	var n int
	if err := s.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM journal WHERE idem_key LIKE 'fill:BTC/USDT:7:%'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 6 {
		t.Fatalf("fill journal entries = %d, want 6 (hold, credit, fee × 2 sides)", n)
	}
	for _, row := range []struct{ kind, avail, coin string }{
		{"fill", "0.0002", "BTC"}, {"fee", "-0.0000002", "BTC"},
		{"fill", "10.00002", "USDT"}, {"fee", "-0.01", "USDT"},
	} {
		var v string
		if err := s.pool.QueryRow(context.Background(),
			`SELECT avail_delta::text FROM journal WHERE idem_key = $1`,
			fmt.Sprintf("fill:BTC/USDT:7:%s:%s", map[bool]string{true: "buy", false: "sell"}[row.coin == "BTC"], map[string]string{"fill": "credit", "fee": "fee"}[row.kind])).Scan(&v); err != nil {
			t.Fatalf("journal %s: %v", row.kind, err)
		}
		s.mustEqual(t, "journal "+row.kind+" "+row.coin, v, row.avail)
	}

	// audit invariant: Ledger == Journal fold for every Coin
	folds, err := VerifyFold(context.Background(), s.pool, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range folds {
		if !f.OK {
			t.Errorf("fold broken for %s: ledger %s/%s journal %s/%s", f.Coin, f.LedgerAvail, f.LedgerFrozen, f.JournalAvail, f.JournalFrozen)
		}
	}
}

// TestCompletionReleasesRemainder: a partially-filled buy order that
// gets canceled releases the un-settled frozen remainder (spec #22
// story 6), idempotently under redelivery.
func TestCompletionReleasesRemainder(t *testing.T) {
	s := newStack(t)
	s.produce(events.TopicMemberRegister, "m-cara", events.MemberRegistered{MemberID: "cara"})
	s.drain(t)
	if err := Credit(context.Background(), s.pool, "cara", "USDT", KindAdjustment,
		"test faucet", "it-faucet:cara", money.MustParse("100"), "", "test"); err != nil {
		t.Fatal(err)
	}
	if err := Credit(context.Background(), s.pool, "dan", "BTC", KindAdjustment,
		"test faucet", "it-faucet:dan", money.MustParse("1"), "", "test"); err != nil {
		t.Fatal(err)
	}
	if err := Freeze(context.Background(), s.pool, "xx", "dan", "BTC/USDT", "sell", "BTC", money.MustParse("0.001")); err != nil {
		t.Fatal(err)
	}
	if err := Freeze(context.Background(), s.pool, "oc1", "cara", "BTC/USDT", "buy", "USDT", money.MustParse("50")); err != nil {
		t.Fatal(err)
	}
	// one partial fill spends 18.5 of the 50 hold
	s.produce(events.TopicTradeFill, "BTC/USDT", events.TradeFill{
		Market: "BTC/USDT", Seq: 1, BuyOrderID: "oc1", SellOrderID: "xx",
		BuyMemberID: "cara", SellMemberID: "dan", TakerSide: "sell",
		Price: "50000", Amount: "0.00037", Turnover: "18.5", BuyTurnover: "18.5",
		BuyFee: "0", SellFee: "0",
	})
	s.drain(t)
	// canceled: the 31.5 remainder releases
	s.produce(events.TopicOrderCompleted, "BTC/USDT", events.OrderCompleted{
		Market: "BTC/USDT", OrderID: "oc1", MemberID: "cara", Side: "buy", Kind: "limit",
		Status: "canceled", FilledAmount: "0.00037", Turnover: "18.5",
	})
	s.drain(t)
	// redelivery of the completion is a no-op
	s.produce(events.TopicOrderCompleted, "BTC/USDT", events.OrderCompleted{
		Market: "BTC/USDT", OrderID: "oc1", MemberID: "cara", Side: "buy", Kind: "limit",
		Status: "canceled", FilledAmount: "0.00037", Turnover: "18.5",
	})
	s.drain(t)

	avail, frozen := s.balance(t, "cara", "USDT")
	s.mustEqual(t, "cara USDT avail", avail, "81.5") // 100 − 18.5
	s.mustEqual(t, "cara USDT frozen", frozen, "0")
	folds, err := VerifyFold(context.Background(), s.pool, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range folds {
		if !f.OK {
			t.Errorf("fold broken for %s", f.Coin)
		}
	}
}

// TestDepositCreditExactlyOnce drives the confirmation counter and the
// credit threshold: detected → counter → credit at exactly N, exactly
// once (spec #22 stories 2/3).
func TestDepositCreditExactlyOnce(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	s.produce(events.TopicMemberRegister, "m-dora", events.MemberRegistered{MemberID: "dora"})
	s.drain(t)

	for h := int64(1); h <= 3; h++ {
		_, err := RecordDetected(ctx, s.pool, Deposit{
			Coin: "BTC", Member: "dora", Address: "sb1qtest", TxID: fmt.Sprintf("tx%d", h),
			Amount: "0.5", Confs: 1,
		}, h)
		if err != nil {
			t.Fatal(err)
		}
	}
	// duplicate detection of the same chain output is a no-op
	if isNew, err := RecordDetected(ctx, s.pool, Deposit{
		Coin: "BTC", Member: "dora", Address: "sb1qtest", TxID: "tx1",
		Amount: "0.5", Confs: 1,
	}, 1); err != nil || isNew {
		t.Fatalf("re-detect = %v/%v, want false/nil", isNew, err)
	}

	// tip 2: tx1 has 2 confs (< 3) — detected but not credited
	if err := UpdateConfirmations(ctx, s.pool, "BTC", 2); err != nil {
		t.Fatal(err)
	}
	if n, err := CreditDue(ctx, s.pool, "BTC", 3); err != nil || n != 0 {
		t.Fatalf("credit at tip 2 = %d/%v, want 0", n, err)
	}
	var status string
	_ = s.pool.QueryRow(ctx, `SELECT status FROM deposits WHERE txid='tx1'`).Scan(&status)
	if status != "detected" {
		t.Fatalf("tx1 status = %s, want detected", status)
	}

	// tip 3: tx1 credits (3 confs), tx2 stays at 2
	if err := UpdateConfirmations(ctx, s.pool, "BTC", 3); err != nil {
		t.Fatal(err)
	}
	if n, err := CreditDue(ctx, s.pool, "BTC", 3); err != nil || n != 1 {
		t.Fatalf("credit at tip 3 = %d/%v, want 1", n, err)
	}
	// repeated credit passes change nothing
	for range 3 {
		if n, err := CreditDue(ctx, s.pool, "BTC", 3); err != nil || n != 0 {
			t.Fatalf("re-credit = %d/%v, want 0", n, err)
		}
	}
	avail, _ := s.balance(t, "dora", "BTC")
	s.mustEqual(t, "dora BTC avail", avail, "0.5")

	// tip 4 credits tx2 (3 confs) and tx3 (2 confs stays detected)
	if err := UpdateConfirmations(ctx, s.pool, "BTC", 4); err != nil {
		t.Fatal(err)
	}
	if n, err := CreditDue(ctx, s.pool, "BTC", 3); err != nil || n != 1 {
		t.Fatalf("credit at tip 4 = %d/%v, want 1", n, err)
	}
	avail, _ = s.balance(t, "dora", "BTC")
	s.mustEqual(t, "dora BTC avail", avail, "1")

	folds, err := VerifyFold(ctx, s.pool, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range folds {
		if !f.OK {
			t.Errorf("fold broken for %s", f.Coin)
		}
	}
}

// TestWithdrawalLifecycle covers the fee semantics (fee deducted from
// the requested amount — spec #22 story 4), the debit at request, the
// reject refund, and the legal/illegal state machine transitions.
func TestWithdrawalLifecycle(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	s.produce(events.TopicMemberRegister, "m-eva", events.MemberRegistered{MemberID: "eva"})
	s.drain(t)
	if err := Credit(ctx, s.pool, "eva", "ETH", KindAdjustment,
		"test faucet", "it-faucet:eva", money.MustParse("10"), "", "test"); err != nil {
		t.Fatal(err)
	}
	coin, err := GetCoin(ctx, s.pool, "ETH")
	if err != nil {
		t.Fatal(err)
	}

	// under-minimum and bad-scale requests reject
	if _, err := RequestWithdrawal(ctx, s.pool, coin, "eva", "0xabc", money.MustParse("0.0000001")); err == nil {
		t.Fatal("under-minimum accepted")
	}
	if _, err := RequestWithdrawal(ctx, s.pool, coin, "eva", "0xabc", money.MustParse("0.1234567890123456789")); err == nil {
		t.Fatal("over-scale accepted")
	}
	// insufficient
	if _, err := RequestWithdrawal(ctx, s.pool, coin, "eva", "0xabc", money.MustParse("10.5")); err == nil || !errors.Is(err, ErrInsufficientFunds) {
		t.Fatalf("overdraw err = %v, want ErrInsufficientFunds", err)
	}

	w, err := RequestWithdrawal(ctx, s.pool, coin, "eva", "0xdEaD", money.MustParse("2"))
	if err != nil {
		t.Fatal(err)
	}
	s.mustEqual(t, "fee", w.Fee.String(), "0.004") // seeded ETH fee
	s.mustEqual(t, "net", w.Net.String(), "1.996")
	avail, _ := s.balance(t, "eva", "ETH")
	s.mustEqual(t, "eva ETH after request", avail, "8")

	// reject refunds
	if err := AuditWithdrawal(ctx, s.pool, w.ID, false, "fin@x"); err != nil {
		t.Fatal(err)
	}
	avail, _ = s.balance(t, "eva", "ETH")
	s.mustEqual(t, "eva ETH after reject", avail, "10")

	// second request walks the machine to confirmed
	w2, err := RequestWithdrawal(ctx, s.pool, coin, "eva", "0xdEaD", money.MustParse("2"))
	if err != nil {
		t.Fatal(err)
	}
	if err := AuditWithdrawal(ctx, s.pool, w2.ID, true, "fin@x"); err != nil {
		t.Fatal(err)
	}
	// illegal: confirm directly from approved
	if err := MarkWithdrawalConfirmed(ctx, s.pool, w2.ID); err == nil || !errors.Is(err, ErrInvalidState) {
		t.Fatalf("skip-state err = %v, want ErrInvalidState", err)
	}
	for _, step := range []struct {
		fn   func() error
		want string
	}{
		{func() error { return MarkWithdrawalSigned(ctx, s.pool, w2.ID, "0xtx") }, WSigned},
		{func() error { return MarkWithdrawalBroadcast(ctx, s.pool, w2.ID, "0xtx") }, WBroadcast},
		{func() error { return MarkWithdrawalConfirmed(ctx, s.pool, w2.ID) }, WConfirmed},
	} {
		if err := step.fn(); err != nil {
			t.Fatal(err)
		}
		var st string
		if err := s.pool.QueryRow(ctx, `SELECT state FROM withdrawals WHERE id=$1`, w2.ID).Scan(&st); err != nil {
			t.Fatal(err)
		}
		if st != step.want {
			t.Fatalf("state = %s, want %s", st, step.want)
		}
	}
	avail, _ = s.balance(t, "eva", "ETH")
	s.mustEqual(t, "eva ETH confirmed", avail, "8")

	// state machine table sanity (unit-grade, no DB)
	for _, bad := range []struct{ from, to string }{
		{WConfirmed, WApproved}, {WRejected, WApproved}, {WAudit, WSigned},
		{WRefunded, WApproved}, {WBroadcast, WAudit},
	} {
		if WithdrawalTransition(bad.from, bad.to) {
			t.Errorf("transition %s→%s must be illegal", bad.from, bad.to)
		}
	}
	if !WithdrawalTransition(WFailed, WApproved) || !WithdrawalTransition(WFailed, WRefunded) {
		t.Error("failed must admit retry and refund")
	}
	for _, bad := range []struct{ from, to string }{
		{SConfirmed, SPending}, {SFunding, SConfirmed}, {SPending, SConfirmed},
	} {
		if SweepTransition(bad.from, bad.to) {
			t.Errorf("sweep transition %s→%s must be illegal", bad.from, bad.to)
		}
	}
	if !SweepTransition(SFailed, SPending) {
		t.Error("failed sweep must admit retry")
	}
	for _, bad := range []struct{ from, to string }{
		{HConfirmed, HPending}, {HPending, HConfirmed},
	} {
		if HotTransferTransition(bad.from, bad.to) {
			t.Errorf("hot transfer %s→%s must be illegal", bad.from, bad.to)
		}
	}

	folds, err := VerifyFold(ctx, s.pool, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range folds {
		if !f.OK {
			t.Errorf("fold broken for %s", f.Coin)
		}
	}
}
