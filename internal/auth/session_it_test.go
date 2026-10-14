//go:build integration

// Session expiry models over the real seam (issue #52): the shared
// session store against Postgres in a throwaway database (unique name —
// sibling IT suites share the dev Postgres). The sliding model pins the
// member-api behavior the refactor must not change; the absolute+idle
// model pins the ADR-0012 Employee shape the parameterization unlocks.
package auth

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/syaikhipin/malxchange/internal/migrations"
	"github.com/syaikhipin/malxchange/internal/pgmig"
)

func TestMain(m *testing.M) {
	if os.Getenv("MAL_TEST_PG_HOST") == "" || os.Getenv("MAL_TEST_PG_SUPERUSER_PASSWORD") == "" {
		fmt.Println("skipping auth integration tests: set MAL_TEST_PG_* (make test-integration)")
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func newAuthDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	ctx := context.Background()

	suffix := make([]byte, 6)
	if _, err := rand.Read(suffix); err != nil {
		t.Fatal(err)
	}
	dbName := "malxchange_itauth_" + hex.EncodeToString(suffix)

	admin, err := pgxpool.New(ctx, fmt.Sprintf("postgres://%s:%s@%s:5432/postgres",
		os.Getenv("MAL_TEST_PG_SUPERUSER"), os.Getenv("MAL_TEST_PG_SUPERUSER_PASSWORD"), os.Getenv("MAL_TEST_PG_HOST")))
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
			os.Getenv("MAL_TEST_PG_SUPERUSER"), os.Getenv("MAL_TEST_PG_SUPERUSER_PASSWORD"), os.Getenv("MAL_TEST_PG_HOST")))
		if err != nil {
			return
		}
		defer admin.Close()
		_, _ = admin.Exec(ctx, fmt.Sprintf(`DROP DATABASE IF EXISTS %s WITH (FORCE)`, dbName))
	})

	pool, err := pgxpool.New(ctx, fmt.Sprintf("postgres://%s:%s@%s:5432/%s?sslmode=disable&search_path=api",
		os.Getenv("MAL_TEST_PG_SUPERUSER"), os.Getenv("MAL_TEST_PG_SUPERUSER_PASSWORD"), os.Getenv("MAL_TEST_PG_HOST"), dbName))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if _, err := pool.Exec(ctx, `CREATE SCHEMA IF NOT EXISTS api`); err != nil {
		t.Fatal(err)
	}
	u := url.URL{
		Scheme:   "postgres",
		User:     url.UserPassword(os.Getenv("MAL_TEST_PG_SUPERUSER"), os.Getenv("MAL_TEST_PG_SUPERUSER_PASSWORD")),
		Host:     os.Getenv("MAL_TEST_PG_HOST") + ":5432",
		Path:     dbName,
		RawQuery: "sslmode=disable&search_path=api",
	}
	if err := pgmig.Run(u.String(), migrations.FS, "api"); err != nil {
		t.Fatalf("api migrations: %v", err)
	}
	return pool
}

