// Verification codes (GLOSSARY): single-use, purpose-scoped, TTL'd
// secrets proving control of a contact or confirming an action. Only the
// SHA-256 of purpose|target|code is stored, so a database leak does not
// leak usable codes.
package api

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// purposeRegister scopes codes to the registration flow; later flows
// (password change, email change, …) add their own constants. A code only
// ever validates against the purpose it was issued for.
const purposeRegister = "register"

var (
	errCodeInvalid = errors.New("verification code is invalid or expired")
	errCodeLocked  = errors.New("too many wrong codes; request a new one")
)

// generateCode returns a cryptographically random 6-digit string.
func generateCode() (string, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(1000000))
	if err != nil {
		return "", fmt.Errorf("api: code entropy: %w", err)
	}
	return fmt.Sprintf("%06d", n.Int64()), nil
}

func hashCode(purpose, target, code string) string {
	sum := sha256.Sum256([]byte(purpose + "|" + target + "|" + code))
	return hex.EncodeToString(sum[:])
}

// issueCode mints a code for purpose+target inside the caller's
// transaction and returns the plaintext for delivery.
func issueCode(ctx context.Context, tx pgx.Tx, purpose, target string, ttl time.Duration) (string, error) {
	code, err := generateCode()
	if err != nil {
		return "", err
	}
	_, err = tx.Exec(ctx,
		`INSERT INTO verification_codes (purpose, target, code_hash, expires_at)
		 VALUES ($1, $2, $3, now() + make_interval(secs => $4::double precision))`,
		purpose, target, hashCode(purpose, target, code), ttl.Seconds())
	if err != nil {
		return "", fmt.Errorf("api: insert code: %w", err)
	}
	return code, nil
}

// verifyCode consumes the newest live code for purpose+target and owns
// its transaction deliberately: burned attempts and the used mark must
// SURVIVE a failed request — a member who fat-fingers the code three
// times must be locked out even though every request rolled back its
// wider work. Missing/expired/used rows and exhausted attempts all
// answer the same generic error (no oracle about which part failed).
func verifyCode(ctx context.Context, pool *pgxpool.Pool, purpose, target, code string, maxAttempts int) error {
	outcome := error(nil)
	err := pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		var id int64
		var hash string
		var attempts int
		err := tx.QueryRow(ctx,
			`SELECT id, code_hash, attempts FROM verification_codes
			 WHERE purpose = $1 AND target = $2 AND used_at IS NULL AND expires_at > now()
			 ORDER BY created_at DESC, id DESC LIMIT 1 FOR UPDATE`,
			purpose, target).Scan(&id, &hash, &attempts)
		if errors.Is(err, pgx.ErrNoRows) {
			outcome = errCodeInvalid
			return nil // commit: nothing to persist
		}
		if err != nil {
			return fmt.Errorf("api: load code: %w", err)
		}
		if attempts >= maxAttempts {
			outcome = errCodeLocked
			return nil // commit: the burn from earlier wrong entries stays
		}
		if subtle.ConstantTimeCompare([]byte(hash), []byte(hashCode(purpose, target, code))) != 1 {
			if _, err := tx.Exec(ctx,
				`UPDATE verification_codes SET attempts = attempts + 1 WHERE id = $1`, id); err != nil {
				return fmt.Errorf("api: burn attempt: %w", err)
			}
			outcome = errCodeInvalid
			return nil // commit the burn
		}
		if _, err := tx.Exec(ctx,
			`UPDATE verification_codes SET used_at = now() WHERE id = $1`, id); err != nil {
			return fmt.Errorf("api: mark code used: %w", err)
		}
		return nil
	})
	if err != nil {
		return err
	}
	return outcome
}

// recentCode reports whether a code for purpose+target was issued within
// the cooldown (rate limiting code requests per target).
func recentCode(ctx context.Context, tx pgx.Tx, purpose, target string, cooldown time.Duration) (bool, error) {
	var n int
	err := tx.QueryRow(ctx,
		`SELECT count(*) FROM verification_codes
		 WHERE purpose = $1 AND target = $2 AND created_at > now() - make_interval(secs => $3::double precision)`,
		purpose, target, cooldown.Seconds()).Scan(&n)
	if err != nil {
		return false, fmt.Errorf("api: code cooldown check: %w", err)
	}
	return n > 0, nil
}
