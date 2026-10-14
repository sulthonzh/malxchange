//go:build integration

// The config-editor surfaces over the served HTTP seam (ticket #61):
// the Coin editor forwards to the wallet's Coin config RPC, the Market
// editor to the exchange's Market-config RPC, and /api/engine relays
// the engine status view to every Role. The upstreams are httptest
// stubs speaking their service's wire shapes — admin-api owns RBAC,
// the Audit envelope (before/after = the upstream's own row JSON), and
// honest error relaying; it owns neither validation nor storage.
package admin

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

	"github.com/syaikhipin/malxchange/internal/migrations"
	"github.com/syaikhipin/malxchange/internal/pgmig"
)

// ---- stub upstreams ----

// stubWallet is a wallet Coin-config RPC fake: an in-memory row per
// Coin, the detail wire without the xpub (write-only custody field).
type stubWallet struct {
	srv    *httptest.Server
	coins  map[string]map[string]any
	sawPut []string // raw bodies of PUTs, in order
}

func newStubWallet(t *testing.T) *stubWallet {
	t.Helper()
	s := &stubWallet{coins: map[string]map[string]any{
		"BTC": {
			"name": "BTC", "family": "btc", "native": true, "contract": "",
			"chainDecimals": 8, "scale": 8, "confirmations": 3,
			"withdrawFee": "0.0005", "minWithdraw": "0.001", "withdrawable": true,
			"sweepThreshold": "0.05", "sweepMinSeconds": 3600, "sweepPaused": false,
			"coldAddress": "", "hotCeiling": "0", "hotFloor": "0",
		},
	}}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /coins", func(w http.ResponseWriter, _ *http.Request) {
		s.writeList(w)
	})
	mux.HandleFunc("GET /coins/{name}", func(w http.ResponseWriter, r *http.Request) {
		row, ok := s.coins[r.PathValue("name")]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": fmt.Sprintf("unknown coin %s", r.PathValue("name"))})
			return
		}
		s.writeRow(w, row)
	})
	mux.HandleFunc("PUT /coins/{name}", func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		row, ok := s.coins[name]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": fmt.Sprintf("unknown coin %s", name)})
			return
		}
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		s.sawPut = append(s.sawPut, string(raw))
		var patch map[string]any
		if err := json.Unmarshal(raw, &patch); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		for k, v := range patch {
			row[k] = v
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"name": name, "updated": "true"})
	})
	s.srv = httptest.NewServer(mux)
	t.Cleanup(s.srv.Close)
	return s
}

func (s *stubWallet) writeRow(w http.ResponseWriter, row map[string]any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(row)
}

func (s *stubWallet) writeList(w http.ResponseWriter) {
	out := make([]map[string]any, 0, len(s.coins))
	for _, row := range s.coins {
		out = append(out, row)
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

// stubExchange is an exchange Market-config + engine-status fake.
type stubExchange struct {
	srv       *httptest.Server
	markets   map[string]map[string]any
	refuseTod bool // when true, a tickSize edit answers the freeze-guard 409
}

func newStubExchange(t *testing.T) *stubExchange {
	t.Helper()
	s := &stubExchange{markets: map[string]map[string]any{
		"BTC/USDT": {
			"symbol": "BTC/USDT", "baseCoin": "BTC", "quoteCoin": "USDT",
			"baseScale": 8, "quoteScale": 2, "tickSize": "0.10", "lotSize": "0.0001",
			"makerFeeRate": "0.001", "takerFeeRate": "0.001",
			"minPrice": nil, "maxPrice": nil, "minAmount": "0", "maxAmount": nil,
			"minTurnover": "5", "marketOrdersEnabled": true,
			"enabled": true, "visible": true, "sort": 1, "zone": "main", "featured": true,
		},
	}}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /markets", func(w http.ResponseWriter, _ *http.Request) {
		out := make([]map[string]any, 0, len(s.markets))
		for _, row := range s.markets {
			out = append(out, row)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
	})
	mux.HandleFunc("PUT /markets/{symbol...}", func(w http.ResponseWriter, r *http.Request) {
		symbol := r.PathValue("symbol")
		row, ok := s.markets[symbol]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": fmt.Sprintf("Unknown Market %q.", symbol)})
			return
		}
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		var patch map[string]any
		if err := json.Unmarshal(raw, &patch); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		if s.refuseTod {
			if _, moves := patch["tickSize"]; moves {
				w.WriteHeader(http.StatusConflict)
				_ = json.NewEncoder(w).Encode(map[string]string{
					"error": "market config frozen while orders rest: " + symbol + " has 2 open orders"})
				return
			}
		}
		for k, v := range patch {
			row[k] = v
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(row)
	})
	mux.HandleFunc("GET /engine/status", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok": true,
			"markets": []map[string]any{
				{"market": "BTC/USDT", "ready": true, "enabled": true, "halted": false, "openOrders": 0, "lastSeq": 7},
				{"market": "ETH/USDT", "ready": true, "enabled": true, "halted": false, "openOrders": 3, "lastSeq": 41},
			},
			"consumerLag": 2,
			"at":          time.Now().UTC(),
		})
	})
	s.srv = httptest.NewServer(mux)
	t.Cleanup(s.srv.Close)
	return s
}

