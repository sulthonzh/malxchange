package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/mail"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/syaikhipin/malxchange/internal/admin"
	"github.com/syaikhipin/malxchange/internal/auth"
	"github.com/syaikhipin/malxchange/internal/config"
)

// newFlagSet builds a quiet flag set: parse errors are surfaced by run
// as the command's error, not by flag's own printing (walletctl
// pattern).
func newFlagSet(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	return fs
}

// addPGFlags registers the shared connection flags. Defaults fall back
// to the same MAL_PG_* environment the services read, so a command run
// inside the compose network (docker compose run admin) needs no flags
// at all; the host default is loopback instead of config.Load's
// "postgres" because this tool runs where that name does not resolve.
func addPGFlags(fs *flag.FlagSet) *pgFlags {
	return &pgFlags{
		host:     fs.String("host", config.EnvDefault("MAL_PG_HOST", "127.0.0.1"), "Postgres host (env MAL_PG_HOST)"),
		port:     fs.String("port", config.EnvDefault("MAL_PG_PORT", "5432"), "Postgres port (env MAL_PG_PORT)"),
		database: fs.String("database", config.EnvDefault("MAL_PG_DATABASE", "malxchange"), "database name (env MAL_PG_DATABASE)"),
		user:     fs.String("user", config.EnvDefault("MAL_PG_USER", "admin"), "Postgres role (env MAL_PG_USER; the admin schema's owner)"),
		schema:   fs.String("schema", config.EnvDefault("MAL_PG_SCHEMA", "admin"), "admin schema (env MAL_PG_SCHEMA)"),
	}
}

type pgFlags struct {
	host, port, database, user, schema *string
}

// openPool connects to the admin schema. The DB password comes from the
// environment only — putting it on argv would leak it to every `ps` on
// the host. The URL pins search_path exactly like config.PG.URL.
func (f *pgFlags) openPool() (*pgxpool.Pool, error) {
	password, ok := os.LookupEnv("MAL_PG_PASSWORD")
	if !ok || password == "" {
		return nil, fmt.Errorf("MAL_PG_PASSWORD is not set (the admin role's DB password; with deploy/.env sourced: MAL_PG_PASSWORD=$MAL_PG_PASSWORD_ADMIN)")
	}
	cfg := config.PG{
		Host:     *f.host,
		Port:     *f.port,
		Database: *f.database,
		User:     *f.user,
		Password: password,
		Schema:   *f.schema,
	}
	pool, err := pgxpool.New(context.Background(), cfg.URL())
	if err != nil {
		return nil, fmt.Errorf("connect %s:%s/%s: %w", *f.host, *f.port, *f.database, err)
	}
	// Eager, time-bounded reachability check: a break-glass session must
	// fail fast with a clear cause, not hang on an unroutable host.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("reach %s:%s/%s as %s: %w", *f.host, *f.port, *f.database, *f.user, err)
	}
	return pool, nil
}

// normalizeEmail mirrors the admin surface's rule (lowercase, trimmed,
// a real single address) so break-glass targets exactly the stored
// identity shape.
func normalizeEmail(raw string) (string, error) {
	email := strings.ToLower(strings.TrimSpace(raw))
	addr, err := mail.ParseAddress(email)
	if err != nil || addr.Address != email || len(email) > 254 {
		return "", errors.New("Enter a valid email address.")
	}
	return email, nil
}

// requireEmail parses the shared --email flag.
func requireEmail(flagged string) (string, error) {
	if flagged == "" {
		return "", fmt.Errorf("--email is required")
	}
	return normalizeEmail(flagged)
}

// oneTimePassword mints the credential the operator hands over: 24
// random bytes as hex, the same shape the installer generates
// (install.sh gen()). CheckPasswordPolicy is satisfied by construction.
func oneTimePassword() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate one-time password: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// resolveOneTimePassword picks the new credential's source: a named
// environment variable (name only on argv — the ps-safe walletctl
// pattern), a file, or generation when neither is given. Operator-chosen
// passwords must clear the shared login policy; generated ones do by
// construction. The returned flag says whether the tool generated it,
// because only generated credentials are printed (an operator-supplied
// secret already lives somewhere; echoing it into terminal scrollback
// buys nothing).
func resolveOneTimePassword(envName, file string) (password string, generated bool, err error) {
	switch {
	case envName != "" && file != "":
		return "", false, fmt.Errorf("give at most one of --password-env and --password-file")
	case envName != "":
		v, ok := os.LookupEnv(envName)
		if !ok || v == "" {
			return "", false, fmt.Errorf("environment variable %s (new one-time password) is not set", envName)
		}
		password = strings.TrimSpace(v)
	case file != "":
		b, err := os.ReadFile(file)
		if err != nil {
			return "", false, fmt.Errorf("read %s: %w", file, err)
		}
		password = strings.TrimSpace(string(b))
		if password == "" {
			return "", false, fmt.Errorf("%s is empty", file)
		}
	default:
		password, err = oneTimePassword()
		if err != nil {
			return "", false, err
		}
		return password, true, nil
	}
	if err := auth.CheckPasswordPolicy(password); err != nil {
		return "", false, err
	}
	return password, false, nil
}

