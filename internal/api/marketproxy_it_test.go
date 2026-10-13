//go:build integration

// Market-data proxy integration tests (spec #24 ticket #43): the real
// market RPC against a throwaway Postgres, fronted by the public proxy
// registrar. Every proxied answer is golden-compared against market's
// internal answer — status, Content-Type, body bytes on success; status
// and message on named errors (the internal {"error": …} body is
// re-emitted as the public {"message": …} envelope). The failure seam
// is the ticket's acceptance: with market off, the proxy answers 503
// within budget, never a hang.
package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/syaikhipin/malxchange/internal/market"
	"github.com/syaikhipin/malxchange/internal/migrations"
	"github.com/syaikhipin/malxchange/internal/pgmig"
)

// mpGate skips when the dev infra env is absent (make test-integration
// sets it). Deliberately no TestMain: further api suites land with the
// sibling tickets, and one TestMain per package would collide at merge.
func mpGate(t *testing.T) {
	t.Helper()
	if os.Getenv("MAL_TEST_PG_HOST") == "" || os.Getenv("MAL_TEST_PG_SUPERUSER_PASSWORD") == "" {
		t.Skip("skipping api market-proxy integration tests: set MAL_TEST_PG_* (make test-integration)")
	}
}

// mpGet fetches one URL through a client with its own budget, so a hung
// server fails the test instead of hanging it.
func mpGet(t *testing.T, rawURL string) (status int, contentType string, body []byte) {
	t.Helper()
	client := &http.Client{Timeout: 10 * time.Second}
	res, err := client.Get(rawURL)
	if err != nil {
		t.Fatalf("GET %s: %v", rawURL, err)
	}
	defer res.Body.Close()
	b, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("GET %s: read body: %v", rawURL, err)
	}
	return res.StatusCode, res.Header.Get("Content-Type"), b
}

// mpPGURL mirrors config.PG.URL for the throwaway database.
func mpPGURL(t *testing.T, db, schema string) string {
	t.Helper()
	u := url.URL{
		Scheme:   "postgres",
		User:     url.UserPassword(os.Getenv("MAL_TEST_PG_SUPERUSER"), os.Getenv("MAL_TEST_PG_SUPERUSER_PASSWORD")),
		Host:     os.Getenv("MAL_TEST_PG_HOST") + ":5432",
		Path:     "/" + db,
		RawQuery: "sslmode=disable&search_path=" + schema,
	}
	return u.String()
}

