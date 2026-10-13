//go:build chain_e2e

// E2E against the live simnet btcd of the compose test profile (issue
// #22). Gated by the chain_e2e build tag and env vars:
//
//	MAL_TEST_BTCD_URL  (default https://127.0.0.1:18556)
//	MAL_TEST_BTCD_USER / MAL_TEST_BTCD_PASS
//
// The node is wallet-less: `generate` mines to the fixed coinbase
// address held by the standard "abandon … about" seed, and all
// signing happens locally through keys + BuildSign.
//
// The LTC half (TestE2ELTC) is skipped — see that test: ltcd v0.23.6
// panics in versionbits on `generate` and errors on
// getblockchaininfo, and no newer ltcd tag exists to bump to. LTC
// logic is covered by the unit tests instead.
package btc

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/btcsuite/btcd/wire"
	"github.com/shopspring/decimal"

	"github.com/syaikhipin/malxchange/internal/wallet/chain"
	"github.com/syaikhipin/malxchange/internal/wallet/keys"
)

const (
	defUser = "malxchange"
	defPass = "50826d30471328b1e87a4b964936d769"
)

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// simnetClient connects to one family's node and derives the shared
// simnet account (whose m/0/0 address is the coinbase key).
func simnetClient(t *testing.T, family, url string) (*RPC, *keys.Account) {
	t.Helper()
	r, err := NewRPC(family, url, envOr("MAL_TEST_BTCD_USER", defUser), envOr("MAL_TEST_BTCD_PASS", defPass), true)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := r.Height(ctx); err != nil {
		t.Fatalf("%s node at %s unreachable: %v", family, url, err)
	}
	acct := testAccount(t, family, keys.SimNet)
	return r, acct
}

// generate mines n blocks (to the fixed coinbase address).
func generate(t *testing.T, r *RPC, n int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	// Discrete `generate` conflicts with a running CPU miner (a
	// crashed earlier run can leave one armed); disarm it first.
	_ = r.call(ctx, "setgenerate", []any{false}, nil)
	var hashes []string
	if err := r.call(ctx, "generate", []any{n}, &hashes); err != nil {
		t.Fatalf("generate %d: %v", n, err)
	}
	if len(hashes) != n {
		t.Fatalf("generate %d: got %d hashes", n, len(hashes))
	}
}

// segwitActivationHeight is where simnet segwit reaches ACTIVE. The
// simnet deployment starts at genesis with 100-block windows and a 75%
// threshold — STARTED at 100, LOCKED_IN at 200, ACTIVE at 300 — and
// btcd's CPU miner signals, so mining to 300 always activates it.
// Below it every witness tx is rejected rpc -26 (#36), and custody
// mints native SegWit deposit addresses (segwitAddress), so this
// suite's funding and spend txs always carry witness data.
const segwitActivationHeight = 300

