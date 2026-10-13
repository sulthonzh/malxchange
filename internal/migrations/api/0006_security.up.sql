-- 0006_security: the sensitive-flow matrix state (ticket #39, ADR-0006)
-- — Authenticator (TOTP) bindings, KYC applications with the document
-- images stored in-row, and the audit trail for admin-mediated
-- Authenticator recovery. The members.phone column landed in 0003.

CREATE TABLE authenticator_bindings (
    member_id   TEXT PRIMARY KEY REFERENCES members(id) ON DELETE CASCADE,
    secret      TEXT NOT NULL,             -- base32 TOTP secret; api schema only, never on a response except once at bind-begin
    enabled     BOOLEAN NOT NULL DEFAULT FALSE, -- pending until the member proves a live code
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    confirmed_at TIMESTAMPTZ
);

CREATE TABLE kyc_applications (
    member_id       TEXT PRIMARY KEY REFERENCES members(id) ON DELETE CASCADE,
    full_name       TEXT NOT NULL,
    document_number TEXT NOT NULL,
    front_image     BYTEA NOT NULL,
    back_image      BYTEA NOT NULL,
    status          TEXT NOT NULL DEFAULT 'pending'
                    CHECK (status IN ('pending', 'verified', 'rejected')),
    review_reason   TEXT NOT NULL DEFAULT '',
    reviewed_by     TEXT NOT NULL DEFAULT '',
    submitted_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    reviewed_at     TIMESTAMPTZ
);

-- Admin-mediated security events (Authenticator recovery today). The
-- actor is the Employee identity admin-api passes; api records it
-- verbatim for the audit trail.
CREATE TABLE security_audit (
    id         BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    member_id  TEXT NOT NULL REFERENCES members(id) ON DELETE CASCADE,
    action     TEXT NOT NULL,
    actor      TEXT NOT NULL DEFAULT '',
    detail     TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX security_audit_member_idx ON security_audit (member_id, created_at);
