//go:build integration

// Integration tests for the platform slice (#20): migrations per service
// schema, cross-schema isolation, the outbox→Redpanda roundtrip, and
// inbox dedupe — against the dev compose infra (make test-integration).
//
// Gate on MAL_TEST_PG_HOST so plain `go test ./...` stays hermetic.
package it

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/syaikhipin/malxchange/internal/config"
	"github.com/syaikhipin/malxchange/internal/events"
	"github.com/syaikhipin/malxchange/internal/inbox"
	"github.com/syaikhipin/malxchange/internal/kafka"
	"github.com/syaikhipin/malxchange/internal/migrations"
	"github.com/syaikhipin/malxchange/internal/outbox"
	"github.com/syaikhipin/malxchange/internal/pgmig"
)

var (
	testPGHost    = os.Getenv("MAL_TEST_PG_HOST")
	testPGSuper   = os.Getenv("MAL_TEST_PG_SUPERUSER")
	testPGSuperPW = os.Getenv("MAL_TEST_PG_SUPERUSER_PASSWORD")
	testSeeds     = []string{envOr("MAL_TEST_KAFKA_SEEDS", "127.0.0.1:9092")}
	testKafkaUser = envOr("MAL_TEST_KAFKA_USER", "malxchange")
	testKafkaPass = os.Getenv("MAL_TEST_KAFKA_PASSWORD")
)

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func superURL(db string) string {
	u := url.URL{
		Scheme: "postgres",
		User:   url.UserPassword(testPGSuper, testPGSuperPW),
		Host:   testPGHost + ":5432",
		Path:   "/" + db,
	}
	return u.String()
}

func TestMain(m *testing.M) {
	if testPGHost == "" || testPGSuperPW == "" {
		fmt.Println("skipping integration tests: MAL_TEST_PG_HOST / MAL_TEST_PG_SUPERUSER_PASSWORD not set (run: make test-integration)")
		os.Exit(0)
	}
	// Unique throwaway names per test binary: sibling worktrees' IT runs
	// share the dev Postgres, and fixed database or role names would let
	// one run's DROP/recreate pull the floor out from under another's
	// mid-test run.
	suffix := make([]byte, 6)
	if _, err := rand.Read(suffix); err != nil {
		fmt.Println("mint throwaway names:", err)
		os.Exit(1)
	}
	testDB = "malxchange_it_" + hex.EncodeToString(suffix)
	roleSuffix = hex.EncodeToString(suffix)
	os.Exit(m.Run())
}

// Minted once per test binary (see TestMain). The roles carry the same
// suffix: they are cluster-global, so a fixed name would collide across
// concurrent runs the way the database name does.
var (
	testDB     string
	roleSuffix string
)

// itRole is this binary's throwaway role/schema name for svc.
func itRole(svc string) string { return "it_" + roleSuffix + "_" + svc }

// freshDB drops and recreates the throwaway database and the per-service
// roles (both named per test binary; a leftover within this binary — a
// cleanup whose reconnect failed — would break CREATE ROLE); every test
// gets a clean slate.
func freshDB(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, superURL("postgres"))
	if err != nil {
		t.Fatalf("connect postgres db: %v", err)
	}
	defer admin.Close()
	if _, err := admin.Exec(ctx, fmt.Sprintf(`DROP DATABASE IF EXISTS %s WITH (FORCE)`, testDB)); err != nil {
		t.Fatalf("drop: %v", err)
	}
	// config.Services must stay in lockstep with migrations.Services (the
	// schema tests iterate the latter): a divergence would leave minted
	// roles undropped and break the next CREATE ROLE.
	for _, svc := range config.Services {
		name := itRole(svc) // prod-named roles belong to the dev stack, not us
		if _, err := admin.Exec(ctx, fmt.Sprintf(`DROP ROLE IF EXISTS "%s"`, name)); err != nil {
			t.Fatalf("drop role %s: %v", name, err)
		}
	}
	if _, err := admin.Exec(ctx, fmt.Sprintf(`CREATE DATABASE %s`, testDB)); err != nil {
		t.Fatalf("create: %v", err)
	}
	t.Cleanup(func() {
		admin, err := pgxpool.New(ctx, superURL("postgres"))
		if err != nil {
			return
		}
		defer admin.Close()
		// Database first: this binary's roles own schemas inside it, and
		// DROP ROLE refuses while they still do.
		_, _ = admin.Exec(ctx, fmt.Sprintf(`DROP DATABASE IF EXISTS %s WITH (FORCE)`, testDB))
		for _, svc := range config.Services {
			_, _ = admin.Exec(ctx, fmt.Sprintf(`DROP ROLE IF EXISTS "%s"`, itRole(svc)))
		}
	})
}

