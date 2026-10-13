package api

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Sessions are per-device rows (ADR-0006): an opaque token of which
// only the SHA-256 digest is stored, a client-supplied device label,
// and a sliding expiry the api re-extends on every authenticated use.

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

// mintSession inserts a per-device session row and returns the opaque
// token for the member's client.
func mintSession(ctx context.Context, db *pgxpool.Pool, memberID, device string, ttl time.Duration) (string, Session, error) {
	token, err := newSecret()
	if err != nil {
		return "", Session{}, err
	}
	sess := Session{ID: uuid.NewString(), MemberID: memberID, Device: device}
	err = db.QueryRow(ctx,
		`INSERT INTO sessions (id, member_id, token_hash, device, expires_at)
		 VALUES ($1, $2, $3, $4, now() + make_interval(secs => $5::double precision))
		 RETURNING created_at, last_used_at, expires_at`,
		sess.ID, memberID, tokenDigest(token), device, ttl.Seconds(),
	).Scan(&sess.CreatedAt, &sess.LastUsedAt, &sess.ExpiresAt)
	if err != nil {
		return "", Session{}, err
	}
	return token, sess, nil
}

// resolveSession authenticates an access-auth-token and slides the
// expiry in the same statement: the conditional UPDATE fails closed on
// revoked or expired rows, and last_used_at/expires_at only move for
// live ones.
func resolveSession(ctx context.Context, db *pgxpool.Pool, token string, ttl time.Duration) (Session, error) {
	var s Session
	err := db.QueryRow(ctx,
		`UPDATE sessions
		    SET last_used_at = now(), expires_at = now() + make_interval(secs => $2::double precision)
		  WHERE token_hash = $1 AND revoked_at IS NULL AND expires_at > now()
		  RETURNING id, member_id, device, created_at, last_used_at, expires_at`,
		tokenDigest(token), ttl.Seconds(),
	).Scan(&s.ID, &s.MemberID, &s.Device, &s.CreatedAt, &s.LastUsedAt, &s.ExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Session{}, ErrSessionRejected
	}
	if err != nil {
		return Session{}, err
	}
	return s, nil
}

// listSessions returns the member's live sessions, newest use first.
func listSessions(ctx context.Context, db *pgxpool.Pool, memberID string) ([]Session, error) {
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

// revokeSession signs out one session (logout); revoking twice is a
// no-op.
func revokeSession(ctx context.Context, db *pgxpool.Pool, id string) error {
	_, err := db.Exec(ctx, `UPDATE sessions SET revoked_at = now() WHERE id = $1 AND revoked_at IS NULL`, id)
	return err
}

// revokeAllSessions signs the member out everywhere; the count is the
// number of live sessions closed.
func revokeAllSessions(ctx context.Context, db *pgxpool.Pool, memberID string) (int64, error) {
	tag, err := db.Exec(ctx,
		`UPDATE sessions SET revoked_at = now()
		  WHERE member_id = $1 AND revoked_at IS NULL AND expires_at > now()`, memberID)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// loginLockedOut reports whether the email has threshold failures
// inside the trailing window. The lock expires exactly when the oldest
// counted failure slides out of the window — recorded failures are the
// only input, and locked-out attempts record none, so a locked email
// cannot extend its own lock.
func loginLockedOut(ctx context.Context, db *pgxpool.Pool, email string, threshold int, window time.Duration) (bool, error) {
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

// recordLoginFailure counts one wrong password.
func recordLoginFailure(ctx context.Context, db *pgxpool.Pool, email string) error {
	_, err := db.Exec(ctx, `INSERT INTO login_failures (email) VALUES ($1)`, email)
	return err
}

// clearLoginFailures resets the counter after a successful login.
func clearLoginFailures(ctx context.Context, db *pgxpool.Pool, email string) error {
	_, err := db.Exec(ctx, `DELETE FROM login_failures WHERE email = $1`, email)
	return err
}
