-- 0003_channels: admin-activated Code channels (ticket #38, ADR-0006).
-- SMS, WhatsApp, and Telegram drivers ship DORMANT: the rows seed
-- inactive and an admin flips one on with credentials through the
-- internal RPC — no code change. Email is always on and has no row.
-- Channel codes never gate funds (ADR-0006).

CREATE TABLE code_channels (
    name         TEXT PRIMARY KEY,            -- 'sms' | 'whatsapp' | 'telegram'
    active       BOOLEAN NOT NULL DEFAULT FALSE,
    credentials  JSONB NOT NULL DEFAULT '{}', -- driver-specific secrets; never on any response
    activated_at TIMESTAMPTZ,
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
INSERT INTO code_channels (name) VALUES ('sms'), ('whatsapp'), ('telegram');

-- The Telegram chat bound to a member: the webhook records it after the
-- member starts the bot with their per-member binding token (deep
-- link). One chat per member and one member per chat — codes to an
-- unbound member are rejected.
CREATE TABLE member_telegram_chats (
    member_id TEXT PRIMARY KEY REFERENCES members(id) ON DELETE CASCADE,
    chat_id   BIGINT NOT NULL UNIQUE,
    bound_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Start-the-bot binding tokens. Like Sessions: an opaque random token
-- of which only the SHA-256 digest is stored — the token alone (all a
-- webhook update ever carries) resolves the row, single-use, TTL'd.
CREATE TABLE channel_binding_tokens (
    token_hash TEXT PRIMARY KEY,
    member_id  TEXT NOT NULL REFERENCES members(id) ON DELETE CASCADE,
    channel    TEXT NOT NULL DEFAULT 'telegram',
    expires_at TIMESTAMPTZ NOT NULL,
    used_at    TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Phone as a secondary contact (ADR-0006): optional, never an identity.
-- The phone-bind sensitive flow (ticket #39) writes it after a code
-- delivered over an activated channel proves control.
ALTER TABLE members ADD COLUMN phone TEXT UNIQUE;
