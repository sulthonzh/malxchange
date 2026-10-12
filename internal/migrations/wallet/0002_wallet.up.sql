-- 0002_wallet: the Ledger and custody domain (issue #22, ADR-0008).
--
-- The wallet service is the sole Ledger writer. Every balance movement
-- appends a Journal row in the SAME transaction as the ledger_accounts
-- update, keyed by an idempotency key, so the Ledger is always the fold
-- of the Journal. Money columns are NUMERIC (ADR-0005).

-- Coin configuration is wallet-owned (admin edits arrive through
-- admin-api's internal RPC).
CREATE TABLE coins (
    name              TEXT PRIMARY KEY,          -- "BTC", "ETH", "USDT"
    family            TEXT NOT NULL CHECK (family IN ('btc','ltc','evm','tron')),
    native            BOOLEAN NOT NULL DEFAULT TRUE,  -- false = token (ERC-20/TRC-20)
    contract          TEXT,                      -- token contract address (native = NULL)
    chain_decimals    INT,                       -- actual on-chain dp; checked at watch time
    scale             INT  NOT NULL,             -- display/validation dp
    confirmations     INT  NOT NULL CHECK (confirmations > 0),
    withdraw_fee      NUMERIC NOT NULL DEFAULT 0 CHECK (withdraw_fee >= 0),
    min_withdraw      NUMERIC NOT NULL DEFAULT 0 CHECK (min_withdraw >= 0),
    withdrawable      BOOLEAN NOT NULL DEFAULT TRUE,
    sweep_threshold   NUMERIC NOT NULL DEFAULT 0,
    sweep_min_seconds INT    NOT NULL DEFAULT 3600,
    sweep_paused      BOOLEAN NOT NULL DEFAULT FALSE,
    xpub              TEXT,                      -- watch-only account key (deposit derivation)
    cold_address      TEXT,                      -- offline custody target
    hot_ceiling       NUMERIC,                   -- alert: too much value online
    hot_floor         NUMERIC,                   -- alert: withdrawal liquidity at risk
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Per-Member per-Coin balance. available + frozen are always >= 0.
CREATE TABLE ledger_accounts (
    member_id  TEXT    NOT NULL,
    coin       TEXT    NOT NULL REFERENCES coins(name),
    available  NUMERIC NOT NULL DEFAULT 0 CHECK (available >= 0),
    frozen     NUMERIC NOT NULL DEFAULT 0 CHECK (frozen >= 0),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (member_id, coin)
);

-- Append-only Journal: the source of truth every balance folds from.
-- avail_delta/frozen_delta carry the signed movement; amount is the
-- display magnitude; idem_key makes every producer idempotent.
CREATE TABLE journal (
    id          UUID    PRIMARY KEY,
    member_id   TEXT    NOT NULL,
    coin        TEXT    NOT NULL,
    kind        TEXT    NOT NULL CHECK (kind IN (
                    'deposit','withdrawal','withdrawal_refund',
                    'fill','fee','freeze','fill_debit','release',
                    'adjustment')),
    avail_delta  NUMERIC NOT NULL DEFAULT 0,
    frozen_delta NUMERIC NOT NULL DEFAULT 0,
    amount       NUMERIC NOT NULL CHECK (amount >= 0),
    reason       TEXT    NOT NULL,
    idem_key     TEXT    NOT NULL UNIQUE,
    ref_id       TEXT,               -- deposit/withdrawal/order/fill reference
    actor        TEXT,               -- Employee for admin-caused movements
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX journal_member_idx ON journal (member_id, coin, created_at);
CREATE INDEX journal_ref_idx ON journal (ref_id);

-- Funds frozen for a resting Order: frozen at placement (synchronous
-- RPC from api), settled per fill, remainder released on completion.
CREATE TABLE order_holds (
    order_id    TEXT PRIMARY KEY,
    member_id   TEXT    NOT NULL,
    market      TEXT    NOT NULL,
    side        TEXT    NOT NULL CHECK (side IN ('buy','sell')),
    coin        TEXT    NOT NULL,
    amount      NUMERIC NOT NULL CHECK (amount > 0),  -- frozen at place
    settled     NUMERIC NOT NULL DEFAULT 0 CHECK (settled >= 0),
    status      TEXT    NOT NULL DEFAULT 'open' CHECK (status IN ('open','released')),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    released_at TIMESTAMPTZ
);

-- Deposit address allocation: one address per Member per Coin,
-- HD-derived lazily from the Coin's watch-only xpub.
CREATE TABLE deposit_addresses (
    coin         TEXT    NOT NULL,
    member_id    TEXT    NOT NULL,
    address      TEXT    NOT NULL,
    path         TEXT    NOT NULL,          -- "m/0/17"
    idx          INT     NOT NULL,
    allocated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (coin, member_id),
    UNIQUE (coin, idx),
    UNIQUE (address)
);

-- Deposits with live confirmation tracking. Credit happens exactly once
-- at the Coin's confirmation count; (coin, txid, vout) is the chain-side
-- identity (vout = output index on UTXO chains, log index on EVM/TRON).
CREATE TABLE deposits (
    id               UUID PRIMARY KEY,
    coin             TEXT    NOT NULL,
    member_id        TEXT    NOT NULL,
    address          TEXT    NOT NULL,
    txid             TEXT    NOT NULL,
    vout             INT     NOT NULL DEFAULT 0,
    amount           NUMERIC NOT NULL CHECK (amount > 0),
    detected_height  BIGINT  NOT NULL,
    confirmations    INT     NOT NULL DEFAULT 0,
    status           TEXT    NOT NULL DEFAULT 'detected'
                     CHECK (status IN ('detected','credited','invalidated')),
    credited_at      TIMESTAMPTZ,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (coin, txid, vout)
);
CREATE INDEX deposits_member_idx ON deposits (member_id, coin, created_at);
CREATE INDEX deposits_pending_idx ON deposits (updated_at) WHERE status = 'detected';

-- Withdrawals: explicit state machine (ADR-0008). audit is the only
-- state an Employee can approve from; failed carries error detail and
-- admits retry or refund.
CREATE TABLE withdrawals (
    id          UUID PRIMARY KEY,
    coin        TEXT    NOT NULL,
    member_id   TEXT    NOT NULL,
    address     TEXT    NOT NULL,
    amount      NUMERIC NOT NULL CHECK (amount > 0), -- requested, debited
    fee         NUMERIC NOT NULL DEFAULT 0,          -- deducted from amount
    net         NUMERIC NOT NULL CHECK (net > 0),    -- broadcast on chain
    state       TEXT    NOT NULL DEFAULT 'audit'
                CHECK (state IN ('audit','approved','signed','broadcast',
                                 'confirmed','rejected','failed','refunded')),
    txid        TEXT,
    error       TEXT,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    confirmed_at TIMESTAMPTZ
);
CREATE INDEX withdrawals_state_idx ON withdrawals (state, created_at);
CREATE INDEX withdrawals_member_idx ON withdrawals (member_id, created_at);

-- Sweeps consolidate deposit-address funds into the hot wallet.
-- ERC-20/TRC-20 sweeps pre-fund chain fees from the hot wallet first
-- (funding_txid); fees are absorbed by the exchange.
CREATE TABLE sweeps (
    id            UUID PRIMARY KEY,
    coin          TEXT    NOT NULL,
    address       TEXT    NOT NULL,       -- deposit address being swept
    amount        NUMERIC NOT NULL CHECK (amount > 0),
    fee           NUMERIC NOT NULL DEFAULT 0,
    funding_txid  TEXT,
    txid          TEXT,
    state         TEXT    NOT NULL DEFAULT 'pending'
                  CHECK (state IN ('pending','funding','signed','broadcast',
                                   'confirmed','failed')),
    error         TEXT,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    confirmed_at  TIMESTAMPTZ
);
CREATE INDEX sweeps_state_idx ON sweeps (state, created_at);

-- Hot→cold transfers are admin-triggered; same signing path as a
-- withdrawal, recorded for the custody trail.
CREATE TABLE hot_transfers (
    id         UUID PRIMARY KEY,
    coin       TEXT    NOT NULL,
    amount     NUMERIC NOT NULL CHECK (amount > 0),
    address    TEXT    NOT NULL,          -- configured cold address
    state      TEXT    NOT NULL DEFAULT 'pending'
               CHECK (state IN ('pending','signed','broadcast','confirmed','failed')),
    txid       TEXT,
    error      TEXT,
    actor      TEXT,                      -- Employee who triggered it
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    confirmed_at TIMESTAMPTZ
);

-- Cold→hot movements happen offline; the Employee records the txid.
CREATE TABLE cold_transfers (
    id       UUID PRIMARY KEY,
    coin     TEXT    NOT NULL,
    amount   NUMERIC NOT NULL CHECK (amount > 0),
    txid     TEXT    NOT NULL,
    actor    TEXT,
    noted_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- The watcher owns its scan cursor (pruned nodes cannot re-serve
-- history); it rewinds on height regression (reorg).
CREATE TABLE watch_cursors (
    coin       TEXT PRIMARY KEY,
    height     BIGINT NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Wallet-side UTXO index for btc/ltc: the daemons carry no wallet
-- (ADR-0008: no keys in daemon wallets), so spendable outputs are
-- tracked here and spent by locally-signed transactions.
CREATE TABLE utxos (
    coin    TEXT    NOT NULL,
    txid    TEXT    NOT NULL,
    vout    INT     NOT NULL,
    address TEXT    NOT NULL,
    amount  NUMERIC NOT NULL CHECK (amount > 0),
    height  BIGINT  NOT NULL,
    spent   BOOLEAN NOT NULL DEFAULT FALSE,
    spent_by TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (coin, txid, vout)
);
CREATE INDEX utxos_spend_idx ON utxos (coin, address) WHERE NOT spent;

-- Admin incidents: deep reorgs invalidating credited deposits, Coin
-- scale drift, chain errors. Manual adjudication by design — never an
-- automatic Ledger clawback.
CREATE TABLE incidents (
    id         UUID PRIMARY KEY,
    coin       TEXT NOT NULL,
    kind       TEXT NOT NULL CHECK (kind IN
               ('reorg_invalidated_deposit','scale_mismatch','chain_error')),
    detail     TEXT NOT NULL,
    status     TEXT NOT NULL DEFAULT 'open' CHECK (status IN ('open','resolved')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    resolved_at TIMESTAMPTZ
);
CREATE INDEX incidents_open_idx ON incidents (created_at) WHERE status = 'open';

-- Ceremony seed data (operator replaces/extends via admin RPC).
-- Confirmation counts per ADR-0008: BTC 3, LTC 6, EVM 12, TRON 20.
INSERT INTO coins (name, family, native, contract, chain_decimals, scale,
                    confirmations, withdraw_fee, min_withdraw,
                    sweep_threshold, sweep_min_seconds, hot_ceiling, hot_floor)
VALUES
  ('BTC',  'btc',  TRUE,  NULL, 8, 8,  3, 0.0004, 0.001,  0.01,  3600, 10, 1),
  ('LTC',  'ltc',  TRUE,  NULL, 8, 8,  6, 0.001,  0.01,   0.5,   3600, 100, 10),
  ('ETH',  'evm',  TRUE,  NULL, 18, 18, 12, 0.004, 0.01,   0.5,   3600, 50, 5),
  ('TRX',  'tron', TRUE,  NULL, 6, 6,  20, 5,     50,     0,     3600, NULL, NULL),
  ('USDT', 'evm',  FALSE, '0xdAC17F958D2ee523a2206206994597C13D831ec7', 6, 6, 12, 2, 10, 20, 3600, NULL, NULL),
  ('USDT-TRC', 'tron', FALSE, 'TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t', 6, 6, 20, 2, 10, 20, 3600, NULL, NULL)
ON CONFLICT (name) DO NOTHING;
