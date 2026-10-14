-- 0002_auth: Employee auth core (ticket #53, ADR-0012) — staff identity
-- (GLOSSARY: Employee), the Roles/Permissions/role-grant tables (schema
-- lands here; the code-seeded catalog and role seeding are #54),
-- Employee Sessions with the 12 h absolute + 30 min idle expiry, the
-- failed-login counters behind the 5-strikes lockout, and the
-- append-only Audit log that the middleware writes.

CREATE TABLE roles (
    id         TEXT PRIMARY KEY,        -- uuid
    name       TEXT NOT NULL UNIQUE,    -- 'super-admin', 'finance', 'compliance', 'support' (#54 seeding)
    system     BOOLEAN NOT NULL DEFAULT false, -- seeded Role: grants editable, never deletable (#54)
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- The permission catalog is seeded idempotently from the endpoint list
-- (#54) and read-only everywhere: a string exists because a Go handler
-- checks it (ADR-0012). Nothing inserts into this table before #54.
CREATE TABLE permissions (
    id   TEXT PRIMARY KEY,              -- uuid
    name TEXT NOT NULL UNIQUE           -- 'verb:resource' (ADR-0012)
);

CREATE TABLE role_permissions (
    role_id       TEXT NOT NULL REFERENCES roles(id),
    permission_id TEXT NOT NULL REFERENCES permissions(id),
    PRIMARY KEY (role_id, permission_id)
);

CREATE TABLE employees (
    id            TEXT PRIMARY KEY,     -- uuid
    email         TEXT NOT NULL UNIQUE, -- sole identity (ADR-0012), stored lowercased
    password_hash TEXT NOT NULL,        -- argon2id PHC string
    role_id       TEXT NOT NULL REFERENCES roles(id), -- exactly one Role (ADR-0012)
    enabled       BOOLEAN NOT NULL DEFAULT true,      -- the single enable flag (legacy's pair dies)
    totp_secret   TEXT,                 -- base32 binding; NULL = unbound (login demands a code when set)
    totp_staged   TEXT,                 -- enrollment begin: secret awaiting its first valid code
    totp_bound_at TIMESTAMPTZ,
    must_rotate   BOOLEAN NOT NULL DEFAULT true, -- one-time credential: password change forced first
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- One row per signed-in device, the shared internal/auth storage shape
-- (issue #52): the opaque token never leaves the client, only its
-- SHA-256 hex digest is stored. `member_id` is the package's column
-- contract and carries the Employee id here.
CREATE TABLE sessions (
    id           TEXT PRIMARY KEY,      -- uuid
    member_id    TEXT NOT NULL REFERENCES employees(id) ON DELETE CASCADE,
    token_hash   TEXT NOT NULL UNIQUE,  -- hex SHA-256 of the opaque token
    device       TEXT NOT NULL DEFAULT '',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_used_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at   TIMESTAMPTZ NOT NULL,  -- 12 h absolute cap, 30 min idle slide (ADR-0012)
    revoked_at   TIMESTAMPTZ
);
CREATE INDEX sessions_member_idx ON sessions (member_id);

-- Failed-login counters (same shape as the member api's): the count
-- inside the trailing window decides the 5-strikes/15-minute lockout.
-- Failures are recorded only for wrong credentials — locked-out
-- attempts record none, so a locked email cannot extend its own lock.
CREATE TABLE login_failures (
    id    BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    email TEXT NOT NULL,
    at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX login_failures_window_idx ON login_failures (email, at);

-- The Audit log (ADR-0012): one append-only table for the whole admin
-- surface, written by middleware — never handlers — for every mutating
-- endpoint plus login/logout/lockout events. before/after carry the
-- redacted row JSON (never password hashes or TOTP secrets).
CREATE TABLE audit_log (
    id         BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    actor      TEXT NOT NULL,           -- Employee email, or 'system:bootstrap'
    action     TEXT NOT NULL,           -- 'verb:resource' (ADR-0012)
    target     TEXT NOT NULL DEFAULT '',-- affected row id (employee/session id, ...)
    before     JSONB,
    after      JSONB,
    origin_ip  TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX audit_log_time_idx ON audit_log (created_at DESC);
CREATE INDEX audit_log_actor_idx ON audit_log (actor, created_at DESC);

-- Append-only is enforced, not commented: no UPDATE, no DELETE, ever.
CREATE FUNCTION audit_log_immutable() RETURNS trigger AS $$
BEGIN
    RAISE EXCEPTION 'audit_log is append-only';
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER audit_log_no_update_delete
    BEFORE UPDATE OR DELETE ON audit_log
    FOR EACH ROW EXECUTE FUNCTION audit_log_immutable();