// ---- the stack: production wiring + stubbed upstreams ----

func newConfigStack(t *testing.T) (*itStack, *stubWallet, *stubExchange) {
	t.Helper()
	ctx := context.Background()

	suffix := make([]byte, 6)
	if _, err := rand.Read(suffix); err != nil {
		t.Fatal(err)
	}
	dbName := "malxchange_itcf_" + hex.EncodeToString(suffix)

	adminPool, err := pgxpool.New(ctx, fmt.Sprintf("postgres://%s:%s@%s:5432/postgres",
		os.Getenv("MAL_TEST_PG_SUPERUSER"), itPGPW, itPGHost))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := adminPool.Exec(ctx, fmt.Sprintf(`DROP DATABASE IF EXISTS %s WITH (FORCE)`, dbName)); err != nil {
		t.Fatal(err)
	}
	if _, err := adminPool.Exec(ctx, fmt.Sprintf(`CREATE DATABASE %s`, dbName)); err != nil {
		t.Fatal(err)
	}
	adminPool.Close()
	t.Cleanup(func() {
		cleanup, err := pgxpool.New(ctx, fmt.Sprintf("postgres://%s:%s@%s:5432/postgres",
			os.Getenv("MAL_TEST_PG_SUPERUSER"), itPGPW, itPGHost))
		if err != nil {
			return
		}
		defer cleanup.Close()
		_, _ = cleanup.Exec(ctx, fmt.Sprintf(`DROP DATABASE IF EXISTS %s WITH (FORCE)`, dbName))
	})

	pool, err := pgxpool.New(ctx, fmt.Sprintf("postgres://%s:%s@%s:5432/%s?sslmode=disable&search_path=admin",
		os.Getenv("MAL_TEST_PG_SUPERUSER"), itPGPW, itPGHost, dbName))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if _, err := pool.Exec(ctx, `CREATE SCHEMA IF NOT EXISTS admin`); err != nil {
		t.Fatal(err)
	}
	u := url.URL{
		Scheme:   "postgres",
		User:     url.UserPassword(os.Getenv("MAL_TEST_PG_SUPERUSER"), itPGPW),
		Host:     itPGHost + ":5432",
		Path:     dbName,
		RawQuery: "sslmode=disable&search_path=admin",
	}
	if err := pgmig.Run(u.String(), migrations.FS, "admin"); err != nil {
		t.Fatalf("admin migrations: %v", err)
	}

	env := &AdminEnv{
		SessionAbsolute:  12 * time.Hour,
		SessionIdle:      30 * time.Minute,
		LockoutThreshold: LockoutThreshold,
		LockoutWindow:    15 * time.Minute,
	}

	wallet := newStubWallet(t)
	exchange := newStubExchange(t)

	srv := NewServer(pool, env, nil)
	srv.SeedRBAC(ctx)
	srv.upstreams.wallet.base = wallet.srv.URL
	srv.upstreams.exchange.base = exchange.srv.URL

	root := http.NewServeMux()
	internal := http.NewServeMux()
	srv.RegisterPublic(root)
	srv.RegisterInternal(internal)
	root.Handle("/internal/", http.StripPrefix("/internal", internal))
	rest := httptest.NewServer(root)
	t.Cleanup(rest.Close)

	return &itStack{pool: pool, env: env, rest: rest, t: t}, wallet, exchange
}

// ---- Coin editor ----

