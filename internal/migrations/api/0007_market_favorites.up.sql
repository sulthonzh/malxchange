-- 0007_market_favorites: the member watchlist (spec #24 story 18) —
-- api-owned Market names the member starred in the market table, so
-- the front follows the member across devices. Names, not ids: the
-- exchange owns Market identity by name (ADR-0009), and a delisted
-- favorite degrades to an unknown name the front simply skips.

CREATE TABLE market_favorites (
    member_id  TEXT NOT NULL REFERENCES members(id) ON DELETE CASCADE,
    market     TEXT NOT NULL,         -- Market name, e.g. 'BTC/USDT'
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (member_id, market)
);
