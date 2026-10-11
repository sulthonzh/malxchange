# ADR-0005: Money representation — NUMERIC storage, decimal domain, engine ticks

Balances, orders, fees, and turnover must be exact: any silent precision loss either corrupts balances or makes wallet audits (sum of member wallets vs on-chain hot wallet) unreconcilable at chain scale. We decided on **`NUMERIC` columns in Postgres with `shopspring/decimal` in Go** end to end, decimal **strings** in every API, and **floor rounding** on every derived amount — with one deliberate exception: the matching engine converts each market's price/amount to `int64` ticks in memory for book arithmetic.

## Considered options

- **`int64` minor units globally**: rejected — `int64` caps at ~9.2×10¹⁸, and ETH is 10¹⁸ (wei), so wei-scale storage caps at ~9.2 ETH; clamping ETH to 10⁸ makes sub-satoshi on-chain amounts unrepresentable and breaks exact reconciliation.
- **Floats anywhere**: rejected outright; never.

## Consequences

- Every money column is `NUMERIC`; every Go money value is `decimal.Decimal`; every API (REST, WebSocket, Kafka) carries money as decimal strings; the Nuxt apps use a decimal library, never `parseFloat`.
- Engine ticks: each market declares **tick size** (price quantum) and **lot size** (amount quantum); orders are quantized to them or rejected; the in-memory book operates on `int64` tick counts, safe because they're bounded by tick size, not by wei.
- **Floor (truncate toward zero) on all derived amounts** — fees, turnover, fills. Rounding can never create money; dust stays with the user.
- Each `coin`/`market` row carries explicit **scale** (display/validation dp: BTC 8, ETH 18, USDT 6) and tick/lot config, checked against the chain's actual decimals at deposit-watching time — replacing the legacy's implicit, inconsistent precision (`decimal(26,16)`/`(18,8)`/`(19,2)`).
