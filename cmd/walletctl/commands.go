package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/syaikhipin/malxchange/internal/wallet/keys"
)

// families is the v1 family set in canonical iteration order.
var families = []string{keys.BTC, keys.LTC, keys.EVM, keys.TRON}

func validFamily(f string) bool {
	for _, fam := range families {
		if fam == f {
			return true
		}
	}
	return false
}

func validNet(net string) bool {
	return net == keys.MainNet || net == keys.SimNet
}

// newFlagSet builds a quiet flag set: parse errors are surfaced by run
// as the command's error, not by flag's own printing.
func newFlagSet(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	return fs
}

// readFileTrim reads a small secret file (mnemonic or passphrase),
// trimming surrounding whitespace. Secrets never go anywhere else.
func readFileTrim(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", path, err)
	}
	s := strings.TrimSpace(string(b))
	if s == "" {
		return "", fmt.Errorf("%s is empty", path)
	}
	return s, nil
}

// passFromEnv reads the keystore passphrase from the named environment
// variable. Only its name ever appears in output.
func passFromEnv(name string) (string, error) {
	v, ok := os.LookupEnv(name)
	if !ok || v == "" {
		return "", fmt.Errorf("environment variable %s (keystore passphrase) is not set", name)
	}
	return v, nil
}

// printJSON writes v as indented JSON, one trailing newline.
func printJSON(out io.Writer, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(out, string(b))
	return err
}

// deriveFromFiles validates the shared flags and derives the account
// from a mnemonic file (plus an optional BIP-39 passphrase file). It is
// the common path of account and verify.
func deriveFromFiles(family, net, mnemonicFile, passphraseFile string) (*keys.Account, error) {
	if !validFamily(family) {
		return nil, fmt.Errorf("--family must be one of btc, ltc, evm, tron (got %q)", family)
	}
	if !validNet(net) {
		return nil, fmt.Errorf("--net must be mainnet or simnet (got %q)", net)
	}
	if mnemonicFile == "" {
		return nil, errors.New("--mnemonic-file is required")
	}
	mnemonic, err := readFileTrim(mnemonicFile)
	if err != nil {
		return nil, err
	}
	passphrase := ""
	if passphraseFile != "" {
		if passphrase, err = readFileTrim(passphraseFile); err != nil {
			return nil, err
		}
	}
	seed, err := keys.Seed(mnemonic, passphrase)
	if err != nil {
		return nil, fmt.Errorf("mnemonic: %w", err)
	}
	return keys.DeriveAccount(seed, family, net)
}

// runGenMnemonic prints fresh ceremony entropy. The operator copies the
// words to paper; the tool never stores them.
func runGenMnemonic(args []string, out io.Writer) error {
	fs := newFlagSet("gen-mnemonic")
	bits := fs.Int("bits", 256, "entropy bits: 128, 192 or 256")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("unexpected argument %q", fs.Arg(0))
	}
	switch *bits {
	case 128, 192, 256:
	default:
		return fmt.Errorf("--bits must be 128, 192 or 256 (got %d)", *bits)
	}
	mnemonic, err := keys.NewMnemonic(*bits)
	if err != nil {
		return err
	}
	fmt.Fprintln(out, mnemonic)
	return nil
}

// accountJSON is the registration payload of one family: the xpub goes
// to the Coin config, the hot address to the ops records.
type accountJSON struct {
	Family     string `json:"family"`
	XPub       string `json:"xpub"`
	HotAddress string `json:"hotAddress"`
}

// runAccount derives one family's account and prints its public
// registration material. The mnemonic stays offline.
func runAccount(args []string, out io.Writer) error {
	fs := newFlagSet("account")
	family := fs.String("family", "", "chain family: btc, ltc, evm or tron")
	net := fs.String("net", keys.MainNet, "network: mainnet or simnet (btc/ltc address encoding)")
	mnemonicFile := fs.String("mnemonic-file", "", "file holding the family mnemonic (required)")
	passphraseFile := fs.String("passphrase-file", "", "file holding the BIP-39 passphrase (25th word; optional)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("unexpected argument %q", fs.Arg(0))
	}
	acct, err := deriveFromFiles(*family, *net, *mnemonicFile, *passphraseFile)
	if err != nil {
		return err
	}
	xpub, err := acct.XPub()
	if err != nil {
		return err
	}
	hot, err := acct.HotAddress()
	if err != nil {
		return err
	}
	return printJSON(out, accountJSON{Family: *family, XPub: xpub, HotAddress: hot})
}