// ensureSegwitActive mines until the tip passes simnet segwit
// activation, then asserts it — so an under-height failure names the
// precondition instead of surfacing as a broadcast rpc -26. Idempotent:
// a chain already past activation (the long-lived dev one) skips
// mining entirely.
func ensureSegwitActive(t *testing.T, r *RPC) {
	t.Helper()
	ctx := context.Background()
	h, err := r.Height(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if h < segwitActivationHeight {
		t.Logf("tip %d below segwit activation: mining %d blocks", h, segwitActivationHeight-h)
		generate(t, r, int(segwitActivationHeight-h))
		if h, err = r.Height(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if h < segwitActivationHeight {
		t.Fatalf("chain height %d still below simnet segwit activation %d — witness txs (custody deposit addresses are native SegWit) would be rejected rpc -26", h, segwitActivationHeight)
	}
}

// coinbaseUtxo mines enough maturity and returns the mature coinbase
// output of the FIRST freshly mined block as a spendable Utxo.
func coinbaseUtxo(t *testing.T, r *RPC, acct *keys.Account) (Utxo, int64) {
	t.Helper()
	ctx := context.Background()
	start, err := r.Height(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// 101 blocks: the first coinbase (height start+1) reaches its
	// 100-block maturity exactly at the new tip.
	generate(t, r, 101)
	cbHeight := start + 1

	var hash string
	if err := r.call(ctx, "getblockhash", []any{cbHeight}, &hash); err != nil {
		t.Fatal(err)
	}
	var blk struct {
		Tx []string `json:"tx"`
	}
	if err := r.call(ctx, "getblock", []any{hash, 1}, &blk); err != nil {
		t.Fatal(err)
	}
	var vtx struct {
		Hex string `json:"hex"`
	}
	if err := r.call(ctx, "getrawtransaction", []any{blk.Tx[0], 1}, &vtx); err != nil {
		t.Fatal(err)
	}
	tx, err := decodeRawTx(vtx.Hex)
	if err != nil {
		t.Fatal(err)
	}
	// The coinbase output paying the mining address (m/0/0 key).
	net, _ := r.resolveNet(ctx)
	miningAddr, err := acct.AddressAt(0)
	if err != nil {
		t.Fatal(err)
	}
	for v, out := range tx.TxOut {
		if addr, ok := DecodeScriptAddr(r.family, net, out.PkScript); ok && addr == miningAddr {
			return Utxo{TxID: blk.Tx[0], Vout: uint32(v), Satoshis: out.Value, Branch: 0, Idx: 0}, cbHeight
		}
	}
	t.Fatalf("coinbase of block %d pays no output to %s", cbHeight, miningAddr)
	return Utxo{}, 0
}

// runE2E drives one family end to end: fund from the coinbase key to
// a derived deposit address, mine, scan the deposit, confirm depth,
// then spend it and scan the spend — all through the watcher seam.
func runE2E(t *testing.T, family, url string) {
	t.Helper()
	ctx := context.Background()
	r, acct := simnetClient(t, family, url)
	if d, err := r.Decimals(ctx, ""); err != nil || d != 8 {
		t.Fatalf("decimals: %d %v", d, err)
	}

	// Custody's deposit addresses are native SegWit, so the first
	// broadcast below needs segwit active (#36).
	ensureSegwitActive(t, r)

	cb, _ := coinbaseUtxo(t, r, acct)
	depositAddr, err := acct.AddressAt(3)
	if err != nil {
		t.Fatal(err)
	}
	hot, err := acct.HotAddress()
	if err != nil {
		t.Fatal(err)
	}

	// Fee rate: simnet may lack estimates; the fallback covers it.
	rate, err := r.FeeRate(ctx)
	if err != nil || rate < 1 {
		t.Fatalf("fee rate: %d %v", rate, err)
	}

	// 1) Fund a deposit: coinbase key -> deposit address.
	const depositSat = int64(50_000)
	built, err := r.BuildSign(ctx, acct, []Utxo{cb}, []Output{{Address: depositAddr, Satoshis: depositSat}}, rate, hot)
	if err != nil {
		t.Fatalf("build funding tx: %v", err)
	}
	txid, err := r.Broadcast(ctx, built.RawHex)
	if err != nil {
		t.Fatalf("broadcast: %v", err)
	}
	if txid != built.TxID {
		t.Fatalf("node txid %s != local %s", txid, built.TxID)
	}

	// 2) Mine one block; the watcher must find the exact deposit.
	generate(t, r, 1)
	height, err := r.Height(ctx)
	if err != nil {
		t.Fatal(err)
	}
	deps, spends, err := r.ScanBlock(ctx, height, map[string]string{depositAddr: family})
	if err != nil {
		t.Fatalf("scan %d: %v", height, err)
	}
	var dep *chain.Deposit
	for i := range deps {
		if deps[i].TxID == txid {
			dep = &deps[i]
		}
	}
	if dep == nil {
		t.Fatalf("deposit %s not found in block %d (deps=%+v)", txid, height, deps)
	}
	if dep.Address != depositAddr {
		t.Fatalf("deposit address %s != %s", dep.Address, depositAddr)
	}
	if dep.Height != height {
		t.Fatalf("deposit height %d != %d", dep.Height, height)
	}
	if want := decimal.New(depositSat, -8); !dep.Amount.Equal(want) {
		t.Fatalf("deposit amount %s != exact %s", dep.Amount, want)
	}
	if dep.Amount.String() != "0.0005" {
		t.Fatalf("deposit amount rendering %q", dep.Amount.String())
	}

	// 3) Depth and embedding right after the confirming block.
	if d, err := r.Depth(ctx, txid); err != nil || d != 1 {
		t.Fatalf("depth: %d %v (want 1)", d, err)
	}
	if ok, err := r.TxExists(ctx, txid, height); err != nil || !ok {
		t.Fatalf("txexists: %v %v", ok, err)
	}
	// Unknown tx depth is -1, not an error.
	if d, err := r.Depth(ctx, "0000000000000000000000000000000000000000000000000000000000000000"); err != nil || d != -1 {
		t.Fatalf("unknown depth: %d %v", d, err)
	}

	// The funding tx also spent the coinbase output: a spend entry.
	foundCbSpend := false
	for _, s := range spends {
		if s.TxID == cb.TxID && s.Vout == int32(cb.Vout) && s.SpentBy == txid {
			foundCbSpend = true
		}
	}
	if !foundCbSpend {
		t.Fatalf("coinbase spend entry missing (spends=%+v)", spends)
	}

	// 4) Spend the deposit output; the next scan must report it.
	spendTx, err := r.BuildSign(ctx, acct,
		[]Utxo{{TxID: txid, Vout: uint32(dep.Vout), Satoshis: depositSat, Branch: 0, Idx: 3}},
		[]Output{{Address: hot, Satoshis: 10_000}}, rate, hot)
	if err != nil {
		t.Fatalf("build spend: %v", err)
	}
	spendTxid, err := r.Broadcast(ctx, spendTx.RawHex)
	if err != nil {
		t.Fatalf("broadcast spend: %v", err)
	}
	generate(t, r, 1)
	h2, err := r.Height(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// Empty watch map: spends only — the balance-index contract.
	_, spends2, err := r.ScanBlock(ctx, h2, map[string]string{})
	if err != nil {
		t.Fatalf("scan %d: %v", h2, err)
	}
	found := false
	for _, s := range spends2 {
		if s.TxID == txid && s.Vout == dep.Vout && s.SpentBy == spendTxid {
			found = true
		}
	}
	if !found {
		t.Fatalf("deposit spend entry missing (spends=%+v)", spends2)
	}

	// 5) The spend's own vsize/fee bookkeeping is sane.
	tx := decodeOrDie(t, spendTx.RawHex)
	var _ = wire.TxVersion
	if spendTx.FeeSat != depositSat-outSumOf(tx) {
		t.Fatalf("spend fee %d != in-out %d", spendTx.FeeSat, depositSat-outSumOf(tx))
	}
	if d, err := r.Depth(ctx, txid); err != nil || d != 2 {
		t.Fatalf("depth after second block: %d %v (want 2)", d, err)
	}
}

func TestE2EBTC(t *testing.T) {
	runE2E(t, keys.BTC, envOr("MAL_TEST_BTCD_URL", "https://127.0.0.1:18556"))
}

// TestE2ELTC is skipped: ltcd v0.23.6 (pinned in deploy/chains) is
// broken for every versionbits-dependent RPC. `generate` panics with a
// nil-pointer dereference in blockchain.deploymentChecker.HasStarted
// (versionbits.go:160) on the first call, and getblockchaininfo fails
// with "Unknown deployment 5 detected" — both from the same upstream
// defect. No newer ltcd tag exists to bump to.
//
// LTC signing/derivation/address logic is fully covered by unit tests:
// TestLTCDerivation (ltc1 shape), TestDecodeScriptAddrForms (ltc1/L/M
// forms), TestBuildSignChangePath (BTC+LTC loop), TestDepositAmountExactness.
// Only the mining-based live integration is unavailable until upstream
// fixes the versionbits crash.
func TestE2ELTC(t *testing.T) {
	t.Skip("ltcd v0.23.6 versionbits nil-deref: `generate` panics and getblockchaininfo errors; no newer ltcd tag to bump to. LTC logic covered by unit tests.")
}
