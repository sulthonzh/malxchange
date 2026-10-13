-- 0003_withdraw_addresses: the member's withdrawal address book
-- (ticket #40) — api-owned: saved withdrawal addresses per Coin with a
-- member-supplied label. Addresses themselves are not trusted here;
-- the wallet owns Coin mechanics, this table only remembers what the
-- member chose to re-use.

CREATE TABLE withdraw_addresses (
    id         TEXT PRIMARY KEY,      -- uuid
    member_id  TEXT NOT NULL REFERENCES members(id) ON DELETE CASCADE,
    coin       TEXT NOT NULL,         -- Coin name (wallet-owned vocabulary)
    address    TEXT NOT NULL,
    label      TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (member_id, coin, address)
);
CREATE INDEX withdraw_addresses_member_idx ON withdraw_addresses (member_id);