// runDepositAddress re-derives a deposit address from the registered
// xpub — the watch-only cross-check that the host wallet derives the
// same address. It accepts no private material (ImportXPub rejects it).
func runDepositAddress(args []string, out io.Writer) error {
	fs := newFlagSet("deposit-address")
	family := fs.String("family", "", "chain family: btc, ltc, evm or tron")
	net := fs.String("net", keys.MainNet, "network: mainnet or simnet (btc/ltc address encoding)")
	xpub := fs.String("xpub", "", "watch-only account xpub registered on the Coin (required)")
	index := fs.Uint("index", 0, "deposit index i of address m/0/i")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("unexpected argument %q", fs.Arg(0))
	}
	if !validFamily(*family) {
		return fmt.Errorf("--family must be one of btc, ltc, evm, tron (got %q)", *family)
	}
	if !validNet(*net) {
		return fmt.Errorf("--net must be mainnet or simnet (got %q)", *net)
	}
	if *xpub == "" {
		return errors.New("--xpub is required")
	}
	if *index > 0x7FFFFFFF {
		return fmt.Errorf("--index out of range (got %d)", *index)
	}
	watch, err := keys.ImportXPub(*family, *net, *xpub)
	if err != nil {
		return fmt.Errorf("--xpub: %w", err)
	}
	addr, err := watch.AddressAt(uint32(*index))
	if err != nil {
		return fmt.Errorf("derive m/0/%d: %w", *index, err)
	}
	fmt.Fprintln(out, addr)
	return nil
}

// hotSummary is the hot-keystore command's output: what to register on
// Coins (xpubs) and in ops records (hot addresses), per family.
type hotSummary struct {
	Net      string                    `json:"net"`
	Out      string                    `json:"out"`
	Accounts map[string]accountSummary `json:"accounts"`
	Skipped  []string                  `json:"skipped,omitempty"`
}

type accountSummary struct {
	XPub       string `json:"xpub"`
	HotAddress string `json:"hotAddress"`
}

// runHotKeystore builds the encrypted hot keystore from whichever
// family mnemonics are given. A family whose mnemonic flag is omitted
// stays watch-only: nothing of it enters the keystore.
func runHotKeystore(args []string, out io.Writer) error {
	fs := newFlagSet("hot-keystore")
	mnemonicFile := map[string]*string{
		keys.BTC:  fs.String("mnemonic-btc", "", "file with the btc mnemonic (omit to keep btc watch-only)"),
		keys.LTC:  fs.String("mnemonic-ltc", "", "file with the ltc mnemonic (omit to keep ltc watch-only)"),
		keys.EVM:  fs.String("mnemonic-evm", "", "file with the evm mnemonic (omit to keep evm watch-only)"),
		keys.TRON: fs.String("mnemonic-tron", "", "file with the tron mnemonic (omit to keep tron watch-only)"),
	}
	passEnv := fs.String("passphrase-env", "MAL_HOT_KEYSTORE_PASSPHRASE", "name of the env var holding the keystore passphrase")
	outFile := fs.String("out", "", "keystore output path (required, written with mode 0600)")
	net := fs.String("net", keys.MainNet, "network: mainnet or simnet")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("unexpected argument %q", fs.Arg(0))
	}
	if !validNet(*net) {
		return fmt.Errorf("--net must be mainnet or simnet (got %q)", *net)
	}
	if *outFile == "" {
		return errors.New("--out is required")
	}
	pass, err := passFromEnv(*passEnv)
	if err != nil {
		return err
	}

	hk := keys.HotKeystore{Accounts: map[string]keys.AccountKeys{}}
	summary := hotSummary{Net: *net, Out: *outFile, Accounts: map[string]accountSummary{}}
	for _, fam := range families {
		path := *mnemonicFile[fam]
		if path == "" {
			summary.Skipped = append(summary.Skipped, fam)
			continue
		}
		acct, err := deriveFromFiles(fam, *net, path, "")
		if err != nil {
			return err
		}
		xpub, err := acct.XPub()
		if err != nil {
			return err
		}
		hot, err := acct.HotAddress()
		if err != nil {
			return err
		}
		hk.Accounts[fam] = keys.AccountKeys{XPrv: acct.XPrv(), HotAddress: hot}
		summary.Accounts[fam] = accountSummary{XPub: xpub, HotAddress: hot}
	}
	if len(hk.Accounts) == 0 {
		return errors.New("no mnemonics given: at least one of --mnemonic-btc, --mnemonic-ltc, --mnemonic-evm, --mnemonic-tron is required")
	}

	blob, err := keys.EncryptHot(hk, pass)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(*outFile, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("create %s: %w", *outFile, err)
	}
	defer f.Close()
	// Chmod covers a pre-existing file, whose permissions OpenFile keeps.
	if err := f.Chmod(0o600); err != nil {
		return fmt.Errorf("chmod %s: %w", *outFile, err)
	}
	if _, err := f.Write(blob); err != nil {
		return fmt.Errorf("write %s: %w", *outFile, err)
	}
	if err := f.Sync(); err != nil {
		return fmt.Errorf("sync %s: %w", *outFile, err)
	}
	return printJSON(out, summary)
}

