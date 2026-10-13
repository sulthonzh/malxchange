// Command devkeys is the test profile's key ceremony: it derives the
// fixed "abandon…about" test mnemonic's account keys, prints each
// account's first deposit address, and — when MAL_PG_HOST is set —
// registers the watch-only xpubs on the Coins so deposit-address
// derivation works in the compose test stack. The real ceremony is
// walletctl on an offline machine (install/key-ceremony.md); these keys
// hold simnet test funds only. EVM/TRON stay unregistered here — the
// test profile runs no chain for them.
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/syaikhipin/malxchange/internal/config"
	"github.com/syaikhipin/malxchange/internal/wallet"
	"github.com/syaikhipin/malxchange/internal/wallet/keys"
)

// testMnemonic is the well-known all-about vector; the compose test
// profile mines its coinbase to this key's first address.
const testMnemonic = "abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about"

func main() {
	seed, err := keys.Seed(testMnemonic, "")
	if err != nil {
		fatal("seed", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var pool *pgxpool.Pool
	if os.Getenv("MAL_PG_HOST") != "" {
		pg := config.PG{
			Host:     envOr("MAL_PG_HOST", "postgres"),
			Port:     envOr("MAL_PG_PORT", "5432"),
			User:     envOr("MAL_PG_USER", "wallet"),
			Password: os.Getenv("MAL_PG_PASSWORD"),
			Database: envOr("MAL_PG_DATABASE", "malxchange"),
			Schema:   envOr("MAL_PG_SCHEMA", "wallet"),
		}
		pool, err = pgxpool.New(ctx, pg.URL())
		if err != nil {
			fatal("pg", err)
		}
		defer pool.Close()
		if err := pool.Ping(ctx); err != nil {
			fatal("pg ping", err)
		}
	}

	for _, fam := range []string{keys.BTC, keys.LTC} {
		acct, err := keys.DeriveAccount(seed, fam, keys.SimNet)
		if err != nil {
			fatal("derive "+fam, err)
		}
		addr, err := acct.AddressAt(0)
		if err != nil {
			fatal("address "+fam, err)
		}
		xpub, err := acct.XPub()
		if err != nil {
			fatal("xpub "+fam, err)
		}
		registered := false
		if pool != nil {
			name := map[string]string{keys.BTC: "BTC", keys.LTC: "LTC"}[fam]
			if err := wallet.SetCoinXPub(ctx, pool, name, xpub); err != nil {
				fatal("register "+name, err)
			}
			registered = true
		}
		fmt.Printf("%s\t%s\tfirst deposit %s\n", fam, xpub, addr)
		if registered {
			fmt.Printf("%s xpub registered on the Coin\n", fam)
		}
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func fatal(what string, err error) {
	fmt.Fprintf(os.Stderr, "devkeys: %s: %v\n", what, err)
	os.Exit(1)
}
