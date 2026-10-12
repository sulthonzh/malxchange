// Command walletctl is the offline key-ceremony tool of ADR-0008. It
// runs on an air-gapped machine: it generates the per-family mnemonics,
// derives the account keys whose xpubs get registered on Coins, builds
// the passphrase-encrypted hot keystore the host runs with, and
// verifies the keystore against the offline mnemonics. It performs no
// network I/O. The full procedure is install/key-ceremony.md.
package main

import (
	"errors"
	"fmt"
	"io"
	"os"
)

const usage = `walletctl — offline key ceremony for the malxchange wallet (ADR-0008)

Usage:

	walletctl <command> [flags]

Commands:

  gen-mnemonic      print a fresh BIP-39 mnemonic (copy it to paper, never store digitally)
  account           derive one family's account and print {family, xpub, hotAddress} as JSON
  deposit-address   derive a deposit address from an account xpub (watch-only cross-check)
  hot-keystore      build the encrypted hot keystore from the family mnemonics
  verify            assert a keystore account matches a fresh derivation from its mnemonic

Run "walletctl <command> -h" for the command's flags. The ceremony
procedure is install/key-ceremony.md; no command talks to the network.
`

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "walletctl:", err)
		os.Exit(1)
	}
}

// run dispatches one command; tests drive it directly with a capture
// buffer instead of a subprocess.
func run(args []string, out io.Writer) error {
	if len(args) == 0 {
		return errors.New("no command given\n\n" + usage)
	}
	cmd, rest := args[0], args[1:]
	var err error
	switch cmd {
	case "gen-mnemonic":
		err = runGenMnemonic(rest, out)
	case "account":
		err = runAccount(rest, out)
	case "deposit-address":
		err = runDepositAddress(rest, out)
	case "hot-keystore":
		err = runHotKeystore(rest, out)
	case "verify":
		err = runVerify(rest, out)
	default:
		return fmt.Errorf("unknown command %q\n\n%s", cmd, usage)
	}
	if err != nil {
		return fmt.Errorf("%s: %w", cmd, err)
	}
	return nil
}
