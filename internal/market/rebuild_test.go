package market

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/syaikhipin/malxchange/internal/events"
)

// The internal rebuild endpoint's contract (ticket #34): POST /internal/
// rebuild wipes the derived state and rewinds the consumer group through
// the ordinary projection loop. These hermetic tests pin the HTTP shape
// only — the wipe-and-replay behavior is the drill IT's job (the drill IS
// the test, spec testing decision).

// stubRebuilder records calls; Rebuild itself is exercised against real
// Postgres + Kafka in the integration drill, never faked here.
type stubRebuilder struct {
	err   error
	calls int
}

func (r *stubRebuilder) Rebuild(context.Context) error {
	r.calls++
	return r.err
}

func newRebuildServer(t *testing.T, r *RPC) *httptest.Server {
	t.Helper()
	root := http.NewServeMux()
	internal := http.NewServeMux()
	root.Handle("/internal/", http.StripPrefix("/internal", internal))
	RegisterRPC(internal, r)
	srv := httptest.NewServer(root)
	t.Cleanup(srv.Close)
	return srv
}

func postRebuild(t *testing.T, srv *httptest.Server) (int, map[string]string) {
	t.Helper()
	resp, err := http.Post(srv.URL+"/internal/rebuild", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]string
	if len(body) > 0 {
		if err := json.Unmarshal(body, &decoded); err != nil {
			t.Fatalf("response body %q: %v", body, err)
		}
	}
	return resp.StatusCode, decoded
}

// TestRebuildWithoutProjectorAnswersUnavailable: a market process that
// never wired the projector (no consumer to rewind) must refuse the drill
// with 503, never silently pretend a rebuild happened.
func TestRebuildWithoutProjectorAnswersUnavailable(t *testing.T) {
	srv := newRebuildServer(t, &RPC{})

	status, body := postRebuild(t, srv)
	if status != http.StatusServiceUnavailable {
		t.Fatalf("POST /internal/rebuild without projector: status %d, want 503", status)
	}
	if !strings.Contains(body["error"], "rebuild unavailable") {
		t.Errorf("error %q, want it to name the rebuild", body["error"])
	}
}

// TestRebuildTriggersRebuilder: the route answers 204 (no content to
// return — convergence is observable on the market-data endpoints) and
// triggers exactly one rebuild.
func TestRebuildTriggersRebuilder(t *testing.T) {
	rb := &stubRebuilder{}
	srv := newRebuildServer(t, &RPC{Rebuild: rb})

	status, body := postRebuild(t, srv)
	if status != http.StatusNoContent {
		t.Fatalf("POST /internal/rebuild: status %d, want 204", status)
	}
	if len(body) != 0 {
		t.Errorf("204 body %v, want empty", body)
	}
	if rb.calls != 1 {
		t.Errorf("rebuild triggered %d times, want 1", rb.calls)
	}
}

// TestRebuildPropagatesFailure: a failed wipe surfaces as 500 so the
// operator retries — the rebuild is idempotent, never half-reported.
func TestRebuildPropagatesFailure(t *testing.T) {
	rb := &stubRebuilder{err: errors.New("wipe: connection refused")}
	srv := newRebuildServer(t, &RPC{Rebuild: rb})

	status, body := postRebuild(t, srv)
	if status != http.StatusInternalServerError {
		t.Fatalf("POST /internal/rebuild on failure: status %d, want 500", status)
	}
	if !strings.Contains(body["error"], "wipe") {
		t.Errorf("error %q, want it to carry the cause", body["error"])
	}
}

// --- the ordering contract, pinned with a scripted client -----------------
//
// fakeConsumerClient scripts the franz-go surface the projector drives, so
// the poll/rewind/commit ordering is pinnable hermetically: scripted poll
// answers return FIFO, every other poll blocks on its context (an idle
// poll), and every call lands in one ordered log.
type fakeConsumerClient struct {
	mu      sync.Mutex
	calls   []string
	answers []kgo.Fetches
	entered chan struct{}
}

func newFakeClient(answers ...kgo.Fetches) *fakeConsumerClient {
	return &fakeConsumerClient{answers: answers, entered: make(chan struct{}, 64)}
}

func (f *fakeConsumerClient) record(call string) {
	f.mu.Lock()
	f.calls = append(f.calls, call)
	f.mu.Unlock()
}

func (f *fakeConsumerClient) has(call string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.calls {
		if c == call {
			return true
		}
	}
	return false
}

func (f *fakeConsumerClient) log() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

func (f *fakeConsumerClient) PollRecords(ctx context.Context, _ int) kgo.Fetches {
	f.record("poll")
	f.entered <- struct{}{}
	f.mu.Lock()
	out := f.answers
	if len(out) > 0 {
		f.answers = out[1:]
	}
	f.mu.Unlock()
	if len(out) > 0 {
		return out[0]
	}
	<-ctx.Done() // idle poll: quiet until the caller's deadline
	return nil
}

func (f *fakeConsumerClient) SetOffsets(map[string]map[int32]kgo.EpochOffset) {
	f.record("setOffsets")
}

func (f *fakeConsumerClient) MarkCommitRecords(recs ...*kgo.Record) {
	for _, r := range recs {
		f.record("mark " + r.Topic + "@" + fmt.Sprint(r.Offset))
	}
}

