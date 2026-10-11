-- 0001_init: platform infrastructure shared by every service.
-- Outbox (ADR-0002): events are written in the SAME transaction as the
-- domain change; the relay publishes them to Redpanda at-least-once.
-- Inbox: idempotent-consumer dedupe, UNIQUE(message_key, message_id),
-- inserted inside the same transaction as the domain write.

CREATE TABLE outbox (
    id             UUID PRIMARY KEY,
    topic          TEXT        NOT NULL,
    key            TEXT        NOT NULL,
    payload        TEXT        NOT NULL,
    status         TEXT        NOT NULL DEFAULT 'pending'
                   CHECK (status IN ('pending', 'publishing', 'published', 'failed')),
    attempts       INT         NOT NULL DEFAULT 0,
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    claimed_at     TIMESTAMPTZ,
    published_at   TIMESTAMPTZ,
    last_error     TEXT,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Claim query shape: pending rows due now, oldest first, SKIP LOCKED.
CREATE INDEX outbox_claim_idx ON outbox (next_attempt_at, created_at)
    WHERE status = 'pending';
CREATE INDEX outbox_stale_idx ON outbox (claimed_at)
    WHERE status = 'publishing';
CREATE INDEX outbox_prune_idx ON outbox (published_at)
    WHERE status = 'published';

-- Wake the relay as soon as a row lands (LISTEN/NOTIFY); polling is the
-- safety net, not the latency path.
CREATE FUNCTION outbox_wakeup() RETURNS trigger AS $$
BEGIN
    PERFORM pg_notify('outbox', NEW.id::text);
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER outbox_insert_notify
    AFTER INSERT ON outbox
    FOR EACH ROW EXECUTE FUNCTION outbox_wakeup();

CREATE TABLE inbox (
    message_key TEXT        NOT NULL,
    message_id  TEXT        NOT NULL,
    consumed_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (message_key, message_id)
);
