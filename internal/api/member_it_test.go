//go:build integration

// Member identity core over the real seam (ticket #37): HTTP against the
// production mux wiring, Postgres in a throwaway database (unique name —
// sibling IT suites share the dev Postgres, see internal/market's
// consumer_it_test.go), an in-process kfake broker carrying
// member.register out of the real outbox relay, and the capture mailer
// standing in for SMTP. Assertions go through the served surface only.
package api

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"regexp"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/twmb/franz-go/pkg/kfake"
	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/syaikhipin/malxchange/internal/auth"
	"github.com/syaikhipin/malxchange/internal/config"
	"github.com/syaikhipin/malxchange/internal/events"
	"github.com/syaikhipin/malxchange/internal/kafka"
	"github.com/syaikhipin/malxchange/internal/migrations"
	"github.com/syaikhipin/malxchange/internal/outbox"
	"github.com/syaikhipin/malxchange/internal/pgmig"
)

var (
	itPGHost = os.Getenv("MAL_TEST_PG_HOST")
	itPGPW   = os.Getenv("MAL_TEST_PG_SUPERUSER_PASSWORD")
)

func TestMain(m *testing.M) {
	if itPGHost == "" || itPGPW == "" {
		fmt.Println("skipping api integration tests: set MAL_TEST_PG_* (make test-integration)")
		os.Exit(0)
	}
	os.Exit(m.Run())
}

type itStack struct {
	pool     *pgxpool.Pool // search_path=api
	db       string
	cfg      config.Kafka
	producer *kgo.Client
	broker   *kfake.Cluster
	relay    *outbox.Relay
	mailer   *CaptureMailer
	env      *APIEnv
	rest     *httptest.Server
	t        *testing.T
}

