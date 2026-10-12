package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/syaikhipin/malxchange/internal/wallet/keys"
)

const testMnemonic = "abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about"

// runCmd runs the CLI in-process and returns stdout, failing on error.
func runCmd(t *testing.T, args ...string) string {
	t.Helper()
	var buf bytes.Buffer
	if err := run(args, &buf); err != nil {
		t.Fatalf("run(%v): %v", args, err)
	}
	return buf.String()
}

// runCmdErr runs the CLI expecting an error and returns it.
func runCmdErr(t *testing.T, args ...string) error {
	t.Helper()
	var buf bytes.Buffer
	err := run(args, &buf)
	if err == nil {
		t.Fatalf("run(%v): expected error, got output %q", args, buf.String())
	}
	return err
}

// genMnemonic generates via the command under test.
func genMnemonic(t *testing.T, bits string) string {
	t.Helper()
	args := []string{"gen-mnemonic"}
	if bits != "" {
		args = append(args, "--bits", bits)
	}
	return strings.TrimSpace(runCmd(t, args...))
}

func writeFile(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestGenMnemonic(t *testing.T) {
	if got := len(strings.Fields(genMnemonic(t, ""))); got != 24 {
		t.Errorf("default --bits 256: got %d words, want 24", got)
	}
	if got := len(strings.Fields(genMnemonic(t, "128"))); got != 12 {
		t.Errorf("--bits 128: got %d words, want 12", got)
	}
	if a, b := genMnemonic(t, ""), genMnemonic(t, ""); a == b {
		t.Error("two generated mnemonics are identical")
	}
	if err := runCmdErr(t, "gen-mnemonic", "--bits", "100"); err == nil {
		t.Error("--bits 100 accepted")
	}
}

func TestAccount(t *testing.T) {
	m := writeFile(t, "m.txt", testMnemonic+"\n")

	var acct accountJSON
	if err := json.Unmarshal([]byte(runCmd(t, "account", "--family", "btc", "--net", "mainnet", "--mnemonic-file", m)), &acct); err != nil {
		t.Fatal(err)
	}
	if acct.Family != "btc" || acct.XPub == "" || !strings.HasPrefix(acct.HotAddress, "bc1") {
		t.Errorf("btc account: %+v", acct)
	}

	// simnet re-encodes addresses and xpub version bytes; same flow.
	var sim accountJSON
	if err := json.Unmarshal([]byte(runCmd(t, "account", "--family", "btc", "--net", "simnet", "--mnemonic-file", m)), &sim); err != nil {
		t.Fatal(err)
	}
	if sim.XPub == "" || !strings.HasPrefix(sim.HotAddress, "sb1") {
		t.Errorf("btc simnet account: %+v", sim)
	}

	// The BIP-39 passphrase (25th word) is optional but honored.
	p := writeFile(t, "p.txt", "extra-secret\n")
	var pp accountJSON
	if err := json.Unmarshal([]byte(runCmd(t, "account", "--family", "evm", "--net", "mainnet", "--mnemonic-file", m, "--passphrase-file", p)), &pp); err != nil {
		t.Fatal(err)
	}
	var plain accountJSON
	if err := json.Unmarshal([]byte(runCmd(t, "account", "--family", "evm", "--net", "mainnet", "--mnemonic-file", m)), &plain); err != nil {
		t.Fatal(err)
	}
	if pp.XPub == plain.XPub || !strings.HasPrefix(pp.HotAddress, "0x") {
		t.Errorf("passphrase did not change the evm account: %+v", pp)
	}

	for _, fam := range []string{"ltc", "evm", "tron"} {
		var a accountJSON
		if err := json.Unmarshal([]byte(runCmd(t, "account", "--family", fam, "--net", "mainnet", "--mnemonic-file", m)), &a); err != nil {
			t.Fatal(err)
		}
		var wantPrefix string
		switch fam {
		case "ltc":
			wantPrefix = "ltc1"
		case "evm":
			wantPrefix = "0x"
		case "tron":
			wantPrefix = "T"
		}
		if a.Family != fam || a.XPub == "" || !strings.HasPrefix(a.HotAddress, wantPrefix) {
			t.Errorf("%s account: %+v", fam, a)
		}
	}

	if err := runCmdErr(t, "account", "--family", "doge", "--net", "mainnet", "--mnemonic-file", m); err == nil {
		t.Error("unknown family accepted")
	}
	if err := runCmdErr(t, "account", "--family", "btc", "--net", "testnet", "--mnemonic-file", m); err == nil {
		t.Error("unknown net accepted")
	}
	if err := runCmdErr(t, "account", "--family", "btc"); err == nil {
		t.Error("missing --mnemonic-file accepted")
	}
}

func TestDepositAddress(t *testing.T) {
	m := writeFile(t, "m.txt", testMnemonic)

	var acct accountJSON
	if err := json.Unmarshal([]byte(runCmd(t, "account", "--family", "btc", "--net", "mainnet", "--mnemonic-file", m)), &acct); err != nil {
		t.Fatal(err)
	}

	seed, err := keys.Seed(testMnemonic, "")
	if err != nil {
		t.Fatal(err)
	}
	priv, err := keys.DeriveAccount(seed, keys.BTC, keys.MainNet)
	if err != nil {
		t.Fatal(err)
	}

	// The watch-only path must match the private derivation m/0/i.
	want0, err := priv.AddressAt(0)
	if err != nil {
		t.Fatal(err)
	}
	got0 := strings.TrimSpace(runCmd(t, "deposit-address", "--family", "btc", "--net", "mainnet", "--xpub", acct.XPub, "--index", "0"))
	if got0 != want0 {
		t.Errorf("m/0/0: got %s, want %s", got0, want0)
	}
	want1, err := priv.AddressAt(1)
	if err != nil {
		t.Fatal(err)
	}
	got1 := strings.TrimSpace(runCmd(t, "deposit-address", "--family", "btc", "--net", "mainnet", "--xpub", acct.XPub, "--index", "1"))
	if got1 != want1 || got1 == got0 {
		t.Errorf("m/0/1: got %s, want %s", got1, want1)
	}

	// An xprv in the xpub slot must be rejected: private material has no
	// business on the watch-only path.
	if err := runCmdErr(t, "deposit-address", "--family", "btc", "--net", "mainnet", "--xpub", priv.XPrv(), "--index", "0"); err == nil {
		t.Error("xprv accepted as --xpub")
	}
	if err := runCmdErr(t, "deposit-address", "--family", "btc", "--net", "mainnet", "--xpub", acct.XPub, "--index", "2147483648"); err == nil {
		t.Error("hardened-range index accepted")
	}
}

func TestHotKeystoreRoundTrip(t *testing.T) {
	dir := t.TempDir()
	const envName = "MAL_HOT_KEYSTORE_PASSPHRASE"
	t.Setenv(envName, "ceremony-test-passphrase")

	var files []string
	for _, fam := range families {
		files = append(files, writeFile(t, fam+".txt", genMnemonic(t, "128")))
	}
	keystore := filepath.Join(dir, "hot.keystore.json")

	var summary hotSummary
	out := runCmd(t, "hot-keystore",
		"--mnemonic-btc", files[0], "--mnemonic-ltc", files[1],
		"--mnemonic-evm", files[2], "--mnemonic-tron", files[3],
		"--passphrase-env", envName, "--out", keystore)
	if err := json.Unmarshal([]byte(out), &summary); err != nil {
		t.Fatal(err)
	}
	if len(summary.Accounts) != len(families) || len(summary.Skipped) != 0 {
		t.Errorf("summary: %+v", summary)
	}

	// The keystore is the only secret on disk and it is owner-only.
	st, err := os.Stat(keystore)
	if err != nil {
		t.Fatal(err)
	}
	if perm := st.Mode().Perm(); perm != 0o600 {
		t.Errorf("keystore mode is %o, want 600", perm)
	}

	// Round-trip: every family verifies against its mnemonic.
	for i, fam := range families {
		got := runCmd(t, "verify", "--keystore", keystore, "--passphrase-env", envName,
			"--family", fam, "--net", "mainnet", "--mnemonic-file", files[i])
		if !strings.HasPrefix(got, "OK "+fam+":") {
			t.Errorf("verify %s: %q", fam, got)
		}
	}

	// A wrong passphrase must fail closed.
	t.Setenv(envName, "wrong-passphrase")
	err = runCmdErr(t, "verify", "--keystore", keystore, "--passphrase-env", envName,
		"--family", "btc", "--net", "mainnet", "--mnemonic-file", files[0])
	if err == nil || !strings.Contains(err.Error(), "passphrase") {
		t.Errorf("wrong passphrase: got %v", err)
	}
	if !errors.Is(err, keys.ErrBadPassphrase) {
		t.Errorf("wrong passphrase: error does not wrap keys.ErrBadPassphrase: %v", err)
	}

	// A different valid mnemonic must fail the equality check.
	t.Setenv(envName, "ceremony-test-passphrase")
	other := writeFile(t, "other-btc.txt", genMnemonic(t, "128"))
	err = runCmdErr(t, "verify", "--keystore", keystore, "--passphrase-env", envName,
		"--family", "btc", "--net", "mainnet", "--mnemonic-file", other)
	if err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Errorf("wrong mnemonic: got %v", err)
	}

	// A corrupted keystore must fail closed too.
	blob, err := os.ReadFile(keystore)
	if err != nil {
		t.Fatal(err)
	}
	blob[len(blob)-4] ^= 0xFF
	corrupt := filepath.Join(dir, "corrupt.keystore.json")
	if err := os.WriteFile(corrupt, blob, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := runCmdErr(t, "verify", "--keystore", corrupt, "--passphrase-env", envName,
		"--family", "btc", "--net", "mainnet", "--mnemonic-file", files[0]); err == nil {
		t.Error("corrupted keystore verified")
	}
}

func TestHotKeystoreSkipsFamiliesWithoutMnemonics(t *testing.T) {
	dir := t.TempDir()
	const envName = "MAL_HOT_KEYSTORE_PASSPHRASE"
	t.Setenv(envName, "ceremony-test-passphrase")
	evm := writeFile(t, "evm.txt", testMnemonic)
	out := filepath.Join(dir, "partial.keystore.json")

	var summary hotSummary
	if err := json.Unmarshal([]byte(runCmd(t, "hot-keystore", "--mnemonic-evm", evm,
		"--passphrase-env", envName, "--out", out)), &summary); err != nil {
		t.Fatal(err)
	}
	if _, ok := summary.Accounts["evm"]; !ok || len(summary.Accounts) != 1 {
		t.Errorf("accounts: %+v", summary.Accounts)
	}
	if strings.Join(summary.Skipped, ",") != "btc,ltc,tron" {
		t.Errorf("skipped: %v", summary.Skipped)
	}

	if got := runCmd(t, "verify", "--keystore", out, "--passphrase-env", envName,
		"--family", "evm", "--net", "mainnet", "--mnemonic-file", evm); !strings.HasPrefix(got, "OK evm:") {
		t.Errorf("verify evm: %q", got)
	}
	err := runCmdErr(t, "verify", "--keystore", out, "--passphrase-env", envName,
		"--family", "btc", "--net", "mainnet", "--mnemonic-file", evm)
	if err == nil || !strings.Contains(err.Error(), "not in the keystore") {
		t.Errorf("verify watch-only family: got %v", err)
	}

	// No mnemonic at all: nothing to seal.
	if err := runCmdErr(t, "hot-keystore", "--passphrase-env", envName, "--out", out); err == nil {
		t.Error("keystore without any mnemonic built")
	}
	// The passphrase must come from the environment.
	if err := runCmdErr(t, "hot-keystore", "--mnemonic-evm", evm,
		"--passphrase-env", "MAL_UNSET_PASSPHRASE", "--out", out); err == nil {
		t.Error("unset passphrase env accepted")
	}
}

func TestRunErrors(t *testing.T) {
	if err := runCmdErr(t); err == nil || !strings.Contains(err.Error(), "no command") {
		t.Errorf("no args: got %v", err)
	}
	if err := runCmdErr(t, "frobnicate"); err == nil || !strings.Contains(err.Error(), "unknown command") {
		t.Errorf("unknown command: got %v", err)
	}
}
