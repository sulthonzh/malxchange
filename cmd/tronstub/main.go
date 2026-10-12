// Command tronstub runs a fake java-tron gRPC node for wallet tests
// (issue #22): a minimal api.Wallet surface over an in-memory ledger.
//
// *** TEST INFRASTRUCTURE, NOT A CHAIN. It does NOT validate
// signatures, does not run contracts, and holds funds in RAM. Point it
// at 127.0.0.1 only. ***
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/shopspring/decimal"

	"github.com/syaikhipin/malxchange/internal/wallet/chain/tron"
	"github.com/syaikhipin/malxchange/internal/wallet/keys"
)

func main() {
	addr := flag.String("addr", ":50051", "gRPC listen address")
	httpAddr := flag.String("http", ":50052", "admin HTTP listen address")
	mineInterval := flag.Duration("mine-interval", time.Second, "auto-mine tick; 0 mines only on broadcast/admin RPC")
	seedMnemonic := flag.String("seed-mnemonic",
		"abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about",
		"BIP-39 mnemonic deriving the funded tumbler address")
	fund := flag.String("fund", "1000000", "tumbler TRX balance (whole TRX)")
	token := flag.String("token", tron.DefaultStubToken, "default TRC-20 token contract")
	tokenDecimals := flag.Int("token-decimals", 6, "token decimals")
	tokenFund := flag.String("token-fund", "1000000", "tumbler token balance (whole token units)")
	flag.Parse()

	seed, err := keys.Seed(*seedMnemonic, "")
	if err != nil {
		fmt.Fprintln(os.Stderr, "tronstub: mnemonic:", err)
		os.Exit(1)
	}
	acct, err := keys.DeriveAccount(seed, keys.TRON, keys.MainNet)
	if err != nil {
		fmt.Fprintln(os.Stderr, "tronstub: derive:", err)
		os.Exit(1)
	}
	tumbler, err := acct.AddressAt(0)
	if err != nil {
		fmt.Fprintln(os.Stderr, "tronstub: tumbler address:", err)
		os.Exit(1)
	}

	trxFund, err := decimal.NewFromString(*fund)
	if err != nil {
		fmt.Fprintln(os.Stderr, "tronstub: bad --fund:", err)
		os.Exit(1)
	}
	tokFund, err := decimal.NewFromString(*tokenFund)
	if err != nil {
		fmt.Fprintln(os.Stderr, "tronstub: bad --token-fund:", err)
		os.Exit(1)
	}

	node := tron.NewStub(tron.StubConfig{
		Tumbler:       tumbler,
		TRXFund:       trxFund.Shift(6).IntPart(),
		Token:         *token,
		TokenDecimals: int32(*tokenDecimals),
		TokenFund:     tokFund.Shift(int32(*tokenDecimals)).BigInt(),
	})
	node.SetAutoMine(*mineInterval != 0)
	if err := node.Start(*addr, *httpAddr); err != nil {
		fmt.Fprintln(os.Stderr, "tronstub:", err)
		os.Exit(1)
	}
	defer node.Close()

	fmt.Printf("STUB java-tron (TEST INFRASTRUCTURE — signature NOT validated)\n")
	fmt.Printf("  gRPC       %s\n  admin HTTP %s (POST /stub/mine?n=1, POST /stub/transfer)\n  tumbler    %s (%s TRX + %s token units)\n",
		node.GRPCAddr(), node.HTTPAddr(), tumbler, trxFund, tokFund)

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
	_ = context.Background()
}