func newStack(t *testing.T) *itStack {
	t.Helper()
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
	addrs := broker.ListenAddrs()
	cfg := config.Kafka{Seeds: []string{addrs[0]}}
	producer, err := kafka.NewProducer(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(producer.Close)
	if err := broker.CreateTopic(events.TopicMemberRegister, 1, nil); err != nil {
		t.Fatal(err)
	}

	relayCtx, relayCancel := context.WithCancel(ctx)
	t.Cleanup(relayCancel)
	relay := outbox.NewRelay(pool, kafka.Publisher{Client: producer})
	go func() { _ = relay.Run(relayCtx) }()

	// Test-tuned env: tight lockout window so the expiry leg is quick.
	env := &APIEnv{
		CodeTTL:          time.Minute,
		CodeCooldown:     200 * time.Millisecond,
		MaxCodeAttempts:  2,
		LockoutThreshold: 3,
		LockoutWindow:    1500 * time.Millisecond,
		SessionTTL:       time.Hour,
	}
	mailer := &CaptureMailer{}

	s := &itStack{pool: pool, db: dbName, cfg: cfg, producer: producer, broker: broker,
		relay: relay, mailer: mailer, env: env, t: t}

	root := http.NewServeMux()
	internal := http.NewServeMux()
	member := &memberAPI{pool: pool, env: env, mailer: mailer,
		ob: outbox.New(pool), log: testLogger()}
	member.RegisterMember(root)
	member.RegisterSensitive(root)
	member.RegisterSensitiveAdmin(internal)
	channels := &channelAPI{pool: pool, env: env, log: testLogger()}
	channels.RegisterChannels(root)
	channels.RegisterChannelAdmin(internal)
	// The boot-harness internal wiring (service.go): /internal/* strips
	// the prefix, so the admin RPC lands at /internal/channels*.
	root.Handle("/internal/", http.StripPrefix("/internal", internal))
	s.rest = httptest.NewServer(root)
	t.Cleanup(s.rest.Close)
	return s
}

// ---- HTTP helpers ----

func (s *itStack) post(path string, body any, token string) (int, map[string]any) {
	s.t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		s.t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPost, s.rest.URL+path, bytes.NewReader(raw))
	if err != nil {
		s.t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("access-auth-token", token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		s.t.Fatal(err)
	}
	defer resp.Body.Close()
	out := map[string]any{}
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func (s *itStack) get(path, token string) (int, map[string]any) {
	s.t.Helper()
	req, err := http.NewRequest(http.MethodGet, s.rest.URL+path, nil)
	if err != nil {
		s.t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("access-auth-token", token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		s.t.Fatal(err)
	}
	defer resp.Body.Close()
	out := map[string]any{}
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

var codeRe = regexp.MustCompile(`\b(\d{6})\b`)

// register's helper: requestCode asks for a registration code and returns
// the code from the NEWEST captured mail for that email (earlier mails
// carry spent codes).
func (s *itStack) requestCode(email string) (string, error) {
	// Re-request through the cooldown window: a too-soon answer carries
	// no new mail, and the newest mail before it would hold a spent code.
	var mail []CapturedMail
	deadline := time.Now().Add(6 * time.Second)
	for time.Now().Before(deadline) {
		before := len(s.mailer.Mail())
		status, body := s.post("/api/uc/register/code", map[string]string{"email": email}, "")
		if status != http.StatusOK && status != http.StatusTooManyRequests {
			return "", fmt.Errorf("request code: status %d body %v", status, body)
		}
		for wait := time.Now().Add(500 * time.Millisecond); time.Now().Before(wait); {
			mail = s.mailer.Mail()
			if len(mail) > before {
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
		if len(mail) > before {
			break
		}
	}
	for i := len(mail) - 1; i >= 0; i-- {
		if mail[i].To == email {
			if match := codeRe.FindStringSubmatch(mail[i].Body); match != nil {
				return match[1], nil
			}
		}
	}
	return "", errors.New("no captured mail carrying a code for " + email)
}

// register drives request-code → capture → register and returns the token.
func (s *itStack) register(email, password string) string {
	s.t.Helper()
	code, err := s.requestCode(email)
	if err != nil {
		s.t.Fatal(err)
	}
	status, body := s.post("/api/uc/register", map[string]string{
		"email": email, "code": code, "password": password}, "")
	if status != http.StatusOK {
		s.t.Fatalf("register: status %d body %v", status, body)
	}
	return body["token"].(string)
}

// ---- the seam ----

func TestRegisterLoginSessionJourney(t *testing.T) {
	s := newStack(t)
	email := "member@example.com"

	token := s.register(email, "a-fine-password")
	if token == "" {
		t.Fatal("register returned no token")
	}

	// The member.register event left through the real outbox relay.
	var memberID string
	if status, body := s.get("/api/uc/me", token); status != http.StatusOK {
		t.Fatalf("me: status %d body %v", status, body)
	} else {
		memberID = body["id"].(string)
		if body["email"] != email {
			t.Fatalf("me: email %v", body["email"])
		}
	}
	if err := s.awaitMemberRegister(memberID, email); err != nil {
		t.Fatal(err)
	}

	// A second device signs in; both sessions list, exactly one current.
	status, body := s.post("/api/uc/login", map[string]string{
		"email": email, "password": "a-fine-password", "device": "laptop"}, "")
	if status != http.StatusOK {
		t.Fatalf("login: status %d body %v", status, body)
	}
	laptop := body["token"].(string)

	req, _ := http.NewRequest(http.MethodGet, s.rest.URL+"/api/uc/sessions", nil)
	req.Header.Set("access-auth-token", laptop)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	rawViews, ioErr := io.ReadAll(resp.Body)
	resp.Body.Close()
	if ioErr != nil {
		t.Fatal(ioErr)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("sessions: status %d body %s", resp.StatusCode, rawViews)
	}
	var views []map[string]any
	if err := json.Unmarshal(rawViews, &views); err != nil {
		t.Fatalf("sessions decode: %v (%s)", err, rawViews)
	}
	if len(views) != 2 {
		var rows int
		_ = s.pool.QueryRow(context.Background(),
			`SELECT count(*) FROM sessions`).Scan(&rows)
		t.Fatalf("sessions: want 2, got %s (db rows: %d)", rawViews, rows)
	}
	current := 0
	for _, v := range views {
		if v["current"] == true {
			current++
		}
	}
	if current != 1 {
		t.Fatalf("sessions: want exactly one current, got %d", current)
	}

	// Revoke-all kills both tokens; login still works afterwards.
	if status, body := s.post("/api/uc/sessions/revoke-all", nil, laptop); status != http.StatusOK {
		t.Fatalf("revoke-all: status %d body %v", status, body)
	}
	for _, tok := range []string{token, laptop} {
		if status, _ := s.get("/api/uc/me", tok); status != http.StatusUnauthorized {
			t.Fatalf("revoked token %q still answers: %d", tok[:8], status)
		}
	}
	status, _ = s.post("/api/uc/login", map[string]string{
		"email": email, "password": "a-fine-password"}, "")
	if status != http.StatusOK {
		t.Fatalf("login after revoke-all: %d", status)
	}
}

// awaitMemberRegister consumes member.register from the kfake topic until
// the envelope for memberID arrives (the relay publishes after commit).
func (s *itStack) awaitMemberRegister(memberID, email string) error {
	consumer, err := kafka.NewConsumer(s.cfg, "api-it-register",
		[]string{events.TopicMemberRegister}, kgo.ConsumeResetOffset(kgo.NewOffset().AtStart()))
	if err != nil {
		return err
	}
	defer consumer.Close()
	deadline := time.Now().Add(10 * time.Second)
	found := false
	for time.Now().Before(deadline) && !found {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		fetches := consumer.PollRecords(ctx, 10)
		fetches.EachError(func(_ string, _ int32, err error) { s.t.Fatal(err) })
		for _, rec := range fetches.Records() {
			env, err := events.Decode(rec.Value)
			if err != nil || env.Key != memberID {
				continue
			}
			var got events.MemberRegistered
			if err := json.Unmarshal(env.Data, &got); err != nil {
				cancel()
				return fmt.Errorf("member.register payload: %w", err)
			}
			if got.MemberID != memberID || got.Email != email {
				cancel()
				return fmt.Errorf("member.register payload mismatch: %+v", got)
			}
			found = true
			break
		}
		cancel()
	}
	if !found {
		return errors.New("member.register not delivered within 10s")
	}
	return nil
}

func TestLoginLockoutEngagesAndExpires(t *testing.T) {
	s := newStack(t)
	email := "locked@example.com"
	s.register(email, "the-right-password")

	wrong := map[string]string{"email": email, "password": "wrong"}
	for i := range 3 {
		if status, body := s.post("/api/uc/login", wrong, ""); status != http.StatusUnauthorized {
			t.Fatalf("wrong password %d: status %d body %v", i, status, body)
		}
	}
	// Threshold reached: even the right password is refused while locked.
	if status, body := s.post("/api/uc/login",
		map[string]string{"email": email, "password": "the-right-password"}, ""); status != http.StatusTooManyRequests {
		t.Fatalf("locked login: status %d body %v", status, body)
	}
	// The lock lifts when the failures slide out of the window.
	time.Sleep(1600 * time.Millisecond)
	if status, body := s.post("/api/uc/login",
		map[string]string{"email": email, "password": "the-right-password"}, ""); status != http.StatusOK {
		t.Fatalf("login after window: status %d body %v", status, body)
	}
}

func TestVerificationCodeMatrix(t *testing.T) {
	s := newStack(t)
	email := "codes@example.com"

	code, err := s.requestCode(email)
	if err != nil {
		t.Fatal(err)
	}
	// Wrong code burns an attempt; with MaxCodeAttempts=2 the second
	// wrong entry locks the row, and then even the right code is dead.
	if status, _ := s.post("/api/uc/register", map[string]string{
		"email": email, "code": "000000", "password": "long-enough"}, ""); status != http.StatusBadRequest {
		t.Fatalf("wrong code: status %d", status)
	}
	if status, body := s.post("/api/uc/register", map[string]string{
		"email": email, "code": "111111", "password": "long-enough"}, ""); status != http.StatusBadRequest {
		t.Fatalf("second wrong code: status %d body %v", status, body)
	}
	if status, _ := s.post("/api/uc/register", map[string]string{
		"email": email, "code": code, "password": "long-enough"}, ""); status != http.StatusBadRequest {
		t.Fatalf("right code after lock: status %d — want the locked row to refuse it", status)
	}

	// A fresh code registers; reuse of a consumed code is refused.
	code2, err := s.requestCode(email)
	if err != nil {
		t.Fatal(err)
	}
	if status, _ := s.post("/api/uc/register", map[string]string{
		"email": email, "code": code2, "password": "long-enough"}, ""); status != http.StatusOK {
		t.Fatalf("register with fresh code: status %d", status)
	}
	if status, _ := s.post("/api/uc/register", map[string]string{
		"email": email, "code": code2, "password": "long-enough"}, ""); status != http.StatusBadRequest {
		t.Fatalf("reuse of consumed code: status %d", status)
	}

	// A code minted for another purpose never validates as register.
	// (Later tickets add purposes; the scoping is pinned here.)
	if status, _ := s.post("/api/uc/register", map[string]string{
		"email": "other@example.com", "code": code2, "password": "long-enough"}, ""); status != http.StatusBadRequest {
		t.Fatalf("cross-target code: status %d", status)
	}
}

func TestSessionSlidingExpiry(t *testing.T) {
	s := newStack(t)
	token := s.register("slider@example.com", "a-fine-password")

	// The session minted at registration carries the full TTL; shrink it
	// to 200ms and let it lapse. (No authenticated use in between — every
	// use slides the window back out to the full TTL, which is the very
	// behavior under test.)
	_, err := s.pool.Exec(context.Background(),
		`UPDATE sessions SET expires_at = now() + interval '200 milliseconds'`)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
	if status, _ := s.get("/api/uc/me", token); status != http.StatusUnauthorized {
		t.Fatalf("expired session: %d", status)
	}
	// A live session slides: use it well inside its TTL and the expiry
	// moves forward instead of the session dying with age.
	login := map[string]string{"email": "slider@example.com", "password": "a-fine-password"}
	_, body := s.post("/api/uc/login", login, "")
	fresh := body["token"].(string)
	_, err = s.pool.Exec(context.Background(),
		`UPDATE sessions SET expires_at = now() + interval '600 milliseconds' WHERE token_hash = $1`,
		auth.TokenDigest(fresh))
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond) // half of the shrunk window elapses
	if status, _ := s.get("/api/uc/me", fresh); status != http.StatusOK {
		t.Fatalf("mid-window use should slide expiry: %d", status)
	}
	time.Sleep(400 * time.Millisecond) // the slid window (1h) far outlasts this
	if status, _ := s.get("/api/uc/me", fresh); status != http.StatusOK {
		t.Fatalf("slid session must stay live: %d", status)
	}
}

func TestRegisterRejectsDuplicateEmail(t *testing.T) {
	s := newStack(t)
	email := "dupe@example.com"
	s.register(email, "first-password")
	code, err := s.requestCode(email)
	if err != nil {
		t.Fatal(err)
	}
	if status, body := s.post("/api/uc/register", map[string]string{
		"email": email, "code": code, "password": "second-password"}, ""); status != http.StatusConflict {
		t.Fatalf("duplicate register: status %d body %v", status, body)
	}
}

// testLogger is a quiet logger for ITs (failures carry the evidence).
func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
