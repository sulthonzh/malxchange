-- 0002_exchange: Market config, Orders, Fills (issue #21, ADR-0009/0010).

CREATE TABLE markets (
    symbol               TEXT   PRIMARY KEY,          -- "BTC/USDT"
    base_coin            TEXT   NOT NULL,
    quote_coin           TEXT   NOT NULL,
    base_scale           INT    NOT NULL,             -- dp of the base Coin
    quote_scale          INT    NOT NULL,             -- dp of the quote Coin
    tick_size            NUMERIC NOT NULL,            -- price quantum
    lot_size             NUMERIC NOT NULL,            -- amount quantum
    maker_fee_rate       NUMERIC NOT NULL DEFAULT 0.001,
    taker_fee_rate       NUMERIC NOT NULL DEFAULT 0.001,
    min_price            NUMERIC,                     -- null/0 = unbounded
    max_price            NUMERIC,
    min_amount           NUMERIC NOT NULL DEFAULT 0,
    max_amount           NUMERIC,
    min_turnover         NUMERIC NOT NULL DEFAULT 0,
    market_orders_enabled BOOLEAN NOT NULL DEFAULT TRUE,
    enabled              BOOLEAN NOT NULL DEFAULT TRUE, -- off = Halted (cancels still flow)
    visible              BOOLEAN NOT NULL DEFAULT TRUE, -- listed on the front
    sort                 INT    NOT NULL DEFAULT 0,
    zone                 TEXT   NOT NULL DEFAULT 'main',
    featured             BOOLEAN NOT NULL DEFAULT FALSE,
    max_open_orders      INT,                          -- per-Member cap (kept from legacy)
    ttl_seconds          INT,                          -- resting-order TTL; null = none
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE orders (
    id            TEXT   PRIMARY KEY,               -- engine order id (uuid)
    market        TEXT   NOT NULL REFERENCES markets(symbol),
    member_id     TEXT   NOT NULL,
    side          TEXT   NOT NULL CHECK (side IN ('buy','sell')),
    kind          TEXT   NOT NULL CHECK (kind IN ('limit','market')),
    price         NUMERIC,                          -- limit only
    amount        NUMERIC NOT NULL,                 -- base amount, or quote budget for market buy
    filled_amount NUMERIC NOT NULL DEFAULT 0,       -- base filled so far
    turnover      NUMERIC NOT NULL DEFAULT 0,       -- quote spent/received so far
    status        TEXT   NOT NULL CHECK (status IN ('open','filled','canceled')),
    seq           BIGINT NOT NULL,                  -- placement seq (price-time priority)
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    canceled_at   TIMESTAMPTZ,
    UNIQUE (market, seq)
);
CREATE INDEX orders_member_idx ON orders (member_id, market, status);
CREATE INDEX orders_sweep_idx ON orders (created_at) WHERE status = 'open';

CREATE TABLE fills (
    market        TEXT    NOT NULL,
    seq           BIGINT  NOT NULL,                 -- shared order/fill seq space
    buy_order_id  TEXT    NOT NULL,
    sell_order_id TEXT    NOT NULL,
    buy_member_id TEXT    NOT NULL,
    sell_member_id TEXT   NOT NULL,
    taker_side    TEXT    NOT NULL CHECK (taker_side IN ('buy','sell')),
    price         NUMERIC NOT NULL,
    amount        NUMERIC NOT NULL,
    turnover      NUMERIC NOT NULL,
    buy_turnover  NUMERIC NOT NULL,                 -- includes burned dust
    buy_fee       NUMERIC NOT NULL,                 -- in base Coin (ADR-0009)
    sell_fee      NUMERIC NOT NULL,                 -- in quote Coin
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (market, seq)
);

-- Installer/bootstrap seeds (spec #20/#21): BTC/USDT and ETH/USDT.
-- Idempotent: an operator edit always wins over re-seeding.
INSERT INTO markets (symbol, base_coin, quote_coin, base_scale, quote_scale,
                     tick_size, lot_size, min_turnover, sort, featured, ttl_seconds)
VALUES
  ('BTC/USDT', 'BTC', 'USDT', 8, 2, 0.10,  0.0001, 5, 1, TRUE,  NULL),
  ('ETH/USDT', 'ETH', 'USDT', 8, 2, 0.01,  0.001,  5, 2, FALSE, NULL)
ON CONFLICT (symbol) DO NOTHING;