// printChange reports one mutation's outcome: what changed, the
// bookkeeping the runbook promises (sessions, audit), and — when the
// tool generated it — the one-time credential, exactly once.
func printChange(out io.Writer, ch admin.BreakglassChange, oneTime string) {
	fmt.Fprintf(out, "%s %s (%s)\n", ch.Action, ch.Email, ch.Role)
	if oneTime != "" {
		fmt.Fprintf(out, "one-time password: %s\n", oneTime)
		fmt.Fprintln(out, "  shown once — the next sign-in forces a new password and TOTP enrollment")
	}
	if ch.SessionsRevoked > 0 {
		fmt.Fprintf(out, "%d live session(s) revoked\n", ch.SessionsRevoked)
	}
	fmt.Fprintf(out, "audit row recorded: %s by %s\n", ch.Action, ch.Actor)
}

// runList shows every Employee's recovery-relevant state. It is the
// runbook's step 1: confirm which super-admins exist before touching
// anything. Read-only, so (like every admin read) it writes no audit
// row; the mutations below all do.
func runList(args []string, out io.Writer) error {
	fs := newFlagSet("list")
	pg := addPGFlags(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	pool, err := pg.openPool()
	if err != nil {
		return err
	}
	defer pool.Close()
	rows, err := admin.BreakglassList(context.Background(), pool)
	if err != nil {
		return err
	}
	tw := tabwriter.NewWriter(out, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "EMAIL\tROLE\tENABLED\tTOTP-BOUND\tMUST-ROTATE")
	for _, r := range rows {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n",
			r.Email, r.Role, yesNo(r.Enabled), yesNo(r.TOTPBound), yesNo(r.MustRotate))
	}
	return tw.Flush()
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

func runResetPassword(args []string, out io.Writer) error {
	fs := newFlagSet("reset-password")
	emailFlag := fs.String("email", "", "the super-admin's email")
	passEnv := fs.String("password-env", "", "environment variable holding the new one-time password (its name only, never the value)")
	passFile := fs.String("password-file", "", "file holding the new one-time password")
	pg := addPGFlags(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	email, err := requireEmail(*emailFlag)
	if err != nil {
		return err
	}
	password, generated, err := resolveOneTimePassword(*passEnv, *passFile)
	if err != nil {
		return err
	}
	pool, err := pg.openPool()
	if err != nil {
		return err
	}
	defer pool.Close()
	ch, err := admin.BreakglassResetPassword(context.Background(), pool, email, password)
	if err != nil {
		return err
	}
	printChange(out, ch, credentialIf(generated, password))
	return nil
}

func runResetTOTP(args []string, out io.Writer) error {
	fs := newFlagSet("reset-totp")
	emailFlag := fs.String("email", "", "the super-admin's email")
	pg := addPGFlags(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	email, err := requireEmail(*emailFlag)
	if err != nil {
		return err
	}
	pool, err := pg.openPool()
	if err != nil {
		return err
	}
	defer pool.Close()
	ch, err := admin.BreakglassResetTOTP(context.Background(), pool, email)
	if err != nil {
		return err
	}
	printChange(out, ch, "")
	return nil
}

func runEnable(args []string, out io.Writer) error {
	fs := newFlagSet("enable")
	emailFlag := fs.String("email", "", "the super-admin's email")
	pg := addPGFlags(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	email, err := requireEmail(*emailFlag)
	if err != nil {
		return err
	}
	pool, err := pg.openPool()
	if err != nil {
		return err
	}
	defer pool.Close()
	ch, err := admin.BreakglassEnable(context.Background(), pool, email)
	if err != nil {
		return err
	}
	printChange(out, ch, "")
	return nil
}

func runCreate(args []string, out io.Writer) error {
	fs := newFlagSet("create")
	emailFlag := fs.String("email", "", "the new super-admin's email")
	passEnv := fs.String("password-env", "", "environment variable holding the one-time password (its name only, never the value)")
	passFile := fs.String("password-file", "", "file holding the one-time password")
	pg := addPGFlags(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	email, err := requireEmail(*emailFlag)
	if err != nil {
		return err
	}
	password, generated, err := resolveOneTimePassword(*passEnv, *passFile)
	if err != nil {
		return err
	}
	pool, err := pg.openPool()
	if err != nil {
		return err
	}
	defer pool.Close()
	ch, err := admin.BreakglassCreateSuperAdmin(context.Background(), pool, email, password)
	if err != nil {
		return err
	}
	printChange(out, ch, credentialIf(generated, password))
	return nil
}

// credentialIf yields the generated credential for printing, and only
// that case: operator-supplied secrets are never echoed.
func credentialIf(generated bool, password string) string {
	if generated {
		return password
	}
	return ""
}
