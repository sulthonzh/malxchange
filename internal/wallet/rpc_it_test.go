//go:build integration

// The wallet internal RPC over real HTTP (ticket #42): wallet's
// RegisterRPC had registered every pattern with the /internal prefix
// already included while the boot harness mounts that mux behind
// StripPrefix("/internal") — so the working path was doubled
// (/internal/internal/freeze) and the intended one 404'd. This IT pins
// the fixed wiring: the production mux shape (root → /internal/ →
// StripPrefix → RegisterRPC) serves POST /internal/freeze at the single
// path, the doubled path 404s, and the freeze lands in the Ledger
// (observable through the accounts RPC on the same wiring).
package wallet

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/syaikhipin/malxchange/internal/money"
)

func TestInternalRPCServesAtSingleInternalPath(t *testing.T) {
	s := newStack(t)

	// The production wiring from internal/service: the internal mux
	// mounted at /internal/ with the prefix stripped.
	internal := http.NewServeMux()
	RegisterRPC(internal, &RPC{DB: s.pool, Log: s.log})
	root := http.NewServeMux()
	root.Handle("/internal/", http.StripPrefix("/internal", internal))
	srv := httptest.NewServer(root)
	t.Cleanup(srv.Close)

	if err := Credit(context.Background(), s.pool, "alice", "USDT", KindAdjustment,
		"test faucet", "it-faucet:rpc", money.MustParse("1000"), "", "test"); err != nil {
		t.Fatal(err)
	}

	freeze := func(path string) (int, string) {
		t.Helper()
		body, _ := json.Marshal(map[string]string{
			"orderId": "of1", "member": "alice", "market": "BTC/USDT",
			"side": "buy", "coin": "USDT", "amount": "10.00002",
		})
		resp, err := http.Post(srv.URL+path, "application/json", bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var out map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&out)
		return resp.StatusCode, fmt.Sprint(out["orderId"])
	}

	if status, orderID := freeze("/internal/freeze"); status != http.StatusOK || orderID != "of1" {
		t.Fatalf("POST /internal/freeze = %d %v, want 200 of1", status, orderID)
	}
	if status, _ := freeze("/internal/internal/freeze"); status != http.StatusNotFound {
		t.Fatalf("POST /internal/internal/freeze = %d, want 404 (the doubled path is gone)", status)
	}

	// The freeze is observable through the member accounts RPC on the
	// same wiring: 1000 − 10.00002 available, 10.00002 frozen.
	resp, err := http.Get(srv.URL + "/internal/accounts/alice")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /internal/accounts/alice = %d, want 200", resp.StatusCode)
	}
	var accounts []map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&accounts); err != nil {
		t.Fatal(err)
	}
	for _, a := range accounts {
		if a["coin"] != "USDT" {
			continue
		}
		if a["frozen"] != "10.000020" || a["available"] != "989.999980" {
			t.Fatalf("alice USDT = available %s frozen %s, want 989.999980 / 10.000020", a["available"], a["frozen"])
		}
		return
	}
	t.Fatal("no USDT account in the listing")
}

// The hot-transfer retry (ticket #60): wallet's RetryHotTransfer
// existed unmounted — this pins the route POST /internal/hot-transfers/
// {id}/retry re-arming a failed hot→cold transfer (state failed →
// pending, error detail cleared) and refusing anything not failed.
func TestHotTransferRetryRouteReArmsFailed(t *testing.T) {
	s := newStack(t)

	internal := http.NewServeMux()
	RegisterRPC(internal, &RPC{DB: s.pool, Log: s.log})
	root := http.NewServeMux()
	root.Handle("/internal/", http.StripPrefix("/internal", internal))
	srv := httptest.NewServer(root)
	t.Cleanup(srv.Close)

	ctx := context.Background()
	if _, err := s.pool.Exec(ctx,
		`UPDATE coins SET cold_address = 'cold-btc-custody' WHERE name = 'BTC'`); err != nil {
		t.Fatal(err)
	}
	coin, err := GetCoin(ctx, s.pool, "BTC")
	if err != nil {
		t.Fatal(err)
	}
	h, err := CreateHotTransfer(ctx, s.pool, coin, money.MustParse("2.5"), "ops-it")
	if err != nil {
		t.Fatal(err)
	}
	if err := MarkHotTransferFailed(ctx, s.pool, h.ID, HPending, "broadcast: chain down"); err != nil {
		t.Fatal(err)
	}

	retry := func(id string) (int, string) {
		t.Helper()
		resp, err := http.Post(srv.URL+"/internal/hot-transfers/"+id+"/retry",
			"application/json", bytes.NewReader(nil))
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(raw)
	}

	if status, body := retry(h.ID); status != http.StatusOK {
		t.Fatalf("POST /internal/hot-transfers/%s/retry = %d %s, want 200", h.ID, status, body)
	}
	transfers, err := ListHotTransfers(ctx, s.pool, "BTC")
	if err != nil {
		t.Fatal(err)
	}
	var rearmed map[string]any
	for _, row := range transfers {
		if row["id"] == h.ID {
			rearmed = row
		}
	}
	if rearmed == nil || rearmed["state"] != HPending || rearmed["error"] != "" {
		t.Fatalf("hot transfer after retry = %v, want state pending with cleared error", rearmed)
	}

	// A live (pending) transfer is not retryable; neither is an unknown id.
	if status, _ := retry(h.ID); status != http.StatusBadRequest {
		t.Fatalf("retry of a pending transfer = %d, want 400", status)
	}
	if status, _ := retry("00000000-0000-0000-0000-000000000000"); status != http.StatusBadRequest {
		t.Fatalf("retry of an unknown transfer = %d, want 400", status)
	}
}
