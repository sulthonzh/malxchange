package btc

import (
	"bytes"
	"context"
	"encoding/hex"
	"testing"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/chaincfg/chainhash"
	"github.com/btcsuite/btcd/wire"
	"github.com/shopspring/decimal"

	"github.com/syaikhipin/malxchange/internal/wallet/keys"
)

// testMnemonic is the standard "abandon … about" vector — the same
// mnemonic the live simnet coinbase key comes from, so E2E and unit
// tests share it.
const testMnemonic = "abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about"

func testAccount(t *testing.T, family, net string) *keys.Account {
	t.Helper()
	seed, err := keys.Seed(testMnemonic, "")
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	acct, err := keys.DeriveAccount(seed, family, net)
	if err != nil {
		t.Fatalf("derive: %v", err)
	}
	return acct
}

func mustAddr(t *testing.T, family, net string, a *keys.Account, idx uint32) string {
	t.Helper()
	addr, err := a.AddressAt(idx)
	if err != nil {
		t.Fatalf("address %d: %v", idx, err)
	}
	return addr
}

func decodeOrDie(t *testing.T, rawHex string) *wire.MsgTx {
	t.Helper()
	raw, err := hex.DecodeString(rawHex)
	if err != nil {
		t.Fatal(err)
	}
	var tx wire.MsgTx
	if err := tx.Deserialize(bytes.NewReader(raw)); err != nil {
		t.Fatal(err)
	}
	return &tx
}

func TestDecodeScriptAddrForms(t *testing.T) {
	acct := testAccount(t, keys.BTC, netMain)
	addr, err := acct.AddressAt(0)
	if err != nil {
		t.Fatal(err)
	}
	script, err := addrScript(famBTC, netMain, addr)
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := DecodeScriptAddr(famBTC, netMain, script); !ok || got != addr {
		t.Fatalf("p2wpkh round trip: got %q ok=%v want %q", got, ok, addr)
	}

	// Same script on LTC renders the ltc-HRP form of the same hash.
	if got, ok := DecodeScriptAddr(famLTC, netMain, script); !ok {
		t.Fatal("ltc decode failed")
	} else if got[:4] != "ltc1" {
		t.Fatalf("ltc hrp: %q", got)
	}

	// Legacy P2PKH (BTC mainnet version 0x00).
	hash20 := []byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20}
	p2pkh := append(append([]byte{0x76, 0xa9, 0x14}, hash20...), 0x88, 0xac)
	if got, ok := DecodeScriptAddr(famBTC, netMain, p2pkh); !ok || got[:1] != "1" {
		t.Fatalf("p2pkh: got %q ok=%v", got, ok)
	}

	// Legacy P2SH (BTC mainnet version 0x05).
	p2sh := append(append([]byte{0xa9, 0x14}, hash20...), 0x87)
	if got, ok := DecodeScriptAddr(famBTC, netMain, p2sh); !ok || got[:1] != "3" {
		t.Fatalf("p2sh: got %q ok=%v", got, ok)
	}

	// LTC mainnet legacy forms use 0x30/0x32.
	if got, ok := DecodeScriptAddr(famLTC, netMain, p2pkh); !ok || got[:1] != "L" {
		t.Fatalf("ltc p2pkh: %q", got)
	}
	if got, ok := DecodeScriptAddr(famLTC, netMain, p2sh); !ok || got[:1] != "M" {
		t.Fatalf("ltc p2sh: %q", got)
	}

	// Unknown forms are a clean miss, not an error.
	if _, ok := DecodeScriptAddr(famBTC, netMain, []byte{0x6a, 0x04, 1, 2, 3, 4}); ok {
		t.Fatal("op_return should not decode")
	}
}

// fundingTx builds an unsigned tx whose output 0 pays the account
// deposit key at idx 0 — the "previous transaction" BuildSign spends.
func fundingTx(t *testing.T, family, net string, acct *keys.Account, satoshis int64) *wire.MsgTx {
	t.Helper()
	addr := mustAddr(t, family, net, acct, 0)
	script, err := addrScript(family, net, addr)
	if err != nil {
		t.Fatal(err)
	}
	tx := wire.NewMsgTx(wire.TxVersion)
	var prevHash chainhash.Hash // zero dummy prevout
	tx.AddTxIn(&wire.TxIn{
		PreviousOutPoint: wire.OutPoint{Hash: prevHash, Index: 0},
		Sequence:         0xffffffff,
	})
	tx.AddTxOut(wire.NewTxOut(satoshis, script))
	return tx
}

