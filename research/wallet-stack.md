# Research: Go Wallet Stack for BTC, ETH/ERC-20, TRC-20, LTC (Issue #6)

Resolves issue #6 of syaikhipin/cryptoexchange (Go + Nuxt 3 rewrite). All repo citations are file:line in the legacy Java codebase.

## 1. Go libraries per chain & maintenance status

| Chain | Library | Status (2026) |
|---|---|---|
| BTC | **btcd** (node + `rpcclient` pkg) + **btcwallet** (wallet daemon) | Actively maintained, canonical Go implementation; btcd ~6.7k stars, regular releases (https://github.com/btcsuite/btcd, https://github.com/btcsuite/btcwallet, releases: https://github.com/btcsuite/btcd/releases) |
| ETH / ERC-20 | **go-ethereum** (`ethclient` + `abigen` bindings; or `go-playground/ethereum`-style wrappers; `golang.org/x/crypto` secp256k1 via btcec) | Actively maintained by Ethereum Foundation (https://github.com/ethereum/go-ethereum). ERC-20 via generated contract bindings from ABI |
| LTC | **ltcd** + **ltcwallet** (ltcsuite fork of btcd) | Maintained fork, v0.23.6 published May 2025 on pkg.go.dev; project self-describes as "active development, Beta" (https://github.com/ltcsuite/ltcd, https://pkg.go.dev/github.com/ltcsuite/ltcd@v0.23.6) |
| TRC-20 (TRON) | **No production-grade maintained Go full-node implementation.** Options: (a) call java-tron's **HTTP / gRPC (full API)** from Go using the protobufs from tronprotocol/protocol (https://github.com/tronprotocol/protocol, actively maintained, 1,100+ commits); (b) thin community SDKs, e.g. `fbsobreira/gotron-sdk` (gRPC wrapper) and `mehdi124/tron-go-sdk` (https://github.com/mehdi124/tron-go-sdk) — all small-community, vet carefully; (c) run **java-tron** node and treat it as a black-box RPC service. **Recommendation: java-tron node + Go client over its gRPC API with generated protobuf stubs**; TRC-20 transfers are `TriggerSmartContract` calls to USDT contract `TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t` |

## 2. Node requirements per chain

| Chain | Own node? | Strategy | Rough pruned footprint |
|---|---|---|---|
| BTC | Yes for custody (you need wallet + `listunspent`-style control) | btcd (full archival ~700 GB; **pruned ~50–60 GB**, + btcwallet) or third-party RPC (QuickNode/GetBlock) for watching only. Note: pruned nodes cannot serve arbitrary old-block lookups; deposit-watcher design must track state itself | ~60 GB disk, 8–16 GB RAM |
| ETH | Yes or third-party | **Geth/Erigon pruned snap node ~150–250 GB** (Geth) / ~80 GB (Erigon w/ history expiry), 16 GB RAM. For a rewrite MVP, third-party RPC (Alchemy/QuickNode/Ankr) is acceptable for *watching*; withdrawals from an exchange hot wallet should still be signed locally in Go — you only need RPC for broadcast, not a node | 80–250 GB if self-hosted |
| TRON | Yes — java-tron (the only real full node) | Full node ~2+ TB solid-state (TRON state is heavy); **GreatVoyage/Mumbai-style: use third-party (TronGrid, QuickNode, getblock) gRPC endpoints** for MVP. java-tron self-host is a Phase-2 concern. java-tron itself is Java, 16–32 GB RAM | 2+ TB self-host; ~0 with TronGrid |
| LTC | Yes (cheap) | ltcd pruned: LTC is ~¼ BTC scale; pruned ~20–30 GB, 4–8 GB RAM | ~25 GB |

General: hosting your own nodes for BTC/LTC is cheap and removes third-party trust for deposits; ETH and especially TRON nodes are heavy — third-party RPC + local signing is the standard exchange pattern (deps on provider availability/rate limits remain a risk).

## 3. Custody patterns: what the Java adapters under `01_wallet_rpc/` actually did

Each chain runs a Spring Boot microservice exposing a REST `/rpc/*` controller, with a `Watcher` thread that replays blocks, matches deposit addresses against MongoDB (`AccountService.isAddressExist`), emits `DepositEvent` → Kafka, and records scan progress in `WatcherLog` (base class `01_wallet_rpc/ltc/src/main/java/com/bizzan/bc/wallet/component/Watcher.java:47-80`, run loop at :149+).

### Address generation (deposit addresses)
- **BTC/LTC/USDT-Omni**: delegate to core wallet daemon — `rpcClient.getNewAddress(account)` (`01_wallet_rpc/bitcoin/.../controller/WalletController.java:40-43`; ltc :42; usdt JsonrpcClient.java:51-56). Keys live in bitcoind/litecoind's wallet.dat.
- **ETH/ERC-20**: web3j `WalletUtils` creates a per-account **encrypted keystore file (UTC/V3)** under `coin.keystore-path`; file name stored on the Account record (`01_wallet_rpc/eth-support/.../EthService.java:52-55`, `01_wallet_rpc/eth/.../controller/WalletController.java:55-64`). ERC-20 module reuses the ETH address if the account already exists (`01_wallet_rpc/erc-token/.../controller/WalletController.java:77-93`).
- **No HD derivation**: addresses are individually generated, not derived from an xpub. In a Go rewrite, HD derivation (BIP-32/44/84 m/44'/0'/0' for BTC, m/44'/60' for ETH, m/44'/195' for TRON) from an xpub for deposit-address generation is the modern pattern — it enables offline cold key custody.

### Deposit watching
- **BTC**: polls `bitcoind` RPC per block; scans every tx output's `scriptPubKey.addresses`, records Deposit if address known (BitcoinWatcher.java:24-53).
- **LTC**: same logic but against a third-party block explorer API (`bizzan.blockApi`, HTTPS GET `block-index/<h>` and `txs/?block=`) rather than its own node — see Watcher.java:63-143. (Risk: explorer trust/rate limits; they even `sleep(20000)` to avoid throttling, Watcher.java:49,53.)
- **ETH**: web3j `ethGetBlockByNumber(n, fullTx)`; matches `tx.to` against known addresses, skips an `ignore-from-address` (their own sweep wallet) to avoid counting internal sweeps as deposits, and syncs per-address balances (EthWatcher.java:41-73).
- **USDT (Omni)**: `omni_listblocktransactions(height)` then `omni_gettransaction` filtering propertyid 31 (`JsonrpcClient.java:117-126`, UsdtWatcher.java:31-33).

### Confirmations
- Confirmation logic is uniform: watcher targets height `networkHeight − confirmation + 1` (Watcher.java:51), i.e. only replays blocks old enough to be considered final. Defaults per config: **LTC 3** confs (`01_wallet_rpc/ltc/.../application.properties:watcher.confirmation=3` — low vs industry norm 6), **ETH & ERC-20: 20** confs (`watcher.confirmation=20` in both application.properties), BTC not found in config → base-class default **3** (Watcher.java:34 `private int confirmation = 3`).

### Withdrawals & hot/cold structure
- **BTC**: `sendTransaction` via bitcoind RPC with explicit fee (`WalletController.java:66-73`, `BitcoinUtil.sendTransaction`). Keys unlocked inside bitcoind.
- **ETH**: dedicated **withdraw (hot) wallet**: a fixed keystore file + password in config; `transferFromWithdrawWallet` loads those credentials and signs locally with web3j, sync or async with `withdrawId` (`EthService.java:74-95`, WalletController.java:84-90). Gas price = network gas price × `gasSpeedUp` multiplier (EthService.java:97-102).
- **Cold sweep (consolidation)**: `CoinCollectJob` + `transferFromWallet` iterate accounts above `coin.min-collect-amount` (0.1 ETH in config) and sweep balances minus fee toward the hot/withdraw wallet (`EthService.java:110-137`). ERC-20 sweep additionally checks each user address has enough ETH for gas before `transferToken` (`01_wallet_rpc/erc-token/.../controller/WalletController.java:95-110`).
- **USDT-Omni**: `omni_send` from configured hot `withdraw-address` (`JsonrpcClient.java:144-154`, usdt WalletController.java:56-63); collect job sweeps `omni_getbalance` per address (usdt WalletController.java:84-93).
- **LTC**: same core-RPC pattern as BTC.

Notable weaknesses to fix in Go: plaintext passwords in application.properties (also plaintext Mongo/Kafka creds — out of scope here), `sendTransaction` on bitcoind with keys hot inside the daemon, no HD/xpub, LTC's 3-conf default, deposit matching only the *first* address of each output (`addresses.get(0)`, BitcoinWatcher.java:39), and no double-spend/reorg detection beyond simple confirmation offsets.

### Recommended custody pattern for the Go rewrite
1. **Deposit addresses**: HD-derived from a watch-only xpub (BIP-32/44/84/60/195); keys never touch the hot server.
2. **Hot wallet**: single HD account; keys encrypted (KMS/age/vault), decrypted only in the signer process; per-withdrawal signing with strict nonce/fee policies and an withdrawal-DB state machine (pending → signed → broadcast → confirmed).
3. **Cold sweep**: threshold-based sweep job (like CoinCollectJob) moving user-address balances to hot wallet; periodically hot → cold (offline) via manual multi-sig; cold keys offline forever.
4. **Per-withdrawal signing**: build+sign locally in Go (btcd txscript / go-ethereum `types.SignTx` / TRON protobuf tx signed with secp256k1), broadcast via node/RPC.

## 4. Deposit confirmation norms

| Chain | Legacy config | Industry norm |
|---|---|---|
| BTC | default 3 | 2–3 confs (small amounts), 6 for large |
| ETH | 20 | ~12–15 blocks to finality post-PoS (2 epochs ≈ 12.8 min); 12+ is safe finality |
| ERC-20 | 20 | same as ETH (finality is chain-level) |
| TRC-20 (TRON) | n/a (not in repo) | ~19–20 blocks for SR super-majority ("solidified") confirmation |
| LTC | 3 (config) | 6 confs standard (MTP-based); raise legacy default |

## 5. Recommendation table

| Chain | Go library | Node strategy | Confirmations |
|---|---|---|---|
| BTC | btcd + btcwallet (`github.com/btcsuite/btcd`, `btcwallet`) | Own pruned btcd+btcwallet (~60 GB); third-party RPC fallback for watching only | 2–3 |
| ETH | go-ethereum (`ethclient` + abigen ERC-20 binding) | Third-party RPC (Alchemy/QuickNode) for MVP; own Geth/Erigon pruned later (~80–250 GB) | 12+ (finality) |
| ERC-20 | go-ethereum + generated contract bindings | Same as ETH; sweep job must pre-fund gas (as legacy did) | 12+ |
| TRC-20 | java-tron gRPC + `tronprotocol/protocol` protobuf stubs (optionally gotron-sdk) | TronGrid/QuickNode gRPC for MVP; self-host java-tron (~2 TB) later | 19–20 (solidified) |
| LTC | ltcd + ltcwallet (`github.com/ltcsuite/ltcd`) | Own pruned ltcd (~25 GB) — cheap; **never** a third-party explorer like legacy Watcher did | 6 |

## Open risks
- **TRON has no first-class Go node**: dependence on java-tron's API surface (protobuf evolution) or third-party TronGrid rate limits/availability; community Go SDKs are unaudited.
- **Third-party RPC for ETH/TRON deposits** reintroduces trust and rate-limit risk; mitigations: dual-provider redundancy, own-node Phase 2.
- **Key management**: HD + encrypted keystore/KMS is new design work; legacy used plaintext passwords in config — must not be reproduced. No HSM/multi-sig covered here.
- **Reorg/double-spend handling**: confirmation offsets alone are weak; need reorg-aware watcher (re-scan N blocks on height regression) — legacy code had none.
- **Pruned nodes** cannot serve historical block queries older than the prune window; the deposit watcher must persist its own scan cursor (legacy `WatcherLog` concept carries over).
- **ltcd is a Beta-state fork** of btcd; LTC upgrade cadence lags Bitcoin; monitor releases (https://github.com/ltcsuite/ltcd).
- USDT-Omni (BTC layer) not in rewrite scope per ticket (TRC-20 USDT only) — legacy omni_ RPC code is reference only.

## Sources
- https://github.com/btcsuite/btcd · https://github.com/btcsuite/btcwallet · https://github.com/btcsuite/btcd/releases
- https://github.com/ethereum/go-ethereum
- https://github.com/ltcsuite/ltcd · https://pkg.go.dev/github.com/ltcsuite/ltcd@v0.23.6
- https://github.com/tronprotocol/protocol · https://github.com/mehdi124/tron-go-sdk
- Repo: paths cited inline (file:line) under `01_wallet_rpc/{bitcoin,eth,eth-support,erc-token,usdt,ltc}`.