// newMarketProxyITStack builds the ticket's two halves against one
// throwaway database: the real market RPC mounted at the production URL
// shape (root mux stripping /internal onto the service-internal mux —
// the boot harness wiring), and the public proxy registrar pointing at
// it. The database name is unique per test binary: sibling worktrees'
// IT suites share the dev Postgres, and a fixed name would let one
// DROP/recreate pull the floor out from under another mid-run.
func newMarketProxyITStack(t *testing.T) (marketSrv, apiSrv *httptest.Server) {
	t.Helper()
	ctx := context.Background()

	suffix := make([]byte, 6)
	if _, err := rand.Read(suffix); err != nil {
		t.Fatal(err)
	}
	dbName := "malxchange_ita_" + hex.EncodeToString(suffix)

	admin, err := pgxpool.New(ctx, fmt.Sprintf("postgres://%s:%s@%s:5432/postgres",
		os.Getenv("MAL_TEST_PG_SUPERUSER"), os.Getenv("MAL_TEST_PG_SUPERUSER_PASSWORD"),
		os.Getenv("MAL_TEST_PG_HOST")))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(ctx,
		fmt.Sprintf(`DROP DATABASE IF EXISTS %s WITH (FORCE)`, dbName)); err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(ctx, fmt.Sprintf(`CREATE DATABASE %s`, dbName)); err != nil {
		t.Fatal(err)
	}
	admin.Close()
	t.Cleanup(func() {
		admin, err := pgxpool.New(ctx, fmt.Sprintf("postgres://%s:%s@%s:5432/postgres",
			os.Getenv("MAL_TEST_PG_SUPERUSER"), os.Getenv("MAL_TEST_PG_SUPERUSER_PASSWORD"),
			os.Getenv("MAL_TEST_PG_HOST")))
		if err != nil {
			return
		}
		defer admin.Close()
		_, _ = admin.Exec(ctx, fmt.Sprintf(`DROP DATABASE IF EXISTS %s WITH (FORCE)`, dbName))
	})

	pool, err := pgxpool.New(ctx, mpPGURL(t, dbName, "market"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if _, err := pool.Exec(ctx, `CREATE SCHEMA IF NOT EXISTS market`); err != nil {
		t.Fatal(err)
	}
	if err := pgmig.Run(mpPGURL(t, dbName, "market"), migrations.FS, "market"); err != nil {
		t.Fatalf("market migrations: %v", err)
	}
	mpSeed(t, pool)

	internal := http.NewServeMux()
	market.RegisterRPC(internal, &market.RPC{DB: pool})
	root := http.NewServeMux()
	root.Handle("/internal/", http.StripPrefix("/internal", internal))
	marketSrv = httptest.NewServer(root)
	t.Cleanup(marketSrv.Close)

	pub := http.NewServeMux()
	RegisterMarketProxy(pub, MarketProxyConfig{
		MarketURL: marketSrv.URL + "/internal",
		Timeout:   5 * time.Second,
	})
	apiSrv = httptest.NewServer(pub)
	t.Cleanup(apiSrv.Close)
	return marketSrv, apiSrv
}

// mpSeed plants a small lawful projection — two visible Markets and one
// invisible, Fills inside the rolling 24h Ticker window, both sides of
// a Depth, Kline bars at two stored intervals. Money values are decimal
// strings end to end (ADR-0005); numeric keeps them verbatim through
// ::text, so the golden comparison exercises real values, not zeros.
func mpSeed(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC()

	_, err := pool.Exec(ctx, `
		INSERT INTO markets (market, base_coin, quote_coin, base_scale, quote_scale,
			tick_size, lot_size, maker_fee_rate, taker_fee_rate, market_orders_enabled,
			enabled, visible, sort, zone, featured)
		VALUES
			('BTC/USDT', 'BTC', 'USDT', 8, 2, 0.10, 0.0001, 0.001, 0.001, true, true, true, 1, 'main', true),
			('ETH/USDT', 'ETH', 'USDT', 8, 2, 0.01, 0.001,  0.001, 0.001, true, true, true, 2, 'main', false),
			('DOGE/USDT', 'DOGE', 'USDT', 8, 2, 0.0001, 1, 0.001, 0.001, true, true, false, 3, 'innovation', false)`)
	if err != nil {
		t.Fatalf("seed markets: %v", err)
	}

	_, err = pool.Exec(ctx, `
		INSERT INTO trades (fill_id, market, taker_side, price, amount, turnover, traded_at)
		VALUES
			('fill-0001', 'BTC/USDT', 'buy',  50000.10, 0.0002, 10.00002,  $1),
			('fill-0002', 'BTC/USDT', 'sell', 50000.50, 0.0003, 15.00015,  $2),
			('fill-0003', 'BTC/USDT', 'buy',  50001.00, 0.0001, 5.0001,    $3)`,
		now.Add(-2*time.Hour), now.Add(-1*time.Hour), now.Add(-30*time.Minute))
	if err != nil {
		t.Fatalf("seed trades: %v", err)
	}

	_, err = pool.Exec(ctx, `
		INSERT INTO depth_levels (market, side, price, amount)
		VALUES
			('BTC/USDT', 'buy',  50000.10, 0.0002),
			('BTC/USDT', 'buy',  49999.00, 0.0004),
			('BTC/USDT', 'sell', 50010.20, 0.0003),
			('BTC/USDT', 'sell', 50011.00, 0.0005)`)
	if err != nil {
		t.Fatalf("seed depth: %v", err)
	}

	bucket := now.Add(-2 * time.Hour).Truncate(time.Minute)
	_, err = pool.Exec(ctx, `
		INSERT INTO klines (market, "interval", bucket_open, open, high, low, close,
			amount, turnover, fill_count, first_at, first_seq, last_at, last_seq)
		VALUES
			('BTC/USDT', '1m', $1, 50000.10, 50010.20, 49999.00, 50005.00, 1.2345, 61728.39, 7, $1, 1, $1, 7),
			('BTC/USDT', '1m', $2, 50005.00, 50020.00, 50001.00, 50012.00, 2.0000, 100034.50, 3, $2, 8, $2, 10),
			('BTC/USDT', '5m', $1, 50000.10, 50020.00, 49999.00, 50012.00, 3.2345, 161762.89, 10, $1, 1, $1, 10)`,
		bucket, bucket.Add(time.Minute))
	if err != nil {
		t.Fatalf("seed klines: %v", err)
	}
}

// TestMarketProxyGoldenComparesAgainstMarketInternal is the ticket's
// acceptance seam: every Market-data endpoint reachable anonymously
// through api with the identical wire answer market serves internally —
// byte-identical successes, and named errors keeping status and message
// while crossing into the public {"message": …} envelope.
func TestMarketProxyGoldenComparesAgainstMarketInternal(t *testing.T) {
	mpGate(t)
	marketSrv, apiSrv := newMarketProxyITStack(t)

	paths := []string{
		// successes, every proxied route
		"/markets",
		"/trades/BTC/USDT",
		"/trades/BTC/USDT?limit=2",
		"/depth/BTC/USDT",
		"/kline/BTC/USDT?interval=1m",
		"/kline/BTC/USDT?interval=5m&limit=1",
		"/ticker/BTC/USDT",
		"/tickers",
		// unknown Markets answer their honest-empty shapes, identically
		"/trades/UNKNOWN/PAIR",
		"/depth/UNKNOWN/PAIR",
		"/ticker/UNKNOWN/PAIR",
		// named errors keep their names through the public envelope
		"/kline/BTC/USDT",                     // absent interval → errBadInterval
		"/kline/BTC/USDT?interval=2m",         // unknown interval → errBadInterval
		"/trades/BTC/USDT?limit=notanumber",   // errBadLimit
		"/trades/BTC/USDT?limit=1001",         // errBadLimit
		"/kline/BTC/USDT?interval=1m&limit=x", // errBadLimit
	}
	for _, path := range paths {
		dStatus, dCT, dBody := mpGet(t, marketSrv.URL+"/internal"+path)
		pStatus, pCT, pBody := mpGet(t, apiSrv.URL+"/api"+path)

		if pStatus != dStatus {
			t.Errorf("GET %s: public status = %d, internal status = %d", path, pStatus, dStatus)
		}
		if dStatus >= http.StatusBadRequest {
			// A named error: same status, same message, public envelope.
			var dErr struct {
				Error string `json:"error"`
			}
			var pErr struct {
				Message string `json:"message"`
			}
			if err := json.Unmarshal(dBody, &dErr); err != nil {
				t.Errorf("GET %s: internal error body does not decode: %v (%q)", path, err, dBody)
				continue
			}
			if err := json.Unmarshal(pBody, &pErr); err != nil {
				t.Errorf("GET %s: public error body does not decode: %v (%q)", path, err, pBody)
				continue
			}
			if pErr.Message == "" {
				t.Errorf("GET %s: public error body carries no message: %q", path, pBody)
				continue
			}
			if pErr.Message != dErr.Error {
				t.Errorf("GET %s: public message = %q, internal error = %q", path, pErr.Message, dErr.Error)
			}
			if pCT != "application/json" {
				t.Errorf("GET %s: public error content-type = %q, want application/json", path, pCT)
			}
			continue
		}
		if string(pBody) != string(dBody) {
			t.Errorf("GET %s: public body = %q, internal body = %q", path, pBody, dBody)
		}
		if pCT != dCT {
			t.Errorf("GET %s: public content-type = %q, internal = %q", path, pCT, dCT)
		}
	}

	// Sanity: the seeds must actually be answerable — a golden compare
	// against an empty projection would pass vacuously.
	_, _, tickBody := mpGet(t, apiSrv.URL+"/api/ticker/BTC/USDT")
	var tick struct {
		Last string `json:"last"`
	}
	if err := json.Unmarshal(tickBody, &tick); err != nil {
		t.Fatalf("ticker body does not decode: %v (%q)", err, tickBody)
	}
	if tick.Last == "" {
		t.Errorf("ticker last = %q, want the seeded Fill's price (empty projection?)", tick.Last)
	}
	_, _, marketsBody := mpGet(t, apiSrv.URL+"/api/markets")
	if m := string(marketsBody); !strings.Contains(m, "BTC/USDT") || strings.Contains(m, "DOGE/USDT") {
		t.Errorf("visible listing = %q, want BTC/USDT listed and the invisible DOGE/USDT absent", m)
	}
}

// TestMarketProxyRealMarketDownAnswers503 is the ticket's failure
// acceptance, against the real stack: the market RPC (real process
// against a real database) goes away, and the proxy answers an explicit
// 503 — not a hang, not a masked 500.
func TestMarketProxyRealMarketDownAnswers503(t *testing.T) {
	mpGate(t)
	marketSrv, apiSrv := newMarketProxyITStack(t)

	// The market RPC goes down mid-flight.
	marketSrv.Close()

	start := time.Now()
	status, contentType, body := mpGet(t, apiSrv.URL+"/api/markets")
	elapsed := time.Since(start)
	if status != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", status)
	}
	if contentType != "application/json" {
		t.Errorf("content-type = %q, want application/json", contentType)
	}
	var pErr struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(body, &pErr); err != nil {
		t.Fatalf("503 body does not decode: %v (%q)", err, body)
	}
	if pErr.Message != errMarketDown.Error() {
		t.Errorf("503 message = %q, want %q", pErr.Message, errMarketDown.Error())
	}
	if elapsed > 3*time.Second {
		t.Fatalf("down market answered after %s; the budget was not enforced", elapsed)
	}
}