func TestBuildSignChangePath(t *testing.T) {
	const funding = int64(100_000)
	for _, family := range []string{keys.BTC, keys.LTC} {
		acct := testAccount(t, family, netMain)
		r, err := NewRPC(family, "http://127.0.0.1:1", "u", "p", false)
		if err != nil {
			t.Fatal(err)
		}
		// Offline RPC: resolveNet keeps the mainnet default and
		// BuildSign itself makes no node calls.
		fund := fundingTx(t, family, netMain, acct, funding)
		hot, err := acct.HotAddress()
		if err != nil {
			t.Fatal(err)
		}
		dest := mustAddr(t, family, netMain, acct, 7)
		const rate = int64(10)
		built, err := r.BuildSign(context.Background(), acct,
			[]Utxo{{TxID: fund.TxHash().String(), Vout: 0, Satoshis: funding, Branch: 0, Idx: 0}},
			[]Output{{Address: dest, Satoshis: 40_000}}, rate, hot)
		if err != nil {
			t.Fatalf("%s: build: %v", family, err)
		}

		tx := decodeOrDie(t, built.RawHex)
		if got := tx.TxHash().String(); got != built.TxID {
			t.Fatalf("%s: txid mismatch %s != %s", family, got, built.TxID)
		}
		// Two outputs (payment + change), witness present, and the
		// change pays the hot address.
		if len(tx.TxOut) != 2 {
			t.Fatalf("%s: outputs %d, want 2 (payment+change)", family, len(tx.TxOut))
		}
		if !tx.HasWitness() || len(tx.TxIn[0].Witness) != 2 {
			t.Fatalf("%s: missing witness stack", family)
		}
		changeScript, err := addrScript(family, netMain, hot)
		if err != nil {
			t.Fatal(err)
		}
		if string(tx.TxOut[1].PkScript) != string(changeScript) {
			t.Fatalf("%s: change script does not pay hot address", family)
		}
		// Exact arithmetic: fee = in - out, and the fee at least
		// covers vsize × rate.
		if built.FeeSat != funding-outSumOf(tx) {
			t.Fatalf("%s: fee %d != in-out %d", family, built.FeeSat, funding-outSumOf(tx))
		}
		if built.FeeSat < vsizeOf(tx)*rate {
			t.Fatalf("%s: fee %d below vsize*rate %d", family, built.FeeSat, vsizeOf(tx)*rate)
		}
		if got := tx.TxOut[1].Value; got != funding-40_000-built.FeeSat {
			t.Fatalf("%s: change %d, want in-out-fee %d", family, got, funding-40_000-built.FeeSat)
		}
		// Witness stack carries a real compressed pubkey.
		if _, err := btcec.ParsePubKey(tx.TxIn[0].Witness[1]); err != nil {
			t.Fatalf("%s: witness item 2 not a pubkey: %v", family, err)
		}
	}
}

func TestBuildSignSubDustRemainderStaysFee(t *testing.T) {
	const funding = int64(10_000)
	acct := testAccount(t, keys.BTC, netMain)
	r, _ := NewRPC(famBTC, "http://127.0.0.1:1", "u", "p", false)
	fund := fundingTx(t, famBTC, netMain, acct, funding)
	hot, err := acct.HotAddress()
	if err != nil {
		t.Fatal(err)
	}
	dest := mustAddr(t, famBTC, netMain, acct, 9)
	// rate 1 → est fee 141; remainder 200 leaves 59 < dust floor
	// 300, so NO change output is emitted and the 200 rides as fee.
	built, err := r.BuildSign(context.Background(), acct,
		[]Utxo{{TxID: fund.TxHash().String(), Vout: 0, Satoshis: funding, Branch: 0, Idx: 0}},
		[]Output{{Address: dest, Satoshis: 9_800}}, 1, hot)
	if err != nil {
		t.Fatal(err)
	}
	tx := decodeOrDie(t, built.RawHex)
	if len(tx.TxOut) != 1 {
		t.Fatalf("outputs %d, want 1 (no dust change)", len(tx.TxOut))
	}
	if built.FeeSat != 200 {
		t.Fatalf("fee %d, want full 200 sat remainder as fee", built.FeeSat)
	}
}

func TestBuildSignRejectsBadShapes(t *testing.T) {
	acct := testAccount(t, keys.BTC, netMain)
	r, _ := NewRPC(famBTC, "http://127.0.0.1:1", "u", "p", false)
	fund := fundingTx(t, famBTC, netMain, acct, 10_000)
	dest := mustAddr(t, famBTC, netMain, acct, 0)
	in := []Utxo{{TxID: fund.TxHash().String(), Vout: 0, Satoshis: 10_000, Branch: 0, Idx: 0}}
	// Outputs swallow the entire input sum (no fee room).
	if _, err := r.BuildSign(context.Background(), acct, in, []Output{{Address: dest, Satoshis: 10_000}}, 10, dest); err == nil {
		t.Fatal("expected error when outputs >= inputs")
	}
	// Empty inputs.
	if _, err := r.BuildSign(context.Background(), acct, nil, []Output{{Address: dest, Satoshis: 1}}, 10, dest); err == nil {
		t.Fatal("expected error for empty inputs")
	}
	// Unknown address form.
	if _, err := r.BuildSign(context.Background(), acct, in, []Output{{Address: "notanaddress", Satoshis: 1}}, 10, dest); err == nil {
		t.Fatal("expected error for undecodable address")
	}
}

func TestDepositAmountExactness(t *testing.T) {
	// Deposits are integer satoshis at 8dp — the decimal rendering
	// must be lossless (never via float JSON "value" fields).
	d := decimal.New(123456789, -8)
	if s := d.String(); s != "1.23456789" {
		t.Fatalf("decimal: %s", s)
	}
}
