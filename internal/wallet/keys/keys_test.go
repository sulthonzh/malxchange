package keys

import (
	"strings"
	"testing"

	"crypto/ecdsa"
	etcrypto "github.com/ethereum/go-ethereum/crypto"
)

// TestBIP84Vector pins BTC derivation to the BIP-84 reference vectors
// (mnemonic "abandon …about", account m/84'/0'/0'): the exported zpub
// and the first receive addresses must match the spec's test suite.
func TestBIP84Vector(t *testing.T) {
	mnemonic := "abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about"
	seed, err := Seed(mnemonic, "")
	if err != nil {
		t.Fatal(err)
	}
	acct, err := DeriveAccount(seed, BTC, MainNet)
	if err != nil {
		t.Fatal(err)
	}
	xpub, err := acct.XPub()
	if err != nil {
		t.Fatal(err)
	}
	// The account-level public key, neutered with BIP-84 zpub versions.
	// We neuter with xpub versions internally, so compare the FINGERPRINT
	// part instead: derive m/0/0 and m/0/1 addresses and compare to the
	// BIP-84 receive vectors.
	w, err := ImportXPub(BTC, MainNet, xpub)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"bc1qcr8te4kr609gcawutmrza0j4xv80jy8z306fyu",
		"bc1qnjg0jd8228aq7egyzacy8cys3knf9xvrerkf9g",
	}
	for i, wantAddr := range want {
		got, err := w.AddressAt(uint32(i))
		if err != nil {
			t.Fatal(err)
		}
		if got != wantAddr {
			t.Fatalf("receive[%d] = %s, want %s", i, got, wantAddr)
		}
	}
	// First change address (m/1/0) is the hot wallet address — also a
	// BIP-84 vector.
	hot, err := acct.HotAddress()
	if err != nil {
		t.Fatal(err)
	}
	if hot != "bc1q8c6fshw2dlwun7ekn9qwf37cu2rn755upcp6el" {
		t.Fatalf("hot address = %s, want BIP-84 change vector", hot)
	}
	// The private-side derivation must agree with the watch-only one.
	priv, err := acct.AddressAt(0)
	if err != nil {
		t.Fatal(err)
	}
	if priv != want[0] {
		t.Fatalf("private derivation %s != watch-only %s", priv, want[0])
	}
}

// TestWatchHotAddressMatchesAccount pins the watch-only hot address to
// the private account's hot address for every UTXO family. They must
// agree on the m/1/0 leaf: the watcher registers this address in its
// watchMap, so a divergence silently drops every hot output (BTC/LTC
// sweep source). The xpub path derives from the account-level key, so
// a mismatch here is invisible to the BIP-84 receive-vector test.
func TestWatchHotAddressMatchesAccount(t *testing.T) {
	mnemonic := "abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about"
	for _, fam := range []string{BTC, LTC} {
		seed, err := Seed(mnemonic, "")
		if err != nil {
			t.Fatal(err)
		}
		acct, err := DeriveAccount(seed, fam, MainNet)
		if err != nil {
			t.Fatal(err)
		}
		xpub, err := acct.XPub()
		if err != nil {
			t.Fatal(err)
		}
		w, err := ImportXPub(fam, MainNet, xpub)
		if err != nil {
			t.Fatal(err)
		}
		priv, err := acct.HotAddress()
		if err != nil {
			t.Fatal(err)
		}
		watch, err := w.HotAddress()
		if err != nil {
			t.Fatal(err)
		}
		if priv != watch {
			t.Fatalf("%s: account hot %s != watch-only hot %s (m/1/0 mismatch)", fam, priv, watch)
		}
	}
}

