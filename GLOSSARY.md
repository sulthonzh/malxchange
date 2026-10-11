# malxchange

The spot-exchange core being rewritten in Go + Nuxt 3 from the legacy Java reference. This glossary is the shared domain language; terms here are the only correct names for these concepts in code, issues, and UI.

## Language

**Coin**:
A tradeable asset with a fixed decimal **scale** (BTC 8, ETH 18, USDT 6) and withdrawal/deposit mechanics.
_Avoid_: currency, token, asset

**Market**:
A tradeable pair of a base Coin against a quote Coin (e.g. BTC/USDT), carrying its tick size and lot size.
_Avoid_: symbol, pair, trading pair

**Tick size**:
The price quantum of a Market; order prices must be whole multiples of it.
_Avoid_: price step, precision

**Lot size**:
The amount quantum of a Market; order amounts must be whole multiples of it.
_Avoid_: step size, amount precision

**Scale**:
The number of decimal places a Coin is exact to; used for display, validation, and string serialization.
_Avoid_: decimals, precision

**Turnover**:
The quote-side value of a fill: price × amount.
_Avoid_: volume, notional

**Fill**:
The execution of some amount of an Order against another Order; produces fee and turnover.
_Avoid_: trade (a Trade is the market-wide record of a Fill pair), execution

**Order**:
A member's instruction to buy or sell in a Market at a limit or market price.
_Avoid_: transaction, position

**Member**:
An authenticated account that owns wallets, orders, and KYC state.
_Avoid_: user, customer, account

**Ledger**:
The single authoritative record of every Member's per-Coin balance (available and frozen), owned and written exclusively by the wallet service. Every fund movement appends a Journal entry.
_Avoid_: member_wallet, wallet (the service), balance table, custody (chain-side funds)

**Journal**:
The append-only record of every Ledger movement (deposit, withdrawal, fill settlement, fee, reward, adjustment) with its reason and idempotency key.
_Avoid_: member_transaction, transaction history, statement
