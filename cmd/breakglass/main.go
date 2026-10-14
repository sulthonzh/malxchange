// Command breakglass is the documented recovery path of ADR-0012: the
// host-side tool that restores access for a locked-out sole super-admin
// when no Employee can sign in to the admin-api HTTP surface. It writes
// the admin schema directly (no HTTP path is involved at all) and lands
// every action in the audit log under the system break-glass actor.
// It is the exception path, not a routine one — the procedure, when it
// applies, and the operator-error context live in install/break-glass.md.
package main

import (
	"errors"
	"fmt"
	"io"
	"os"
)

const usage = `breakglass — recover a locked-out super-admin from the host (ADR-0012)

No admin-api HTTP path is involved: the tool talks to the admin schema
in Postgres directly. Every action is recorded in the audit log under
the system break-glass actor. The full procedure — when to use it, and
what state the employee ends up in — is install/break-glass.md.

Usage:

  breakglass <command> [flags]

Commands:

  list              show Employees (email, role, enabled, totp bound, must-rotate)
  reset-password    set a fresh one-time password for a super-admin
  reset-totp        clear a super-admin's TOTP binding
  enable            re-enable a disabled super-admin
  create            create a new super-admin with a one-time password

A password reset or TOTP reset revokes every live session and re-arms
forced first-login rotation, so the recovered Employee must set a new
password and (re-)enroll TOTP before anything else.

Connection: --host (env MAL_PG_HOST, default 127.0.0.1), --port
(MAL_PG_PORT, 5432), --database (MAL_PG_DATABASE, malxchange), --user
(MAL_PG_USER, admin), --schema (MAL_PG_SCHEMA, admin). The DB password
comes from the MAL_PG_PASSWORD environment variable only — never a
flag, never argv. Run "breakglass <command> -h" for command flags.
`

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "breakglass:", err)
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
	case "list":
		err = runList(rest, out)
	case "reset-password":
		err = runResetPassword(rest, out)
	case "reset-totp":
		err = runResetTOTP(rest, out)
	case "enable":
		err = runEnable(rest, out)
	case "create":
		err = runCreate(rest, out)
	default:
		return fmt.Errorf("unknown command %q\n\n%s", cmd, usage)
	}
	if err != nil {
		return fmt.Errorf("%s: %w", cmd, err)
	}
	return nil
}
