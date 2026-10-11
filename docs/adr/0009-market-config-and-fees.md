# ADR-0009: Per-Market maker/taker fees and a two-state Market config

Fees shape the fill event, the Ledger, and the admin surface at once, and the legacy shapes — a single blended rate netted silently out of credits, three overlapping state flags, minimums enforced per order kind — were inconsistent enough that porting them verbatim would lock in the weirdness. We decided: **per-Market `maker_fee_rate` + `taker_fee_rate`** (decimal strings, 4 dp, 0 ≤ rate < 1, seeded equal — default 0.001), charged **in the Coin the side receives** (buy: amount × rate in base; sell: turnover × rate in quote), **floored to the fee Coin's scale per fill** (a fee may floor to zero; dust stays with the member), at the **rate in force at fill time** (no placement snapshot), with **no exemptions** — legacy's hardcoded member IDs 1/10001 die with the robot scope. The wallet applies each fill as **gross credit plus a separate fee Journal entry** (reason `fee`, idempotency key = fill id + side + `fee`), and the fill event carries everything settlement needs — per side: order id, member id, maker/taker role, fee amount + fee Coin, gross credited — so the wallet never reads the exchange schema.

A Market carries exactly two state flags: `enabled` (orders accepted, engine active) and `visible` (listed on the front); halting = `enabled` off with **cancels still allowed**, legacy's `exchangeable` and the start/stop-trader RPC are cut (boot and recovery are engine-internal; recovery itself is the open question on [ticket 15](https://github.com/syaikhipin/malxchange/issues/15)). Order rules are unified: collars `min_price`/`max_price` (0/null = unbounded) checked against the limit price on both sides with market orders passing; `min_amount` and `max_amount` on every order; `min_turnover` wherever turnover is known at submission (limit at limit price, market-buy on budget); a single `market_orders_enabled` toggle replacing legacy's split pair. Kept from legacy: `max_trading_time` (order TTL auto-cancel), `max_open_orders` (per member + Market + direction), `sort`, `zone`, `featured`. Cut: `robot_type` (robots out of scope) and the QIANGGOU/FENTAN issuance-activity block. Admission **rejects** prices/amounts that are not whole multiples of tick size / lot size. Config edits propagate live to the engine, except tick size, lot size, and Coin scales, which are **frozen while any order on the Market is open** (the delete guard generalized).

## Considered options

- **Flat platform-wide fee rate**: rejected — a Market row exists anyway; per-Market costs nothing more and matches the reference.
- **Per-Member fee tiers**: rejected — reintroduces the member-level machinery ADR-0006 cut, with no tiers to populate in v1.
- **Legacy netted fee application** (`credit = proceeds − fee`): rejected — fee revenue vanishes into an annotated credit row and becomes unqueryable.
- **Legacy silent truncation to scale**: rejected — silently moving a member's limit price at submission is a footgun; rejection is explicit.
- **Legacy asymmetric collars and kind-exclusive minimums**: rejected — `min_turnover` only on market-buy and `min_volume` only on limit/market-sell had no rationale to preserve.

## Consequences

- Deliberate divergences from the Java reference: explicit fee Journal entry vs netted credit; rejection vs truncation; two state flags vs three; unified collars and minimums; one market-order toggle; per-fill fee flooring.
- The fill (`trade.*`) event is self-contained for settlement; wallet needs no exchange-schema joins (ADR-0002 idempotent inbox unchanged).
- The engine rejects non-conforming orders at submission and guards tick/lot/scale changes against resting orders — a new config-freeze guard alongside legacy's delete guard.
- The admin market-edit audit-trail shape is deferred to the admin-permission decision.