// setupServiceSchema mints a throwaway role+schema named it_<suffix>_<svc>
// (the per-binary suffix from TestMain). Roles are cluster-global and the
// dev stack already has prod-named ones (with grants on the real
// database), so tests must not reuse those names. The migration set still
// comes from the service's directory.
func setupServiceSchema(t *testing.T, svc string) config.PG {
	t.Helper()
	name := itRole(svc)
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, superURL(testDB))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer admin.Close()
	pw := uuid.NewString()
	if _, err := admin.Exec(ctx, fmt.Sprintf(`CREATE ROLE "%s" LOGIN PASSWORD '%s'`, name, pw)); err != nil {
		t.Fatalf("role %s: %v", name, err)
	}
	if _, err := admin.Exec(ctx, fmt.Sprintf(`CREATE SCHEMA "%s" AUTHORIZATION "%s"`, name, name)); err != nil {
		t.Fatalf("schema %s: %v", name, err)
	}
	if _, err := admin.Exec(ctx, fmt.Sprintf(`GRANT CREATE ON DATABASE %s TO "%s"`, testDB, name)); err != nil {
		t.Fatalf("grant: %v", err)
	}
	return config.PG{
		Host: testPGHost, Port: "5432", Database: testDB,
		User: name, Password: pw, Schema: name,
	}
}

func TestMigrationsLandInEachServiceSchema(t *testing.T) {
	freshDB(t)
	for _, svc := range migrations.Services {
		pgCfg := setupServiceSchema(t, svc)
		if err := pgmig.Run(pgCfg.URL(), migrations.FS, svc); err != nil {
			t.Fatalf("%s: migrations: %v", svc, err)
		}
		ctx := context.Background()
		admin, err := pgxpool.New(ctx, superURL(testDB))
		if err != nil {
			t.Fatal(err)
		}
		for _, table := range []string{"outbox", "inbox"} {
			var reg *string
			err := admin.QueryRow(ctx,
				fmt.Sprintf(`SELECT to_regclass('%s.%s')`, pgCfg.Schema, table)).Scan(&reg)
			if err != nil || reg == nil {
				t.Fatalf("%s: table %s missing (err=%v reg=%v)", pgCfg.Schema, table, err, reg)
			}
		}
		admin.Close()

		// Running again is a no-op (boot-idempotence).
		if err := pgmig.Run(pgCfg.URL(), migrations.FS, svc); err != nil {
			t.Fatalf("%s: re-run migrations: %v", svc, err)
		}
	}
}

func TestSchemaIsolationBlocksCrossServiceReads(t *testing.T) {
	freshDB(t)
	apiCfg := setupServiceSchema(t, "api")
	walletCfg := setupServiceSchema(t, "wallet")
	if err := pgmig.Run(apiCfg.URL(), migrations.FS, "api"); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, apiCfg.URL())
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	// api writes into its own schema fine...
	if _, err := pool.Exec(ctx, `SELECT count(*) FROM outbox`); err != nil {
		t.Fatalf("api cannot read its own outbox: %v", err)
	}
	// ...and cannot even see wallet's schema (USAGE denied by default).
	_, err = pool.Exec(ctx, fmt.Sprintf(`SELECT count(*) FROM %s.outbox`, walletCfg.Schema))
	if err == nil {
		t.Fatal("api could read wallet.outbox — schema isolation broken")
	}
}

