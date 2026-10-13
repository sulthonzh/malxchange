// Authenticator codes: RFC 4226 HOTP / RFC 6238 TOTP (SHA-1, 30-second
// step, 6 digits) — the withdrawal second factor (ADR-0006). The secret
// is a 160-bit base32 value shown to the member exactly once, at bind
// begin; only the api schema ever stores it.
package api

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1" //nolint:gosec // RFC 6238 defines TOTP over HMAC-SHA-1; the secret's entropy carries the security.
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"net/url"
	"strings"
	"time"
)

const (
	totpStep   = 30 * time.Second
	totpDigits = 6
)

// generateTOTPSecret mints a fresh 160-bit secret in base32 (no padding,
// the form authenticator apps accept).
func generateTOTPSecret() (string, error) {
	raw := make([]byte, 20)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("api: authenticator secret: %w", err)
	}
	return base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(raw), nil
}

// totpCode computes the code for the step containing t.
func totpCode(secret string, t time.Time) (string, error) {
	key, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(strings.ToUpper(secret))
	if err != nil {
		return "", fmt.Errorf("api: authenticator secret decode: %w", err)
	}
	counter := uint64(t.Unix()) / uint64(totpStep.Seconds())
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], counter)
	mac := hmac.New(sha1.New, key)
	mac.Write(buf[:])
	sum := mac.Sum(nil)
	offset := sum[len(sum)-1] & 0x0f
	value := (uint32(sum[offset]&0x7f) << 24) |
		(uint32(sum[offset+1]) << 16) |
		(uint32(sum[offset+2]) << 8) |
		uint32(sum[offset+3])
	return fmt.Sprintf("%06d", value%1000000), nil
}

// verifyTOTP accepts the current step's code and one step either side
// (clock skew between the member's phone and the server).
func verifyTOTP(secret, code string, now time.Time) bool {
	if len(code) != totpDigits {
		return false
	}
	for _, delta := range []time.Duration{-totpStep, 0, totpStep} {
		want, err := totpCode(secret, now.Add(delta))
		if err != nil {
			return false
		}
		if subtle.ConstantTimeCompare([]byte(want), []byte(code)) == 1 {
			return true
		}
	}
	return false
}

// otpauthURI renders the provisioning URI authenticator apps scan. The
// label carries the member's email so the account is recognizable in
// the app; the issuer pins the service name.
func otpauthURI(email, secret string) string {
	label := url.PathEscape("malxchange:" + email)
	q := url.Values{
		"secret": {secret},
		"issuer": {"malxchange"},
		"digits": {"6"},
		"period": {"30"},
	}
	return "otpauth://totp/" + label + "?" + q.Encode()
}