func (f *fakeConsumerClient) CommitMarkedOffsets(context.Context) error {
	f.record("commit")
	return nil
}

// fakePool/fakeTx stand in for Postgres: the inbox insert and the derived
// upserts always succeed, which is all the ordering contract needs.
type fakePool struct{}

func (fakePool) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, nil
}
func (fakePool) Query(context.Context, string, ...any) (pgx.Rows, error) {
	return nil, errors.New("fakePool: Query unused")
}
func (fakePool) QueryRow(context.Context, string, ...any) pgx.Row {
	return nil
}
func (fakePool) Begin(context.Context) (pgx.Tx, error) { return fakeTx{}, nil }

type fakeTx struct{ pgx.Tx }

func (fakeTx) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	return pgconn.NewCommandTag("INSERT 0 1"), nil
}
func (fakeTx) Commit(context.Context) error { return nil }
func (fakeTx) Rollback(context.Context) error {
	return pgx.ErrTxClosed // pgx.BeginFunc always rolls back after commit
}
func (fakeTx) QueryRow(context.Context, string, ...any) pgx.Row {
	return noRow{} // every kline bucket reads fresh
}

type noRow struct{}

func (noRow) Scan(...any) error { return pgx.ErrNoRows }

func testProjector(cl consumerClient) *Projector {
	return NewProjector(fakePool{}, slog.New(slog.NewTextHandler(io.Discard, nil)), cl)
}

// TestRebuildWaitsForInFlightPoll: the rewind can never interleave with a
// poll. With a poll in flight (blocked, no records), Rebuild must not run
// SetOffsets until that poll returned — the whole consume cycle and the
// rewind share one mutex. (Fails under a loop that polls outside the
// lock: there the rewind sails through mid-poll within microseconds.)
func TestRebuildWaitsForInFlightPoll(t *testing.T) {
	cl := newFakeClient() // every poll blocks until its deadline
	proj := testProjector(cl)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- proj.Consume(ctx) }()
	<-cl.entered // the loop is mid-poll

	rebuildDone := make(chan error, 1)
	go func() { rebuildDone <- proj.Rebuild(context.Background()) }()

	// The poll is still in flight: no rewind may start. Under the old
	// interleaving this fires immediately; under the fixed one it is
	// impossible — the poll holds the mutex Rebuild needs.
	time.Sleep(150 * time.Millisecond)
	if cl.has("setOffsets") {
		t.Fatal("SetOffsets ran while a poll was in flight — the rewind interleaves with polling")
	}

	// The in-flight poll ends on its own deadline; the cycle completes and
	// the rewind takes its turn.
	select {
	case err := <-rebuildDone:
		if err != nil {
			t.Fatalf("rebuild: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("rebuild never finished after the in-flight poll cycle")
	}
	if !cl.has("setOffsets") {
		t.Fatal("rebuild did not rewind after the in-flight cycle completed")
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("consume loop: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("consume loop never stopped")
	}
}

// TestBatchCommitPrecedesRewind: a batch polled before the rewind commits
// BEFORE the rewind runs — poll, apply and commit form one critical
// section with the rewind, so a pre-rewind batch can never re-commit head
// offsets over the wiped tables (the interleaving that would let a crash
// mid-replay resume at the head and permanently skip the replay).
func TestBatchCommitPrecedesRewind(t *testing.T) {
	at := time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC)
	fill := events.TradeFill{
		Market: "BTC/USDT", Seq: 1, TakerSide: "buy",
		Price: "50000.1", Amount: "0.0002", Turnover: "10.00002",
	}
	_, body, err := events.New("fill-1", "BTC/USDT", events.TopicTradeFill, at, fill)
	if err != nil {
		t.Fatal(err)
	}
	rec := &kgo.Record{
		Topic: events.TopicTradeFill, Partition: 0, Offset: 5,
		Key: []byte("BTC/USDT"), Value: body,
	}
	batch := kgo.Fetches{{
		Topics: []kgo.FetchTopic{{Topic: events.TopicTradeFill,
			Partitions: []kgo.FetchPartition{{Partition: 0, Records: []*kgo.Record{rec}}}}},
	}}

	cl := newFakeClient(batch) // first poll returns the batch, rest idle
	proj := testProjector(cl)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- proj.Consume(ctx) }()

	// Wait for the batch's commit, then rewind while the loop idles.
	deadline := time.Now().Add(10 * time.Second)
	for !cl.has("commit") {
		if time.Now().After(deadline) {
			t.Fatal("the polled batch never committed")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if err := proj.Rebuild(ctx); err != nil {
		t.Fatalf("rebuild: %v", err)
	}

	// No commit may land after the rewind: every commit either precedes
	// it or belongs to a replayed batch polled after it.
	calls := cl.log()
	lastCommit, set := -1, -1
	for i, c := range calls {
		if c == "commit" {
			lastCommit = i
		}
		if c == "setOffsets" {
			set = i
		}
	}
	if set == -1 {
		t.Fatalf("no rewind in the call log: %v", calls)
	}
	if lastCommit > set {
		t.Fatalf("commit landed after the rewind (log %v) — a pre-rewind batch re-committed head offsets", calls)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("consume loop: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("consume loop never stopped")
	}
}
