-- 0005_order_intents (ticket #42): the api-side record of every order
-- submission that passed validation and wallet freeze. api holds no
-- order-table copy of the exchange's state (spec #24 story 30) — this
-- row is the submit-time record only: it authorizes cancel (a member
-- may cancel only orders api accepted for them) and audits what was
-- frozen for which command. Exchange remains the authority for order
-- status.

CREATE TABLE order_intents (
    id            TEXT PRIMARY KEY,      -- uuid; the OrderCommand.orderId and wallet hold key
    member_id     TEXT NOT NULL REFERENCES members(id) ON DELETE CASCADE,
    market        TEXT NOT NULL,         -- Market symbol, e.g. "BTC/USDT"
    side          TEXT NOT NULL CHECK (side IN ('buy', 'sell')),
    kind          TEXT NOT NULL CHECK (kind IN ('limit', 'market')),
    price         TEXT,                  -- decimal string; limit only
    amount        TEXT NOT NULL,         -- decimal string; base amount, or the quote budget for a market buy
    frozen_coin   TEXT NOT NULL,         -- the Coin the wallet hold carries
    frozen_amount TEXT NOT NULL,         -- decimal string; exactly what the freeze RPC moved
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX order_intents_member_idx ON order_intents (member_id, created_at);
