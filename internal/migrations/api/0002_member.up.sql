-- 0002_member: member identity core (ticket #37, ADR-0006) — Members,
-- per-device Sessions with a sliding 7-day expiry, single-use
-- purpose-scoped Verification codes, and failed-login counters for
-- lockout. Postgres only — codes/sessions/counters are tables, no Redis
-- (spec #24 Out of Scope).

CREATE TABLE members (
    id            TEXT PRIMARY KEY,      -- uuid; member.register is keyed on it
    email         TEXT NOT NULL UNIQUE,  -- sole identity (ADR-0006), stored lowercased
    password_hash TEXT NOT NULL,         -- argon2id PHC string
    status        TEXT NOT NULL DEFAULT 'active'
                  CHECK (status IN ('active', 'locked')),
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- One row per signed-in device. The opaque token never leaves the
-- member's client; only its SHA-256 hex digest is stored, so a database
-- leak does not leak usable sessions.
CREATE TABLE sessions (
    id           TEXT PRIMARY KEY,       -- uuid
    member_id    TEXT NOT NULL REFERENCES members(id) ON DELETE CASCADE,
    token_hash   TEXT NOT NULL UNIQUE,   -- hex SHA-256 of the opaque token
    device       TEXT NOT NULL DEFAULT '', -- client-supplied label
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_used_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at   TIMESTAMPTZ NOT NULL,   -- slid forward on every authenticated use
    revoked_at   TIMESTAMPTZ
);
CREATE INDEX sessions_member_idx ON sessions (member_id);

-- Verification codes (GLOSSARY): single-use, purpose-scoped, TTL'd
-- secrets proving control of a contact or confirming an action. The
-- same table carries the short-lived registration ticket a member gets
-- after presenting the emailed 6-digit code (purpose
-- 'register-ticket'), so ticket TTL/single-use ride the proven
-- machinery.
CREATE TABLE verification_codes (
    id         BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    purpose    TEXT NOT NULL,            -- 'register', 'register-ticket', later flows
    target     TEXT NOT NULL,            -- contact the secret is scoped to (email)
    code_hash  TEXT NOT NULL,            -- hex SHA-256 of purpose:target:secret
    attempts   INT NOT NULL DEFAULT 0,   -- wrong entries recorded against the row
    expires_at TIMESTAMPTZ NOT NULL,
    used_at    TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX verification_codes_lookup_idx
    ON verification_codes (purpose, code_hash, created_at);

-- Failed-login counters: the count inside the trailing window decides
-- lockout, so a lock expires exactly when the oldest counted failure
-- slides out of the window (lockout replaces captcha, spec #24 story 4).
-- Failures are recorded only for wrong passwords — locked-out attempts
-- do not extend the window.
CREATE TABLE login_failures (
    id    BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    email TEXT NOT NULL,
    at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX login_failures_window_idx ON login_failures (email, at);
