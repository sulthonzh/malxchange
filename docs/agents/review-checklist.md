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
