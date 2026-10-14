package auth

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Sessions are per-device rows (ADR-0006): an opaque token of which
// only the SHA-256 digest is stored, a client-supplied device label,
// and an expiry whose shape the SessionPolicy parameterizes.

// Session is one signed-in device.
type Session struct {
	ID         string
	MemberID   string
	Device     string
	CreatedAt  time.Time
	LastUsedAt time.Time
	ExpiresAt  time.Time
}

// ErrSessionRejected marks a token that does not resolve to a live
// session — missing, unknown, expired, and revoked all answer 401 the
// same way, so the surface leaks nothing about which one it was.
var ErrSessionRejected = errors.New("session rejected")

// SessionPolicy parameterizes session expiry. Idle is the inactivity
// window: every authenticated use slides expires_at forward from now by
// Idle. Absolute, when positive, caps the total lifetime from creation
// and stops the slide. Absolute zero is the pure sliding model (member
// api, ADR-0006: 7 days); a positive Absolute gives the absolute+idle
// model (Employee sessions, ADR-0012: 12 h + 30 min).
type SessionPolicy struct {
	Absolute time.Duration
	Idle     time.Duration
}

// mintExpiry is the widest window the policy allows, so a fresh row
// starts at its most generous expiry and ResolveSession only narrows it.
func (p SessionPolicy) mintExpiry() time.Duration {
	if p.Absolute > p.Idle {
		return p.Absolute
	}
	return p.Idle
}

// MintSession inserts a per-device session row and returns the opaque
// token for the client (the caller sends it once; only the digest is
// stored).
func MintSession(ctx context.Context, db *pgxpool.Pool, memberID, device string, p SessionPolicy) (string, Session, error) {
	if p.Idle <= 0 {
		return "", Session{}, fmt.Errorf("auth: session policy needs a positive idle window")
	}
	token, err := NewSecret()
	if err != nil {
		return "", Session{}, err
	}
	sess := Session{ID: uuid.NewString(), MemberID: memberID, Device: device}
	err = db.QueryRow(ctx,
		`INSERT INTO sessions (id, member_id, token_hash, device, expires_at)
		 VALUES ($1, $2, $3, $4, now() + make_interval(secs => $5::double precision))
		 RETURNING created_at, last_used_at, expires_at`,
		sess.ID, memberID, TokenDigest(token), device, p.mintExpiry().Seconds(),
	).Scan(&sess.CreatedAt, &sess.LastUsedAt, &sess.ExpiresAt)
	if err != nil {
		return "", Session{}, err
	}
	return token, sess, nil
}

// ResolveSession authenticates an access-auth-token and refreshes the
// expiry in the same statement: the conditional UPDATE fails closed on
// revoked, idle-expired, or absolutely-expired rows, and
// last_used_at/expires_at only move for live ones. The slide target is
// now()+Idle, capped by created_at+Absolute when one is set; with no
// Absolute the model is the member api's pure slide (every use
// re-extends the window from now).
func ResolveSession(ctx context.Context, db *pgxpool.Pool, token string, p SessionPolicy) (Session, error) {
	var s Session
	err := db.QueryRow(ctx,
		`UPDATE sessions
		    SET last_used_at = now(),
		        expires_at = CASE WHEN $3::double precision > 0
		                          THEN LEAST(created_at + make_interval(secs => $3::double precision),
		                                     now() + make_interval(secs => $2::double precision))
		                          ELSE now() + make_interval(secs => $2::double precision)
		                     END
		  WHERE token_hash = $1
		    AND revoked_at IS NULL
		    AND expires_at > now()
		    AND last_used_at + make_interval(secs => $2::double precision) > now()
		    AND ($3::double precision <= 0
		         OR created_at + make_interval(secs => $3::double precision) > now())
		  RETURNING id, member_id, device, created_at, last_used_at, expires_at`,
		TokenDigest(token), p.Idle.Seconds(), p.Absolute.Seconds(),
	).Scan(&s.ID, &s.MemberID, &s.Device, &s.CreatedAt, &s.LastUsedAt, &s.ExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Session{}, ErrSessionRejected
	}
	if err != nil {
		return Session{}, err
	}
	return s, nil
}

// ListSessions returns the member's live sessions, newest use first.
func ListSessions(ctx context.Context, db *pgxpool.Pool, memberID string) ([]Session, error) {
	rows, err := db.Query(ctx,
		`SELECT id, member_id, device, created_at, last_used_at, expires_at
		   FROM sessions
		  WHERE member_id = $1 AND revoked_at IS NULL AND expires_at > now()
		  ORDER BY last_used_at DESC`, memberID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Session{}
	for rows.Next() {
		var s Session
		if err := rows.Scan(&s.ID, &s.MemberID, &s.Device, &s.CreatedAt, &s.LastUsedAt, &s.ExpiresAt); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// RevokeSession signs out one session (logout); revoking twice is a
// no-op.
func RevokeSession(ctx context.Context, db *pgxpool.Pool, id string) error {
	_, err := db.Exec(ctx, `UPDATE sessions SET revoked_at = now() WHERE id = $1 AND revoked_at IS NULL`, id)
	return err
}

// RevokeAllSessions signs the member out everywhere; the count is the
// number of live sessions closed.
func RevokeAllSessions(ctx context.Context, db *pgxpool.Pool, memberID string) (int64, error) {
	tag, err := db.Exec(ctx,
		`UPDATE sessions SET revoked_at = now()
		  WHERE member_id = $1 AND revoked_at IS NULL AND expires_at > now()`, memberID)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// LoginLockedOut reports whether the email has threshold failures
// inside the trailing window. The lock expires exactly when the oldest
// counted failure slides out of the window — recorded failures are the
// only input, and locked-out attempts record none, so a locked email
// cannot extend its own lock.
func LoginLockedOut(ctx context.Context, db *pgxpool.Pool, email string, threshold int, window time.Duration) (bool, error) {
	var n int
	err := db.QueryRow(ctx,
		`SELECT count(*) FROM login_failures
		  WHERE email = $1 AND at > now() - make_interval(secs => $2::double precision)`,
		email, window.Seconds()).Scan(&n)
	if err != nil {
		return false, err
	}
	return n >= threshold, nil
}

// RecordLoginFailure counts one wrong password.
func RecordLoginFailure(ctx context.Context, db *pgxpool.Pool, email string) error {
	_, err := db.Exec(ctx, `INSERT INTO login_failures (email) VALUES ($1)`, email)
	return err
}

// ClearLoginFailures resets the counter after a successful login.
func ClearLoginFailures(ctx context.Context, db *pgxpool.Pool, email string) error {
	_, err := db.Exec(ctx, `DELETE FROM login_failures WHERE email = $1`, email)
	return err
}