func TestCoinEditorPersistsWithAudit(t *testing.T) {
	s, wallet, _ := newConfigStack(t)
	root := s.fullyProvisioned(t, "root@cfgit.test")
	rootCookie := root.freshCookie(t, s)

	// The list is the wallet's list, byte-equivalent in shape.
	status, _, raw := s.do(t, "GET", "/api/coins", "", rootCookie)
	if status != http.StatusOK {
		t.Fatalf("list coins: %d %s", status, raw)
	}
	var coins []map[string]any
	if err := json.Unmarshal([]byte(raw), &coins); err != nil {
		t.Fatalf("list body: %s", raw)
	}
	if len(coins) != 1 || coins[0]["name"] != "BTC" {
		t.Fatalf("coin list wrong: %s", raw)
	}

	// An edit forwards only the fields the client sent — the wallet
	// stays the single decode point of truth.
	status, _, raw = s.do(t, "PUT", "/api/coins/BTC", `{"confirmations":6,"sweepPaused":true}`, rootCookie)
	if status != http.StatusOK {
		t.Fatalf("edit coin: %d %s", status, raw)
	}
	var after map[string]any
	if err := json.Unmarshal([]byte(raw), &after); err != nil {
		t.Fatalf("edit body: %s", raw)
	}
	if after["confirmations"].(float64) != 6 || after["sweepPaused"] != true {
		t.Fatalf("edit did not follow the new config: %s", raw)
	}
	if len(wallet.sawPut) != 1 || wallet.sawPut[0] != `{"confirmations":6,"sweepPaused":true}` {
		t.Fatalf("wallet saw %q, want the verbatim edit body", wallet.sawPut)
	}

	// The audit row carries who, what, and the wallet's before/after.
	rows := s.auditRows(t, PermEditCoin)
	if len(rows) != 1 {
		t.Fatalf("want exactly one %s row, got %v", PermEditCoin, rows)
	}
	if rows[0].Actor != root.Email || rows[0].Target != "BTC" {
		t.Fatalf("audit row actor/target wrong: %+v", rows[0])
	}
	var before map[string]any
	if err := json.Unmarshal(rows[0].Before, &before); err != nil {
		t.Fatalf("audit before: %s", rows[0].Before)
	}
	if before["confirmations"].(float64) != 3 || before["sweepPaused"] != false {
		t.Fatalf("audit before wrong: %s", rows[0].Before)
	}
	if after["confirmations"].(float64) != 6 {
		t.Fatalf("audit after wrong: %s", rows[0].After)
	}

	// Unknown Coin → the wallet's 404 sentence, relayed.
	status, _, raw = s.do(t, "PUT", "/api/coins/LTC", `{"confirmations":6}`, rootCookie)
	if status != http.StatusNotFound || !strings.Contains(raw, "unknown coin LTC") {
		t.Fatalf("unknown coin relay: %d %s", status, raw)
	}

	// Malformed JSON is refused before any upstream call.
	if status, _, _ = s.do(t, "PUT", "/api/coins/BTC", `{oops`, rootCookie); status != http.StatusBadRequest {
		t.Fatalf("bad json: %d", status)
	}
}

// ---- Market editor ----

