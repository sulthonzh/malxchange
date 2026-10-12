# Key ceremony runbook (issue #22, ADR-0008)

How the malxchange wallet keys come into existence. The ceremony runs
`walletctl` — a std-lib flag CLI with **no network I/O of any kind** — on
an offline machine, and produces exactly three artifacts:

1. paper records (mnemonics, indexes) that never exist digitally,
2. watch-only **xpubs** registered on the Coins,
3. one **hot keystore** file, encrypted under the env passphrase, that is
   the only key material the host ever sees.

Everything here is performed by two roles: the *keyholder* (offline,
holds the paper) and the *operator* (admin on the host).

## Threat model

The single-host topology (ADR-0007) has no KMS, so the design goal is
**host compromise ≠ fund loss**:

- **Deposit addresses are watch-only.** The host derives them from
  account-level xpubs; an attacker who reads the whole database learns
  balances and address mappings but can sign nothing.
- **The hot wallet is the only online key material**, and it lives in
  one file sealed with XChaCha20-Poly1305 under a passphrase derived
  with argon2id. Stealing it means nothing without the passphrase from
  `deploy/.env`; both together expose only the *hot* balance.
- **Cold keys never touch the host.** The bulk of funds sits at cold
  addresses whose seed exists only on paper, offline. The host knows
  the addresses, nothing more.
- **The ceremony machine is the trust root.** If it is compromised, the
  ceremony is compromised — hence the offline machine, the live OS, and
  the "never digital" rule for mnemonics below.

## Prerequisites

- An **offline machine**: air-gapped, ideally booted from a fresh live
  OS, with a terminal and no wireless hardware enabled. Verify: no
  route, no DNS, nothing.
- **Removable media** (USB stick) to carry `walletctl` in and the
  keystore out. Wipe it after each transfer.
- The binary, built on a trusted dev machine from this checkout:

      go build -o walletctl ./cmd/walletctl

  (`./walletctl` with no arguments prints the command list; every
  command documents its flags with `-h`.)
- Paper cards and a pen — one card per mnemonic, one for the cold
  indexes. No photos, no cloud scans, ever.
- On the host: `deploy/.env` with `MAL_HOT_KEYSTORE_PASSPHRASE` set
  (32-byte hex from step 3) and `MAL_WALLET_KEYSTORE` pointing at the
  keystore path (e.g. `deploy/secrets/hot.keystore.json`).

## Step 1 — Generate the four family mnemonics

One master seed **per chain family** (btc, ltc, evm, tron). Run four
times and copy each mnemonic to its own paper card by hand:

    $ ./walletctl gen-mnemonic              # 24 words (--bits 256, default)
    $ ./walletctl gen-mnemonic --bits 128   # 12 words, acceptable minimum

The v1 ceremony uses an **empty BIP-39 passphrase** everywhere (no 25th
word): `hot-keystore` and `verify` derive that way, so adding one in
`account --passphrase-file` would produce keys the keystore cannot hold.

Keep the cards in separate sealed envelopes. A card controls one chain
family's hot funds; the set of four is the whole hot wallet.

## Step 2 — Derive the accounts and register the xpubs

On the offline machine, type each mnemonic into a temporary file (lives
only on the offline disk; `shred -u` it at the end of the ceremony):

    $ ./walletctl account --family btc --net mainnet --mnemonic-file btc.txt
    {
      "family": "btc",
      "xpub": "xpub6Ci7zzge3...",
      "hotAddress": "bc1q..."
    }

The **xpub is what gets registered on the Coin**; the hot address goes
into the ops bookkeeping (it is the sweep destination and the
withdrawal change address). `--net` only changes btc/ltc address
encoding (bech32 HRP); evm/tron addresses are net-invariant.

Register per family — `btc` → BTC, `ltc` → LTC, `evm` → ETH **and every
ERC-20 Coin** (they share the account), `tron` → TRX/TRC-20:

- **Primary: the wallet service's internal admin RPC**
  `PUT /internal/coins/{name}` with the xpub field (spec #22; the
  internal listener is compose-internal only — Caddy never routes it).
- **Fallback that always works: SQL** against the wallet schema —
  this is exactly what `internal/wallet.SetCoinXPub` runs:

      UPDATE coins SET xpub = '<xpub>', updated_at = now() WHERE name = 'btc';

Cross-check what the host will derive, from the registered xpub alone:

    $ ./walletctl deposit-address --family btc --net mainnet \
          --xpub xpub6Ci7zzge3... --index 0

The address must equal the first deposit address the wallet service
hands a test member for that Coin (`m/0/0`).

## Step 3 — Build the hot keystore

Generate the keystore passphrase on the offline machine (32 random
bytes, hex) and put it in the environment — never on the command line,
never in a file that leaves the machine except into `deploy/.env`:

    $ export MAL_HOT_KEYSTORE_PASSPHRASE="$(openssl rand -hex 32)"
    $ echo "$MAL_HOT_KEYSTORE_PASSPHRASE"   # copy ONCE, into deploy/.env later

    $ ./walletctl hot-keystore \
          --mnemonic-btc btc.txt --mnemonic-ltc ltc.txt \
          --mnemonic-evm evm.txt --mnemonic-tron tron.txt \
          --passphrase-env MAL_HOT_KEYSTORE_PASSPHRASE \
          --out hot.keystore.json

Output is the registration record (the same xpubs/hot addresses as
step 2 — they must match) plus a `skipped` list. A family whose
mnemonic flag is omitted stays **watch-only**: nothing of it enters the
keystore, and the wallet simply cannot sign for it. The file is written
mode `0600`, argon2id + XChaCha20-Poly1305 sealed.