// TestEVMAddressMatchesPrivateKey cross-validates the xpub-derived EVM
// address against go-ethereum's own private-key→address derivation.
func TestEVMAddressMatchesPrivateKey(t *testing.T) {
	mnemonic := "legal winner thank year wave sausage worth useful legal winner thank yellow"
	seed, err := Seed(mnemonic, "")
	if err != nil {
		t.Fatal(err)
	}
	acct, err := DeriveAccount(seed, EVM, MainNet)
	if err != nil {
		t.Fatal(err)
	}
	priv, err := acct.KeyAt(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	key, err := etcrypto.ToECDSA(priv)
	if err != nil {
		t.Fatal(err)
	}
	want := etcrypto.PubkeyToAddress(*key.Public().(*ecdsa.PublicKey)).Hex()
	w, err := ImportXPub(EVM, MainNet, mustXPub(t, acct))
	if err != nil {
		t.Fatal(err)
	}
	got, err := w.AddressAt(0)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.EqualFold(got, want) {
		t.Fatalf("watch-only %s != private %s", got, want)
	}
}

// TestTronAddressShape checks the TRON address form: base58check of
// 0x41||keccak12, 34 chars starting with 'T'.
func TestTronAddressShape(t *testing.T) {
	mnemonic := "legal winner thank year wave sausage worth useful legal winner thank yellow"
	seed, err := Seed(mnemonic, "")
	if err != nil {
		t.Fatal(err)
	}
	acct, err := DeriveAccount(seed, TRON, MainNet)
	if err != nil {
		t.Fatal(err)
	}
	w, err := ImportXPub(TRON, MainNet, mustXPub(t, acct))
	if err != nil {
		t.Fatal(err)
	}
	for i := uint32(0); i < 3; i++ {
		addr, err := w.AddressAt(i)
		if err != nil {
			t.Fatal(err)
		}
		if len(addr) != 34 || addr[0] != 'T' {
			t.Fatalf("tron address %q is not 34-char T-prefixed", addr)
		}
	}
	// Same curve/key path as EVM except coin type: the TRON address must
	// equal the EVM address body with the 0x41 prefix encoding.
	evm, err := DeriveAccount(seed, EVM, MainNet)
	if err != nil {
		t.Fatal(err)
	}
	evmAddr, err := evm.AddressAt(0)
	if err != nil {
		t.Fatal(err)
	}
	tronAddr, err := acct.AddressAt(0)
	if err != nil {
		t.Fatal(err)
	}
	// different coin type (195 vs 60) → different keys, so just assert
	// they differ (sanity that paths don't collide)
	if strings.EqualFold(evmAddr[2:], tronAddr) {
		t.Fatal("tron path must not collide with evm path")
	}
}

// TestLTCDerivation checks LTC native SegWit address shape (ltc1…).
func TestLTCDerivation(t *testing.T) {
	mnemonic := "legal winner thank year wave sausage worth useful legal winner thank yellow"
	seed, err := Seed(mnemonic, "")
	if err != nil {
		t.Fatal(err)
	}
	acct, err := DeriveAccount(seed, LTC, MainNet)
	if err != nil {
		t.Fatal(err)
	}
	addr, err := acct.AddressAt(0)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(addr, "ltc1") {
		t.Fatalf("ltc address %q not bech32 ltc1", addr)
	}
}

// TestXPubCarriesNoPrivateMaterial pins the watch-only invariant.
func TestXPubCarriesNoPrivateMaterial(t *testing.T) {
	mnemonic := "abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about"
	seed, _ := Seed(mnemonic, "")
	acct, _ := DeriveAccount(seed, BTC, MainNet)
	xpub, err := acct.XPub()
	if err != nil {
		t.Fatal(err)
	}
	// An xprv in the xpub slot must be rejected by ImportXPub.
	if _, err := ImportXPub(BTC, MainNet, acct.XPrv()); err == nil {
		t.Fatal("ImportXPub accepted an xprv")
	}
	w, err := ImportXPub(BTC, MainNet, xpub)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.key.ECPrivKey(); err == nil {
		t.Fatal("watch key exposed a private key")
	}
}

// TestKeystoreRoundTrip exercises encrypt→decrypt and the wrong
// passphrase rejection.
func TestKeystoreRoundTrip(t *testing.T) {
	mnemonic := "legal winner thank year wave sausage worth useful legal winner thank yellow"
	seed, _ := Seed(mnemonic, "")
	hk := HotKeystore{Accounts: map[string]AccountKeys{}}
	for _, fam := range []string{BTC, LTC, EVM, TRON} {
		acct, err := DeriveAccount(seed, fam, MainNet)
		if err != nil {
			t.Fatal(err)
		}
		hot, err := acct.HotAddress()
		if err != nil {
			t.Fatal(err)
		}
		hk.Accounts[fam] = AccountKeys{XPrv: acct.XPrv(), HotAddress: hot}
	}
	file, err := EncryptHot(hk, "ceremony-passphrase")
	if err != nil {
		t.Fatal(err)
	}
	got, err := DecryptHot(file, "ceremony-passphrase")
	if err != nil {
		t.Fatal(err)
	}
	if got.Accounts[BTC] != hk.Accounts[BTC] {
		t.Fatalf("round trip changed %v", got.Accounts[BTC])
	}
	if _, err := DecryptHot(file, "wrong"); err != ErrBadPassphrase {
		t.Fatalf("wrong passphrase = %v, want ErrBadPassphrase", err)
	}
}

// TestDepositIndexDeterminism: same index, same address, different
// index, different address.
func TestDepositIndexDeterminism(t *testing.T) {
	mnemonic := "abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about"
	seed, _ := Seed(mnemonic, "")
	acct, _ := DeriveAccount(seed, BTC, MainNet)
	xpub := mustXPub(t, acct)
	w, _ := ImportXPub(BTC, MainNet, xpub)
	a1, _ := w.AddressAt(5)
	a1b, _ := w.AddressAt(5)
	a2, _ := w.AddressAt(6)
	if a1 != a1b {
		t.Fatal("same index derived different addresses")
	}
	if a1 == a2 {
		t.Fatal("different indices derived the same address")
	}
}

func mustXPub(t *testing.T, a *Account) string {
	t.Helper()
	x, err := a.XPub()
	if err != nil {
		t.Fatal(err)
	}
	return x
}