func TestMarketEditorPersistsWithAudit(t *testing.T) {
	s, _, exchange := newConfigStack(t)
	root := s.fullyProvisioned(t, "root@cfgit.test")
	rootCookie := root.freshCookie(t, s)

	status, _, raw := s.do(t, "GET", "/api/markets", "", rootCookie)
	if status != http.StatusOK {
		t.Fatalf("list markets: %d %s", status, raw)
	}

	// A fee edit forwards, persists, and audits before/after.
	status, _, raw = s.do(t, "PUT", "/api/markets/BTC/USDT", `{"makerFeeRate":"0.002","visible":false}`, rootCookie)
	if status != http.StatusOK {
		t.Fatalf("edit market: %d %s", status, raw)
	}
	var after map[string]any
	if err := json.Unmarshal([]byte(raw), &after); err != nil {
		t.Fatalf("edit body: %s", raw)
	}
	if after["makerFeeRate"] != "0.002" || after["visible"] != false {
		t.Fatalf("edit echo wrong: %s", raw)
	}

	rows := s.auditRows(t, PermEditMarket)
	if len(rows) != 1 {
		t.Fatalf("want exactly one %s row, got %v", PermEditMarket, rows)
	}
	if rows[0].Target != "BTC/USDT" {
		t.Fatalf("audit target wrong: %+v", rows[0])
	}
	var before map[string]any
	if err := json.Unmarshal(rows[0].Before, &before); err != nil {
		t.Fatalf("audit before: %s", rows[0].Before)
	}
	if before["makerFeeRate"] != "0.001" || before["visible"] != true {
		t.Fatalf("audit before wrong: %s", rows[0].Before)
	}

	// Unknown Market → the exchange's 404 sentence.
	status, _, raw = s.do(t, "PUT", "/api/markets/NOPE/USDT", `{"visible":false}`, rootCookie)
	if status != http.StatusNotFound || !strings.Contains(raw, "Unknown Market") {
		t.Fatalf("unknown market relay: %d %s", status, raw)
	}

	// The freeze guard's refusal rides through untouched: a 409 with
	// the exchange's sentence — the row never changed.
	exchange.refuseTod = true
	status, _, raw = s.do(t, "PUT", "/api/markets/BTC/USDT", `{"tickSize":"0.01"}`, rootCookie)
	if status != http.StatusConflict || !strings.Contains(raw, "orders rest") {
		t.Fatalf("freeze guard relay: %d %s", status, raw)
	}
	exchange.refuseTod = false

	// Malformed JSON → 400, nothing forwarded.
	if status, _, _ = s.do(t, "PUT", "/api/markets/BTC/USDT", `not json`, rootCookie); status != http.StatusBadRequest {
		t.Fatalf("bad json: %d", status)
	}
}

// ---- engine status: every Role's window ----

func TestEngineStatusVisibleToAllRoles(t *testing.T) {
	s, _, _ := newConfigStack(t)
	root := s.fullyProvisioned(t, "root@cfgit.test")
	rootCookie := root.freshCookie(t, s)

	for _, role := range []string{RoleFinance, RoleCompliance, RoleSupport} {
		p := s.staff(t, root, rootCookie, role+"@cfgit.test", role)
		cookie := p.freshCookie(t, s)
		status, _, raw := s.do(t, "GET", "/api/engine", "", cookie)
		if status != http.StatusOK {
			t.Fatalf("engine status as %s: %d %s", role, status, raw)
		}
		var body struct {
			Markets []map[string]any `json:"markets"`
			Lag     *int64           `json:"consumerLag"`
		}
		if err := json.Unmarshal([]byte(raw), &body); err != nil {
			t.Fatalf("status body: %s", raw)
		}
		if body.Lag == nil || *body.Lag != 2 {
			t.Fatalf("consumerLag wrong: %s", raw)
		}
		if len(body.Markets) != 2 || body.Markets[0]["market"] != "BTC/USDT" {
			t.Fatalf("per-Market view wrong: %s", raw)
		}
		if body.Markets[1]["openOrders"].(float64) != 3 {
			t.Fatalf("open orders wrong: %s", raw)
		}
	}

	// The super-admin sees the same view.
	status, _, raw := s.do(t, "GET", "/api/engine", "", rootCookie)
	if status != http.StatusOK {
		t.Fatalf("engine status as super-admin: %d %s", status, raw)
	}

	// GETs are not audited — the log stays mutations-only.
	if rows := s.auditRows(t, PermViewEngine); len(rows) != 0 {
		t.Fatalf("engine status must not audit, got %v", rows)
	}
}

// Only super-admin permissions admit the config edits: one explicit
// refusal per surface (the generic matrix sweep is
// TestRoleMatrixRefusesNonMatrixRoles).
func TestConfigEditsRefusedForNonMatrixRoles(t *testing.T) {
	s, _, _ := newConfigStack(t)
	root := s.fullyProvisioned(t, "root@cfgit.test")
	rootCookie := root.freshCookie(t, s)

	p := s.staff(t, root, rootCookie, "fin@cfgit.test", RoleFinance)
	cookie := p.freshCookie(t, s)

	if status, _, _ := s.do(t, "PUT", "/api/coins/BTC", `{"confirmations":6}`, cookie); status != http.StatusForbidden {
		t.Fatalf("finance coin edit: %d", status)
	}
	if status, _, _ := s.do(t, "PUT", "/api/markets/BTC/USDT", `{"makerFeeRate":"0.5"}`, cookie); status != http.StatusForbidden {
		t.Fatalf("finance market edit: %d", status)
	}
	if rows := s.auditRows(t, PermEditCoin); len(rows) != 0 {
		t.Fatalf("a refused edit must not audit, got %v", rows)
	}
}