// memberFixture inserts the owner row the sessions FK points at.
func memberFixture(t *testing.T, pool *pgxpool.Pool, id string) {
	t.Helper()
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO members (id, email, password_hash) VALUES ($1, $2, 'x') ON CONFLICT DO NOTHING`,
		id, id+"@example.com"); err != nil {
		t.Fatal(err)
	}
}

// mintInto mints a session for a fixture member and returns the token.
func mintInto(t *testing.T, pool *pgxpool.Pool, member string, p SessionPolicy) (string, Session) {
	t.Helper()
	token, sess, err := MintSession(context.Background(), pool, member, "it device", p)
	if err != nil {
		t.Fatalf("MintSession: %v", err)
	}
	return token, sess
}

func mustResolve(t *testing.T, pool *pgxpool.Pool, token string, p SessionPolicy) Session {
	t.Helper()
	s, err := ResolveSession(context.Background(), pool, token, p)
	if err != nil {
		t.Fatalf("ResolveSession rejected a live token: %v", err)
	}
	return s
}

func mustReject(t *testing.T, pool *pgxpool.Pool, token string, p SessionPolicy) {
	t.Helper()
	if _, err := ResolveSession(context.Background(), pool, token, p); !errors.Is(err, ErrSessionRejected) {
		t.Fatalf("ResolveSession err = %v, want ErrSessionRejected", err)
	}
}

// durUntil reports how far a scanned expiry lies ahead of the server
// clock (tz-safe: only the distance is compared).
func durUntil(t *testing.T, at time.Time) time.Duration {
	t.Helper()
	return time.Until(at)
}

func TestSessionSlidesWithinIdleWindow(t *testing.T) {
	pool := newAuthDB(t)
	memberFixture(t, pool, "m-slide")
	p := SessionPolicy{Idle: time.Hour}

	token, minted := mintInto(t, pool, "m-slide", p)
	if d := durUntil(t, minted.ExpiresAt); d < 59*time.Minute || d > 61*time.Minute {
		t.Fatalf("minted expiry %v from now, want ~1h", d)
	}
	s := mustResolve(t, pool, token, p)
	if d := durUntil(t, s.ExpiresAt); d < 59*time.Minute || d > 61*time.Minute {
		t.Fatalf("resolve did not slide to the full window: %v from now", d)
	}

	// A session left unused dies when its window lapses.
	if _, err := pool.Exec(context.Background(),
		`UPDATE sessions SET expires_at = now() + interval '200 milliseconds'`); err != nil {
		t.Fatal(err)
	}
	time.Sleep(400 * time.Millisecond)
	mustReject(t, pool, token, p)

	// A live session slides: use it mid-window and the expiry moves back
	// out to the full window instead of the session dying with age.
	fresh, _ := mintInto(t, pool, "m-slide", p)
	if _, err := pool.Exec(context.Background(),
		`UPDATE sessions SET expires_at = now() + interval '600 milliseconds' WHERE token_hash = $1`,
		TokenDigest(fresh)); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond) // half of the shrunk window elapses
	slid := mustResolve(t, pool, fresh, p)
	if d := durUntil(t, slid.ExpiresAt); d < 59*time.Minute {
		t.Fatalf("mid-window use must slide to the full window: %v from now", d)
	}
}

func TestSessionAbsoluteCapsSlideAndIdleExpires(t *testing.T) {
	pool := newAuthDB(t)
	memberFixture(t, pool, "m-abs")
	p := SessionPolicy{Absolute: 2 * time.Hour, Idle: 30 * time.Minute}

	// Mint starts at the absolute lifetime, not the idle window, and the
	// fresh session resolves.
	token, minted := mintInto(t, pool, "m-abs", p)
	if d := durUntil(t, minted.ExpiresAt); d < 118*time.Minute || d > 122*time.Minute {
		t.Fatalf("minted expiry %v from now, want ~2h (absolute, not idle)", d)
	}
	mustResolve(t, pool, token, p)

	// Idle expiry: unused past the idle window, the session is rejected
	// even though its absolute lifetime has room left.
	tok := mustMintToken(t, pool, "m-abs", p)
	if _, err := pool.Exec(context.Background(),
		`UPDATE sessions SET last_used_at = now() - interval '31 minutes' WHERE token_hash = $1`,
		TokenDigest(tok)); err != nil {
		t.Fatal(err)
	}
	mustReject(t, pool, tok, p)

	// The slide is capped by the absolute lifetime: used near the end of
	// the absolute window, the expiry lands on created_at + Absolute even
	// though the idle window alone would allow more.
	tok = mustMintToken(t, pool, "m-abs", p)
	if _, err := pool.Exec(context.Background(),
		`UPDATE sessions SET created_at = now() - interval '1 hour 50 minutes',
		                               last_used_at = now() - interval '5 minutes'
		 WHERE token_hash = $1`, TokenDigest(tok)); err != nil {
		t.Fatal(err)
	}
	capped := mustResolve(t, pool, tok, p)
	if d := durUntil(t, capped.ExpiresAt); d > 12*time.Minute {
		t.Fatalf("absolute must cap the slide: expiry %v from now, want ~10m", d)
	}

	// Absolute expiry: past created_at + Absolute, the session is dead
	// even with fresh use.
	tok = mustMintToken(t, pool, "m-abs", p)
	if _, err := pool.Exec(context.Background(),
		`UPDATE sessions SET created_at = now() - interval '2 hours 5 minutes',
		                               last_used_at = now() - interval '1 minute'
		 WHERE token_hash = $1`, TokenDigest(tok)); err != nil {
		t.Fatal(err)
	}
	mustReject(t, pool, tok, p)
}

func TestMintSessionNeedsIdleWindow(t *testing.T) {
	pool := newAuthDB(t)
	memberFixture(t, pool, "m-zero")
	for _, p := range []SessionPolicy{{}, {Absolute: time.Hour}} {
		if _, _, err := MintSession(context.Background(), pool, "m-zero", "d", p); err == nil {
			t.Fatalf("policy %+v minted a session", p)
		}
	}
}

func mustMintToken(t *testing.T, pool *pgxpool.Pool, member string, p SessionPolicy) string {
	t.Helper()
	token, _, err := MintSession(context.Background(), pool, member, "it device", p)
	if err != nil {
		t.Fatalf("MintSession: %v", err)
	}
	return token
}
