# Break-glass runbook (issue #55, ADR-0012)

How to restore access when the **sole super-admin is locked out** of the
admin surface: the `breakglass` CLI runs on the host, writes the admin
schema in Postgres directly, and gets nobody through a single HTTP path
of admin-api — there is no session, no TOTP, no Step-up, because by
definition no Employee can sign in. This file is the procedure; the
known recovery cost of ADR-0012 is *host access + this runbook*, not
improvisation.

Every action the CLI takes lands in `audit_log` attributed to the
system actor `system:breakglass`, in the same transaction as the change
itself: a break-glass action without its record cannot commit. Treat
any `system:breakglass` row you did not expect as a security event.

## When it applies

The operator-error context that leads here (ADR-0012): the Employee
management guards make it hard to cause from inside the UI, but the
*sole* super-admin can still end up unable to sign in — and with no
second super-admin there is no one left who could fix it from the
admin surface. Concretely:

| Situation | Command |
|---|---|
| Password lost or forgotten | `reset-password` |
| TOTP device/secret lost, or binding stuck unconfirmed | `reset-totp` |
| Account disabled (and no other super-admin can re-enable it) | `enable` |
| The existing super-admin row is not the answer (person gone for good) | `create` |

It does **not** apply when:

- **Another super-admin can sign in.** Use the admin surface's Employee
  management (issue #54) instead — it is Step-up-gated and audited as
  the acting Employee. Break-glass is the exception path, not a
  routine one (ADR-0012). `breakglass list` shows who exists.
- **It is only the 15-minute login lockout** (5 failed attempts). The
  lock self-expires; wait it out.
- **A member (exchange user) is locked out.** Different surface,
  different recovery; this tool touches Employees only.
- **The target is not a super-admin.** The CLI refuses non-super-admin
  targets on purpose — break-glass recovers the super-admin, it does
  not route around Employee management.

## What each command does to the account

- `reset-password` sets a fresh password (one-time credential), **arms
  forced rotation** (`must_rotate`): the next sign-in lands in the
  restricted state and demands a new password — plus TOTP enrollment
  if the account has no binding. It **revokes every live session** and
  clears the email's failed-login counters, so old strikes cannot lock
  the new credential.
- `reset-totp` clears the TOTP binding (confirmed *and* staged). The
  account becomes unprovisioned: next sign-in is password-only and
  lands in the restricted state until a new authenticator is enrolled
  and confirmed. Live sessions are revoked.
- `enable` flips `enabled` back on. Nothing else — a disabled Employee
  holds no usable sessions.
- `create` adds a new super-admin with a one-time password (forced
  rotation armed, same first-login contract as the installer's
  bootstrap Employee). Unlike the installer's one-shot RPC it works
  even when Employees already exist.

In all cases the password the CLI prints is **shown once** and single-
use; nothing is written to `deploy/.env`.

## Prerequisites

- Root (or equivalent) on the host running the stack — host access is
  the trust root of ADR-0007's single-host topology.
- The repo checkout on that host (to build the tool) and `deploy/.env`
  (for the admin DB password).
- Postgres reachable from where the tool runs. On the dev stack
  (`make dev-up`) that is `127.0.0.1:5432`; in production the database
  is not published, so run the tool *inside* the compose network
  (below) — no ports are opened, nothing changes in the stack.

## Running it

### Dev / host-reachable Postgres

    set -a; . deploy/.env; set +a
    MAL_PG_HOST=127.0.0.1 MAL_PG_USER=admin MAL_PG_PASSWORD=$MAL_PG_PASSWORD_ADMIN \
      go run ./cmd/breakglass list

(Or `go build -o breakglass ./cmd/breakglass` once and run `./breakglass`.)

### Production — inside the compose network

Postgres is not published in production, so mount the built binary into
a one-off container on the `admin` service's network and env (the
service env already carries `MAL_PG_HOST=postgres`, the database name,
and `MAL_PG_PASSWORD` from `MAL_PG_PASSWORD_ADMIN`):

    go build -o /tmp/breakglass ./cmd/breakglass
    cd deploy && docker compose -f docker-compose.yaml run --rm -T \
      --entrypoint /breakglass -v /tmp/breakglass:/breakglass:ro \
      admin reset-password --email admin@example.com

## The procedure

1. **Look before you touch.**

       breakglass list

   Confirm there really is no other *enabled, provisioned* super-admin
   who could fix this from the admin surface — if there is, stop here.

2. **Fix the actual blocker.** Password and authenticator both lost?
   Run both resets — order does not matter:

       breakglass reset-password --email admin@example.com
       breakglass reset-totp   --email admin@example.com

   Disabled on top of it? Add:

       breakglass enable --email admin@example.com

   No usable row at all:

       breakglass create --email admin@example.com

   The tool prints a one-time password (48 hex characters) exactly
   once. Hand it to the Employee over a channel you consider
   acceptable for a single-use credential. Prefer the generated one;
   `--password-env NAME` (the variable's *name*, never the value) or
   `--password-file PATH` exist for sites with a credential-handover
   convention — operator-chosen passwords must satisfy the standard
   login policy (8+ characters).

3. **The Employee signs in.** First login with the one-time password
   lands in the forced-setup state: change the password, (re-)enroll
   the authenticator, confirm a code. Until then the session reaches
   nothing else — that is by design (ADR-0012).

4. **Verify the recovery left a trail.**

       breakglass list

   shows `MUST-ROTATE no` and `TOTP-BOUND yes` for the recovered
   account, and the admin surface's audit log shows the
   `system:breakglass` rows (`reset:employee-password`,
   `reset:employee-totp`, `enable:employee`, `create:employee`) with
   before/after state.

## Operational notes

- **No secrets in the audit log.** The audit rows carry the same
  redacted Employee view everywhere else in admin-api: no password
  hashes, no TOTP secrets — before/after JSON records the *flip*
  (`totp_bound`), not the material.
- **The audit row is written in the same transaction as the change.**
  Unlike the HTTP middleware (which writes after the response), the
  CLI cannot commit a mutation without its record.
- **Connection knobs** (flags with `MAL_PG_*` env fallbacks):
  `--host`, `--port`, `--database`, `--user`, `--schema`. The DB
  password comes from the `MAL_PG_PASSWORD` environment variable only —
  never a flag, so it never appears in `ps` output or shell history.
- **Every use is visible.** The audit log (readable by super-admin/
  finance/compliance, ADR-0012) is the tripwire: an unexpected
  `system:breakglass` row means someone with host access touched the
  Employee table — investigate.
