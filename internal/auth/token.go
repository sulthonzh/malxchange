package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
)

// Opaque secrets (session tokens, registration tickets) are random
// 256-bit values in base64url; only their SHA-256 hex digest is stored,
// so a database leak does not leak usable credentials.

const secretBytes = 32

// NewSecret mints an opaque secret (token or ticket).
func NewSecret() (string, error) {
	b := make([]byte, secretBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("auth: secret: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// TokenDigest digests an opaque token for storage. Unlike 6-digit
// verification codes the token is 256-bit random, so purpose scoping
// adds nothing — the plain digest is the stored form.
func TokenDigest(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
