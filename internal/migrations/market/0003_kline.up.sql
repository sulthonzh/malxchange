-- 0003_market: Kline bars (ticket #31) — OHLCV history per stored
-- interval, folded from trade.fill in Fill event order. Writes are
-- idempotent upserts on the natural key (market, interval, bucket_open),
-- so crash redelivery and rebuild replay cannot double-count. Like
-- everything in this schema the table is rebuildable: drop the schema and
-- replay the topics. Member and Order identifiers are absent by design —
-- bars carry market-wide values only (GLOSSARY: Kline; ADR-0011).

CREATE TABLE klines (
    market      TEXT        NOT NULL,
    "interval"  TEXT        NOT NULL CHECK ("interval" IN ('1m', '5m', '15m', '30m', '1h', '4h', '1d', '1w')),
    bucket_open TIMESTAMPTZ NOT NULL,
    open        NUMERIC     NOT NULL,
    high        NUMERIC     NOT NULL,
    low         NUMERIC     NOT NULL,
    close       NUMERIC     NOT NULL,
    amount      NUMERIC     NOT NULL,
    turnover    NUMERIC     NOT NULL,
    fill_count  BIGINT      NOT NULL,
    first_at    TIMESTAMPTZ NOT NULL,
    first_seq   BIGINT      NOT NULL,
    last_at     TIMESTAMPTZ NOT NULL,
    last_seq    BIGINT      NOT NULL,
    PRIMARY KEY (market, "interval", bucket_open)
);

-- Chart reads take the latest N bars of one Market and interval.
CREATE INDEX klines_recent_idx ON klines (market, "interval", bucket_open DESC);