## Step 4 — Verify (the ceremony end-check, still offline)

Before anything leaves the machine, prove the keystore opens with the
passphrase and equals a fresh derivation from the paper mnemonics —
once per family:

    $ ./walletctl verify --keystore hot.keystore.json \
          --passphrase-env MAL_HOT_KEYSTORE_PASSPHRASE \
          --family btc --net mainnet --mnemonic-file btc.txt
    OK btc: keystore matches the offline mnemonic (hot address bc1q...)

Any non-zero exit here = **destroy the keystore and redo the ceremony**.
As a negative control, run once with a deliberately wrong passphrase
and confirm it fails. The mnemonics never travel to the host, so this
check happens only here.

## Step 5 — Move the keystore to the host

1. Copy `hot.keystore.json` to the host via the removable media, to the
   path `MAL_WALLET_KEYSTORE` names (e.g. `deploy/secrets/`).
2. `chmod 600` it and confirm ownership is the service user.
3. Put the step-3 passphrase into `deploy/.env` as
   `MAL_HOT_KEYSTORE_PASSPHRASE`.
4. Wipe the removable media copy.
5. Restart the wallet service so the signer loads the keystore.

## Step 6 — Cold addresses (the fifth mnemonic)

Cold storage is a **fifth mnemonic that exists only on paper**, never in
any keystore, and per-family cold addresses at a **random high index**
of its external branch (so the address is indistinguishable from any
other derivation and not adjacent to anything the host ever derives):

    $ ./walletctl gen-mnemonic     # copy to paper card #5 by hand
    $ ./walletctl account --family btc --net mainnet --mnemonic-file cold.txt
    $ ./walletctl deposit-address --family btc --net mainnet \
          --xpub <cold-xpub> --index 41037    # pick a random index ≥ 10,000

Record **family → index** on the cold card, then register the address as
the Coin's `cold_address` (admin RPC, or
`UPDATE coins SET cold_address = '<addr>' WHERE name = 'btc';`). Repeat
per family. `walletctl` has no key-export command by design: a cold
spend re-derives `m/<purpose>'/<coin>'/0'/0/<index>` offline with
standard HD tooling (path table below).

## Hot ↔ cold operations

- **hot → cold** is admin-triggered (admin UI or admin RPC): it creates
  a `hot_transfer_record`, signs exactly like a withdrawal to the
  Coin's cold address. Run it when the hot balance exceeds the
  per-Coin ceiling — the admin dashboard alerts on ceilings and floors.
- **cold → hot** is always an offline manual procedure: on the offline
  machine, build and sign the transaction with the cold key, carry only
  the **signed transaction** to any networked machine, broadcast, then
  record the txid via admin RPC `POST /internal/cold-transfers` so the
  wallet ledger reflects the refill. Keys never cross to a networked
  machine — signed bytes and a txid do.

## Derivation paths

| Family | Account (xprv in keystore / xpub on Coin) | Deposits (watch-only) | Hot wallet |
|---|---|---|---|
| btc   | `m/84'/0'/0'`   | `m/0/i` | `m/1/0` |
| ltc   | `m/84'/2'/0'`   | `m/0/i` | `m/1/0` |
| evm   | `m/44'/60'/0'`  | `m/0/i` (ETH + ERC-20 share the address) | `m/1/0` |
| tron  | `m/44'/195'/0'` | `m/0/i` (TRC-20 on its own path) | `m/1/0` |

Cold addresses live at `<account>/0/<random index>` of the fifth
mnemonic, e.g. btc cold = `m/84'/0'/0'/0/41037`.

## What must never touch the host

- The four family mnemonics (paper cards 1–4) — in any form.
- The fifth (cold) mnemonic and the family→index card.
- Any xprv outside the sealed keystore file.
- The keystore passphrase outside `deploy/.env` (never in a command
  line, log, ticket, message, or backup script).
- Digital images/transcripts of any paper record.
- The removable-media copy of the keystore after step 5 (wipe it).
- The temporary `*.txt` mnemonic files (they die on the offline disk —
  `shred -u` at ceremony end).

## Simnet rehearsal

Rehearse the entire ceremony against the compose test profile before
doing it for mainnet — same commands, `--net simnet` everywhere:

    # dev machine (simnet keys protect nothing; dev-machine generation is fine)
    $ go build -o walletctl ./cmd/walletctl
    $ ./walletctl gen-mnemonic --bits 128 > sim-btc.txt   # x4, one per family
    $ ./walletctl account --family btc --net simnet --mnemonic-file sim-btc.txt
    $ MAL_HOT_KEYSTORE_PASSPHRASE=sim-pass ./walletctl hot-keystore \
          --mnemonic-btc sim-btc.txt --mnemonic-ltc sim-ltc.txt \
          --mnemonic-evm sim-evm.txt --mnemonic-tron sim-tron.txt \
          --passphrase-env MAL_HOT_KEYSTORE_PASSPHRASE --out sim.keystore.json
    $ ./walletctl verify --keystore sim.keystore.json \
          --passphrase-env MAL_HOT_KEYSTORE_PASSPHRASE \
          --family btc --net simnet --mnemonic-file sim-btc.txt

Then bring the stack up with the test profile and register the simnet
xpubs on the simnet Coins the same way (admin RPC or SQL):

    $ make test-profile-up   # simnet btcd/ltcd + anvil EVM devnet

btc/ltc simnet addresses are `sb1...` / `sltc1...`; evm/tron encodings
are net-invariant. The unit tests in `cmd/walletctl/main_test.go` run
this exact round-trip (gen → account → hot-keystore → verify, wrong
passphrase and wrong mnemonic rejected) on every `make test`.
