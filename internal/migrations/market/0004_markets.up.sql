-- 0004_markets: market's own copy of the Market config (#33), converged by
-- consuming market.updated (ticket #29). The event is the lawful source
-- (self-contained full row), so no cross-schema read of exchange's markets
-- table ever happens (schemas are isolated by design). Like everything in
-- this schema the copy is rebuildable: replaying market.updated regenerates
-- it. One row per Market (keyed on the Market itself); a Market flipped
-- invisible keeps its row — it only leaves the visible listing.
CREATE TABLE markets (
    market                TEXT        PRIMARY KEY,
    base_coin             TEXT        NOT NULL,
    quote_coin            TEXT        NOT NULL,
    base_scale            INT         NOT NULL,
    quote_scale           INT         NOT NULL,
    tick_size             NUMERIC     NOT NULL,
    lot_size              NUMERIC     NOT NULL,
    maker_fee_rate        NUMERIC     NOT NULL,
    taker_fee_rate        NUMERIC     NOT NULL,
    min_price             NUMERIC,                 -- NULL = unbounded
    max_price             NUMERIC,                 -- NULL = unbounded
    min_amount            NUMERIC,                 -- NULL = unbounded
    max_amount            NUMERIC,                 -- NULL = unbounded
    min_turnover          NUMERIC,                 -- NULL = unbounded
    market_orders_enabled BOOLEAN     NOT NULL,
    enabled               BOOLEAN     NOT NULL,    -- off = Halted; cancels still flow
    visible               BOOLEAN     NOT NULL,    -- listed on the front
    sort                  INT         NOT NULL,
    zone                  TEXT        NOT NULL,
    featured              BOOLEAN     NOT NULL,
    max_open_orders       INT,                     -- per-Member cap; NULL = none
    ttl_seconds           INT,                     -- resting-order TTL; NULL = none
    updated_at            TIMESTAMPTZ NOT NULL DEFAULT now()
);
