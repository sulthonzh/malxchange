// Package auth carries the login primitives shared by the member api
// and the admin-api Employee surface (issue #52): argon2id password
// hashing with the login password policy, RFC 6238 Authenticator codes,
// opaque-token minting and digesting, and Postgres-backed sessions with
// parameterized expiry. Session storage (ADR-0006) lives in the
// service's own schema; this package only speaks SQL against the
// `sessions` / `login_failures` shapes both services share.
package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// Login passwords hash with argon2id (ADR-0006) in the PHC string
// format, so hashes stay self-describing for parameter upgrades. The
// parameters are the current OWASP interactive-login baseline.
const (
	argonTime    uint32 = 1
	argonMemory  uint32 = 64 * 1024
	argonThreads uint8  = 4
	argonKeyLen  uint32 = 32
	argonSaltLen        = 16
)

// HashPassword derives the argon2id PHC string for a login password.
func HashPassword(password string) (string, error) {
	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("auth: salt: %w", err)
	}
	key := argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argonMemory, argonTime, argonThreads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key)), nil
}

// VerifyPassword reports whether the password matches the stored PHC
// string. A malformed stored hash never verifies and never errors — a
// corrupt row must not become a login bypass or a 500.
func VerifyPassword(phc, password string) bool {
	p, err := parsePHC(phc)
	if err != nil {
		return false
	}
	key := argon2.IDKey([]byte(password), p.salt, p.time, p.memory, p.threads, p.keyLen)
	return subtle.ConstantTimeCompare(key, p.key) == 1
}

// CheckPasswordPolicy enforces the login password bounds shared by
// registration, password change, and forgot-password reset. The
// messages are member-facing (ADR-0013: English-only).
func CheckPasswordPolicy(pw string) error {
	if len(pw) < 8 {
		return errors.New("Choose a password of at least 8 characters.")
	}
	if len(pw) > 512 {
		return errors.New("Choose a password of at most 512 characters.")
	}
	return nil
}

type phcParams struct {
	salt, key    []byte
	time, memory uint32
	threads      uint8
	keyLen       uint32
}

func parsePHC(phc string) (phcParams, error) {
	parts := strings.Split(phc, "$")
	// "", "argon2id", "v=19", "m=...,t=...,p=...", salt, key
	if len(parts) != 6 || parts[1] != "argon2id" {
		return phcParams{}, fmt.Errorf("auth: not an argon2id PHC string")
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return phcParams{}, fmt.Errorf("auth: unsupported argon2 version")
	}
	var p phcParams
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &p.memory, &p.time, &p.threads); err != nil {
		return phcParams{}, fmt.Errorf("auth: bad argon2 parameters")
	}
	var err error
	if p.salt, err = base64.RawStdEncoding.DecodeString(parts[4]); err != nil {
		return phcParams{}, fmt.Errorf("auth: bad salt")
	}
	if p.key, err = base64.RawStdEncoding.DecodeString(parts[5]); err != nil {
		return phcParams{}, fmt.Errorf("auth: bad key")
	}
	p.keyLen = uint32(len(p.key))
	return p, nil
}