func TestOutboxRoundtripThroughRedpanda(t *testing.T) {
	freshDB(t)
	svc := "exchange"
	pgCfg := setupServiceSchema(t, svc)
	if err := pgmig.Run(pgCfg.URL(), migrations.FS, svc); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	pool, err := pgxpool.New(ctx, pgCfg.URL())
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	kcfg := config.Kafka{Seeds: testSeeds, User: testKafkaUser, Password: testKafkaPass}
	producer, err := kafka.NewProducer(kcfg)
	if err != nil {
		t.Fatal(err)
	}
	defer producer.Close()
	if err := kafka.Ping(ctx, producer); err != nil {
		t.Fatalf("broker unreachable (SASL creds correct?): %v", err)
	}

	topic := "it.outbox." + uuid.NewString()[:8] // fresh topic per run: no stale records
	if err := kafka.EnsureTopics(ctx, producer, []kafka.TopicDef{{Name: topic, Partitions: 1, Retention: time.Hour}}); err != nil {
		t.Fatal(err)
	}

	// Subscribe before producing; reset to earliest so the assertion is
	// independent of group-offset state.
	group := "it-" + uuid.NewString()
	consumer, err := kafka.NewConsumer(kcfg, group, []string{topic}, kgo.ConsumeResetOffset(kgo.NewOffset().AtStart()))
	if err != nil {
		t.Fatal(err)
	}
	defer consumer.Close()

	// Enqueue inside a domain transaction, as a service would.
	type tradeFill struct {
		Market string `json:"market"`
		Price  string `json:"price"`
	}
	envID := uuid.NewString()
	_, body, err := events.New(envID, "BTC/USDT", events.TopicTradeFill, time.Now().UTC(), tradeFill{Market: "BTC/USDT", Price: "65000.5"})
	if err != nil {
		t.Fatal(err)
	}
	err = pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		return outbox.New(pool).Enqueue(ctx, tx, topic, "BTC/USDT", body)
	})
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	// Relay publishes it.
	relay := outbox.NewRelay(pool, kafka.Publisher{Client: producer})
	relayCtx, relayCancel := context.WithCancel(ctx)
	go func() { _ = relay.Run(relayCtx) }()
	defer relayCancel()

	deadline := time.Now().Add(20 * time.Second)
	published := false
	for time.Now().Before(deadline) {
		var status string
		if err := pool.QueryRow(ctx, `SELECT status FROM outbox WHERE id=$1`, envID).Scan(&status); err == nil && status == "published" {
			published = true
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if !published {
		t.Fatal("outbox row never reached status=published")
	}

	// And Redpanda serves the envelope to a consumer.
	got := ""
	deadline = time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) && got == "" {
		for rec := range consumer.PollRecords(ctx, 10).RecordsAll() {
			if string(rec.Key) != "BTC/USDT" {
				continue
			}
			e, err := events.Decode(rec.Value)
			if err != nil {
				t.Fatalf("consumed undecodable envelope: %v", err)
			}
			if e.ID != envID {
				t.Fatalf("consumed unexpected envelope id %s, want %s", e.ID, envID)
			}
			got = string(e.Data)
		}
		consumer.AllowRebalance()
	}
	if got != `{"market":"BTC/USDT","price":"65000.5"}` {
		t.Fatalf("consumer did not see the fill envelope; got %q", got)
	}
}

func TestInboxDedupeAppliesExactlyOnce(t *testing.T) {
	freshDB(t)
	svc := "market"
	pgCfg := setupServiceSchema(t, svc)
	if err := pgmig.Run(pgCfg.URL(), migrations.FS, svc); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, pgCfg.URL())
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if _, err := pool.Exec(ctx, `CREATE TABLE credits (id serial primary key, amount text not null)`); err != nil {
		t.Fatal(err)
	}

	apply := func() error {
		return pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
			return inbox.Apply(ctx, tx, "trade:1", "outbox-uuid-1", func(tx pgx.Tx) error {
				_, err := tx.Exec(ctx, `INSERT INTO credits (amount) VALUES ('1.5')`)
				return err
			})
		})
	}
	if err := apply(); err != nil {
		t.Fatalf("first apply: %v", err)
	}
	if err := apply(); err != nil { // at-least-once redelivery
		t.Fatalf("duplicate apply: %v", err)
	}

	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM credits`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("credits applied %d times, want exactly 1", n)
	}
}
