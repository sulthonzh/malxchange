# ADR-0008: Deposit confirmations and hot/cold custody

The legacy wallet adapters kept signing keys hot inside chain daemons, stored keystore passwords in plaintext config, had no HD derivation, watched LTC through a third-party block explorer, and were blind to reorgs (`research/wallet-stack.md`) — none of this may carry over, and the single-host topology (ADR-0007) has no KMS. We fix the custody shape for the wallet service: **deposit addresses are watch-only (xpub-derived), the hot wallet is the only online key material, and cold keys never touch the host.**

## Decision

- **Confirmations** (per-Coin config, flat — no amount tiers): BTC 3, LTC 6 (fixing legacy's 3), ETH/ERC-20 12 (post-PoS finality), TRC-20 20 (solidified blocks). Deposits are Member-visible as detected → credited with a live confirmation counter; the Ledger credits only at N.
- **Reorgs**: the watcher persists its own scan cursor (pruned nodes cannot re-serve history) and re-scans on height regression. A reorg deeper than a Coin's confirmation window that invalidates an already-credited deposit is an admin incident handled by manual adjudication — never an automatic Ledger clawback, since the Member may already have traded the funds.
- **Keys**: an offline ceremony generates one master seed per chain; only watch-only xpubs reach the host. Deposit addresses are HD-derived, one per Member per Coin, allocated lazily on first deposit-address request with a persisted index; ERC-20 reuses the ETH address, TRC-20 gets its own path, BTC uses native SegWit (BIP-84). The hot wallet is one HD account per chain, stored as a keystore encrypted with XChaCha20-Poly1305 whose passphrase comes from the env file and is decrypted only in the signer's memory. Cold keys stay offline; the host knows cold addresses only.
- **Sweeps (deposit addresses → hot)**: automated and threshold-based per Coin (configurable `min-sweep-amount` + minimum interval, pausable per Coin by an admin), fees absorbed by the exchange. ERC-20 sweeps pre-fund gas on the token address from the hot wallet; TRON fees burn TRX from a hot-wallet TRX buffer for v1 — staking for energy is deferred until volume justifies the machinery.
- **Hot ↔ cold**: hot→cold is admin-triggered from the admin UI (creates a `hot_transfer_record`, signs like a withdrawal to the configured cold address, suggested when hot balance exceeds a per-Coin ceiling); cold→hot stays an offline manual procedure whose txid the admin records. The admin dashboard alerts on hot-balance ceilings (too high) and floors (too low to cover withdrawals).
- **Failures**: withdrawals and sweeps run explicit state machines (`pending → signed → broadcast → confirmed`, plus `failed` with error detail). Failures surface on an admin Wallet Ops page with retry and per-Coin pause; no email/paging in v1.

## Considered options (rejected)

- **Legacy key practice** (keys in bitcoind `wallet.dat`, plaintext keystore passwords): rejected — the standing constraint this decision carries.
- **Per-amount confirmation tiers** (e.g. 2 confs small / 6 large): rejected for v1 — flat per-Coin counts are simpler and tunable later without schema change.
- **Automatic clawback on deep reorg**: rejected — it can negative-balance a Member who already traded the deposit; the loss is bounded by ops response time instead.
- **Fresh address per deposit**: rejected — one address per Member per Coin keeps reconciliation as simple as legacy's.
- **Automated hot→cold**: rejected — v1 keeps a human in the loop for every movement to cold.
- **Staked TRON energy in v1**: rejected — burn-TRX is simpler to operate; revisit when TRC-20 volume grows.
- **KMS/Vault container**: rejected — outside ADR-0007's container list; env-passphrase encryption is the v1 ceiling.

## Consequences

- A key-ceremony runbook becomes install documentation (seed generation, xpub export, hot-keystore creation, cold-address registration); the offline tooling (`walletctl`-style CLI) is spec work, not a further decision.
- Member withdrawal fee semantics port from legacy: a per-Coin configured fee deducted from the requested amount.
- Deep-reorg losses after credit are possible but bounded by ops response; the Wallet Ops page is the detection surface.
- TRX burn cost per TRC-20 transfer is materially higher than staked energy until volume grows.