// runVerify is the ceremony end-check: the keystore must decrypt with
// the env passphrase and the stored account must equal a fresh
// derivation from the offline mnemonic. Any mismatch is a non-zero
// exit.
func runVerify(args []string, out io.Writer) error {
	fs := newFlagSet("verify")
	keystoreFile := fs.String("keystore", "", "hot keystore path (required)")
	passEnv := fs.String("passphrase-env", "MAL_HOT_KEYSTORE_PASSPHRASE", "name of the env var holding the keystore passphrase")
	family := fs.String("family", "", "chain family to verify: btc, ltc, evm or tron")
	net := fs.String("net", keys.MainNet, "network: mainnet or simnet (btc/ltc address encoding)")
	mnemonicFile := fs.String("mnemonic-file", "", "offline mnemonic file of the family (required)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("unexpected argument %q", fs.Arg(0))
	}
	if !validFamily(*family) {
		return fmt.Errorf("--family must be one of btc, ltc, evm, tron (got %q)", *family)
	}
	if !validNet(*net) {
		return fmt.Errorf("--net must be mainnet or simnet (got %q)", *net)
	}
	if *keystoreFile == "" {
		return errors.New("--keystore is required")
	}
	if *mnemonicFile == "" {
		return errors.New("--mnemonic-file is required")
	}
	pass, err := passFromEnv(*passEnv)
	if err != nil {
		return err
	}
	blob, err := os.ReadFile(*keystoreFile)
	if err != nil {
		return fmt.Errorf("read %s: %w", *keystoreFile, err)
	}
	hk, err := keys.DecryptHot(blob, pass)
	if err != nil {
		if errors.Is(err, keys.ErrBadPassphrase) {
			return fmt.Errorf("keystore does not open (wrong passphrase or corrupted file): %w", err)
		}
		return err
	}
	stored, ok := hk.Accounts[*family]
	if !ok {
		return fmt.Errorf("family %q is not in the keystore (it was built watch-only)", *family)
	}
	acct, err := deriveFromFiles(*family, *net, *mnemonicFile, "")
	if err != nil {
		return err
	}
	hot, err := acct.HotAddress()
	if err != nil {
		return err
	}
	if stored.XPrv != acct.XPrv() {
		return fmt.Errorf("stored %s xprv does not match a derivation from the mnemonic", *family)
	}
	if stored.HotAddress != hot {
		return fmt.Errorf("stored %s hot address %s does not match derivation %s", *family, stored.HotAddress, hot)
	}
	fmt.Fprintf(out, "OK %s: keystore matches the offline mnemonic (hot address %s)\n", *family, stored.HotAddress)
	return nil
}
