# Review checklist

Rules the code reviewer enforces on every diff. Each earned its place by
a concrete bug from the wallet slice (#22, commit 7fc8b60) — the linked
symptom is what recurrence looks like.

## Test fixtures fund both sides

Every fill a test produces must have a real hold behind both orders:
credit each member's account, then freeze the sold amount, before
producing the `trade.fill`. A fill against an unfunded or unheld order
does not fail loudly — it silently distorts every downstream balance
assertion and surfaces as an unrelated CHECK violation.
(Symptom: `ledger_accounts_frozen_check` on a fill whose seller never
froze.)

## Error assertions use errors.Is

Assert error identity with `errors.Is(err, ErrX)`. A hand-rolled
unwrap-and-compare helper drifts from the stdlib and gets the order of
string-compare vs unwrap wrong. (Symptom: a local `is()` helper that
compared `err.Error()` before unwrapping, missing wrapped sentinels.)

## Checked balance columns move via open-at-zero + UPDATE

For a table whose columns carry `CHECK (x >= 0)`, apply a movement by
inserting the row at zero (`ON CONFLICT (key) DO NOTHING`) and then a
plain `UPDATE ... SET x = x + delta`. `INSERT ... ON CONFLICT DO UPDATE`
evaluates CHECK constraints against the **proposed** row before conflict
resolution, so a negative delta fails even when the post-move balance is
valid. (Symptom: every freeze from a funded account failed
`available >= 0`.)

## Domain vocabulary follows GLOSSARY.md

New identifiers, route paths, and comments use `GLOSSARY.md` terms;
check them against the Avoid list of the relevant entry before
approving. The lists are entry-relative — "account" is banned as a
Member synonym yet required for Ledger accounts, "token" is banned for
Coin yet correct for ERC-20 plumbing — so judge term-by-term; a global
grep cannot do it. (Symptom: shipped `GET /internal/statements/{member}`;
GLOSSARY bans "statement" for Journal.)

## Read keyed collections by key, not by position

A slice built by iterating a Go map has no stable order, so `s[0]` is a
coin flip that re-flips on every call: tests flake in both directions
and published endpoints shuffle their output. Sort the slice before
publishing it, and have callers select the entry they mean by its key
(see `statusFor` in the exchange tests). `make lint` greps for known
positional reads; a new keyed collection gets the same discipline.
(Symptom: `open orders after recovery = 2, want 0` in one run and
`= 0, want 2` in the next — `Status()[0]` resolved a random Market
(#26).)

## Determinism guarantees ship pinned

A change that promises determinism — stable ordering, reproducible
folds, derived-once values — lands with the test that fails when the
promise regresses, in the same commit. Repo precedent:
TestDeterministicFold, TestRestoreRebuildsIdenticalBook,
TestDepositIndexDeterminism.
(Symptom: Engine.Status() ordered snapshots by map iteration until #26
added both the sort and TestStatusStableMarketOrder.)

## IT stacks mint per-binary throwaway DB names

An integration-test stack that DROPs/CREATEs a fixed database name
serializes every test binary on the machine against a shared dev
Postgres: two concurrent runs — two agent worktrees, or a local run
beside CI — invalidate each other's connections, and the failure looks
like a code bug (dropped relations mid-run, 3D000/57P01). Mint a
`malxchange_it_<suffix>` name per test binary and FORCE-drop it in
`t.Cleanup`, the way the market harness does.
(Symptom: every "exchange IT flake" of the #23 session — a different
test trio per attempt, all collision artifacts; a pristine baseline
failed the same way under sibling load.)

## ITs assert through the served surface

An integration test that SELECTs from a derived table pins the
projection's internal shape: a schema-improving refactor then fails
tests members never depended on, while wire-contract regressions slip
past unasserted. Assert through the endpoint the api proxy would call;
keep a direct table read only where no surface exists (rebuild
tripwires like `trades == fills`), with a one-line comment saying so.
(Symptom: five of six market-slice tickets shipped table-reading ITs
against the spec's "observable through endpoints" testing decision;
caught only at slice-end review.)

## Route-404 assertions wait out boot time

A test that boots a service's production `Run` behind the harness mux
fails on an unmounted-route 404 only after the service has had boot
time: `Run` registers its RPCs after recovery and connection dials, so
the first poll of a cold run can see the mux still empty and fail on
correctly wired code. Gate the 404 on a grace window (or on a route
`Run` registers earlier) before treating it as the symptom — the red
tree must still fail fast, just not faster than boot.
(Symptom: the green tree failing `GET /internal/orders/current = 404 —
Run did not mount the member-order RPC` on a cold first poll — ticket
#65's first draft, caught in review before it flaked CI.)

## Fatal exit paths log their cause before returning

A worker whose error propagates to boot-shutdown must log it first:
the harness's deferred closes (producer, pool) can block indefinitely,
so the `service exited` line the harness promises may never print —
the process dies with no trace. Pair with the readiness rule: compose
healthchecks probe `/readyz`, never `/healthz` (liveness answers ok
unconditionally; `make lint` enforces the self-healthcheck side).
(Symptom: the #51 zombie — a stale `trade.fill` failed settlement,
the unlogged fatal error deadlocked `pool.Close` on the outbox LISTEN
conn, and the wallet served `closed pool` 500s for hours while Docker
reported it healthy.)

## A new app ships its CI job in the same ticket

Every deployable app in the repo (Go service, Nuxt app, future admin)
gets its lint/test/build wired into `.github/workflows/ci.yml` by the
ticket that adds it — not by a later review pass. An unwired app is
untested on every push until someone notices.
(Symptom: the web app's vitest/build job landed only in the #51
review-fix commit, after the ticket's own acceptance had already said
"CI wiring matches existing repo patterns".)

## Deliberate rule-breaks carry a greppable waiver marker

A comment that documents an intentional exception (a coercion an ADR
bans, a banned synonym on a pinned legacy path) opens with the marker
`ADR-NNNN waiver —` so reviewers and future lint greps find it by
pattern. Reviewers grep the marker before flagging the exception site;
an unmarked waiver reads as a violation and burns a review cycle.
(Symptom: the #24 review flagged `Number()` in KlineChart although a
waiver comment sat three lines below it.)

## Client paths are suffix paths

`apps/web`'s `useApi` and the admin e2e's `MEMBER_API` carry the `/api`
prefix in the base URL, so the path argument is the suffix (`/uc/kyc`,
`/announcements`) — a literal `/api/...` path doubles the prefix and
404s on `/api/api/...`, which no unit test sees because only the browser
resolves the URL. Enforced by `make lint`.
(Symptom: spec #25's e2e found every member content page rendering its
empty state, and the admin e2e's member leg 404ing.)

## Nuxt param pages opt into route-params-as-props

A page that reads route params via `defineProps<{ id }>` must also
declare `definePageMeta({ props: true })`; without it the prop binds
`undefined` and the page fetches `/undefined`. Page-mount unit tests
pass props directly, so only the filesystem check
(`apps/web/tests/param-pages-props.spec.ts`) or a real browser sees the
failure. Enforced by that spec.
(Symptom: announcement and help detail pages fetched
`/api/announcements/undefined`.)

## Compose overlays must parse

`docker compose ... config -q` over both overlays runs in `make lint`:
a duplicate env key or malformed service makes compose refuse the whole
file, which otherwise only surfaces when an e2e or installer target
tries to boot the stack.
(Symptom: a duplicated `MAL_ADMIN_WALLET_BASE_URL` from a union merge
blocked `make admin-e2e` entirely.)
