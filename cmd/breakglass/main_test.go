package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Hermetic unit tests: dispatch, flag validation, one-time password
// generation, and input errors that must fire before any connection is
// attempted. The per-command database behavior is the integration
// suite (breakglass_it_test.go, walletctl pattern: run() in-process).

// runOut runs the CLI in-process and returns stdout and the error.
func runOut(t *testing.T, args ...string) (string, error) {
	t.Helper()
	var buf bytes.Buffer
	err := run(args, &buf)
	return buf.String(), err
}

// wantErr asserts the command failed with message containing want.
func wantErr(t *testing.T, args []string, want string) {
	t.Helper()
	out, err := runOut(t, args...)
	if err == nil {
		t.Fatalf("run(%v): expected error %q, got success (out: %q)", args, want, out)
	}
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("run(%v): error %q does not contain %q", args, err, want)
	}
}

func TestNoCommandIsUsage(t *testing.T) {
	wantErr(t, nil, "no command given")
}

func TestUnknownCommand(t *testing.T) {
	wantErr(t, []string{"frobnicate"}, `unknown command "frobnicate"`)
}

func TestResetPasswordRequiresEmail(t *testing.T) {
	wantErr(t, []string{"reset-password"}, "--email is required")
}

func TestResetPasswordRejectsInvalidEmail(t *testing.T) {
	wantErr(t, []string{"reset-password", "--email", "not-an-email"}, "Enter a valid email address")
}

func TestResetPasswordEnvNameMissing(t *testing.T) {
	wantErr(t, []string{"reset-password", "--email", "root@example.com",
		"--password-env", "BREAKGLASS_NOPE"}, "BREAKGLASS_NOPE")
}

func TestResetPasswordFileMissing(t *testing.T) {
	wantErr(t, []string{"reset-password", "--email", "root@example.com",
		"--password-file", filepath.Join(t.TempDir(), "absent.txt")}, "absent.txt")
}

// The policy check must fire before any connection attempt: with the
// DB password env unset AND a too-short operator password supplied, the
// complaint is the password policy, not the connection.
func TestResetPasswordPolicyBeforeConnect(t *testing.T) {
	t.Setenv("MAL_PG_PASSWORD", "")
	t.Setenv("BREAKGLASS_SHORT", "short")
	wantErr(t, []string{"reset-password", "--email", "root@example.com",
		"--password-env", "BREAKGLASS_SHORT"}, "at least 8 characters")
}

func TestCreatePolicyBeforeConnect(t *testing.T) {
	t.Setenv("MAL_PG_PASSWORD", "")
	t.Setenv("BREAKGLASS_SHORT", "short")
	wantErr(t, []string{"create", "--email", "root@example.com",
		"--password-env", "BREAKGLASS_SHORT"}, "at least 8 characters")
}

func TestConnectRequiresDBPassword(t *testing.T) {
	t.Setenv("MAL_PG_PASSWORD", "")
	wantErr(t, []string{"reset-totp", "--email", "root@example.com"}, "MAL_PG_PASSWORD")
}

// The generated one-time credential matches the installer's shape:
// 24 bytes as hex (48 characters), never repeated.
func TestOneTimePasswordShape(t *testing.T) {
	a, err := oneTimePassword()
	if err != nil {
		t.Fatal(err)
	}
	if len(a) != 48 {
		t.Fatalf("one-time password has %d characters, want 48", len(a))
	}
	for _, c := range a {
		if !strings.ContainsRune("0123456789abcdef", c) {
			t.Fatalf("one-time password %q is not hex", a)
		}
	}
	b, err := oneTimePassword()
	if err != nil {
		t.Fatal(err)
	}
	if a == b {
		t.Fatal("two generated one-time passwords are identical")
	}
}

// The password a file supplies is trimmed, never echoed anywhere.
func TestResolveOneTimePasswordFromFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pw.txt")
	if err := os.WriteFile(path, []byte("  file-password-1  \n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, generated, err := resolveOneTimePassword("", path)
	if err != nil {
		t.Fatal(err)
	}
	if generated {
		t.Fatal("file-supplied password reported as generated")
	}
	if got != "file-password-1" {
		t.Fatalf("file password = %q, want trimmed value", got)
	}
}
