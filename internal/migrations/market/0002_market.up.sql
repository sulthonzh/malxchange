-- 0002_market: derived market-data state (#30), projected from
-- trade.fill / market.plate into market's own schema. Everything here is
-- rebuildable: dropping the schema and replaying the topics regenerates it.

-- Sanitized trade history. A Trade is the market-wide record of a Fill
-- pair (GLOSSARY): Member and Order identifiers are deliberately absent —
-- they exist on trade.fill, never in the projection. fill_id is the
-- trade.fill envelope ID, kept so clients can dedupe on reconnect
-- (ADR-0011).
CREATE TABLE trades (
    fill_id     TEXT        NOT NULL,
    market      TEXT        NOT NULL,
    taker_side  TEXT        NOT NULL CHECK (taker_side IN ('buy', 'sell')),
    price       NUMERIC     NOT NULL,
    amount      NUMERIC     NOT NULL,
    turnover    NUMERIC     NOT NULL,
    traded_at   TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (market, fill_id)
);

-- Latest first (reconnect dedupe reads the head of the list).
CREATE INDEX trades_recent_idx ON trades (market, traded_at DESC, fill_id DESC);

-- Last-known Depth: one row per published (Market, side, price) level.
-- A plate publish replaces only the sides it carries, so a one-side
-- publish never wipes the other side; a Market with no rows was never
-- published (true cold start, ADR-0011 Warmup rule).
CREATE TABLE depth_levels (
    market  TEXT    NOT NULL,
    side    TEXT    NOT NULL CHECK (side IN ('buy', 'sell')),
    price   NUMERIC NOT NULL,
    amount  NUMERIC NOT NULL,
    PRIMARY KEY (market, side, price)
);
